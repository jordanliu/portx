package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Check prints a diagnostic result line (doctor-style).
func CheckOK(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(SuccessStyle.Render("✓") + "  " + ValueStyle.Render(msg))
}

func CheckWarn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	fmt.Println(warn.Render("!") + "  " + ValueStyle.Render(msg))
}

func CheckFail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Println(ErrorStyle.Render("✗") + "  " + ValueStyle.Render(msg))
}

// Summary prints a doctor-style footer.
func Summary(passed, warned, failed int) {
	Blank()
	parts := fmt.Sprintf("%d passed", passed)
	if warned > 0 {
		parts += fmt.Sprintf("  ·  %d warning", warned)
		if warned != 1 {
			parts += "s"
		}
	}
	if failed > 0 {
		parts += fmt.Sprintf("  ·  %d failed", failed)
	}
	if failed > 0 {
		fmt.Println(ErrorStyle.Render(parts))
	} else if warned > 0 {
		fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(parts))
	} else {
		fmt.Println(SuccessStyle.Render(parts))
	}
}
