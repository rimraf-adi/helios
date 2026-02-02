package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Message types
const (
	MsgHello        uint8 = 0x01
	MsgHelloAck     uint8 = 0x02
	MsgManifest     uint8 = 0x03
	MsgTransferPlan uint8 = 0x04
	MsgChunk        uint8 = 0x05
	MsgChunkAck     uint8 = 0x06
	MsgFileComplete uint8 = 0x07
	MsgError        uint8 = 0xFF
)

// Transfer actions
const (
	ActionSkip         uint8 = 0x00
	ActionNeedComplete uint8 = 0x01
	ActionNeedPartial  uint8 = 0x02
)

// Ack status
const (
	AckOK        uint8 = 0x00
	AckCorrupted uint8 = 0x01
	AckRetry     uint8 = 0x02
)

// ProtocolVersion is the current protocol version
const ProtocolVersion uint16 = 1

// Hello is sent by client to initiate connection
type Hello struct {
	Version      uint16
	MaxStreams   uint16
	ChunkSize    uint32
	Capabilities uint32
}

// HelloAck is sent by server in response to Hello
type HelloAck struct {
	Version    uint16
	Accepted   bool
	MaxStreams uint16
}

// FileInfo describes a file to transfer
type FileInfo struct {
	RelativePath string
	Size         int64
	ModTime      int64
	FileHash     [32]byte
	NumChunks    uint32
	ChunkHashes  [][32]byte
}

// Manifest contains all files to be transferred
type Manifest struct {
	TotalFiles uint32
	TotalSize  int64
	Files      []FileInfo
}

// FileDecision describes what action to take for a file
type FileDecision struct {
	FileIndex    uint32
	Action       uint8
	NeededChunks []uint32
}

// TransferPlan is the server's response to a manifest
type TransferPlan struct {
	Decisions []FileDecision
}

// ChunkHeader precedes chunk data
type ChunkHeader struct {
	FileIndex  uint32
	ChunkIndex uint32
	Offset     int64
	Size       uint32
	Hash       [32]byte
}

// ChunkAck acknowledges a chunk
type ChunkAck struct {
	FileIndex  uint32
	ChunkIndex uint32
	Status     uint8
}

// FileComplete signals a file is fully received
type FileComplete struct {
	FileIndex uint32
	Success   bool
	FinalHash [32]byte
}

// Error represents a protocol error
type Error struct {
	Code    uint32
	Message string
}

// Chunk represents a file chunk to be transferred
type Chunk struct {
	FileIndex  uint32
	ChunkIndex uint32
	Offset     int64
	Size       int64
	Hash       [32]byte
	FilePath   string
}

// ChunkResult represents the result of a chunk transfer
type ChunkResult struct {
	FileIndex  uint32
	ChunkIndex uint32
	BytesSent  int64
	Success    bool
	Error      error
	RetryCount int
}

// writeUint8 writes a single byte
func writeUint8(w io.Writer, v uint8) error {
	_, err := w.Write([]byte{v})
	return err
}

// writeUint16 writes a uint16 in big-endian
func writeUint16(w io.Writer, v uint16) error {
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, v)
	_, err := w.Write(buf)
	return err
}

// writeUint32 writes a uint32 in big-endian
func writeUint32(w io.Writer, v uint32) error {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, v)
	_, err := w.Write(buf)
	return err
}

// writeUint64 writes a uint64 in big-endian
func writeUint64(w io.Writer, v uint64) error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	_, err := w.Write(buf)
	return err
}

// writeInt64 writes an int64 in big-endian
func writeInt64(w io.Writer, v int64) error {
	return writeUint64(w, uint64(v))
}

// writeString writes a length-prefixed string
func writeString(w io.Writer, s string) error {
	if err := writeUint32(w, uint32(len(s))); err != nil {
		return err
	}
	_, err := w.Write([]byte(s))
	return err
}

// writeBytes writes a length-prefixed byte slice
func writeBytes(w io.Writer, b []byte) error {
	if err := writeUint32(w, uint32(len(b))); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

// writeHash writes a 32-byte hash
func writeHash(w io.Writer, h [32]byte) error {
	_, err := w.Write(h[:])
	return err
}

// readUint8 reads a single byte
func readUint8(r io.Reader) (uint8, error) {
	buf := make([]byte, 1)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	return buf[0], nil
}

// readUint16 reads a uint16 in big-endian
func readUint16(r io.Reader) (uint16, error) {
	buf := make([]byte, 2)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(buf), nil
}

// readUint32 reads a uint32 in big-endian
func readUint32(r io.Reader) (uint32, error) {
	buf := make([]byte, 4)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf), nil
}

// readUint64 reads a uint64 in big-endian
func readUint64(r io.Reader) (uint64, error) {
	buf := make([]byte, 8)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(buf), nil
}

// readInt64 reads an int64 in big-endian
func readInt64(r io.Reader) (int64, error) {
	v, err := readUint64(r)
	return int64(v), err
}

// readString reads a length-prefixed string
func readString(r io.Reader) (string, error) {
	length, err := readUint32(r)
	if err != nil {
		return "", err
	}
	if length > 10*1024*1024 { // 10MB max
		return "", fmt.Errorf("string too long: %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// readHash reads a 32-byte hash
func readHash(r io.Reader) ([32]byte, error) {
	var h [32]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return h, err
	}
	return h, nil
}
