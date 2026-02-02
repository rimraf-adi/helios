package tui

import "github.com/charmbracelet/lipgloss"

// Colors for the TUI
var (
	// Primary colors
	ColorPrimary   = lipgloss.Color("#00D9FF") // Cyan
	ColorSecondary = lipgloss.Color("#FF6B9D") // Pink
	ColorSuccess   = lipgloss.Color("#00FF88") // Green
	ColorWarning   = lipgloss.Color("#FFB800") // Orange
	ColorError     = lipgloss.Color("#FF4757") // Red
	ColorMuted     = lipgloss.Color("#6B7280") // Gray

	// Background colors
	ColorBgDark   = lipgloss.Color("#0D1117")
	ColorBgMedium = lipgloss.Color("#161B22")
	ColorBgLight  = lipgloss.Color("#21262D")

	// Progress bar colors
	ColorProgressFill  = lipgloss.Color("#00D9FF")
	ColorProgressEmpty = lipgloss.Color("#30363D")
)

// Styles for different UI components
var (
	// Container styles
	ContainerStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary)

	// Title styles
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			MarginBottom(1)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true)

	// Status styles
	StatusRunningStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorPrimary)

	StatusCompleteStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorSuccess)

	StatusErrorStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorError)

	// Stats styles
	StatLabelStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	StatValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF"))

	SpeedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorSuccess)

	ETAStyle = lipgloss.NewStyle().
			Foreground(ColorWarning)

	// File name styles
	FileNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF"))

	FilePathStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Progress bar styles
	ProgressStyle = lipgloss.NewStyle()

	ProgressFilledStyle = lipgloss.NewStyle().
				Foreground(ColorProgressFill)

	ProgressEmptyStyle = lipgloss.NewStyle().
				Foreground(ColorProgressEmpty)

	PercentStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	// Event log styles
	EventLogStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(ColorBgLight).
			Padding(0, 1)

	EventSuccessStyle = lipgloss.NewStyle().
				Foreground(ColorSuccess)

	EventWarningStyle = lipgloss.NewStyle().
				Foreground(ColorWarning)

	EventErrorStyle = lipgloss.NewStyle().
			Foreground(ColorError)

	EventInfoStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Divider style
	DividerStyle = lipgloss.NewStyle().
			Foreground(ColorBgLight)

	// Key binding styles
	KeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	KeyDescStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Section header styles
	SectionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorSecondary).
				MarginTop(1)
)

// Icons for different states
const (
	IconSuccess  = "✓"
	IconError    = "✗"
	IconWarning  = "⚠"
	IconProgress = "●"
	IconPending  = "○"
	IconRetry    = "⟳"
	IconSpeed    = "↑"
	IconFile     = "📄"
	IconFolder   = "📁"
	IconSend     = "→"
	IconReceive  = "←"
)
