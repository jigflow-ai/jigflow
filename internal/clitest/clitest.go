// Package clitest is the subprocess test harness for the jigflow CLI (Seam A).
//
// A test package calls Build from its TestMain to compile the binary once as a
// static executable (plus a `jfl` alias next to it), then uses NewProject to get
// a temporary project folder and Run to execute commands against it.
package clitest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Binary is a built jigflow executable and its `jfl` alias.
type Binary struct {
	Dir     string // directory holding both names
	Jigflow string // path to the `jigflow` executable
	Jfl     string // path to the `jfl` alias
}

// ClockEnv is the environment variable through which the tests set the time
// the CLI reads its clock at, as an RFC 3339 timestamp. Only the binary
// Build compiles reads it: a shipped jfl has no such variable, so no agent
// can set the time the Ledger records (ADR 0007).
const ClockEnv = "JFL_TEST_CLOCK"

// Build compiles the given main package (e.g. "github.com/jigflow-ai/jigflow/cmd/jigflow")
// with CGO disabled into a fresh temporary directory and links `jfl` to it.
// The binary reads its clock from ClockEnv when a test sets it.
// Call Cleanup on the result when done.
func Build(pkg string) (*Binary, error) {
	dir, err := os.MkdirTemp("", "jigflow-bin-")
	if err != nil {
		return nil, err
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	b := &Binary{
		Dir:     dir,
		Jigflow: filepath.Join(dir, "jigflow"+exe),
		Jfl:     filepath.Join(dir, "jfl"+exe),
	}
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-X github.com/jigflow-ai/jigflow/internal/cli.clockEnv="+ClockEnv, "-o", b.Jigflow, pkg)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Cleanup()
		return nil, fmt.Errorf("go build %s: %v\n%s", pkg, err, out)
	}
	if err := os.Symlink(b.Jigflow, b.Jfl); err != nil {
		b.Cleanup()
		return nil, err
	}
	return b, nil
}

// BuildHelper compiles another main package the tests run, such as a fake
// Connector, into the build directory, and returns the executable's path.
func (b *Binary) BuildHelper(pkg, name string) (string, error) {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(b.Dir, name)
	cmd := exec.Command("go", "build", "-trimpath", "-o", exe, pkg)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build %s: %v\n%s", pkg, err, out)
	}
	return exe, nil
}

// Cleanup removes the build directory.
func (b *Binary) Cleanup() { _ = os.RemoveAll(b.Dir) }

// Result is the outcome of one CLI invocation.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Project is a temporary project folder the CLI runs in.
type Project struct {
	t     testing.TB
	bin   *Binary
	Dir   string
	clock string   // the time commands read their clock at, or "" for the real one
	extra []string // further environment variables, as key=value
}

// Setenv sets an environment variable for every command run from now on,
// such as where Claude Code keeps its records.
func (p *Project) Setenv(key, value string) { p.extra = append(p.extra, key+"="+value) }

// At makes every command run from now on read its clock at the given time,
// an RFC 3339 timestamp such as "2026-09-28T09:00:00Z", as if that much time
// had passed.
func (p *Project) At(clock string) { p.clock = clock }

// NewProject creates an empty temporary project folder.
func (b *Binary) NewProject(t testing.TB) *Project {
	t.Helper()
	return &Project{t: t, bin: b, Dir: t.TempDir()}
}

// Write creates a file (and its parent directories) relative to the project root.
func (p *Project) Write(rel, content string) {
	p.t.Helper()
	path := filepath.Join(p.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// Read returns the content of a file relative to the project root.
func (p *Project) Read(rel string) string {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.Dir, rel))
	if err != nil {
		p.t.Fatal(err)
	}
	return string(data)
}

// SessionEnv is the environment variable through which Adapters give an
// agent session its session id.
const SessionEnv = "JFL_SESSION"

// Run executes `jfl args...` in the project folder, as a person without a
// terminal: no session id, and stdin that is not a TTY.
func (p *Project) Run(args ...string) Result {
	p.t.Helper()
	return p.run(p.bin.Jfl, "", args)
}

// RunInSession executes `jfl args...` as the agent session with the given id,
// the way an Adapter launches it.
func (p *Project) RunInSession(session string, args ...string) Result {
	p.t.Helper()
	return p.run(p.bin.Jfl, session, args)
}

// RunAs executes the given executable (e.g. Binary.Jigflow) in the project folder.
func (p *Project) RunAs(exe string, args ...string) Result {
	p.t.Helper()
	return p.run(exe, "", args)
}

// env is the test process's environment without a session id or clock, plus
// the given session id when it isn't empty and the project's clock when it
// is set, so tests don't depend on how `go test` was started.
func (p *Project) env(session string) []string {
	var e []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, SessionEnv+"=") && !strings.HasPrefix(kv, ClockEnv+"=") {
			e = append(e, kv)
		}
	}
	if session != "" {
		e = append(e, SessionEnv+"="+session)
	}
	if p.clock != "" {
		e = append(e, ClockEnv+"="+p.clock)
	}
	return append(e, p.extra...)
}

// RunWithInput executes `jfl args...` with stdin reading input, as the agent
// session with the given id, or as a person when it is empty: the way a
// coding agent runs a hook command.
func (p *Project) RunWithInput(session, input string, args ...string) Result {
	p.t.Helper()
	return p.runWith(p.bin.Jfl, session, strings.NewReader(input), args)
}

func (p *Project) run(exe, session string, args []string) Result {
	p.t.Helper()
	return p.runWith(exe, session, nil, args)
}

func (p *Project) runWith(exe, session string, stdin io.Reader, args []string) Result {
	p.t.Helper()
	cmd := exec.Command(exe, args...)
	cmd.Dir = p.Dir
	cmd.Env = p.env(session)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			p.t.Fatalf("running %s %v: %v", exe, args, err)
		}
		code = exitErr.ExitCode()
	}
	return Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
}

// MustRun runs `jfl args...` and fails the test unless it exits 0.
func (p *Project) MustRun(args ...string) Result {
	p.t.Helper()
	r := p.Run(args...)
	if r.ExitCode != 0 {
		p.t.Fatalf("jfl %v exited %d\nstdout: %s\nstderr: %s", args, r.ExitCode, r.Stdout, r.Stderr)
	}
	return r
}
