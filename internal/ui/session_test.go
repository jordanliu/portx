package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestSessionViewFillsTerminal(t *testing.T) {
	session := NewConnectingSession("Starting…")
	if view := session.View(); view != "" {
		t.Fatalf("dashboard rendered before terminal size was known: %q", view)
	}

	model, _ := session.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	session = model.(Session)

	view := session.View()
	if got := lipgloss.Height(view); got != session.height {
		t.Fatalf("dashboard height = %d, want %d", got, session.height)
	}

	if got := lipgloss.Width(view); got != session.width {
		t.Fatalf("dashboard width = %d, want %d", got, session.width)
	}
}

func TestSessionHeaderOnlyShowsProductName(t *testing.T) {
	session := NewConnectingSession("Starting…")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	session = model.(Session)

	header := session.header()
	if got := lipgloss.Height(header); got != 1 {
		t.Fatalf("header height = %d, want 1", got)
	}
	if !strings.Contains(header, "portx") {
		t.Fatalf("header does not contain product name: %q", header)
	}
	if strings.Contains(header, "quit") {
		t.Fatalf("header contains redundant quit hint: %q", header)
	}
}

func TestSessionSeparatesRouteNameFromForwarding(t *testing.T) {
	session := NewConnectingSession("Starting…")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	session = model.(Session)
	session.Connecting = false
	session.Status = "online"
	session.Profile = "personal"
	session.Mode = "managed"
	session.Routes = []Route{{
		Name:   "mcp",
		URL:    "https://motion-dev.example.com",
		Target: "http://127.0.0.1:8787",
	}}

	metadata := session.metadata()
	if !strings.Contains(metadata, "Route") || !strings.Contains(metadata, "mcp") {
		t.Fatalf("metadata is missing route name: %q", metadata)
	}
	if !strings.Contains(metadata, "Forwarding") || !strings.Contains(metadata, "https://motion-dev.example.com") {
		t.Fatalf("metadata is missing forwarding: %q", metadata)
	}
	forwardingLine := strings.Split(metadata, "\n")[4]
	if strings.Contains(forwardingLine, "mcp") {
		t.Fatalf("forwarding line contains route name: %q", forwardingLine)
	}
}

func TestSessionReadyLayoutUsesMeasuredTerminalSize(t *testing.T) {
	session := NewConnectingSession("Starting…")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 166, Height: 13})
	session = model.(Session)

	model, cmd := session.Update(readyMsg{
		URL:     "https://motion-dev.example.com",
		Target:  "http://127.0.0.1:8787",
		Mode:    "managed",
		Status:  "online",
		Profile: "personal",
	})
	session = model.(Session)
	if cmd == nil {
		t.Fatal("ready dashboard did not request a fresh terminal measurement")
	}

	panel := session.View()
	if got := lipgloss.Height(panel); got != 13 {
		t.Fatalf("dashboard height = %d, want 13", got)
	}
	if !strings.Contains(panel, "portx") {
		t.Fatal("dashboard header is missing from the first measured frame")
	}
	if strings.Contains(panel, "Version") {
		t.Fatal("dashboard includes the version row")
	}
	if !strings.Contains(panel, "↑/↓") {
		t.Fatal("dashboard footer is missing request scroll arrows")
	}
}

func TestSessionCompactsRoutesToKeepFooterVisible(t *testing.T) {
	session := NewConnectingSession("Starting…")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 166, Height: 13})
	session = model.(Session)
	session.Connecting = false
	session.Status = "online"
	session.Profile = "personal"
	session.Mode = "managed"
	for i := 0; i < 20; i++ {
		session.Routes = append(session.Routes, Route{
			Name:   "route",
			URL:    "https://example.test",
			Target: "http://127.0.0.1:3000",
		})
	}
	session.resizeRequests()

	view := session.View()
	if got := lipgloss.Height(view); got != 13 {
		t.Fatalf("dashboard height = %d, want 13", got)
	}
	if !strings.Contains(view, "20 routes") {
		t.Fatalf("dashboard did not summarize routes: %q", view)
	}
	if !strings.Contains(view, "scroll") {
		t.Fatalf("dashboard footer is not visible: %q", view)
	}
}

func TestSessionKeepsRequestStreamNoticeVisibleWhenCompacted(t *testing.T) {
	session := NewConnectingSession("Starting…")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 166, Height: 13})
	session = model.(Session)
	session.Connecting = false
	session.Status = "online"
	session.Profile = "personal"
	session.Mode = "managed"
	session.URL = "https://example.test"
	session.Target = "http://127.0.0.1:3000"

	model, _ = session.Update(noteMsg("Request logging stopped: connection closed"))
	session = model.(Session)
	view := session.View()
	if !strings.Contains(view, "Request logging stopped") {
		t.Fatalf("compacted dashboard hid stream failure: %q", view)
	}
}

func TestRequestPathWidthUsesAvailableTerminalSpace(t *testing.T) {
	if got := requestPathWidth(166); got != requestPathMaxWidth {
		t.Fatalf("wide terminal path width = %d, want %d", got, requestPathMaxWidth)
	}
	if got := requestPathWidth(100); got != 62 {
		t.Fatalf("medium terminal path width = %d, want 62", got)
	}
	if got := requestPathWidth(60); got != requestPathMinWidth {
		t.Fatalf("narrow terminal path width = %d, want %d", got, requestPathMinWidth)
	}
}

