package bufferpool

import "sync"

// DefaultChunkSize is the default size for file chunks (8MB)
const DefaultChunkSize = 8 * 1024 * 1024

// Pool manages reusable byte buffers to reduce GC pressure
type Pool struct {
	pool     sync.Pool
	buffSize int
}

// New creates a new buffer pool with the specified buffer size
func New(size int) *Pool {
	return &Pool{
		buffSize: size,
		pool: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, size)
				return &buf
			},
		},
	}
}

// NewDefault creates a pool with default chunk size (8MB)
func NewDefault() *Pool {
	return New(DefaultChunkSize)
}

// Get retrieves a buffer from the pool
func (p *Pool) Get() []byte {
	bufPtr := p.pool.Get().(*[]byte)
	return *bufPtr
}

// Put returns a buffer to the pool
func (p *Pool) Put(buf []byte) {
	if cap(buf) >= p.buffSize {
		b := buf[:p.buffSize]
		p.pool.Put(&b)
	}
}

// Size returns the buffer size for this pool
func (p *Pool) Size() int {
	return p.buffSize
}
