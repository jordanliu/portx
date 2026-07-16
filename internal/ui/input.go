package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// InputOpts configures a single-line text prompt.
type InputOpts struct {
	Title       string
	Placeholder string
	Default     string
	Password    bool
	Hint        string
}

// Input shows a text field (arrow-ready bubbletea textinput). Enter submits.
// Non-TTY falls back to a plain prompt.
func Input(opts InputOpts) (string, error) {
	if !isInteractive() {
		return inputFallback(opts)
	}

	ti := textinput.New()
	ti.Placeholder = opts.Placeholder
	ti.SetValue(opts.Default)
	ti.Focus()
	ti.CharLimit = 512
	ti.Width = 48
	ti.Prompt = "  ❯ "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(ColorAccent)
	ti.TextStyle = ValueStyle
	ti.PlaceholderStyle = HintStyle
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(ColorAccent)
	if opts.Password {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}

	m := inputModel{title: opts.Title, hint: opts.Hint, input: ti}
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	out := final.(inputModel)
	if out.cancelled {
		return "", fmt.Errorf("cancelled")
	}
	v := strings.TrimSpace(out.input.Value())
	if v == "" {
		v = opts.Default
	}
	return v, nil
}

type inputModel struct {
	title     string
	hint      string
	input     textinput.Model
	cancelled bool
	done      bool
}

func (m inputModel) Init() tea.Cmd { return textinput.Blink }

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			m.done = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m inputModel) View() string {
	var b strings.Builder
	if m.title != "" {
		b.WriteString(TitleStyle.Render(m.title))
		b.WriteString("\n")
		b.WriteString(HintStyle.Render(strings.Repeat("─", min(40, max(8, len(m.title))))))
		b.WriteString("\n")
	}
	if m.hint != "" {
		// first line of hint only if multi-line
		hint := m.hint
		if i := strings.Index(hint, "\n"); i >= 0 {
			// keep multi-line but compact
			for _, line := range strings.Split(hint, "\n") {
				b.WriteString(HintStyle.Render(strings.TrimSpace(line)))
				b.WriteString("\n")
			}
		} else {
			b.WriteString(HintStyle.Render(hint))
			b.WriteString("\n")
		}
	}
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(HintStyle.Render("  enter  esc"))
	b.WriteString("\n")
	return b.String()
}

func inputFallback(opts InputOpts) (string, error) {
	if opts.Title != "" {
		Title(opts.Title)
	}
	if opts.Hint != "" {
		Dim("  %s", opts.Hint)
	}
	label := opts.Placeholder
	if opts.Default != "" {
		label = opts.Default
	}
	if label != "" {
		Prompt(fmt.Sprintf("  [%s]: ", label))
	} else {
		Prompt("  > ")
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(line)
	if v == "" {
		v = opts.Default
	}
	return v, nil
}