func TestRequestPathTruncationPreservesUnicode(t *testing.T) {
	path := strings.Repeat("界", 40)
	got := truncateRequestPath(path, 28)
	if lipgloss.Width(got) > 28 {
		t.Fatalf("truncated path display width = %d, want at most 28", lipgloss.Width(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncated path = %q, want ellipsis", got)
	}
}

func TestSessionShowsResizeStateWhenTerminalIsTooShort(t *testing.T) {
	session := NewConnectingSession("Starting…")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 100, Height: 8})
	session = model.(Session)

	view := session.View()
	if got := lipgloss.Height(view); got != 8 {
		t.Fatalf("dashboard height = %d, want 8", got)
	}
	if !strings.Contains(view, "too short") {
		t.Fatalf("dashboard did not request a resize: %q", view)
	}
}

func TestSessionFooterFormatsKeyBindings(t *testing.T) {
	session := NewConnectingSession("Starting…")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	session = model.(Session)

	footer := session.footer()
	expected := []string{
		"↑/↓", "scroll", "PgUp/PgDn", "page", "Home/End",
		"first/last", "q", "quit", "•",
	}
	for _, text := range expected {
		if !strings.Contains(footer, text) {
			t.Fatalf("footer is missing %q: %q", text, footer)
		}
	}
	if got := lipgloss.Height(footer); got != 1 {
		t.Fatalf("footer height = %d, want 1", got)
	}
}

func TestSessionLeavesBlankRowAboveFooter(t *testing.T) {
	session := NewConnectingSession("Starting…")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	session = model.(Session)
	session.Connecting = false
	session.Status = "online"

	for i := 0; i < 30; i++ {
		model, _ = session.Update(requestMsg{request: RequestLog{
			Timestamp: time.Unix(int64(i), 0),
			Method:    "GET",
			Path:      "/health",
			Status:    200,
		}})
		session = model.(Session)
	}

	lines := strings.Split(session.View(), "\n")
	for index, line := range lines {
		if !strings.Contains(line, "scroll") {
			continue
		}
		if index < 2 {
			t.Fatalf("footer rendered too early: %q", session.View())
		}
		if strings.Contains(lines[index-1], "GET") {
			t.Fatalf("request touches footer: %q", lines[index-1])
		}
		if !strings.Contains(lines[index-2], "GET") {
			t.Fatalf("last request is not above footer spacing: %q", lines[index-2])
		}
		return
	}
	t.Fatal("footer was not rendered")
}

func TestSessionConnectingFooterStaysAtBottom(t *testing.T) {
	session := NewConnectingSession("Connecting Cloudflare tunnel")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	session = model.(Session)

	lines := strings.Split(session.View(), "\n")
	footerLine := lines[len(lines)-2]
	if !strings.Contains(footerLine, "↑/↓") {
		t.Fatalf("connecting footer is not above the bottom border: %q", footerLine)
	}
}

func TestSessionErrorIsPrintedAfterAltScreenCloses(t *testing.T) {
	expected := errors.New("hostname is already active")
	session := NewConnectingSession("Registering hostname")
	session.err = expected

	err := sessionRunError(session, nil, expected)
	if !errors.Is(err, expected) {
		t.Fatalf("session error = %v, want %v", err, expected)
	}
	var shown ShownError
	if errors.As(err, &shown) {
		t.Fatal("alternate-screen error was incorrectly marked as already shown")
	}
}

func TestSessionRequiresInteractiveTerminal(t *testing.T) {
	err := requireInteractiveSession(false)
	if err == nil || !strings.Contains(err.Error(), "use --json") {
		t.Fatalf("requireInteractiveSession error = %v, want --json guidance", err)
	}
	if err := requireInteractiveSession(true); err != nil {
		t.Fatalf("interactive terminal rejected: %v", err)
	}
}

func TestSessionScrollsRequestsWithoutMovingHeader(t *testing.T) {
	session := NewConnectingSession("Starting…")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	session = model.(Session)
	session.Connecting = false
	session.Status = "online"
	header := session.header()

	for i := 0; i < 20; i++ {
		model, _ = session.Update(requestMsg{request: RequestLog{
			Timestamp: time.Unix(int64(i), 0),
			Method:    "GET",
			Path:      "/health",
			Status:    200,
		}})
		session = model.(Session)
	}

	model, _ = session.Update(tea.KeyMsg{Type: tea.KeyUp})
	session = model.(Session)
	if session.requests.YOffset == 0 {
		t.Fatal("request viewport did not scroll")
	}
	if session.header() != header {
		t.Fatal("header changed while scrolling requests")
	}

	model, _ = session.Update(tea.KeyMsg{Type: tea.KeyEnd})
	session = model.(Session)
	if !session.requests.AtBottom() {
		t.Fatal("End did not move to the newest request")
	}

	model, _ = session.Update(tea.KeyMsg{Type: tea.KeyHome})
	session = model.(Session)
	if !session.requests.AtTop() {
		t.Fatal("Home did not move to the oldest request")
	}
}

func TestSessionResizePreservesBottomFollowing(t *testing.T) {
	session := NewConnectingSession("Starting…")
	model, _ := session.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	session = model.(Session)
	session.Connecting = false
	session.Status = "online"

	for i := 0; i < 20; i++ {
		model, _ = session.Update(requestMsg{request: RequestLog{
			Timestamp: time.Unix(int64(i), 0),
			Method:    "GET",
			Path:      "/health",
			Status:    200,
		}})
		session = model.(Session)
	}
	if !session.requests.AtBottom() {
		t.Fatal("request viewport did not start at the bottom")
	}

	model, _ = session.Update(tea.WindowSizeMsg{Width: 100, Height: 8})
	session = model.(Session)
	if !session.requests.AtBottom() {
		t.Fatal("request viewport stopped following the bottom after resize")
	}
}
