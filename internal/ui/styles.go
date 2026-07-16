package ui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	ColorMuted   = lipgloss.Color("244")
	ColorLabel   = lipgloss.Color("183") // soft purple like ngrok
	ColorOnline  = lipgloss.Color("114") // green
	ColorOffline = lipgloss.Color("203") // red
	ColorAccent  = lipgloss.Color("212") // pink/purple
	ColorWhite   = lipgloss.Color("255")
	ColorDim     = lipgloss.Color("240")

	TitleStyle = lipgloss.NewStyle().
			Foreground(ColorAccent).
			Bold(true)

	HeaderStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	LabelStyle = lipgloss.NewStyle().
			Foreground(ColorLabel)

	ValueStyle = lipgloss.NewStyle().
			Foreground(ColorWhite)

	OnlineStyle = lipgloss.NewStyle().
			Foreground(ColorOnline).
			Bold(true)

	OfflineStyle = lipgloss.NewStyle().
			Foreground(ColorOffline).
			Bold(true)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(ColorOffline).
			Bold(true)

	HintStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	CodeStyle = lipgloss.NewStyle().
			Foreground(ColorAccent)

	SuccessStyle = lipgloss.NewStyle().
			Foreground(ColorOnline).
			Bold(true)

	BannerStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorDim).
			Padding(0, 1)

	HelpStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)
)

func Row(label, value string) string {
	return LabelStyle.Render(label) + "  " + ValueStyle.Render(value)
}

// RowW is like Row but with an explicit label column width.
func RowW(width int, label, value string) string {
	style := lipgloss.NewStyle().Foreground(ColorLabel).Width(width)
	return style.Render(label) + "  " + ValueStyle.Render(value)
}

func StatusValue(online bool, text string) string {
	if online {
		return OnlineStyle.Render(text)
	}
	return OfflineStyle.Render(text)
}
