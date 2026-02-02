package transfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/helios-transfer/helios/internal/config"
	"github.com/helios-transfer/helios/internal/protocol"
	"github.com/helios-transfer/helios/pkg/bufferpool"
	"github.com/quic-go/quic-go"
	"lukechampine.com/blake3"
)

// Receiver handles incoming file transfers
type Receiver struct {
	cfg        *config.Config
	outputDir  string
	manifest   *protocol.Manifest
	progress   chan ProgressEvent
	bufferPool *bufferpool.Pool
	files      map[uint32]*receivedFile
	filesMu    sync.RWMutex
	stats      *ReceiverStats
}

type receivedFile struct {
	file         *os.File
	path         string
	size         int64
	chunksNeeded int
	chunksRecv   int32
	mu           sync.Mutex
}

// ReceiverStats holds receiver statistics
type ReceiverStats struct {
	BytesReceived  int64
	ChunksReceived int64
	FilesComplete  int32
	FilesTotal     int32
	StartTime      time.Time
	CurrentSpeed   float64
}

// NewReceiver creates a new receiver
func NewReceiver(cfg *config.Config, outputDir string) *Receiver {
	return &Receiver{
		cfg:        cfg,
		outputDir:  outputDir,
		progress:   make(chan ProgressEvent, 100),
		bufferPool: bufferpool.NewDefault(),
		files:      make(map[uint32]*receivedFile),
		stats:      &ReceiverStats{},
	}
}

// Progress returns the progress event channel
func (r *Receiver) Progress() <-chan ProgressEvent {
	return r.progress
}

// Stats returns current receiver stats
func (r *Receiver) Stats() *ReceiverStats {
	return r.stats
}

// HandleConnection handles an incoming QUIC connection
func (r *Receiver) HandleConnection(ctx context.Context, conn *quic.Conn) error {
	r.stats.StartTime = time.Now()
	defer close(r.progress)

	// Accept control stream (bidirectional)
	controlStream, err := conn.AcceptStream(ctx)
	if err != nil {
		return fmt.Errorf("failed to accept control stream: %w", err)
	}
	defer controlStream.Close()

	codec := protocol.NewCodec(controlStream)

	// Read HELLO
	hello, err := codec.ReadHello()
	if err != nil {
		return fmt.Errorf("failed to read hello: %w", err)
	}

	// Send HELLO_ACK
	helloAck := &protocol.HelloAck{
		Version:    protocol.ProtocolVersion,
		Accepted:   hello.Version == protocol.ProtocolVersion,
		MaxStreams: uint16(r.cfg.Network.MaxStreams),
	}
	if err := codec.WriteHelloAck(helloAck); err != nil {
		return fmt.Errorf("failed to send hello ack: %w", err)
	}

	if !helloAck.Accepted {
		return fmt.Errorf("protocol version mismatch: client=%d, server=%d", hello.Version, protocol.ProtocolVersion)
	}

	// Read manifest
	r.manifest, err = codec.ReadManifest()
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	r.stats.FilesTotal = int32(r.manifest.TotalFiles)

	// Build transfer plan
	plan, err := r.buildTransferPlan()
	if err != nil {
		return fmt.Errorf("failed to build transfer plan: %w", err)
	}

	// Send transfer plan
	if err := codec.WriteTransferPlan(plan); err != nil {
		return fmt.Errorf("failed to send transfer plan: %w", err)
	}

	// Prepare files for receiving
	if err := r.prepareFiles(plan); err != nil {
		return fmt.Errorf("failed to prepare files: %w", err)
	}

	// Channel for file completion events to be sent back to sender
	completionCh := make(chan protocol.FileComplete, 100)
	defer close(completionCh)

	// Start control stream writer
	go func() {
		for comp := range completionCh {
			if err := codec.WriteFileComplete(&comp); err != nil {
				// Log error but don't stop everything?
				// The connection might be closing anyway
				return
			}
		}
	}()

	// Accept and process data streams
	errCh := make(chan error, r.cfg.Network.MaxStreams)
	acceptorDone := make(chan struct{})
	var wg sync.WaitGroup

	// Stream acceptor
	go func() {
		defer close(acceptorDone)
		for {
			stream, err := conn.AcceptUniStream(ctx)
			if err != nil {
				return
			}

			wg.Add(1)
			go func(s *quic.ReceiveStream) {
				defer wg.Done()
				if err := r.handleDataStream(ctx, s, completionCh); err != nil {
					select {
					case errCh <- err:
					default:
					}
				}
			}(stream)
		}
	}()

	// Wait for completion or error
	select {
	case <-acceptorDone:
		// Connection closed or no more streams, wait for active transfers
		wg.Wait()

		// Check if we have any pending errors
		select {
		case err := <-errCh:
			return err
		default:
		}

		// Check if all files are complete
		r.progress <- ProgressEvent{
			Type:      ProgressTransferComplete,
			Timestamp: time.Now(),
		}
	case err := <-errCh:
		if err != nil && ctx.Err() == nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	// Cleanup
	r.closeAllFiles()

	return nil
}

// buildTransferPlan checks which files/chunks are needed
func (r *Receiver) buildTransferPlan() (*protocol.TransferPlan, error) {
	plan := &protocol.TransferPlan{
		Decisions: make([]protocol.FileDecision, len(r.manifest.Files)),
	}

	for i, file := range r.manifest.Files {
		decision := protocol.FileDecision{
			FileIndex: uint32(i),
		}

		destPath := filepath.Join(r.outputDir, filepath.FromSlash(file.RelativePath))

		// Check if file exists
		info, err := os.Stat(destPath)
		if os.IsNotExist(err) {
			// File doesn't exist, need complete transfer
			decision.Action = protocol.ActionNeedComplete
		} else if err != nil {
			return nil, err
		} else if info.Size() == file.Size {
			// File exists with same size, check hash
			existingHash, err := hashFile(destPath)
			if err != nil {
				decision.Action = protocol.ActionNeedComplete
			} else if existingHash == file.FileHash {
				decision.Action = protocol.ActionSkip
			} else {
				decision.Action = protocol.ActionNeedComplete
			}
		} else {
			decision.Action = protocol.ActionNeedComplete
		}

		plan.Decisions[i] = decision
	}

	return plan, nil
}

// hashFile computes BLAKE3 hash of a file
func hashFile(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()

	hasher := blake3.New(32, nil)
	if _, err := io.Copy(hasher, f); err != nil {
		return [32]byte{}, err
	}

	var hash [32]byte
	hasher.Sum(hash[:0])
	return hash, nil
}

// prepareFiles creates destination files and pre-allocates space
func (r *Receiver) prepareFiles(plan *protocol.TransferPlan) error {
	r.filesMu.Lock()
	defer r.filesMu.Unlock()

	for _, decision := range plan.Decisions {
		if decision.Action == protocol.ActionSkip {
			continue
		}

		fileInfo := r.manifest.Files[decision.FileIndex]
		destPath := filepath.Join(r.outputDir, filepath.FromSlash(fileInfo.RelativePath))

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return err
		}

		// Create/truncate file
		f, err := os.Create(destPath)
		if err != nil {
			return err
		}

		// Pre-allocate space
		if fileInfo.Size > 0 {
			if err := f.Truncate(fileInfo.Size); err != nil {
				f.Close()
				return err
			}
		}

		r.files[decision.FileIndex] = &receivedFile{
			file:         f,
			path:         destPath,
			size:         fileInfo.Size,
			chunksNeeded: int(fileInfo.NumChunks),
		}
	}

	return nil
}

