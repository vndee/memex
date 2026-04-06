package tui

import "github.com/charmbracelet/lipgloss"

// Color palette.
var (
	colorPrimary   = lipgloss.Color("63")  // purple
	colorSecondary = lipgloss.Color("39")  // blue
	colorAccent    = lipgloss.Color("212") // pink
	colorMuted     = lipgloss.Color("241") // gray
	colorSuccess   = lipgloss.Color("78")  // green
	colorWarning   = lipgloss.Color("214") // orange
	colorDanger    = lipgloss.Color("196") // red
	colorBorder    = lipgloss.Color("238") // dark gray
	colorHighlight = lipgloss.Color("229") // light yellow
	colorBg        = lipgloss.Color("235") // status bar bg
	colorTabActive = lipgloss.Color("63")  // active tab
	colorTabInact  = lipgloss.Color("240") // inactive tab
)

// Pane styles.
var (
	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder)

	activePaneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary)

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Padding(0, 1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	selectedStyle = lipgloss.NewStyle().
			Foreground(colorHighlight).
			Bold(true)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(colorBg).
			Padding(0, 1)

	statusBarKeyStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Background(colorBg).
				Bold(true)

	statusBarDescStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("246")).
				Background(colorBg)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorDanger)

	successStyle = lipgloss.NewStyle().
			Foreground(colorSuccess)

	labelStyle = lipgloss.NewStyle().
			Foreground(colorSecondary).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	tabActiveStyle = lipgloss.NewStyle().
			Foreground(colorHighlight).
			Background(colorTabActive).
			Bold(true).
			Padding(0, 1)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("250")).
				Background(colorTabInact).
				Padding(0, 1)

	kbActiveStyle = lipgloss.NewStyle().
			Foreground(colorHighlight).
			Bold(true)

	kbCursorStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("57"))

	kbMetaStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			PaddingLeft(4)

	countBadgeStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

)
