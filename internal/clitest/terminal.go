package clitest

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// expectTimeout bounds how long Expect waits for output.
const expectTimeout = 10 * time.Second

// Terminal is a jfl process running in a pseudo-terminal, the way a person
// runs it: stdin, stdout and stderr are all the terminal. It is how tests answer confirmations; the CLI has no other way
// in (ADR 0003).
type Terminal struct {
	t    testing.TB
	cmd  *exec.Cmd
	pty  *os.File
	mu   sync.Mutex
	out  bytes.Buffer
	more chan struct{} // signalled after each read; closed at end of output
	done chan struct{} // closed when the reader has seen the end of output
}

// TerminalResult is the outcome of a jfl process run in a Terminal.
type TerminalResult struct {
	Output   string // everything written to the terminal, with \r\n as \n
	ExitCode int
}

// StartInTerminal starts `jfl args...` in the project folder inside a new
// pseudo-terminal, as a person.
func (p *Project) StartInTerminal(args ...string) *Terminal {
	p.t.Helper()
	return p.startInTerminal("", args)
}

// StartInTerminalInSession starts `jfl args...` inside a new pseudo-terminal
// as the agent session with the given id, as when a coding agent runs its
// commands in a terminal.
func (p *Project) StartInTerminalInSession(session string, args ...string) *Terminal {
	p.t.Helper()
	return p.startInTerminal(session, args)
}

func (p *Project) startInTerminal(session string, args []string) *Terminal {
	p.t.Helper()
	cmd := exec.Command(p.bin.Jfl, args...)
	cmd.Dir = p.Dir
	cmd.Env = env(session)
	f, err := pty.Start(cmd)
	if err != nil {
		p.t.Fatalf("starting jfl %v in a pseudo-terminal: %v", args, err)
	}
	term := &Terminal{t: p.t, cmd: cmd, pty: f, more: make(chan struct{}, 1), done: make(chan struct{})}
	go term.read()
	p.t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = f.Close()
	})
	return term
}

func (term *Terminal) read() {
	defer close(term.done)
	buf := make([]byte, 4096)
	for {
		n, err := term.pty.Read(buf)
		term.mu.Lock()
		term.out.Write(buf[:n])
		term.mu.Unlock()
		select {
		case term.more <- struct{}{}:
		default:
		}
		if err != nil { // io.EOF, or EIO on Linux once the process has exited
			return
		}
	}
}

func (term *Terminal) output() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return strings.ReplaceAll(term.out.String(), "\r\n", "\n")
}

// Expect waits until the terminal has shown s, and fails the test if it
// doesn't within a few seconds or the process ends first.
func (term *Terminal) Expect(s string) {
	term.t.Helper()
	deadline := time.After(expectTimeout)
	for {
		if strings.Contains(term.output(), s) {
			return
		}
		select {
		case <-term.more:
		case <-term.done:
			if strings.Contains(term.output(), s) {
				return
			}
			term.t.Fatalf("jfl ended without showing %q; terminal:\n%s", s, term.output())
		case <-deadline:
			term.t.Fatalf("jfl didn't show %q within %v; terminal:\n%s", s, expectTimeout, term.output())
		}
	}
}

// Type sends keystrokes to the process, as a person typing at the terminal.
func (term *Terminal) Type(s string) {
	term.t.Helper()
	if _, err := term.pty.Write([]byte(s)); err != nil {
		term.t.Fatalf("typing %q: %v", s, err)
	}
}

// Wait waits for the process to exit and returns what it showed and its exit
// code.
func (term *Terminal) Wait() TerminalResult {
	term.t.Helper()
	select {
	case <-term.done:
	case <-time.After(expectTimeout):
		term.t.Fatalf("jfl didn't exit within %v; terminal:\n%s", expectTimeout, term.output())
	}
	code := 0
	if err := term.cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			term.t.Fatalf("waiting for jfl: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return TerminalResult{Output: term.output(), ExitCode: code}
}
