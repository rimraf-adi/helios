package tui

import (
	"sync"
	"time"

	"github.com/helios-transfer/helios/internal/transfer"
)

// TransferState represents the current state of the transfer
type TransferState int

const (
	StateIdle TransferState = iota
	StateScanning
	StateConnecting
	StateTransferring
	StateComplete
	StateError
)

// Model holds the TUI state
type Model struct {
	// Transfer state
	State         TransferState
	Mode          string // "send" or "receive"
	StatusMessage string
	ErrorMessage  string

	// Progress tracking
	TotalBytes       int64
	BytesTransferred int64
	TotalFiles       int
	FilesComplete    int
	TotalChunks      int64
	ChunksComplete   int64

	// Current file info
	CurrentFile         string
	CurrentFileSize     int64
	CurrentFileProgress int64

	// Speed and timing
	StartTime     time.Time
	CurrentSpeed  float64 // bytes per second
	speedHistory  []float64
	lastSpeedCalc time.Time
	lastBytes     int64

	// Stream info
	ActiveStreams int
	MaxStreams    int

	// Event log
	Events    []Event
	maxEvents int

	// UI state
	Width   int
	Height  int
	spinner Spinner

	// Progress bars
	overallProgress *GradientProgressBar
	fileProgress    *ProgressBar

	// Synchronization
	mu sync.RWMutex
}

// Event represents a log event
type Event struct {
	Time    time.Time
	Type    EventType
	Message string
}

// EventType categorizes events
type EventType int

const (
	EventInfo EventType = iota
	EventSuccess
	EventWarning
	EventError
)

// NewModel creates a new TUI model
func NewModel(mode string, maxStreams int) *Model {
	return &Model{
		Mode:            mode,
		State:           StateIdle,
		MaxStreams:      maxStreams,
		Events:          make([]Event, 0, 50),
		maxEvents:       10,
		overallProgress: NewGradientProgressBar(40),
		fileProgress:    NewProgressBar(40),
		speedHistory:    make([]float64, 0, 10),
		Width:           80,
		Height:          24,
	}
}

// UpdateFromProgress updates model from a progress event
func (m *Model) UpdateFromProgress(event transfer.ProgressEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch event.Type {
	case transfer.ProgressChunkComplete:
		m.BytesTransferred += event.BytesSent
		m.ChunksComplete++

	case transfer.ProgressFileStarted:
		m.CurrentFile = event.FileName
		m.CurrentFileSize = event.TotalBytes
		m.CurrentFileProgress = 0
		m.addEvent(EventInfo, "Started: "+truncateFileName(event.FileName, 40))

	case transfer.ProgressFileComplete:
		m.FilesComplete++
		m.addEvent(EventSuccess, "Complete: "+truncateFileName(event.FileName, 40))

	case transfer.ProgressFileFailed:
		errMsg := "unknown error"
		if event.Error != nil {
			errMsg = event.Error.Error()
		}
		m.addEvent(EventError, "Failed: "+truncateFileName(event.FileName, 30)+" - "+errMsg)

	case transfer.ProgressChunkFailed:
		m.addEvent(EventWarning, "Chunk retry: file "+string(rune('0'+event.FileIndex%10))+" chunk "+string(rune('0'+event.ChunkIndex%100)))

	case transfer.ProgressTransferComplete:
		m.State = StateComplete
		m.StatusMessage = "Transfer complete!"
	}

	// Update speed
	m.updateSpeed()
}

// UpdateStats updates statistics from sender/receiver stats
func (m *Model) UpdateStats(bytesSent, totalBytes int64, speed float64, filesComplete, filesTotal int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.BytesTransferred = bytesSent
	m.TotalBytes = totalBytes
	m.FilesComplete = filesComplete
	m.TotalFiles = filesTotal
	m.CurrentSpeed = speed
}

// SetState sets the current state
func (m *Model) SetState(state TransferState, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.State = state
	m.StatusMessage = message

	if state == StateTransferring && m.StartTime.IsZero() {
		m.StartTime = time.Now()
	}
}

// SetError sets an error state
func (m *Model) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.State = StateError
	m.ErrorMessage = err.Error()
	m.addEvent(EventError, err.Error())
}

// SetTotals sets the total counts
func (m *Model) SetTotals(files int, bytes int64, chunks int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.TotalFiles = files
	m.TotalBytes = bytes
	m.TotalChunks = chunks
}

// SetActiveStreams sets the active stream count
func (m *Model) SetActiveStreams(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ActiveStreams = count
}

// addEvent adds an event to the log
func (m *Model) addEvent(eventType EventType, message string) {
	m.Events = append(m.Events, Event{
		Time:    time.Now(),
		Type:    eventType,
		Message: message,
	})

	// Keep only recent events
	if len(m.Events) > m.maxEvents {
		m.Events = m.Events[len(m.Events)-m.maxEvents:]
	}
}

// updateSpeed calculates current transfer speed
func (m *Model) updateSpeed() {
	now := time.Now()
	if m.lastSpeedCalc.IsZero() {
		m.lastSpeedCalc = now
		m.lastBytes = m.BytesTransferred
		return
	}

	elapsed := now.Sub(m.lastSpeedCalc).Seconds()
	if elapsed >= 0.5 { // Update every 500ms
		speed := float64(m.BytesTransferred-m.lastBytes) / elapsed
		m.speedHistory = append(m.speedHistory, speed)
		if len(m.speedHistory) > 10 {
			m.speedHistory = m.speedHistory[1:]
		}

		// Average speed
		var total float64
		for _, s := range m.speedHistory {
			total += s
		}
		m.CurrentSpeed = total / float64(len(m.speedHistory))

		m.lastSpeedCalc = now
		m.lastBytes = m.BytesTransferred
	}
}

// GetProgress returns the overall progress (0.0 to 1.0)
func (m *Model) GetProgress() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.TotalBytes == 0 {
		return 0
	}
	return float64(m.BytesTransferred) / float64(m.TotalBytes)
}

// GetETA returns estimated time remaining in seconds
func (m *Model) GetETA() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.CurrentSpeed <= 0 {
		return 0
	}
	remaining := m.TotalBytes - m.BytesTransferred
	return int(float64(remaining) / m.CurrentSpeed)
}

// Tick advances animation state
func (m *Model) Tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spinner.Tick()
}

// truncateFileName truncates a file name to fit
func truncateFileName(name string, maxLen int) string {
	if len(name) <= maxLen {
		return name
	}
	return "..." + name[len(name)-maxLen+3:]
}
