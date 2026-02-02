package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// View renders the TUI
func (m *Model) View() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var builder strings.Builder

	// Header
	builder.WriteString(m.renderHeader())
	builder.WriteString("\n")

	// Status section
	builder.WriteString(m.renderStatus())
	builder.WriteString("\n")

	// Progress section
	if m.State == StateTransferring || m.State == StateComplete {
		builder.WriteString(m.renderProgress())
		builder.WriteString("\n")
	}

	// Event log
	builder.WriteString(m.renderEventLog())
	builder.WriteString("\n")

	// Footer with key bindings
	builder.WriteString(m.renderFooter())

	// Apply container style
	content := builder.String()
	return ContainerStyle.Width(m.Width - 4).Render(content)
}

// renderHeader renders the title bar
func (m *Model) renderHeader() string {
	title := "⚡ Helios File Transfer"
	if m.Mode == "send" {
		title += " " + IconSend + " Sending"
	} else {
		title += " " + IconReceive + " Receiving"
	}

	return TitleStyle.Render(title)
}

// renderStatus renders the status section
func (m *Model) renderStatus() string {
	var parts []string

	// Status indicator
	var statusText string
	switch m.State {
	case StateIdle:
		statusText = SubtitleStyle.Render("Ready")
	case StateScanning:
		statusText = m.spinner.View() + " " + StatusRunningStyle.Render("Scanning files...")
	case StateConnecting:
		statusText = m.spinner.View() + " " + StatusRunningStyle.Render("Connecting...")
	case StateTransferring:
		statusText = m.spinner.View() + " " + StatusRunningStyle.Render("Transferring")
	case StateComplete:
		statusText = StatusCompleteStyle.Render(IconSuccess + " Complete")
	case StateError:
		statusText = StatusErrorStyle.Render(IconError + " Error: " + m.ErrorMessage)
	}
	parts = append(parts, "Status: "+statusText)

	// Speed and ETA (if transferring)
	if m.State == StateTransferring || m.State == StateComplete {
		speedStr := SpeedStyle.Render(FormatSpeed(m.CurrentSpeed))
		parts = append(parts, StatLabelStyle.Render("Speed: ")+speedStr)

		if m.State == StateTransferring && m.CurrentSpeed > 0 {
			eta := m.GetETA()
			etaStr := ETAStyle.Render(FormatDuration(eta))
			parts = append(parts, StatLabelStyle.Render("ETA: ")+etaStr)
		}
	}

	// File counts
	if m.TotalFiles > 0 {
		fileCount := fmt.Sprintf("%d/%d files", m.FilesComplete, m.TotalFiles)
		parts = append(parts, StatLabelStyle.Render("Files: ")+StatValueStyle.Render(fileCount))
	}

	// Streams
	if m.State == StateTransferring {
		streamCount := fmt.Sprintf("%d/%d", m.ActiveStreams, m.MaxStreams)
		parts = append(parts, StatLabelStyle.Render("Streams: ")+StatValueStyle.Render(streamCount))
	}

	return strings.Join(parts, "  │  ")
}

// renderProgress renders the progress section
func (m *Model) renderProgress() string {
	var builder strings.Builder

	// Current file (if any)
	if m.CurrentFile != "" {
		builder.WriteString(SectionHeaderStyle.Render("Current File"))
		builder.WriteString("\n")
		builder.WriteString(FileNameStyle.Render(TruncatePath(m.CurrentFile, 50)))
		builder.WriteString("\n")

		// File progress bar
		if m.CurrentFileSize > 0 {
			m.fileProgress.SetProgress(float64(m.CurrentFileProgress) / float64(m.CurrentFileSize))
			builder.WriteString(m.fileProgress.View())
			builder.WriteString(" ")
			builder.WriteString(StatValueStyle.Render(FormatBytes(m.CurrentFileProgress)))
			builder.WriteString(StatLabelStyle.Render(" / "))
			builder.WriteString(StatValueStyle.Render(FormatBytes(m.CurrentFileSize)))
			builder.WriteString("\n")
		}
	}

	// Overall progress
	builder.WriteString("\n")
	builder.WriteString(SectionHeaderStyle.Render("Overall Progress"))
	builder.WriteString("\n")

	m.overallProgress.SetProgress(m.GetProgress())
	builder.WriteString(m.overallProgress.View())
	builder.WriteString("\n")

	// Bytes transferred
	bytesStr := fmt.Sprintf("%s / %s",
		FormatBytes(m.BytesTransferred),
		FormatBytes(m.TotalBytes))
	builder.WriteString(StatValueStyle.Render(bytesStr))

	return builder.String()
}

// renderEventLog renders the event log section
func (m *Model) renderEventLog() string {
	var builder strings.Builder

	builder.WriteString(SectionHeaderStyle.Render("Recent Events"))
	builder.WriteString("\n")

	if len(m.Events) == 0 {
		builder.WriteString(EventInfoStyle.Render("  No events yet"))
		return builder.String()
	}

	// Show last N events
	start := 0
	if len(m.Events) > 5 {
		start = len(m.Events) - 5
	}

	for _, event := range m.Events[start:] {
		var icon string
		var style lipgloss.Style

		switch event.Type {
		case EventSuccess:
			icon = IconSuccess
			style = EventSuccessStyle
		case EventWarning:
			icon = IconWarning
			style = EventWarningStyle
		case EventError:
			icon = IconError
			style = EventErrorStyle
		default:
			icon = IconProgress
			style = EventInfoStyle
		}

		timeStr := event.Time.Format("15:04:05")
		line := fmt.Sprintf("  %s %s %s",
			EventInfoStyle.Render(timeStr),
			style.Render(icon),
			style.Render(event.Message))
		builder.WriteString(line)
		builder.WriteString("\n")
	}

	return builder.String()
}

// renderFooter renders the key bindings footer
func (m *Model) renderFooter() string {
	keys := []struct {
		key  string
		desc string
	}{
		{"q", "quit"},
		{"+/-", "bandwidth"},
		{"p", "pause"},
	}

	var parts []string
	for _, k := range keys {
		parts = append(parts, KeyStyle.Render(k.key)+KeyDescStyle.Render(" "+k.desc))
	}

	return DividerStyle.Render(strings.Repeat("─", m.Width-8)) + "\n" + strings.Join(parts, "  ")
}
