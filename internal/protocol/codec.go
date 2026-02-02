package protocol

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
)

// Codec handles message encoding and decoding
type Codec struct {
	writer *bufio.Writer
	reader *bufio.Reader
}

// NewCodec creates a new codec for the given reader/writer
func NewCodec(rw io.ReadWriter) *Codec {
	return &Codec{
		writer: bufio.NewWriterSize(rw, 64*1024),
		reader: bufio.NewReaderSize(rw, 64*1024),
	}
}

// NewCodecFromStreams creates a codec from separate reader and writer
func NewCodecFromStreams(r io.Reader, w io.Writer) *Codec {
	return &Codec{
		writer: bufio.NewWriterSize(w, 64*1024),
		reader: bufio.NewReaderSize(r, 64*1024),
	}
}

// Flush flushes the buffered writer
func (c *Codec) Flush() error {
	return c.writer.Flush()
}

// WriteHello encodes and writes a Hello message
func (c *Codec) WriteHello(h *Hello) error {
	// Calculate message size
	size := uint32(2 + 2 + 4 + 4) // version + maxStreams + chunkSize + capabilities

	// Write length prefix and type
	if err := writeUint32(c.writer, size+1); err != nil {
		return err
	}
	if err := writeUint8(c.writer, MsgHello); err != nil {
		return err
	}

	// Write fields
	if err := writeUint16(c.writer, h.Version); err != nil {
		return err
	}
	if err := writeUint16(c.writer, h.MaxStreams); err != nil {
		return err
	}
	if err := writeUint32(c.writer, h.ChunkSize); err != nil {
		return err
	}
	if err := writeUint32(c.writer, h.Capabilities); err != nil {
		return err
	}

	return c.writer.Flush()
}

// ReadHello reads and decodes a Hello message
func (c *Codec) ReadHello() (*Hello, error) {
	// Read length and type
	length, err := readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	msgType, err := readUint8(c.reader)
	if err != nil {
		return nil, err
	}
	if msgType != MsgHello {
		return nil, fmt.Errorf("expected Hello message, got %d", msgType)
	}
	_ = length // already validated by reading all fields

	h := &Hello{}
	h.Version, err = readUint16(c.reader)
	if err != nil {
		return nil, err
	}
	h.MaxStreams, err = readUint16(c.reader)
	if err != nil {
		return nil, err
	}
	h.ChunkSize, err = readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	h.Capabilities, err = readUint32(c.reader)
	if err != nil {
		return nil, err
	}

	return h, nil
}

// WriteHelloAck encodes and writes a HelloAck message
func (c *Codec) WriteHelloAck(h *HelloAck) error {
	size := uint32(2 + 1 + 2) // version + accepted + maxStreams

	if err := writeUint32(c.writer, size+1); err != nil {
		return err
	}
	if err := writeUint8(c.writer, MsgHelloAck); err != nil {
		return err
	}
	if err := writeUint16(c.writer, h.Version); err != nil {
		return err
	}
	accepted := uint8(0)
	if h.Accepted {
		accepted = 1
	}
	if err := writeUint8(c.writer, accepted); err != nil {
		return err
	}
	if err := writeUint16(c.writer, h.MaxStreams); err != nil {
		return err
	}

	return c.writer.Flush()
}

// ReadHelloAck reads and decodes a HelloAck message
func (c *Codec) ReadHelloAck() (*HelloAck, error) {
	_, err := readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	msgType, err := readUint8(c.reader)
	if err != nil {
		return nil, err
	}
	if msgType != MsgHelloAck {
		return nil, fmt.Errorf("expected HelloAck message, got %d", msgType)
	}

	h := &HelloAck{}
	h.Version, err = readUint16(c.reader)
	if err != nil {
		return nil, err
	}
	accepted, err := readUint8(c.reader)
	if err != nil {
		return nil, err
	}
	h.Accepted = accepted == 1
	h.MaxStreams, err = readUint16(c.reader)
	if err != nil {
		return nil, err
	}

	return h, nil
}

