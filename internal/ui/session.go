package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Route is one forwarding line in the session dashboard.
type Route struct {
	Name   string
	URL    string
	Target string
}

type RequestLog struct {
	Timestamp time.Time
	Method    string
	Path      string
	Status    int
	Duration  time.Duration
	Bytes     int64
}

type footerBinding struct {
	key    string
	action string
}

const maxLiveRequests = 256

// Session is an ngrok-style live dashboard.
type Session struct {
	URL      string
	Target   string
	Mode     string
	Status   string
	Profile  string
	Note     string
	Routes   []Route
	Requests []RequestLog

	Connecting bool
	Phase      string

	spinner   spinner.Model
	requests  viewport.Model
	width     int
	height    int
	sizeReady bool
	quitting  bool
	err       error
}

type (
	phaseMsg string
	readyMsg struct {
		URL, Target, Mode, Status, Profile, Note string
		Routes                                   []Route
	}
	errMsg     struct{ err error }
	noteMsg    string
	requestMsg struct{ request RequestLog }
)

func NewConnectingSession(phase string) Session {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(ColorAccent)
	requests := viewport.New(1, 1)
	requests.SetContent(HintStyle.Render("No requests yet"))
	return Session{
		Connecting: true,
		Phase:      phase,
		Status:     "connecting",
		spinner:    s,
		requests:   requests,
	}
}

func (m Session) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tea.WindowSize())
}

func (m Session) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.sizeReady = true
		m.resizeRequests()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "home":
			m.requests.GotoTop()
			return m, nil
		case "end":
			m.requests.GotoBottom()
			return m, nil
		}
		var cmd tea.Cmd
		m.requests, cmd = m.requests.Update(msg)
		return m, cmd

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
		m.resizeRequests()
		return m, tea.WindowSize()

	case requestMsg:
		atBottom := m.requests.AtBottom()
		m.Requests = append(m.Requests, msg.request)
		if len(m.Requests) > maxLiveRequests {
			m.Requests = m.Requests[len(m.Requests)-maxLiveRequests:]
		}
		m.syncRequests()
		if atBottom {
			m.requests.GotoBottom()
		}
		return m, nil

	case noteMsg:
		m.Note = string(msg)
		m.resizeRequests()
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
	if !m.sizeReady {
		return ""
	}
	if m.quitting && m.err == nil {
		return ""
	}
	if m.height < 10 {
		return m.smallTerminalView()
	}

	if m.Connecting {
		content := strings.Join([]string{
			m.header(),
			"",
			m.spinner.View() + "  " + ValueStyle.Render(m.Phase),
		}, "\n")
		return m.renderDashboardWithFooter(content)
	}

	if m.err != nil {
		lines := []string{
			m.header(),
			"",
			OfflineStyle.Render("offline"),
		}
		errorLines := strings.Split(strings.TrimSpace(m.err.Error()), "\n")
		for _, line := range errorLines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			lines = append(lines, ErrorStyle.Render(line))
		}
		return m.renderDashboardWithFooter(strings.Join(lines, "\n"))
	}

	sections := []string{
		m.header(),
		"",
		m.visibleMetadata(),
		"",
		m.requestHeading(),
		m.requests.View(),
		"",
		m.footer(),
	}
	return m.renderDashboard(strings.Join(sections, "\n"))
}

func (m Session) header() string {
	return TitleStyle.Render("portx")
}

func (m Session) footer() string {
	bindings := []footerBinding{
		{key: "↑/↓", action: "scroll"},
		{key: "PgUp/PgDn", action: "page"},
		{key: "Home/End", action: "first/last"},
		{key: "q", action: "quit"},
	}
	footer := renderFooterBindings(bindings)
	if lipgloss.Width(footer) <= m.contentWidth() {
		return footer
	}

	return renderFooterBindings([]footerBinding{
		{key: "↑/↓", action: "scroll"},
		{key: "q", action: "quit"},
	})
}

func renderFooterBindings(bindings []footerBinding) string {
	parts := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		key := CodeStyle.Bold(true).Render(binding.key)
		parts = append(parts, key+" "+HelpStyle.Render(binding.action))
	}
	return strings.Join(parts, HelpStyle.Render("  •  "))
}

