package ui

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Choice is one selectable row.
type Choice struct {
	Label string
	Desc  string
	Value string // optional; defaults to Label
}

// Select shows an arrow-key menu. Returns the chosen index and choice.
// Falls back to numbered stdin input when stdin is not a TTY.
func Select(title string, choices []Choice) (int, Choice, error) {
	if len(choices) == 0 {
		return -1, Choice{}, fmt.Errorf("no choices")
	}
	if !isInteractive() {
		return selectFallback(title, choices)
	}

	m := selectModel{
		title:   title,
		choices: choices,
		cursor:  0,
		chosen:  -1,
	}
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return -1, Choice{}, err
	}
	out := final.(selectModel)
	if out.cancelled || out.chosen < 0 || out.chosen >= len(choices) {
		return -1, Choice{}, fmt.Errorf("cancelled")
	}
	return out.chosen, choices[out.chosen], nil
}

// Confirm is a yes/no arrow menu. defaultYes controls which option is first.
func Confirm(title string, defaultYes bool) (bool, error) {
	choices := []Choice{
		{Label: "No", Desc: "cancel", Value: "no"},
		{Label: "Yes", Desc: "continue", Value: "yes"},
	}
	if defaultYes {
		choices = []Choice{
			{Label: "Yes", Desc: "continue", Value: "yes"},
			{Label: "No", Desc: "cancel", Value: "no"},
		}
	}
	_, c, err := Select(title, choices)
	if err != nil {
		return false, err
	}
	return c.Value == "yes", nil
}

type selectModel struct {
	title     string
	choices   []Choice
	cursor    int
	chosen    int
	cancelled bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter", " ":
			m.chosen = m.cursor
			return m, tea.Quit
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			n := int(msg.String()[0] - '1')
			if n >= 0 && n < len(m.choices) {
				m.chosen = n
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m selectModel) View() string {
	var b strings.Builder
	if m.title != "" {
		b.WriteString(TitleStyle.Render(m.title))
		b.WriteString("\n")
		b.WriteString(HintStyle.Render(strings.Repeat("─", min(40, max(8, len(m.title))))))
		b.WriteString("\n")
	}

	itemStyle := lipgloss.NewStyle().Foreground(ColorWhite)
	selectedStyle := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(ColorMuted)
	cursorStyle := lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)

	labelW := 0
	for _, c := range m.choices {
		if w := len(c.Label); w > labelW {
			labelW = w
		}
	}

	for i, c := range m.choices {
		cursor := "  "
		style := itemStyle
		if i == m.cursor {
			cursor = cursorStyle.Render("❯ ")
			style = selectedStyle
		}
		line := style.Render(padRight(c.Label, labelW))
		if c.Desc != "" {
			line += "  " + descStyle.Render(c.Desc)
		}
		b.WriteString(cursor + line + "\n")
	}

	b.WriteString(HintStyle.Render("  ↑/↓  enter  q"))
	b.WriteString("\n")
	return b.String()
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func selectFallback(title string, choices []Choice) (int, Choice, error) {
	if title != "" {
		Title(title)
	}
	for i, c := range choices {
		line := c.Label
		if c.Desc != "" {
			line += "  -  " + c.Desc
		}
		Step(i+1, "%s", line)
	}
	Blank()
	Prompt("Choice [1]: ")
	var line string
	_, _ = fmt.Fscanln(os.Stdin, &line)
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, choices[0], nil
	}
	var n int
	if _, err := fmt.Sscanf(line, "%d", &n); err != nil || n < 1 || n > len(choices) {
		return 0, choices[0], nil
	}
	return n - 1, choices[n-1], nil
}