// WriteManifest encodes and writes a Manifest message
func (c *Codec) WriteManifest(m *Manifest) error {
	// Write header with placeholder size
	sizePos := 0 // We'll need to calculate and write the size

	// Build the entire message in memory first for accurate size
	buf := make([]byte, 0, 4096)

	// Message type
	buf = append(buf, MsgManifest)

	// Fields
	buf = binary.BigEndian.AppendUint32(buf, m.TotalFiles)
	buf = binary.BigEndian.AppendUint64(buf, uint64(m.TotalSize))

	// Files
	for _, f := range m.Files {
		// RelativePath
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(f.RelativePath)))
		buf = append(buf, f.RelativePath...)

		// Size
		buf = binary.BigEndian.AppendUint64(buf, uint64(f.Size))

		// ModTime
		buf = binary.BigEndian.AppendUint64(buf, uint64(f.ModTime))

		// FileHash
		buf = append(buf, f.FileHash[:]...)

		// NumChunks
		buf = binary.BigEndian.AppendUint32(buf, f.NumChunks)

		// ChunkHashes count and data
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(f.ChunkHashes)))
		for _, h := range f.ChunkHashes {
			buf = append(buf, h[:]...)
		}
	}

	// Write length prefix
	_ = sizePos
	if err := writeUint32(c.writer, uint32(len(buf))); err != nil {
		return err
	}

	// Write message body
	if _, err := c.writer.Write(buf); err != nil {
		return err
	}

	return c.writer.Flush()
}

// ReadManifest reads and decodes a Manifest message
func (c *Codec) ReadManifest() (*Manifest, error) {
	_, err := readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	msgType, err := readUint8(c.reader)
	if err != nil {
		return nil, err
	}
	if msgType != MsgManifest {
		return nil, fmt.Errorf("expected Manifest message, got %d", msgType)
	}

	m := &Manifest{}
	m.TotalFiles, err = readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	m.TotalSize, err = readInt64(c.reader)
	if err != nil {
		return nil, err
	}

	m.Files = make([]FileInfo, m.TotalFiles)
	for i := uint32(0); i < m.TotalFiles; i++ {
		f := &m.Files[i]

		f.RelativePath, err = readString(c.reader)
		if err != nil {
			return nil, err
		}
		f.Size, err = readInt64(c.reader)
		if err != nil {
			return nil, err
		}
		f.ModTime, err = readInt64(c.reader)
		if err != nil {
			return nil, err
		}
		f.FileHash, err = readHash(c.reader)
		if err != nil {
			return nil, err
		}
		f.NumChunks, err = readUint32(c.reader)
		if err != nil {
			return nil, err
		}

		hashCount, err := readUint32(c.reader)
		if err != nil {
			return nil, err
		}
		f.ChunkHashes = make([][32]byte, hashCount)
		for j := uint32(0); j < hashCount; j++ {
			f.ChunkHashes[j], err = readHash(c.reader)
			if err != nil {
				return nil, err
			}
		}
	}

	return m, nil
}

// WriteTransferPlan encodes and writes a TransferPlan message
func (c *Codec) WriteTransferPlan(p *TransferPlan) error {
	buf := make([]byte, 0, 1024)
	buf = append(buf, MsgTransferPlan)

	// Number of decisions
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(p.Decisions)))

	for _, d := range p.Decisions {
		buf = binary.BigEndian.AppendUint32(buf, d.FileIndex)
		buf = append(buf, d.Action)
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(d.NeededChunks)))
		for _, chunk := range d.NeededChunks {
			buf = binary.BigEndian.AppendUint32(buf, chunk)
		}
	}

	if err := writeUint32(c.writer, uint32(len(buf))); err != nil {
		return err
	}
	if _, err := c.writer.Write(buf); err != nil {
		return err
	}

	return c.writer.Flush()
}

// ReadTransferPlan reads and decodes a TransferPlan message
func (c *Codec) ReadTransferPlan() (*TransferPlan, error) {
	_, err := readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	msgType, err := readUint8(c.reader)
	if err != nil {
		return nil, err
	}
	if msgType != MsgTransferPlan {
		return nil, fmt.Errorf("expected TransferPlan message, got %d", msgType)
	}

	count, err := readUint32(c.reader)
	if err != nil {
		return nil, err
	}

	p := &TransferPlan{
		Decisions: make([]FileDecision, count),
	}

	for i := uint32(0); i < count; i++ {
		d := &p.Decisions[i]
		d.FileIndex, err = readUint32(c.reader)
		if err != nil {
			return nil, err
		}
		d.Action, err = readUint8(c.reader)
		if err != nil {
			return nil, err
		}
		chunkCount, err := readUint32(c.reader)
		if err != nil {
			return nil, err
		}
		d.NeededChunks = make([]uint32, chunkCount)
		for j := uint32(0); j < chunkCount; j++ {
			d.NeededChunks[j], err = readUint32(c.reader)
			if err != nil {
				return nil, err
			}
		}
	}

	return p, nil
}

