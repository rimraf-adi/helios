package transfer

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/helios-transfer/helios/internal/protocol"
	"lukechampine.com/blake3"
)

// FileScanner scans directories and prepares file manifests
type FileScanner struct {
	chunkSize int64
	excludes  []string
}

// NewFileScanner creates a new scanner with the given chunk size
func NewFileScanner(chunkSize int64) *FileScanner {
	return &FileScanner{
		chunkSize: chunkSize,
		excludes:  []string{".DS_Store", ".git", ".gitignore"},
	}
}

// ScanResult holds the result of scanning a path
type ScanResult struct {
	Files     []protocol.FileInfo
	TotalSize int64
	Errors    []error
}

// Scan walks the path and builds a file manifest
func (s *FileScanner) Scan(rootPath string) (*ScanResult, error) {
	result := &ScanResult{
		Files: make([]protocol.FileInfo, 0),
	}

	// Get absolute path
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}

	// Check if it's a single file or directory
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		// Single file
		fileInfo, err := s.scanFile(absRoot, filepath.Base(absRoot))
		if err != nil {
			result.Errors = append(result.Errors, err)
		} else {
			result.Files = append(result.Files, *fileInfo)
			result.TotalSize = fileInfo.Size
		}
		return result, nil
	}

	// Directory walk
	err = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			result.Errors = append(result.Errors, err)
			return nil // Continue walking
		}

		// Skip directories
		if info.IsDir() {
			// Check if should exclude
			if s.shouldExclude(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip excluded files
		if s.shouldExclude(info.Name()) {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(absRoot, path)
		if err != nil {
			result.Errors = append(result.Errors, err)
			return nil
		}

		fileInfo, err := s.scanFile(path, relPath)
		if err != nil {
			result.Errors = append(result.Errors, err)
			return nil
		}

		result.Files = append(result.Files, *fileInfo)
		result.TotalSize += fileInfo.Size

		return nil
	})

	return result, err
}

// scanFile creates FileInfo for a single file
func (s *FileScanner) scanFile(absPath, relPath string) (*protocol.FileInfo, error) {
	file, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}

	// Calculate number of chunks
	size := info.Size()
	numChunks := uint32((size + s.chunkSize - 1) / s.chunkSize)
	if size == 0 {
		numChunks = 1 // Even empty files have one "chunk"
	}

	// Hash the file and chunks
	fileHash, chunkHashes, err := s.hashFile(file, size)
	if err != nil {
		return nil, err
	}

	return &protocol.FileInfo{
		RelativePath: filepath.ToSlash(relPath), // Use forward slashes for cross-platform
		Size:         size,
		ModTime:      info.ModTime().Unix(),
		FileHash:     fileHash,
		NumChunks:    numChunks,
		ChunkHashes:  chunkHashes,
	}, nil
}

