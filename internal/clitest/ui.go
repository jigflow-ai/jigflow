package clitest

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// UI is a running Dashboard (Seam C): `jfl ui` started in the project folder
// on a free loopback port, reached over HTTP.
type UI struct {
	t   testing.TB
	cmd *exec.Cmd
	// URL is where the Dashboard says it serves, e.g. http://127.0.0.1:41234/.
	URL string
}

// StartUI starts `jfl ui` as a person, on a port the system picks, and
// waits until it says where it serves. It is stopped when the test ends.
func (p *Project) StartUI() *UI {
	p.t.Helper()
	cmd := exec.Command(p.bin.Jfl, "ui", "--addr", "127.0.0.1:0")
	cmd.Dir = p.Dir
	cmd.Env = p.env("")
	out, err := cmd.StdoutPipe()
	if err != nil {
		p.t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		p.t.Fatal(err)
	}
	u := &UI{t: p.t, cmd: cmd}
	p.t.Cleanup(u.Stop)

	line := make(chan string, 1)
	go func() {
		l, _ := bufio.NewReader(out).ReadString('\n')
		line <- l
		_, _ = io.Copy(io.Discard, out)
	}()
	select {
	case l := <-line:
		i := strings.Index(l, "http://")
		if i < 0 {
			_ = cmd.Wait()
			p.t.Fatalf("jfl ui didn't say where it serves: %q\nstderr: %s", l, stderr.String())
		}
		u.URL = strings.TrimSpace(l[i:])
	case <-time.After(10 * time.Second):
		p.t.Fatalf("jfl ui didn't start within 10s\nstderr: %s", stderr.String())
	}
	return u
}

// Stop interrupts the Dashboard, as Ctrl-C would, and waits for it.
func (u *UI) Stop() {
	if u.cmd.ProcessState != nil {
		return
	}
	if err := u.cmd.Process.Signal(os.Interrupt); err != nil {
		_ = u.cmd.Process.Kill() // no interrupt to send, as on Windows
	}
	done := make(chan struct{})
	go func() { _ = u.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = u.cmd.Process.Kill()
		<-done
	}
}

// Page is one response of the Dashboard.
type Page struct {
	Status int
	HTML   string
}

// Get requests the path, e.g. "/ledger", and returns the response.
func (u *UI) Get(path string) Page {
	u.t.Helper()
	return u.Do(mustRequest(u.t, http.MethodGet, strings.TrimSuffix(u.URL, "/")+path))
}

// Do sends the request to the Dashboard and returns the response.
func (u *UI) Do(req *http.Request) Page {
	u.t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		u.t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		u.t.Fatal(err)
	}
	return Page{Status: resp.StatusCode, HTML: string(body)}
}

func mustRequest(t testing.TB, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