func (m Session) metadata() string {
	online := strings.EqualFold(m.Status, "online")
	lines := []string{
		RowW(12, "Status", StatusValue(online, m.Status)),
	}
	if m.Profile != "" {
		lines = append(lines, RowW(12, "Profile", m.Profile))
	}
	if m.Mode != "" {
		lines = append(lines, RowW(12, "Mode", m.Mode))
	}

	if len(m.Routes) > 0 {
		for i, route := range m.Routes {
			label := "Forwarding"
			if i > 0 {
				label = ""
			}
			forwarding := route.URL
			if route.Target != "" {
				forwarding = route.URL + " → " + route.Target
			}
			if route.Name != "" {
				forwarding = CodeStyle.Render(route.Name) + "  " + forwarding
			}
			lines = append(lines, RowW(12, label, forwarding))
		}
	} else if m.URL != "" {
		forwarding := m.URL
		if m.Target != "" {
			forwarding += " → " + m.Target
		}
		lines = append(lines, RowW(12, "Forwarding", forwarding))
	}

	if m.Note != "" {
		lines = append(lines, "", HintStyle.Render(m.Note))
	}
	return strings.Join(lines, "\n")
}

func (m Session) visibleMetadata() string {
	metadata := m.metadata()
	if lipgloss.Height(metadata) <= m.metadataBudget() {
		return metadata
	}
	return strings.Join(m.compactMetadata(m.metadataBudget()), "\n")
}

func (m Session) metadataBudget() int {
	const fixedLines = 7
	budget := m.contentHeight() - fixedLines
	if budget < 1 {
		return 1
	}
	return budget
}

func (m Session) compactMetadata(limit int) []string {
	lines := []string{RowW(12, "Status", StatusValue(strings.EqualFold(m.Status, "online"), m.Status))}
	if limit <= len(lines) {
		return lines[:limit]
	}

	forwarding := m.forwardingSummary()
	note := compactNote(m.Note)
	optionalSlots := limit - len(lines)
	if forwarding != "" {
		optionalSlots--
	}
	if note != "" {
		optionalSlots--
	}
	includeMode := m.Mode != "" && optionalSlots > 0
	if includeMode {
		optionalSlots--
	}
	includeProfile := m.Profile != "" && optionalSlots > 0
	if includeProfile {
		lines = append(lines, RowW(12, "Profile", m.Profile))
	}
	if includeMode {
		lines = append(lines, RowW(12, "Mode", m.Mode))
	}
	if forwarding != "" && len(lines) < limit {
		lines = append(lines, forwarding)
	}
	if note != "" && len(lines) < limit {
		lines = append(lines, HintStyle.Render(note))
	}
	return lines
}

