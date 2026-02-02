package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/helios-transfer/helios/internal/config"
	"github.com/helios-transfer/helios/internal/quic"
	"github.com/helios-transfer/helios/internal/transfer"
	"github.com/helios-transfer/helios/internal/tui"
	"github.com/spf13/cobra"
)

var (
	cfgFile    string
	serverAddr string
	listenAddr string
	outputDir  string
	streams    int
	chunkSize  int
	noTUI      bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "helios",
		Short: "High-performance file transfer over QUIC",
		Long: `Helios is a fast, reliable file transfer tool using the QUIC protocol.
It supports parallel chunk transfers, resumable transfers, and provides
a beautiful terminal UI to monitor progress.`,
	}

	// Send command
	sendCmd := &cobra.Command{
		Use:   "send [path]",
		Short: "Send files to a Helios server",
		Args:  cobra.ExactArgs(1),
		RunE:  runSend,
	}
	sendCmd.Flags().StringVarP(&serverAddr, "to", "t", "", "Server address (host:port)")
	sendCmd.MarkFlagRequired("to")
	sendCmd.Flags().IntVarP(&streams, "streams", "s", 16, "Number of parallel streams")
	sendCmd.Flags().IntVarP(&chunkSize, "chunk-size", "c", 8, "Chunk size in MB")
	sendCmd.Flags().BoolVar(&noTUI, "no-tui", false, "Disable terminal UI")

	// Serve command
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start a Helios server to receive files",
		RunE:  runServe,
	}
	serveCmd.Flags().StringVarP(&listenAddr, "listen", "l", ":4433", "Listen address (host:port)")
	serveCmd.Flags().StringVarP(&outputDir, "output", "o", "./received", "Output directory")
	serveCmd.Flags().IntVarP(&streams, "streams", "s", 16, "Max parallel streams")
	serveCmd.Flags().BoolVar(&noTUI, "no-tui", false, "Disable terminal UI")

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Config file path")

	rootCmd.AddCommand(sendCmd, serveCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runSend(cmd *cobra.Command, args []string) error {
	sourcePath := args[0]

	// Load config
	cfg := config.Default()
	if cfgFile != "" {
		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
	}

	// Override with flags
	cfg.Network.MaxStreams = streams
	cfg.Network.ChunkSizeMB = chunkSize
	cfg.TUI.Enabled = !noTUI

	// Create context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nInterrupted, shutting down...")
		cancel()
	}()

	// Create TLS config
	tlsConfig := quic.ClientTLSConfig(cfg.Network.InsecureSkipVerify)

	// Connect to server
	fmt.Printf("Connecting to %s...\n", serverAddr)
	clientCfg := quic.DefaultClientConfig()
	clientCfg.MaxIncomingStreams = int64(cfg.Network.MaxStreams * 2)

	client, err := quic.Connect(ctx, serverAddr, tlsConfig, clientCfg)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	// Create sender
	sender := transfer.NewSender(cfg, client, sourcePath)

	if cfg.TUI.Enabled {
		// Run with TUI
		app := tui.NewApp("send", cfg.Network.MaxStreams)

		// Start transfer in background
		errCh := make(chan error, 1)
		go func() {
			app.SetState(tui.StateScanning, "Scanning files...")
			time.Sleep(100 * time.Millisecond) // Let TUI initialize

			app.SetState(tui.StateConnecting, "Connected, sending manifest...")
			time.Sleep(100 * time.Millisecond)

			app.SetState(tui.StateTransferring, "Transferring...")

			// Forward progress events
			go func() {
				for event := range sender.Progress() {
					app.SendProgress(event)
				}
			}()

			// Start periodic stats updates
			go func() {
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						stats := sender.Stats()
						sent, total, speed, fc, ft := stats.GetStats()
						app.SendStats(sent, total, speed, fc, ft)
					}
				}
			}()

			err := sender.Send(ctx)
			if err != nil {
				app.SetError(err)
			} else {
				app.SetState(tui.StateComplete, "Transfer complete!")
			}
			errCh <- err
		}()

		// Run TUI
		if err := app.Run(ctx); err != nil {
			return err
		}

		// Check for transfer error
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	} else {
		// Run without TUI
		go tui.RunSimple("send", sender.Progress())
		return sender.Send(ctx)
	}
}

func runServe(cmd *cobra.Command, args []string) error {
	// Load config
	cfg := config.Default()
	if cfgFile != "" {
		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
	}

	// Override with flags
	cfg.Network.MaxStreams = streams
	cfg.Transfer.OutputDir = outputDir
	cfg.TUI.Enabled = !noTUI

	// Create context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nInterrupted, shutting down...")
		cancel()
	}()

	// Generate TLS certificate
	cert, err := quic.GenerateSelfSignedCert()
	if err != nil {
		return fmt.Errorf("failed to generate certificate: %w", err)
	}
	tlsConfig := quic.ServerTLSConfig(cert)

	// Create server
	serverCfg := quic.DefaultServerConfig()
	serverCfg.MaxIncomingStreams = int64(cfg.Network.MaxStreams * 2)

	server, err := quic.NewServer(listenAddr, tlsConfig, serverCfg)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}
	defer server.Close()

	fmt.Printf("⚡ Helios server listening on %s\n", server.Addr())
	fmt.Printf("   Output directory: %s\n", outputDir)
	fmt.Println("   Waiting for connections...")

	// Accept connections
	for {
		conn, err := server.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // Clean shutdown
			}
			fmt.Fprintf(os.Stderr, "Accept error: %v\n", err)
			continue
		}

		fmt.Printf("\n📥 New connection from %s\n", conn.RemoteAddr())

		// Handle connection
		go func() {
			receiver := transfer.NewReceiver(cfg, outputDir)

			if cfg.TUI.Enabled {
				// Run with TUI for this connection
				app := tui.NewApp("receive", cfg.Network.MaxStreams)

				go func() {
					app.SetState(tui.StateTransferring, "Receiving files...")

					// Forward progress events
					go func() {
						for event := range receiver.Progress() {
							app.SendProgress(event)
						}
					}()

					err := receiver.HandleConnection(ctx, conn)
					if err != nil {
						app.SetError(err)
					} else {
						app.SetState(tui.StateComplete, "Transfer complete!")
					}
				}()

				if err := app.Run(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
				}
			} else {
				go tui.RunSimple("receive", receiver.Progress())
				if err := receiver.HandleConnection(ctx, conn); err != nil {
					fmt.Fprintf(os.Stderr, "Connection error: %v\n", err)
				}
			}
		}()
	}
}
