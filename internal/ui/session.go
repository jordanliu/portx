package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"portx/internal/buildinfo"
)

// Route is one forwarding line in the session dashboard.
type Route struct {
	Name   string
	URL    string
	Target string
}

// Session is an ngrok-style live dashboard.
type Session struct {
	URL     string
	Target  string
	Mode    string
	Status  string
	Profile string
	Version string
	Note    string
	Routes  []Route

	Connecting bool
	Phase      string

	spinner  spinner.Model
	width    int
	quitting bool
	err      error
}

type (
	phaseMsg string
	readyMsg struct {
		URL, Target, Mode, Status, Profile, Note string
		Routes                                   []Route
	}
	errMsg struct{ err error }
)

func NewConnectingSession(phase string) Session {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(ColorAccent)
	return Session{
		Connecting: true,
		Phase:      phase,
		Status:     "connecting",
		Version:    buildinfo.Version,
		spinner:    s,
		width:      80,
	}
}

func (m Session) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m Session) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		}

	case phaseMsg:
		m.Phase = string(msg)
		return m, nil

	case readyMsg:
		m.Connecting = false
		m.URL = msg.URL
		m.Target = msg.Target
		m.Mode = msg.Mode
		m.Status = msg.Status
		m.Profile = msg.Profile
		m.Note = msg.Note
		m.Routes = msg.Routes
		if m.Status == "" {
			m.Status = "online"
		}
		return m, nil

	case errMsg:
		m.err = msg.err
		m.Connecting = false
		m.Status = "offline"
		m.quitting = true
		return m, tea.Quit

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Session) View() string {
	if m.quitting && m.err == nil {
		return ""
	}

	var b strings.Builder
	left := TitleStyle.Render("portx")
	right := HelpStyle.Render("ctrl+c quit")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	b.WriteString(left + strings.Repeat(" ", gap) + right + "\n")

	if m.Connecting {
		b.WriteString(m.spinner.View() + "  " + ValueStyle.Render(m.Phase) + "\n")
		return b.String()
	}

	if m.err != nil {
		body := OfflineStyle.Render("offline") + "\n"
		lines := strings.Split(strings.TrimSpace(m.err.Error()), "\n")
		if len(lines) > 0 {
			body += ErrorStyle.Render(lines[0]) + "\n"
			for _, line := range lines[1:] {
				if strings.TrimSpace(line) == "" {
					continue
				}
				body += HintStyle.Render(line) + "\n"
			}
		}
		b.WriteString(BannerStyle.Render(strings.TrimRight(body, "\n")) + "\n")
		return b.String()
	}

	var body strings.Builder
	online := strings.EqualFold(m.Status, "online")
	const col = 12
	body.WriteString(RowW(col, "Status", StatusValue(online, m.Status)) + "\n")
	if m.Version != "" {
		body.WriteString(RowW(col, "Version", m.Version) + "\n")
	}
	if m.Profile != "" {
		body.WriteString(RowW(col, "Profile", m.Profile) + "\n")
	}
	if m.Mode != "" {
		body.WriteString(RowW(col, "Mode", m.Mode) + "\n")
	}

	if len(m.Routes) > 0 {
		for i, r := range m.Routes {
			label := "Forwarding"
			if i > 0 {
				label = ""
			}
			fwd := r.URL
			if r.Target != "" {
				fwd = r.URL + " → " + r.Target
			}
			if r.Name != "" {
				fwd = CodeStyle.Render(r.Name) + "  " + fwd
			}
			body.WriteString(RowW(col, label, fwd) + "\n")
		}
	} else if m.URL != "" {
		fwd := m.URL
		if m.Target != "" {
			fwd = m.URL + " → " + m.Target
		}
		body.WriteString(RowW(col, "Forwarding", fwd) + "\n")
	}

	if m.Note != "" {
		body.WriteString("\n" + HintStyle.Render(m.Note) + "\n")
	}

	b.WriteString(BannerStyle.Render(strings.TrimRight(body.String(), "\n")) + "\n")
	b.WriteString(HelpStyle.Render("q / ctrl+c  stop") + "\n")
	return b.String()
}