func compactNote(note string) string {
	for _, line := range strings.Split(note, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func (m Session) forwardingSummary() string {
	if len(m.Routes) == 1 {
		return forwardingRow("Forwarding", m.Routes[0])
	}
	if len(m.Routes) > 1 {
		return RowW(12, "Forwarding", fmt.Sprintf("%d routes", len(m.Routes)))
	}
	if m.URL == "" {
		return ""
	}
	forwarding := m.URL
	if m.Target != "" {
		forwarding += " → " + m.Target
	}
	return RowW(12, "Forwarding", forwarding)
}

func forwardingRow(label string, route Route) string {
	forwarding := route.URL
	if route.Target != "" {
		forwarding += " → " + route.Target
	}
	if route.Name != "" {
		forwarding = CodeStyle.Render(route.Name) + "  " + forwarding
	}
	return RowW(12, label, forwarding)
}

func (m Session) requestHeading() string {
	count := len(m.Requests)
	if count == 0 {
		return HintStyle.Render("Requests")
	}
	return HintStyle.Render(fmt.Sprintf("Requests  %d", count))
}

func (m Session) renderDashboard(body string) string {
	contentHeight := m.contentHeight()
	body = padLines(strings.TrimRight(body, "\n"), contentHeight)
	return m.dashboardStyle().Render(body)
}

func (m Session) smallTerminalView() string {
	content := strings.Join([]string{
		m.header(),
		"",
		HintStyle.Render("Terminal is too short. Resize to continue."),
	}, "\n")
	return m.renderDashboardWithFooter(content)
}

func (m Session) renderDashboardWithFooter(content string) string {
	content = strings.TrimRight(content, "\n")
	footer := m.footer()
	padding := m.contentHeight() - lipgloss.Height(content) - lipgloss.Height(footer) + 1
	if padding < 1 {
		padding = 1
	}
	body := content + strings.Repeat("\n", padding) + footer
	return m.dashboardStyle().Render(body)
}

func (m Session) dashboardStyle() lipgloss.Style {
	return BannerStyle.
		Width(m.panelWidth()).
		Height(m.contentHeight())
}

func (m Session) panelWidth() int {
	width := m.width - 2
	if width < 1 {
		return 1
	}
	return width
}

func (m Session) contentWidth() int {
	width := m.width - 4
	if width < 1 {
		return 1
	}
	return width
}

func (m Session) contentHeight() int {
	height := m.height - 2
	if height < 1 {
		return 1
	}
	return height
}

func (m *Session) resizeRequests() {
	atBottom := m.requests.AtBottom()
	offset := m.requests.YOffset
	m.requests.Width = m.contentWidth()
	m.requests.Height = m.requestHeight()
	m.syncRequests()
	if atBottom {
		m.requests.GotoBottom()
		return
	}
	m.requests.SetYOffset(offset)
}

func (m Session) requestHeight() int {
	lines := lipgloss.Height(m.header()) + 1
	lines += lipgloss.Height(m.visibleMetadata()) + 1
	lines += lipgloss.Height(m.requestHeading())
	lines++ // Keep one blank row between requests and the fixed footer.
	lines += lipgloss.Height(m.footer())
	height := m.contentHeight() - lines
	if height < 1 {
		return 1
	}
	return height
}

func (m *Session) syncRequests() {
	if len(m.Requests) == 0 {
		m.requests.SetContent(HintStyle.Render("No requests yet"))
		return
	}

	lines := make([]string, 0, len(m.Requests))
	for _, request := range m.Requests {
		lines = append(lines, CodeStyle.Render(formatRequestLog(request)))
	}
	m.requests.SetContent(strings.Join(lines, "\n"))
}

func padLines(body string, height int) string {
	lineCount := lipgloss.Height(body)
	if lineCount >= height {
		return body
	}
	padding := height - lineCount - 2
	if padding < 0 {
		padding = 0
	}
	return body + strings.Repeat("\n", padding)
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

func AddRequest(p *tea.Program, request RequestLog) {
	if p != nil {
		p.Send(requestMsg{request: request})
	}
}

func SetNote(p *tea.Program, note string) {
	if p != nil {
		p.Send(noteMsg(note))
	}
}

func formatRequestLog(request RequestLog) string {
	return fmt.Sprintf(
		"%s  %-6s %-28s %3d %7s %8s",
		request.Timestamp.Local().Format("15:04:05"),
		request.Method,
		truncateRequestPath(request.Path),
		request.Status,
		formatRequestDuration(request.Duration),
		formatRequestBytes(request.Bytes),
	)
}

func truncateRequestPath(path string) string {
	const maxPathLength = 28
	if len(path) <= maxPathLength {
		return path
	}
	return path[:maxPathLength-3] + "..."
}

func formatRequestDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return "<1ms"
	}
	return duration.Round(time.Millisecond).String()
}

func formatRequestBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

// RunSession runs the dashboard. bootstrap performs connect work and should call
// SetPhase / SetReady / SetError. After ready, onTick is called every interval
// until the user quits (for lease renewal).
func RunSession(ctx context.Context, bootstrap func(context.Context, *tea.Program) error, onTick func() error, tickEvery time.Duration) error {
	if err := requireInteractiveSession(IsInteractive()); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := NewConnectingSession("Starting…")
	p := tea.NewProgram(m, tea.WithAltScreen())

	done := make(chan struct{})
	errCh := make(chan error, 1)
	uiDone := make(chan struct{})
	go func() {
		select {
		case <-sessionCtx.Done():
			p.Quit()
		case <-uiDone:
		}
	}()

	go func() {
		err := bootstrap(sessionCtx, p)
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
	close(uiDone)
	cancel()
	close(done)
	bootErr := <-errCh

	return sessionRunError(finalModel, runErr, bootErr)
}

func requireInteractiveSession(interactive bool) error {
	if interactive {
		return nil
	}
	return errors.New("interactive dashboard requires a terminal; use --json for non-interactive output")
}

func sessionRunError(finalModel tea.Model, runErr, bootErr error) error {
	if runErr != nil {
		return runErr
	}
	if sess, ok := finalModel.(Session); ok && sess.Err() != nil {
		return sess.Err()
	}
	if bootErr != nil {
		return bootErr
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

func PrintSetupProvisioned(domain, tunnel string) {
	suffix := domain
	if strings.HasPrefix(domain, "*.") {
		suffix = domain[2:]
	}
	var body strings.Builder
	body.WriteString(SuccessStyle.Render("PortX provisioning complete") + "\n")
	body.WriteString(RowW(8, "Domain", domain) + "\n")
	body.WriteString(RowW(8, "Tunnel", tunnel) + "\n")
	body.WriteString(HintStyle.Render("Try:") + "  ")
	body.WriteString(CodeStyle.Render("portx http 3000 --url=my-app") + "\n")
	body.WriteString(HintStyle.Render(fmt.Sprintf("→ https://my-app.%s", suffix)))
	fmt.Println(BannerStyle.Render(body.String()))
}
