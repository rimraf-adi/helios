package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ProgressBar renders a progress bar
type ProgressBar struct {
	Width       int
	Filled      float64 // 0.0 to 1.0
	ShowPercent bool
}

// NewProgressBar creates a new progress bar
func NewProgressBar(width int) *ProgressBar {
	return &ProgressBar{
		Width:       width,
		ShowPercent: true,
	}
}

// SetProgress sets the fill percentage (0.0 to 1.0)
func (pb *ProgressBar) SetProgress(filled float64) {
	if filled < 0 {
		filled = 0
	}
	if filled > 1 {
		filled = 1
	}
	pb.Filled = filled
}

// View renders the progress bar as a string
func (pb *ProgressBar) View() string {
	// Calculate filled and empty portions
	filledWidth := int(float64(pb.Width) * pb.Filled)
	emptyWidth := pb.Width - filledWidth

	// Build the bar
	filled := strings.Repeat("█", filledWidth)
	empty := strings.Repeat("░", emptyWidth)

	bar := ProgressFilledStyle.Render(filled) + ProgressEmptyStyle.Render(empty)

	if pb.ShowPercent {
		percent := PercentStyle.Render(fmt.Sprintf(" %3.0f%%", pb.Filled*100))
		return bar + percent
	}

	return bar
}

// GradientProgressBar renders a gradient-style progress bar
type GradientProgressBar struct {
	Width  int
	Filled float64
	Chars  []string
	Colors []string
}

// NewGradientProgressBar creates a gradient progress bar
func NewGradientProgressBar(width int) *GradientProgressBar {
	return &GradientProgressBar{
		Width:  width,
		Chars:  []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"},
		Colors: []string{"#004D66", "#006680", "#008099", "#0099B3", "#00B3CC", "#00CCE6", "#00D9FF"},
	}
}

// SetProgress sets the fill percentage
func (gpb *GradientProgressBar) SetProgress(filled float64) {
	if filled < 0 {
		filled = 0
	}
	if filled > 1 {
		filled = 1
	}
	gpb.Filled = filled
}

// View renders the gradient progress bar
func (gpb *GradientProgressBar) View() string {
	totalChars := float64(gpb.Width * len(gpb.Chars))
	filledChars := gpb.Filled * totalChars

	var builder strings.Builder

	for i := 0; i < gpb.Width; i++ {
		charIndex := int(filledChars) - (i * len(gpb.Chars))

		if charIndex >= len(gpb.Chars) {
			// Full block with gradient color
			colorIdx := int(float64(i) / float64(gpb.Width) * float64(len(gpb.Colors)-1))
			if colorIdx >= len(gpb.Colors) {
				colorIdx = len(gpb.Colors) - 1
			}
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(gpb.Colors[colorIdx]))
			builder.WriteString(style.Render("█"))
		} else if charIndex > 0 && charIndex < len(gpb.Chars) {
			// Partial block
			colorIdx := int(float64(i) / float64(gpb.Width) * float64(len(gpb.Colors)-1))
			if colorIdx >= len(gpb.Colors) {
				colorIdx = len(gpb.Colors) - 1
			}
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(gpb.Colors[colorIdx]))
			builder.WriteString(style.Render(gpb.Chars[charIndex]))
		} else {
			// Empty
			builder.WriteString(ProgressEmptyStyle.Render("░"))
		}
	}

	percent := PercentStyle.Render(fmt.Sprintf(" %5.1f%%", gpb.Filled*100))
	return builder.String() + percent
}

// SpinnerFrames for animated spinner
var SpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner provides an animated spinner
type Spinner struct {
	frame int
}

// NewSpinner creates a new spinner
func NewSpinner() *Spinner {
	return &Spinner{}
}

// Tick advances the spinner
func (s *Spinner) Tick() {
	s.frame = (s.frame + 1) % len(SpinnerFrames)
}

// View renders the spinner
func (s *Spinner) View() string {
	return StatusRunningStyle.Render(SpinnerFrames[s.frame])
}

// FormatBytes formats bytes into human-readable format
func FormatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// FormatSpeed formats bytes per second into human-readable format
func FormatSpeed(bytesPerSec float64) string {
	const unit = 1024
	if bytesPerSec < unit {
		return fmt.Sprintf("%.0f B/s", bytesPerSec)
	}
	div, exp := float64(unit), 0
	for n := bytesPerSec / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB/s", bytesPerSec/div, "KMGTPE"[exp])
}

// FormatDuration formats seconds into human-readable duration
func FormatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh %dm", seconds/3600, (seconds%3600)/60)
}

// TruncatePath truncates a path to fit in width
func TruncatePath(path string, width int) string {
	if len(path) <= width {
		return path
	}
	if width < 10 {
		return path[:width]
	}
	// Keep first and last parts
	half := (width - 3) / 2
	return path[:half] + "..." + path[len(path)-half:]
}
