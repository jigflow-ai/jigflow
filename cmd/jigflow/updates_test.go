package main_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// The Dashboard's pages update themselves (ADR 0025): each listens to a
// stream on which jfl ui says only that the project changed.

// signalWithin is how long a change jfl writes may take to be signalled on
// the stream: about a second, with room for a slow machine.
const signalWithin = 3 * time.Second

// listen opens the Dashboard's change stream as a page's script does.
func listen(t *testing.T, ui *clitest.UI) *clitest.Stream {
	t.Helper()
	s := ui.Listen(ui.NewRequest(http.MethodGet, "/changes", nil))
	if s.Status != http.StatusOK || s.ContentType != "text/event-stream" {
		t.Fatalf("GET /changes: status %d, Content-Type %q, want 200 and an event stream", s.Status, s.ContentType)
	}
	return s
}

// wantSignal fails the test unless the stream says the project changed
// within signalWithin, and then waits until it has said all it will about
// that change, so the next change is signalled on its own.
func wantSignal(t *testing.T, s *clitest.Stream, change string) {
	t.Helper()
	e, ok := s.Next(signalWithin)
	if !ok {
		t.Fatalf("%s wasn't signalled on the stream within %s", change, signalWithin)
	}
	if e != "changed" {
		t.Errorf("the stream said %q of %s, want only that something changed", e, change)
	}
	for ok {
		_, ok = s.Next(1500 * time.Millisecond)
	}
}

func TestTheStreamSignalsEachChangeJflWritesToTheProject(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Login page")
	ui := p.StartUI()
	s := listen(t, ui)

	p.MustRun("create", "Ticket", "--title", "Signup page")
	wantSignal(t, s, "a new Artifact")
	agentNext(t, p)
	wantSignal(t, s, "a Ledger entry of a Focus change")
	agentMove(t, p, "T-1", "in-progress", 0)
	wantSignal(t, s, "an Artifact's Status")
	p.Write("author.yaml", typeProposal("a Playbook for notes", "Note", noteType, map[string]string{"write-note": writeNote}))
	if r := p.RunInSession("A", "propose", "author.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d: %s", r.ExitCode, r.Stderr)
	}
	wantSignal(t, s, "a new Proposal")
	if page := ui.Post("/proposals/P-1/approve", nil); page.Status != http.StatusOK {
		t.Fatalf("approving P-1: status %d\n%s", page.Status, text(page.HTML))
	}
	wantSignal(t, s, "an approved change to the Playbook")
}

func TestTheStreamSignalsNothingWhileNothingChanges(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Login page")
	ui := p.StartUI()
	s := listenAsPage(t, ui, get(t, ui, "/"))

	// Reading the project changes nothing, and neither does time passing.
	get(t, ui, "/ledger")
	p.MustRun("query")
	if e, ok := s.Next(2500 * time.Millisecond); ok {
		t.Errorf("the stream said %q while nothing changed", e)
	}
}

func TestTheStreamNeedsNoKeyAndServesOnlyThisMachine(t *testing.T) {
	p := ticketPlaybook(t)
	// A Dashboard an agent session started, only to look at, updates too.
	ui := p.StartUIInSession("A")
	s := listen(t, ui) // with no key, as any program on this machine

	p.MustRun("create", "Ticket", "--title", "Login page")
	wantSignal(t, s, "a new Artifact")

	req := ui.NewRequest(http.MethodGet, "/changes", nil)
	req.Host = "attacker.example:80"
	if s := ui.Listen(req); s.Status != http.StatusForbidden {
		t.Errorf("a stream for attacker.example got status %d, want 403", s.Status)
	}
}

func TestTheStreamEndsWhenJflUiStops(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()
	s := listen(t, ui)

	start := time.Now()
	ui.Stop()
	if !s.Ended(time.Second) {
		t.Error("the stream didn't end when jfl ui stopped")
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("jfl ui took %s to stop with a page listening, want it to stop at once", took)
	}
}

// streamOf is the change stream an inline script of a page listens to.
var streamOf = regexp.MustCompile(`(?s)<script>.*?new EventSource\("(/changes\?since=[^"]*)"\).*?</script>`)

// listens reports whether the page carries an inline script that listens to
// the change stream.
func listens(page string) bool { return streamOf.MatchString(page) }

// listenAsPage opens the change stream the page's script listens to.
func listenAsPage(t *testing.T, ui *clitest.UI, page string) *clitest.Stream {
	t.Helper()
	m := streamOf.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("the page doesn't listen to the change stream")
	}
	return ui.Listen(ui.NewRequest(http.MethodGet, m[1], nil))
}

func TestAChangeWrittenBeforeThePageListensIsSignalledAtOnce(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()
	page := get(t, ui, "/")

	// The agent moves on while the page loads, before its script listens.
	p.MustRun("create", "Ticket", "--title", "Login page")
	wantSignal(t, listenAsPage(t, ui, page), "a change written since the page was shown")

	// A page shown after the change has nothing to catch up on.
	if e, ok := listenAsPage(t, ui, get(t, ui, "/")).Next(2500 * time.Millisecond); ok {
		t.Errorf("the stream said %q to a page shown after the last change", e)
	}
}

func TestEveryPageListensToTheStreamToUpdateItself(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()

	for _, path := range []string{"/", "/workflows", "/ledger"} {
		if !listens(get(t, ui, path)) {
			t.Errorf("%s doesn't listen to the change stream", path)
		}
	}
	// A page that can't be shown now updates once it can.
	p.Write(".jigflow/playbook.yaml", "name: [broken\n")
	if page := ui.Get("/"); page.Status == http.StatusOK || !listens(page.HTML) {
		t.Errorf("the page saying why the backlog can't be shown (status %d) doesn't listen to the change stream", page.Status)
	}
}

func TestTheLedgerPageSaysAsOfWhenItsTimesAre(t *testing.T) {
	p := ticketPlaybook(t)
	p.At("2026-09-28T09:00:00Z")
	p.MustRun("create", "Ticket", "--title", "Login page")
	p.At("2026-09-28T10:15:00Z")
	ui := p.StartUI()

	page := get(t, ui, "/ledger")
	wantText(t, page, "Times as of")
	if !strings.Contains(page, `<time datetime="2026-09-28T10:15:00Z">`) {
		t.Errorf("the Ledger page doesn't say as of 2026-09-28T10:15:00Z when its times are:\n%s", text(page))
	}
}
