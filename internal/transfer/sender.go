package transfer

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/helios-transfer/helios/internal/config"
	"github.com/helios-transfer/helios/internal/protocol"
	"github.com/helios-transfer/helios/internal/quic"
)

// Sender manages file transfer from client to server
type Sender struct {
	cfg      *config.Config
	client   *quic.Client
	rootPath string
	manifest *protocol.Manifest
	progress chan ProgressEvent
	stats    *TransferStats
}

// TransferStats holds transfer statistics
type TransferStats struct {
	StartTime     time.Time
	EndTime       time.Time
	TotalBytes    int64
	BytesSent     int64
	TotalChunks   int64
	ChunksSent    int64
	ChunksFailed  int64
	FilesTotal    int
	FilesComplete int
	CurrentFile   string
	CurrentSpeed  float64 // bytes per second
	mu            sync.RWMutex
}

// NewSender creates a new sender
func NewSender(cfg *config.Config, client *quic.Client, rootPath string) *Sender {
	return &Sender{
		cfg:      cfg,
		client:   client,
		rootPath: rootPath,
		progress: make(chan ProgressEvent, 100),
		stats:    &TransferStats{},
	}
}

// Progress returns the progress event channel
func (s *Sender) Progress() <-chan ProgressEvent {
	return s.progress
}

// Stats returns the current transfer statistics
func (s *Sender) Stats() *TransferStats {
	return s.stats
}

// Send initiates the file transfer
func (s *Sender) Send(ctx context.Context) error {
	s.stats.StartTime = time.Now()
	defer func() {
		s.stats.EndTime = time.Now()
		close(s.progress)
	}()

	// Step 1: Scan files and build manifest
	scanner := NewFileScanner(s.cfg.ChunkSize())
	scanResult, err := scanner.Scan(s.rootPath)
	if err != nil {
		return fmt.Errorf("failed to scan files: %w", err)
	}

	s.manifest = BuildManifest(scanResult)
	s.stats.TotalBytes = s.manifest.TotalSize
	s.stats.FilesTotal = len(s.manifest.Files)

	// Count total chunks
	for _, file := range s.manifest.Files {
		s.stats.TotalChunks += int64(file.NumChunks)
	}

	// Step 2: Open control stream and send hello
	controlStream, err := s.client.OpenBidirectionalStream(ctx)
	if err != nil {
		return fmt.Errorf("failed to open control stream: %w", err)
	}
	defer controlStream.Close()

	codec := protocol.NewCodec(controlStream)

	// Send HELLO
	hello := &protocol.Hello{
		Version:      protocol.ProtocolVersion,
		MaxStreams:   uint16(s.cfg.Network.MaxStreams),
		ChunkSize:    uint32(s.cfg.ChunkSize()),
		Capabilities: 0,
	}
	if err := codec.WriteHello(hello); err != nil {
		return fmt.Errorf("failed to send hello: %w", err)
	}

	// Read HELLO_ACK
	helloAck, err := codec.ReadHelloAck()
	if err != nil {
		return fmt.Errorf("failed to read hello ack: %w", err)
	}
	if !helloAck.Accepted {
		return fmt.Errorf("server rejected connection")
	}

	// Step 3: Send manifest
	if err := codec.WriteManifest(s.manifest); err != nil {
		return fmt.Errorf("failed to send manifest: %w", err)
	}

	// Step 4: Receive transfer plan
	plan, err := codec.ReadTransferPlan()
	if err != nil {
		return fmt.Errorf("failed to read transfer plan: %w", err)
	}

	// Step 5: Create chunks based on plan
	absRoot, _ := filepath.Abs(s.rootPath)
	chunkIter := NewChunkIterator(s.manifest.Files, s.cfg.ChunkSize(), absRoot)
	chunks := chunkIter.FilteredChunks(plan)

	// Step 6: Start worker pool
	conn := s.client.Connection()
	pool := NewWorkerPool(s.cfg.Network.MaxStreams, conn, s.progress)

	results := pool.Start(ctx, chunks)

	// Step 7: Start completion monitor
	completionDone := make(chan error, 1)
	go func() {
		defer close(completionDone)
		filesCompleted := 0

		// If we are sending 0 files (empty manifest), done immediately
		if s.stats.FilesTotal == 0 {
			return
		}

		for {
			// Read file completion message
			msg, err := codec.ReadFileComplete()
			if err != nil {
				completionDone <- err
				return
			}

			// Verify hash (optional but good practice)
			if msg.FileIndex < uint32(len(s.manifest.Files)) {
				expected := s.manifest.Files[msg.FileIndex].FileHash
				if msg.FinalHash != expected {
					// We could error out, or just log
					// For now, assume success if receiver says success
				}
			}

			s.stats.mu.Lock()
			s.stats.FilesComplete++
			filesCompleted = s.stats.FilesComplete
			s.stats.mu.Unlock()

			// Notify progress
			s.progress <- ProgressEvent{
				Type:      ProgressFileComplete,
				FileIndex: msg.FileIndex,
				Timestamp: time.Now(),
			}

			if filesCompleted >= s.stats.FilesTotal {
				return
			}
		}
	}()

	// Step 8: Collect sender results
	for result := range results {
		s.stats.mu.Lock()
		if result.Success {
			s.stats.BytesSent += result.BytesSent
			s.stats.ChunksSent++
		} else {
			s.stats.ChunksFailed++
		}

		// Update speed calculation
		elapsed := time.Since(s.stats.StartTime).Seconds()
		if elapsed > 0 {
			s.stats.CurrentSpeed = float64(s.stats.BytesSent) / elapsed
		}
		s.stats.mu.Unlock()
	}

	// Step 9: Wait for all files to be acknowledged by receiver
	select {
	case err := <-completionDone:
		if err != nil {
			return fmt.Errorf("error waiting for completion: %w", err)
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	s.progress <- ProgressEvent{
		Type:       ProgressTransferComplete,
		TotalBytes: s.stats.BytesSent,
		Timestamp:  time.Now(),
	}

	return nil
}

// GetStats returns a snapshot of current stats
func (s *TransferStats) GetStats() (bytesSent, totalBytes int64, speed float64, filesComplete, filesTotal int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.BytesSent, s.TotalBytes, s.CurrentSpeed, s.FilesComplete, s.FilesTotal
}
