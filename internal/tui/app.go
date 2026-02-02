package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/helios-transfer/helios/internal/transfer"
)

// KeyMap defines keyboard shortcuts
type KeyMap struct {
	Quit       key.Binding
	IncreaseBW key.Binding
	DecreaseBW key.Binding
	Pause      key.Binding
}

// DefaultKeyMap returns default key bindings
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		IncreaseBW: key.NewBinding(
			key.WithKeys("+", "="),
			key.WithHelp("+", "increase bandwidth"),
		),
		DecreaseBW: key.NewBinding(
			key.WithKeys("-", "_"),
			key.WithHelp("-", "decrease bandwidth"),
		),
		Pause: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "pause"),
		),
	}
}

// App wraps the bubbletea program
type App struct {
	model   *Model
	program *tea.Program
	keyMap  KeyMap
	cancel  context.CancelFunc
	paused  bool
}

// NewApp creates a new TUI application
func NewApp(mode string, maxStreams int) *App {
	model := NewModel(mode, maxStreams)
	return &App{
		model:  model,
		keyMap: DefaultKeyMap(),
	}
}

// tickMsg is sent periodically to update animations
type tickMsg time.Time

// progressMsg wraps a progress event
type progressMsg transfer.ProgressEvent

// statsMsg carries stats update
type statsMsg struct {
	bytesSent     int64
	totalBytes    int64
	speed         float64
	filesComplete int
	filesTotal    int
}

// stateMsg changes state
type stateMsg struct {
	state   TransferState
	message string
}

// errorMsg carries an error
type errorMsg struct{ err error }

// totalsMsg sets totals
type totalsMsg struct {
	files  int
	bytes  int64
	chunks int64
}

// Init implements tea.Model
func (a *App) Init() tea.Cmd {
	return tea.Batch(
		tickCmd(),
	)
}

// Update implements tea.Model
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, a.keyMap.Quit):
			if a.cancel != nil {
				a.cancel()
			}
			return a, tea.Quit

		case key.Matches(msg, a.keyMap.Pause):
			a.paused = !a.paused
			return a, nil
		}

	case tea.WindowSizeMsg:
		a.model.Width = msg.Width
		a.model.Height = msg.Height
		return a, nil

	case tickMsg:
		a.model.Tick()
		return a, tickCmd()

	case progressMsg:
		a.model.UpdateFromProgress(transfer.ProgressEvent(msg))
		return a, nil

	case statsMsg:
		a.model.UpdateStats(msg.bytesSent, msg.totalBytes, msg.speed, msg.filesComplete, msg.filesTotal)
		return a, nil

	case stateMsg:
		a.model.SetState(msg.state, msg.message)
		if msg.state == StateComplete {
			// Auto-quit after 2 seconds on completion
			return a, tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
				return tea.Quit()
			})
		}
		return a, nil

	case totalsMsg:
		a.model.SetTotals(msg.files, msg.bytes, msg.chunks)
		return a, nil

	case errorMsg:
		a.model.SetError(msg.err)
		return a, nil
	}

	return a, nil
}

// View implements tea.Model
func (a *App) View() string {
	return a.model.View()
}

// tickCmd returns a command that ticks every 100ms
func tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Run starts the TUI application
func (a *App) Run(ctx context.Context) error {
	ctx, a.cancel = context.WithCancel(ctx)

	a.program = tea.NewProgram(a, tea.WithAltScreen())

	if _, err := a.program.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// SetState sets the transfer state
func (a *App) SetState(state TransferState, message string) {
	if a.program != nil {
		a.program.Send(stateMsg{state: state, message: message})
	}
}

// SetTotals sets the total file/byte counts
func (a *App) SetTotals(files int, bytes int64, chunks int64) {
	if a.program != nil {
		a.program.Send(totalsMsg{files: files, bytes: bytes, chunks: chunks})
	}
}

// SetError sets an error
func (a *App) SetError(err error) {
	if a.program != nil {
		a.program.Send(errorMsg{err: err})
	}
}

// SendProgress sends a progress event to the TUI
func (a *App) SendProgress(event transfer.ProgressEvent) {
	if a.program != nil {
		a.program.Send(progressMsg(event))
	}
}

// SendStats sends stats update
func (a *App) SendStats(bytesSent, totalBytes int64, speed float64, filesComplete, filesTotal int) {
	if a.program != nil {
		a.program.Send(statsMsg{
			bytesSent:     bytesSent,
			totalBytes:    totalBytes,
			speed:         speed,
			filesComplete: filesComplete,
			filesTotal:    filesTotal,
		})
	}
}

// Quit stops the TUI
func (a *App) Quit() {
	if a.program != nil {
		a.program.Quit()
	}
}

// RunWithProgress runs the TUI and listens to a progress channel
func (a *App) RunWithProgress(ctx context.Context, progress <-chan transfer.ProgressEvent) error {
	ctx, a.cancel = context.WithCancel(ctx)

	// Start progress listener
	go func() {
		for event := range progress {
			a.SendProgress(event)
		}
	}()

	a.program = tea.NewProgram(a, tea.WithAltScreen())

	if _, err := a.program.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	return nil
}

// RunSimple runs a simpler version without full TUI for non-interactive use
func RunSimple(mode string, progress <-chan transfer.ProgressEvent) {
	var lastPrint time.Time
	var totalBytes, sentBytes int64

	for event := range progress {
		switch event.Type {
		case transfer.ProgressFileStarted:
			fmt.Printf("\n%s Starting: %s\n", IconProgress, event.FileName)

		case transfer.ProgressFileComplete:
			fmt.Printf("%s Complete: %s\n", IconSuccess, event.FileName)

		case transfer.ProgressChunkComplete:
			sentBytes += event.BytesSent
			if time.Since(lastPrint) > 500*time.Millisecond {
				if totalBytes > 0 {
					percent := float64(sentBytes) / float64(totalBytes) * 100
					fmt.Printf("\r  Progress: %.1f%% (%s / %s)",
						percent, FormatBytes(sentBytes), FormatBytes(totalBytes))
				}
				lastPrint = time.Now()
			}

		case transfer.ProgressTransferComplete:
			fmt.Printf("\n\n%s Transfer complete! %s transferred\n",
				IconSuccess, FormatBytes(sentBytes))

		case transfer.ProgressFileFailed:
			fmt.Fprintf(os.Stderr, "\n%s Failed: %s - %v\n",
				IconError, event.FileName, event.Error)
		}
	}
}
