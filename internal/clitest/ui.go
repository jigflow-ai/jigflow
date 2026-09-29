package clitest

import (
	"bufio"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// UI is a running Dashboard (Seam C): `jfl ui` started in the project folder
// on a free loopback port, reached over HTTP, either from the browser of the
// person who started it or by any other program on this machine.
type UI struct {
	t   testing.TB
	cmd *exec.Cmd
	// URL is where the Dashboard serves, e.g. http://127.0.0.1:41234/.
	URL string
	// Link is the link jfl ui printed for the person who started it to
	// open, e.g. http://127.0.0.1:41234/?key=…, or URL when it printed none.
	Link    string
	browser *http.Client
}

// StartUI starts `jfl ui` as a person, on a port the system picks, waits
// until it says where it serves, and opens the link it printed in the
// person's browser. It is stopped when the test ends.
func (p *Project) StartUI() *UI {
	p.t.Helper()
	return p.startUI("")
}

// StartUIInSession starts `jfl ui` as the agent session with the given id,
// the way an agent would start it, and opens the link it printed.
func (p *Project) StartUIInSession(session string) *UI {
	p.t.Helper()
	return p.startUI(session)
}

func (p *Project) startUI(session string) *UI {
	p.t.Helper()
	cmd := exec.Command(p.bin.Jfl, "ui", "--addr", "127.0.0.1:0")
	cmd.Dir = p.Dir
	cmd.Env = p.env(session)
	out, err := cmd.StdoutPipe()
	if err != nil {
		p.t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		p.t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		p.t.Fatal(err)
	}
	u := &UI{t: p.t, cmd: cmd, browser: &http.Client{Jar: jar}}
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
		u.Link, _, _ = strings.Cut(strings.TrimSpace(l[i:]), " ")
		link, err := url.Parse(u.Link)
		if err != nil {
			p.t.Fatalf("jfl ui printed %q, which isn't a link: %v", u.Link, err)
		}
		u.URL = (&url.URL{Scheme: link.Scheme, Host: link.Host, Path: "/"}).String()
	case <-time.After(10 * time.Second):
		p.t.Fatalf("jfl ui didn't start within 10s\nstderr: %s", stderr.String())
	}
	if page := u.Get(strings.TrimPrefix(u.Link, strings.TrimSuffix(u.URL, "/"))); page.Status != http.StatusOK {
		p.t.Fatalf("opening %s: status %d", u.Link, page.Status)
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

// Get requests the path, e.g. "/ledger", from the person's browser.
func (u *UI) Get(path string) Page {
	u.t.Helper()
	return u.Do(mustRequest(u.t, http.MethodGet, u.url(path), nil))
}

// Post submits a form to the path, e.g. "/proposals/P-1/approve", from the
// person's browser, which says the Dashboard's page sent it, and returns
// the page it answers with.
func (u *UI) Post(path string, form url.Values) Page {
	u.t.Helper()
	req := mustRequest(u.t, http.MethodPost, u.url(path), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", strings.TrimSuffix(u.URL, "/"))
	return u.Do(req)
}

// Do sends the request from the person's browser, with what the Dashboard
// gave it when it opened the link, and returns the response.
func (u *UI) Do(req *http.Request) Page {
	u.t.Helper()
	return u.send(u.browser, req)
}

// Curl sends the request as any other program on this machine, such as an
// agent's curl, which knows where the Dashboard serves but never opened the
// link printed in the person's terminal.
func (u *UI) Curl(req *http.Request) Page {
	u.t.Helper()
	return u.send(http.DefaultClient, req)
}

// NewRequest builds a request for the path, e.g. "/", with the form as its
// body when it isn't nil.
func (u *UI) NewRequest(method, path string, form url.Values) *http.Request {
	u.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := mustRequest(u.t, method, u.url(path), body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

func (u *UI) send(c *http.Client, req *http.Request) Page {
	u.t.Helper()
	resp, err := c.Do(req)
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

// Stream is the Dashboard's change stream, read as a page listens to it:
// the events it sends, one at a time, until it ends.
type Stream struct {
	// Status and ContentType are those of the response that opened it.
	Status      int
	ContentType string
	events      chan string
}

// Listen opens the change stream with the request, sent as any program on
// this machine sends it, with no key: a page's script listens with what the
// browser carries, which the stream doesn't need. The stream is closed when
// the test ends.
func (u *UI) Listen(req *http.Request) *Stream {
	u.t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		u.t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	done := make(chan struct{})
	u.t.Cleanup(func() { close(done); _ = resp.Body.Close() })
	s := &Stream{Status: resp.StatusCode, ContentType: resp.Header.Get("Content-Type"), events: make(chan string, 16)}
	go func() {
		defer close(s.events)
		r := bufio.NewReader(resp.Body)
		var data []string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if data != nil {
					select {
					case s.events <- strings.Join(data, "\n"):
					case <-done: // the test ended without reading it
						return
					}
				}
				data = nil
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
	}()
	return s
}

// Next waits up to within for the stream's next event and returns its
// data, or false when none came in that time or the stream ended.
func (s *Stream) Next(within time.Duration) (string, bool) {
	select {
	case e, ok := <-s.events:
		return e, ok
	case <-time.After(within):
		return "", false
	}
}

// Ended waits up to within for the stream to end, skipping any events sent
// meanwhile, and reports whether it did.
func (s *Stream) Ended(within time.Duration) bool {
	deadline := time.After(within)
	for {
		select {
		case _, ok := <-s.events:
			if !ok {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func (u *UI) url(path string) string { return strings.TrimSuffix(u.URL, "/") + path }

func mustRequest(t testing.TB, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
