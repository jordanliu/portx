package ui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// StatusLine is a single-line spinner that rewrites in place on stderr.
type StatusLine struct {
	mu     sync.Mutex
	msg    string
	stopCh chan struct{}
	done   chan struct{}
	active bool
	width  int
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// StartStatus begins an inline spinner with the given message.
func StartStatus(msg string) *StatusLine {
	s := &StatusLine{
		msg:    msg,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
		width:  80,
	}
	if !isTTY(os.Stderr) {
		fmt.Fprintln(os.Stderr, HintStyle.Render("  "+msg+"…"))
		s.active = false
		close(s.done)
		return s
	}
	s.active = true
	go s.loop()
	return s
}

func (s *StatusLine) Set(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
	if !s.active {
		fmt.Fprintln(os.Stderr, HintStyle.Render("  "+msg+"…"))
	}
}

// Stop clears the spinner line.
func (s *StatusLine) Stop() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	s.mu.Unlock()
	close(s.stopCh)
	<-s.done
	s.clear()
}

// Fail clears the spinner and prints a failure line.
func (s *StatusLine) Fail(err error) {
	s.mu.Lock()
	msg := s.msg
	if s.active {
		s.active = false
		s.mu.Unlock()
		close(s.stopCh)
		<-s.done
	} else {
		s.mu.Unlock()
	}
	s.clear()
	if err != nil {
		fmt.Fprintln(os.Stderr, ErrorStyle.Render(fmt.Sprintf("✗  %s: %v", msg, err)))
	} else {
		fmt.Fprintln(os.Stderr, ErrorStyle.Render("✗  "+msg+" failed"))
	}
}

func (s *StatusLine) loop() {
	defer close(s.done)
	t := time.NewTicker(80 * time.Millisecond)
	defer t.Stop()
	i := 0
	spinStyle := lipgloss.NewStyle().Foreground(ColorAccent)
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.mu.Lock()
			msg := s.msg
			s.mu.Unlock()
			frame := spinStyle.Render(spinnerFrames[i%len(spinnerFrames)])
			i++
			// visible width approx without ansi
			plain := fmt.Sprintf("  %s  %s", spinnerFrames[(i-1)%len(spinnerFrames)], msg)
			line := fmt.Sprintf("  %s  %s", frame, ValueStyle.Render(msg))
			pad := s.width - len(plain)
			if pad < 0 {
				pad = 0
			}
			fmt.Fprintf(os.Stderr, "\r%s%s", line, strings.Repeat(" ", pad))
		}
	}
}

func (s *StatusLine) clear() {
	if !isTTY(os.Stderr) {
		return
	}
	fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", s.width))
}

func isTTY(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}