// hashFile computes file hash and chunk hashes in a single pass
func (s *FileScanner) hashFile(file *os.File, size int64) ([32]byte, [][32]byte, error) {
	fileHasher := blake3.New(32, nil)

	numChunks := int((size + s.chunkSize - 1) / s.chunkSize)
	if size == 0 {
		numChunks = 1
	}
	chunkHashes := make([][32]byte, 0, numChunks)

	buf := make([]byte, 256*1024) // 256KB read buffer
	var chunkBuf []byte
	chunkHasher := blake3.New(32, nil)

	for {
		n, err := file.Read(buf)
		if n > 0 {
			// Write to file hasher
			fileHasher.Write(buf[:n])

			// Accumulate for chunk hasher
			chunkBuf = append(chunkBuf, buf[:n]...)

			// Check if we've completed a chunk
			for int64(len(chunkBuf)) >= s.chunkSize {
				// Hash the chunk
				chunkHasher.Reset()
				chunkHasher.Write(chunkBuf[:s.chunkSize])
				var hash [32]byte
				chunkHasher.Sum(hash[:0])
				chunkHashes = append(chunkHashes, hash)

				// Move to next chunk
				chunkBuf = chunkBuf[s.chunkSize:]
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return [32]byte{}, nil, err
		}
	}

	// Hash remaining bytes (last partial chunk)
	if len(chunkBuf) > 0 || size == 0 {
		chunkHasher.Reset()
		chunkHasher.Write(chunkBuf)
		var hash [32]byte
		chunkHasher.Sum(hash[:0])
		chunkHashes = append(chunkHashes, hash)
	}

	var fileHash [32]byte
	fileHasher.Sum(fileHash[:0])

	return fileHash, chunkHashes, nil
}

// shouldExclude checks if a name should be excluded
func (s *FileScanner) shouldExclude(name string) bool {
	for _, exc := range s.excludes {
		if strings.EqualFold(name, exc) {
			return true
		}
		if strings.HasPrefix(name, ".") && exc == ".*" {
			return true
		}
	}
	return false
}

// BuildManifest creates a protocol Manifest from scan results
func BuildManifest(result *ScanResult) *protocol.Manifest {
	return &protocol.Manifest{
		TotalFiles: uint32(len(result.Files)),
		TotalSize:  result.TotalSize,
		Files:      result.Files,
	}
}

// ChunkIterator provides an iterator over file chunks
type ChunkIterator struct {
	files     []protocol.FileInfo
	chunkSize int64
	rootPath  string
}

// NewChunkIterator creates an iterator for the given files
func NewChunkIterator(files []protocol.FileInfo, chunkSize int64, rootPath string) *ChunkIterator {
	return &ChunkIterator{
		files:     files,
		chunkSize: chunkSize,
		rootPath:  rootPath,
	}
}

// AllChunks returns a channel of all chunks to transfer
func (ci *ChunkIterator) AllChunks() <-chan protocol.Chunk {
	ch := make(chan protocol.Chunk, 100)

	go func() {
		defer close(ch)

		for fileIdx, file := range ci.files {
			absPath := filepath.Join(ci.rootPath, filepath.FromSlash(file.RelativePath))

			for chunkIdx := uint32(0); chunkIdx < file.NumChunks; chunkIdx++ {
				offset := int64(chunkIdx) * ci.chunkSize
				size := ci.chunkSize
				if remaining := file.Size - offset; remaining < size {
					size = remaining
				}
				if size < 0 {
					size = 0
				}

				var hash [32]byte
				if int(chunkIdx) < len(file.ChunkHashes) {
					hash = file.ChunkHashes[chunkIdx]
				}

				ch <- protocol.Chunk{
					FileIndex:  uint32(fileIdx),
					ChunkIndex: chunkIdx,
					Offset:     offset,
					Size:       size,
					Hash:       hash,
					FilePath:   absPath,
				}
			}
		}
	}()

	return ch
}

// FilteredChunks returns only the chunks specified by the transfer plan
func (ci *ChunkIterator) FilteredChunks(plan *protocol.TransferPlan) <-chan protocol.Chunk {
	ch := make(chan protocol.Chunk, 100)

	go func() {
		defer close(ch)

		for _, decision := range plan.Decisions {
			if decision.Action == protocol.ActionSkip {
				continue
			}

			fileIdx := decision.FileIndex
			if int(fileIdx) >= len(ci.files) {
				continue
			}
			file := ci.files[fileIdx]
			absPath := filepath.Join(ci.rootPath, filepath.FromSlash(file.RelativePath))

			if decision.Action == protocol.ActionNeedComplete {
				// Send all chunks
				for chunkIdx := uint32(0); chunkIdx < file.NumChunks; chunkIdx++ {
					offset := int64(chunkIdx) * ci.chunkSize
					size := ci.chunkSize
					if remaining := file.Size - offset; remaining < size {
						size = remaining
					}
					if size < 0 {
						size = 0
					}

					var hash [32]byte
					if int(chunkIdx) < len(file.ChunkHashes) {
						hash = file.ChunkHashes[chunkIdx]
					}

					ch <- protocol.Chunk{
						FileIndex:  fileIdx,
						ChunkIndex: chunkIdx,
						Offset:     offset,
						Size:       size,
						Hash:       hash,
						FilePath:   absPath,
					}
				}
			} else if decision.Action == protocol.ActionNeedPartial {
				// Send only needed chunks
				for _, chunkIdx := range decision.NeededChunks {
					if chunkIdx >= file.NumChunks {
						continue
					}

					offset := int64(chunkIdx) * ci.chunkSize
					size := ci.chunkSize
					if remaining := file.Size - offset; remaining < size {
						size = remaining
					}
					if size < 0 {
						size = 0
					}

					var hash [32]byte
					if int(chunkIdx) < len(file.ChunkHashes) {
						hash = file.ChunkHashes[chunkIdx]
					}

					ch <- protocol.Chunk{
						FileIndex:  fileIdx,
						ChunkIndex: chunkIdx,
						Offset:     offset,
						Size:       size,
						Hash:       hash,
						FilePath:   absPath,
					}
				}
			}
		}
	}()

	return ch
}

// ProgressEvent represents a transfer progress update
type ProgressEvent struct {
	Type       ProgressType
	FileIndex  uint32
	ChunkIndex uint32
	FileName   string
	BytesSent  int64
	TotalBytes int64
	Error      error
	Timestamp  time.Time
}

// ProgressType indicates the type of progress event
type ProgressType int

const (
	ProgressChunkStarted ProgressType = iota
	ProgressChunkComplete
	ProgressChunkFailed
	ProgressFileStarted
	ProgressFileComplete
	ProgressFileFailed
	ProgressTransferComplete
)
