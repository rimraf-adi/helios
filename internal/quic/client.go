package quic

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"github.com/quic-go/quic-go"
)

// ClientConfig holds configuration for the QUIC client
type ClientConfig struct {
	MaxIncomingStreams         int64
	MaxStreamReceiveWindow     uint64
	MaxConnectionReceiveWindow uint64
	HandshakeTimeout           time.Duration
	MaxRetries                 int
	RetryBackoff               time.Duration
}

// DefaultClientConfig returns sensible defaults
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		MaxIncomingStreams:         64,
		MaxStreamReceiveWindow:     16 * 1024 * 1024,
		MaxConnectionReceiveWindow: 256 * 1024 * 1024,
		HandshakeTimeout:           30 * time.Second,
		MaxRetries:                 5,
		RetryBackoff:               time.Second,
	}
}

// Client wraps a QUIC connection
type Client struct {
	conn   *quic.Conn
	config ClientConfig
}

// Connect establishes a QUIC connection to the server
func Connect(ctx context.Context, addr string, tlsConfig *tls.Config, config ClientConfig) (*Client, error) {
	quicConfig := &quic.Config{
		MaxIncomingStreams:         config.MaxIncomingStreams,
		MaxStreamReceiveWindow:     config.MaxStreamReceiveWindow,
		MaxConnectionReceiveWindow: config.MaxConnectionReceiveWindow,
		KeepAlivePeriod:            30 * time.Second,
		EnableDatagrams:            false,
	}

	var conn *quic.Conn
	var err error

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := config.RetryBackoff * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		connCtx, cancel := context.WithTimeout(ctx, config.HandshakeTimeout)
		conn, err = quic.DialAddr(connCtx, addr, tlsConfig, quicConfig)
		cancel()

		if err == nil {
			break
		}
	}

	if err != nil {
		return nil, fmt.Errorf("failed to connect after %d attempts: %w", config.MaxRetries+1, err)
	}

	return &Client{
		conn:   conn,
		config: config,
	}, nil
}

// OpenStream opens a new unidirectional stream for sending data
func (c *Client) OpenStream(ctx context.Context) (*quic.SendStream, error) {
	return c.conn.OpenUniStreamSync(ctx)
}

// OpenBidirectionalStream opens a bidirectional stream
func (c *Client) OpenBidirectionalStream(ctx context.Context) (*quic.Stream, error) {
	return c.conn.OpenStreamSync(ctx)
}

// AcceptStream accepts an incoming stream from the server
func (c *Client) AcceptStream(ctx context.Context) (*quic.Stream, error) {
	return c.conn.AcceptStream(ctx)
}

// Close closes the connection
func (c *Client) Close() error {
	return c.conn.CloseWithError(0, "client closed")
}

// Connection returns the underlying QUIC connection
func (c *Client) Connection() *quic.Conn {
	return c.conn
}
