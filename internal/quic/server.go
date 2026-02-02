package quic

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"

	"github.com/quic-go/quic-go"
)

// ServerConfig holds configuration for the QUIC server
type ServerConfig struct {
	MaxIncomingStreams         int64
	MaxStreamReceiveWindow     uint64
	MaxConnectionReceiveWindow uint64
	KeepAlivePeriod            int // seconds
	MaxIdleTimeout             int // seconds
}

// DefaultServerConfig returns sensible defaults
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		MaxIncomingStreams:         64,
		MaxStreamReceiveWindow:     16 * 1024 * 1024,  // 16MB per stream
		MaxConnectionReceiveWindow: 256 * 1024 * 1024, // 256MB per connection
		KeepAlivePeriod:            30,
		MaxIdleTimeout:             300, // 5 minutes
	}
}

// Server wraps a QUIC listener
type Server struct {
	listener *quic.Listener
	config   ServerConfig
}

// NewServer creates a new QUIC server
func NewServer(addr string, tlsConfig *tls.Config, config ServerConfig) (*Server, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve address: %w", err)
	}

	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on UDP: %w", err)
	}

	quicConfig := &quic.Config{
		MaxIncomingStreams:         config.MaxIncomingStreams,
		MaxStreamReceiveWindow:     config.MaxStreamReceiveWindow,
		MaxConnectionReceiveWindow: config.MaxConnectionReceiveWindow,
		KeepAlivePeriod:            0,
		EnableDatagrams:            false,
	}

	listener, err := quic.Listen(udpConn, tlsConfig, quicConfig)
	if err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("failed to create QUIC listener: %w", err)
	}

	return &Server{
		listener: listener,
		config:   config,
	}, nil
}

// Accept waits for and returns the next connection
func (s *Server) Accept(ctx context.Context) (*quic.Conn, error) {
	return s.listener.Accept(ctx)
}

// Close shuts down the server
func (s *Server) Close() error {
	return s.listener.Close()
}

// Addr returns the server's address
func (s *Server) Addr() net.Addr {
	return s.listener.Addr()
}