func (m Session) Err() error { return m.err }

func SetPhase(p *tea.Program, phase string) {
	if p != nil {
		p.Send(phaseMsg(phase))
	}
}

// ReadyInfo is the online session payload.
type ReadyInfo struct {
	URL     string
	Target  string
	Mode    string
	Profile string
	Note    string
	Routes  []Route
}

func SetReady(p *tea.Program, info ReadyInfo) {
	if p == nil {
		return
	}
	if len(info.Routes) > 0 && info.URL == "" {
		info.URL = info.Routes[0].URL
		info.Target = info.Routes[0].Target
	}
	p.Send(readyMsg{
		URL: info.URL, Target: info.Target, Mode: info.Mode, Status: "online",
		Profile: info.Profile, Note: info.Note, Routes: info.Routes,
	})
}

func SetError(p *tea.Program, err error) {
	if p != nil {
		p.Send(errMsg{err: err})
	}
}

// RunSession runs the dashboard. bootstrap performs connect work and should call
// SetPhase / SetReady / SetError. After ready, onTick is called every interval
// until the user quits (for lease renewal).
func RunSession(bootstrap func(p *tea.Program) error, onTick func() error, tickEvery time.Duration) error {
	m := NewConnectingSession("Starting…")
	p := tea.NewProgram(m)

	done := make(chan struct{})
	errCh := make(chan error, 1)

	go func() {
		err := bootstrap(p)
		if err != nil {
			SetError(p, err)
			errCh <- err
			return
		}
		if onTick != nil {
			if tickEvery <= 0 {
				tickEvery = 15 * time.Second
			}
			t := time.NewTicker(tickEvery)
			defer t.Stop()
			for {
				select {
				case <-done:
					errCh <- nil
					return
				case <-t.C:
					if err := onTick(); err != nil {
						SetError(p, err)
						errCh <- err
						return
					}
				}
			}
		}
		<-done
		errCh <- nil
	}()

	finalModel, runErr := p.Run()
	close(done)
	bootErr := <-errCh

	if runErr != nil {
		return runErr
	}
	if sess, ok := finalModel.(Session); ok && sess.Err() != nil {
		return ShownError{Err: sess.Err()}
	}
	if bootErr != nil {
		return ShownError{Err: bootErr}
	}
	return nil
}

// ShownError means the UI already displayed the error.
type ShownError struct{ Err error }

func (e ShownError) Error() string { return e.Err.Error() }
func (e ShownError) Unwrap() error { return e.Err }

func PrintError(err error) {
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr)
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	if len(lines) > 0 {
		fmt.Fprintln(os.Stderr, ErrorStyle.Render("✗  "+lines[0]))
		for _, line := range lines[1:] {
			if strings.TrimSpace(line) == "" {
				fmt.Fprintln(os.Stderr)
				continue
			}
			fmt.Fprintln(os.Stderr, HintStyle.Render("   "+line))
		}
	}
	fmt.Fprintln(os.Stderr)
}

func PrintSetupReady(domain, tunnel string) {
	suffix := domain
	if strings.HasPrefix(domain, "*.") {
		suffix = domain[2:]
	}
	var body strings.Builder
	body.WriteString(SuccessStyle.Render("PortX is ready") + "\n")
	body.WriteString(RowW(8, "Domain", domain) + "\n")
	body.WriteString(RowW(8, "Tunnel", tunnel) + "\n")
	body.WriteString(HintStyle.Render("Try:") + "  ")
	body.WriteString(CodeStyle.Render("portx http --url=my-app 3000") + "\n")
	body.WriteString(HintStyle.Render(fmt.Sprintf("→ https://my-app.%s", suffix)))
	fmt.Println(BannerStyle.Render(body.String()))
}