// WriteChunkHeader writes a chunk header (without data)
func (c *Codec) WriteChunkHeader(h *ChunkHeader) error {
	size := uint32(4 + 4 + 8 + 4 + 32) // fileIndex + chunkIndex + offset + size + hash

	if err := writeUint32(c.writer, size+1); err != nil {
		return err
	}
	if err := writeUint8(c.writer, MsgChunk); err != nil {
		return err
	}
	if err := writeUint32(c.writer, h.FileIndex); err != nil {
		return err
	}
	if err := writeUint32(c.writer, h.ChunkIndex); err != nil {
		return err
	}
	if err := writeInt64(c.writer, h.Offset); err != nil {
		return err
	}
	if err := writeUint32(c.writer, h.Size); err != nil {
		return err
	}
	if err := writeHash(c.writer, h.Hash); err != nil {
		return err
	}

	return c.writer.Flush()
}

// ReadChunkHeader reads a chunk header
func (c *Codec) ReadChunkHeader() (*ChunkHeader, error) {
	_, err := readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	msgType, err := readUint8(c.reader)
	if err != nil {
		return nil, err
	}
	if msgType != MsgChunk {
		return nil, fmt.Errorf("expected Chunk message, got %d", msgType)
	}

	h := &ChunkHeader{}
	h.FileIndex, err = readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	h.ChunkIndex, err = readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	h.Offset, err = readInt64(c.reader)
	if err != nil {
		return nil, err
	}
	h.Size, err = readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	h.Hash, err = readHash(c.reader)
	if err != nil {
		return nil, err
	}

	return h, nil
}

// WriteChunkAck writes a chunk acknowledgment
func (c *Codec) WriteChunkAck(a *ChunkAck) error {
	size := uint32(4 + 4 + 1) // fileIndex + chunkIndex + status

	if err := writeUint32(c.writer, size+1); err != nil {
		return err
	}
	if err := writeUint8(c.writer, MsgChunkAck); err != nil {
		return err
	}
	if err := writeUint32(c.writer, a.FileIndex); err != nil {
		return err
	}
	if err := writeUint32(c.writer, a.ChunkIndex); err != nil {
		return err
	}
	if err := writeUint8(c.writer, a.Status); err != nil {
		return err
	}

	return c.writer.Flush()
}

// ReadChunkAck reads a chunk acknowledgment
func (c *Codec) ReadChunkAck() (*ChunkAck, error) {
	_, err := readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	msgType, err := readUint8(c.reader)
	if err != nil {
		return nil, err
	}
	if msgType != MsgChunkAck {
		return nil, fmt.Errorf("expected ChunkAck message, got %d", msgType)
	}

	a := &ChunkAck{}
	a.FileIndex, err = readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	a.ChunkIndex, err = readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	a.Status, err = readUint8(c.reader)
	if err != nil {
		return nil, err
	}

	return a, nil
}

// WriteFileComplete writes a file complete message
func (c *Codec) WriteFileComplete(f *FileComplete) error {
	size := uint32(4 + 1 + 32) // fileIndex + success + hash

	if err := writeUint32(c.writer, size+1); err != nil {
		return err
	}
	if err := writeUint8(c.writer, MsgFileComplete); err != nil {
		return err
	}
	if err := writeUint32(c.writer, f.FileIndex); err != nil {
		return err
	}
	success := uint8(0)
	if f.Success {
		success = 1
	}
	if err := writeUint8(c.writer, success); err != nil {
		return err
	}
	if err := writeHash(c.writer, f.FinalHash); err != nil {
		return err
	}

	return c.writer.Flush()
}

// ReadFileComplete reads a file complete message
func (c *Codec) ReadFileComplete() (*FileComplete, error) {
	_, err := readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	msgType, err := readUint8(c.reader)
	if err != nil {
		return nil, err
	}
	if msgType != MsgFileComplete {
		return nil, fmt.Errorf("expected FileComplete message, got %d", msgType)
	}

	f := &FileComplete{}
	f.FileIndex, err = readUint32(c.reader)
	if err != nil {
		return nil, err
	}
	success, err := readUint8(c.reader)
	if err != nil {
		return nil, err
	}
	f.Success = success == 1
	f.FinalHash, err = readHash(c.reader)
	if err != nil {
		return nil, err
	}

	return f, nil
}

// WriteRawData writes raw chunk data directly
func (c *Codec) WriteRawData(data []byte) error {
	_, err := c.writer.Write(data)
	if err != nil {
		return err
	}
	return c.writer.Flush()
}

// ReadRawData reads raw chunk data
func (c *Codec) ReadRawData(size int) ([]byte, error) {
	data := make([]byte, size)
	_, err := io.ReadFull(c.reader, data)
	return data, err
}

// Reader returns the underlying reader
func (c *Codec) Reader() io.Reader {
	return c.reader
}

// Writer returns the underlying writer
func (c *Codec) Writer() io.Writer {
	return c.writer
}
