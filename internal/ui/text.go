package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Text helpers for non-TUI flows (setup, doctor, cloudflared install).

func Title(s string) {
	fmt.Println(TitleStyle.Render(s))
	fmt.Println(HintStyle.Render(strings.Repeat("─", min(40, max(8, len(s))))))
}

func Blank() { fmt.Println() }

func Info(format string, args ...any) {
	fmt.Println(ValueStyle.Render(fmt.Sprintf(format, args...)))
}

func Dim(format string, args ...any) {
	fmt.Fprintln(os.Stderr, HintStyle.Render(fmt.Sprintf(format, args...)))
}

func Success(format string, args ...any) {
	fmt.Println(SuccessStyle.Render("✓  " + fmt.Sprintf(format, args...)))
}

func Warn(format string, args ...any) {
	fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Foreground(ColorOffline).Render("!  "+fmt.Sprintf(format, args...)))
}

func Step(n int, format string, args ...any) {
	num := CodeStyle.Render(fmt.Sprintf("  %d.", n))
	fmt.Println(num + " " + ValueStyle.Render(fmt.Sprintf(format, args...)))
}

func Prompt(label string) {
	// No fixed width: prompts like "Hostname namespace [*.zone]: " must not wrap mid-label.
	fmt.Print(lipgloss.NewStyle().Foreground(ColorLabel).Render(label))
}

func KeyValue(key, value string) {
	fmt.Println(Row(key, value))
}

func Code(cmd string) {
	fmt.Println(CodeStyle.Render("  " + cmd))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