// handleDataStream processes an incoming chunk stream
func (r *Receiver) handleDataStream(ctx context.Context, stream *quic.ReceiveStream, completionCh chan<- protocol.FileComplete) error {
	defer stream.CancelRead(0)

	codec := protocol.NewCodecFromStreams(stream, nil)

	// Read chunk header
	header, err := codec.ReadChunkHeader()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return fmt.Errorf("failed to read chunk header: %w", err)
	}

	// Get buffer
	buf := r.bufferPool.Get()
	defer r.bufferPool.Put(buf)

	if int(header.Size) > len(buf) {
		buf = make([]byte, header.Size)
	}

	// Read chunk data
	n, err := io.ReadFull(codec.Reader(), buf[:header.Size])
	if err != nil {
		return fmt.Errorf("failed to read chunk data: %w", err)
	}

	// Verify hash
	hasher := blake3.New(32, nil)
	hasher.Write(buf[:n])
	var computedHash [32]byte
	hasher.Sum(computedHash[:0])

	if computedHash != header.Hash {
		return fmt.Errorf("chunk hash mismatch for file %d chunk %d", header.FileIndex, header.ChunkIndex)
	}

	// Write to file
	r.filesMu.RLock()
	rf, exists := r.files[header.FileIndex]
	r.filesMu.RUnlock()

	if !exists {
		return fmt.Errorf("unknown file index: %d", header.FileIndex)
	}

	rf.mu.Lock()
	_, err = rf.file.WriteAt(buf[:n], header.Offset)
	rf.mu.Unlock()

	if err != nil {
		return fmt.Errorf("failed to write chunk: %w", err)
	}

	// Update stats
	atomic.AddInt64(&r.stats.BytesReceived, int64(n))
	atomic.AddInt64(&r.stats.ChunksReceived, 1)

	// Check if file is complete
	chunksRecv := atomic.AddInt32(&rf.chunksRecv, 1)
	if int(chunksRecv) == rf.chunksNeeded {
		atomic.AddInt32(&r.stats.FilesComplete, 1)

		// Send MsgFileComplete logic
		completionCh <- protocol.FileComplete{
			FileIndex: header.FileIndex,
			Success:   true,
			FinalHash: r.manifest.Files[header.FileIndex].FileHash,
		}

		r.progress <- ProgressEvent{
			Type:      ProgressFileComplete,
			FileIndex: header.FileIndex,
			FileName:  rf.path,
			Timestamp: time.Now(),
		}
	} else {
		r.progress <- ProgressEvent{
			Type:       ProgressChunkComplete,
			FileIndex:  header.FileIndex,
			ChunkIndex: header.ChunkIndex,
			BytesSent:  int64(n),
			Timestamp:  time.Now(),
		}
	}

	return nil
}

// closeAllFiles closes all open file handles
func (r *Receiver) closeAllFiles() {
	r.filesMu.Lock()
	defer r.filesMu.Unlock()

	for _, rf := range r.files {
		if rf.file != nil {
			rf.file.Close()
		}
	}
}
