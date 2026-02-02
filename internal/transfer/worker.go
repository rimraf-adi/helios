package transfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/helios-transfer/helios/internal/protocol"
	"github.com/helios-transfer/helios/pkg/bufferpool"
	"github.com/quic-go/quic-go"
	"lukechampine.com/blake3"
)

// WorkerPool manages concurrent chunk transfers
type WorkerPool struct {
	numWorkers  int
	conn        *quic.Conn
	bufferPool  *bufferpool.Pool
	progress    chan<- ProgressEvent
	results     chan protocol.ChunkResult
	wg          sync.WaitGroup
	activeCount int32
	maxRetries  int
}

// NewWorkerPool creates a new worker pool
func NewWorkerPool(numWorkers int, conn *quic.Conn, progress chan<- ProgressEvent) *WorkerPool {
	return &WorkerPool{
		numWorkers: numWorkers,
		conn:       conn,
		bufferPool: bufferpool.NewDefault(),
		progress:   progress,
		results:    make(chan protocol.ChunkResult, numWorkers*2),
		maxRetries: 3,
	}
}

// Start begins processing chunks from the input channel
func (wp *WorkerPool) Start(ctx context.Context, chunks <-chan protocol.Chunk) <-chan protocol.ChunkResult {
	for i := 0; i < wp.numWorkers; i++ {
		wp.wg.Add(1)
		go wp.worker(ctx, i, chunks)
	}

	// Close results channel when all workers are done
	go func() {
		wp.wg.Wait()
		close(wp.results)
	}()

	return wp.results
}

// ActiveWorkers returns the number of currently active workers
func (wp *WorkerPool) ActiveWorkers() int {
	return int(atomic.LoadInt32(&wp.activeCount))
}

// worker processes chunks from the channel
func (wp *WorkerPool) worker(ctx context.Context, id int, chunks <-chan protocol.Chunk) {
	defer wp.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-chunks:
			if !ok {
				return
			}

			atomic.AddInt32(&wp.activeCount, 1)
			result := wp.sendChunk(ctx, chunk)
			atomic.AddInt32(&wp.activeCount, -1)

			select {
			case wp.results <- result:
			case <-ctx.Done():
				return
			}
		}
	}
}

// sendChunk sends a single chunk with retry logic
func (wp *WorkerPool) sendChunk(ctx context.Context, chunk protocol.Chunk) protocol.ChunkResult {
	result := protocol.ChunkResult{
		FileIndex:  chunk.FileIndex,
		ChunkIndex: chunk.ChunkIndex,
	}

	for attempt := 0; attempt <= wp.maxRetries; attempt++ {
		result.RetryCount = attempt

		err := wp.doSendChunk(ctx, chunk)
		if err == nil {
			result.Success = true
			result.BytesSent = chunk.Size

			if wp.progress != nil {
				wp.progress <- ProgressEvent{
					Type:       ProgressChunkComplete,
					FileIndex:  chunk.FileIndex,
					ChunkIndex: chunk.ChunkIndex,
					BytesSent:  chunk.Size,
					Timestamp:  time.Now(),
				}
			}
			return result
		}

		result.Error = err

		// Check if we should retry
		if ctx.Err() != nil {
			break
		}

		if attempt < wp.maxRetries {
			// Exponential backoff
			backoff := time.Duration(1<<attempt) * 100 * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return result
			}
		}
	}

	if wp.progress != nil {
		wp.progress <- ProgressEvent{
			Type:       ProgressChunkFailed,
			FileIndex:  chunk.FileIndex,
			ChunkIndex: chunk.ChunkIndex,
			Error:      result.Error,
			Timestamp:  time.Now(),
		}
	}

	return result
}

// doSendChunk performs the actual chunk send
func (wp *WorkerPool) doSendChunk(ctx context.Context, chunk protocol.Chunk) error {
	// Open a new stream for this chunk
	stream, err := wp.conn.OpenUniStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	// Open the file
	file, err := os.Open(chunk.FilePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get buffer from pool
	buf := wp.bufferPool.Get()
	defer wp.bufferPool.Put(buf)

	// Read chunk data
	if chunk.Size > int64(len(buf)) {
		buf = make([]byte, chunk.Size)
	}
	n, err := file.ReadAt(buf[:chunk.Size], chunk.Offset)
	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to read file: %w", err)
	}
	if int64(n) != chunk.Size {
		return fmt.Errorf("short read: got %d, expected %d", n, chunk.Size)
	}

	// Verify hash
	hasher := blake3.New(32, nil)
	hasher.Write(buf[:chunk.Size])
	var computedHash [32]byte
	hasher.Sum(computedHash[:0])

	if computedHash != chunk.Hash {
		return fmt.Errorf("source file hash mismatch")
	}

	// Create codec and write header
	codec := protocol.NewCodecFromStreams(nil, stream)

	header := &protocol.ChunkHeader{
		FileIndex:  chunk.FileIndex,
		ChunkIndex: chunk.ChunkIndex,
		Offset:     chunk.Offset,
		Size:       uint32(chunk.Size),
		Hash:       chunk.Hash,
	}

	if err := codec.WriteChunkHeader(header); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

	// Write data
	if err := codec.WriteRawData(buf[:chunk.Size]); err != nil {
		return fmt.Errorf("failed to write data: %w", err)
	}

	return nil
}

// Wait waits for all workers to complete
func (wp *WorkerPool) Wait() {
	wp.wg.Wait()
}
