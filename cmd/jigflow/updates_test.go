package main_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakegithub"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakelinear"
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

// changedBar is the bar a page shows, instead of loading again, when the
// project changes after a form on it was edited: its attributes and what it
// holds.
var changedBar = regexp.MustCompile(`(?s)<div\b([^>]*\bid="changed"[^>]*)>(.*?)</div>`)

var (
	hidden  = regexp.MustCompile(`(^|\s)hidden(\s|=|$)`)
	anchors = regexp.MustCompile(`(?s)<a\b([^>]*)>(.*?)</a>`)
)

func TestEveryPageHasABarToSayTheProjectChangedWithoutLosingEdits(t *testing.T) {
	p := proposedBreakdown(t)
	ui := p.StartUI()

	for _, path := range []string{"/", "/workflows", "/ledger"} {
		m := changedBar.FindStringSubmatch(get(t, ui, path))
		if m == nil {
			t.Errorf("%s has no bar to say the project changed", path)
			continue
		}
		// Hidden until the page's script shows it: with JavaScript off,
		// nothing ever says so, as before.
		if !hidden.MatchString(m[1]) {
			t.Errorf("%s shows the bar before the project changed: <div%s>", path, m[1])
		}
		wantText(t, m[2], "The project changed")
		a := anchors.FindStringSubmatch(m[2])
		if a == nil || attrs(a[1])["href"] != path || text(a[2]) != "Reload" {
			t.Errorf("%s's bar has no Reload link to the page it shows:\n%s", path, m[2])
		}
	}
}

func TestAProposalEditedBeforeTheProjectChangedIsApprovedWithTheEdits(t *testing.T) {
	p := proposedBreakdown(t)
	ui := p.StartUI()
	page := get(t, ui, "/")
	s := listenAsPage(t, ui, page)

	// The agent works on while the person edits P-1 on the page, which the
	// change doesn't reload: the form the page shows still approves.
	p.MustRun("create", "Issue", "--title", "Crash on logout")
	wantSignal(t, s, "a new Artifact")

	approved := submit(t, ui, section(t, page, "Pending Proposals"), "Approve all 4 changes", url.Values{
		"item-2-title":           {"Reset endpoint, rate-limited"},
		"item-2-link-blocked_by": {""},
	})
	if approved.Status != http.StatusOK {
		t.Fatalf("approving P-1 with edits after the project changed: status %d\n%s", approved.Status, text(approved.HTML))
	}
	wantText(t, section(t, approved.HTML, "Done"), `item 2 edited: create Ticket "Reset endpoint, rate-limited", part_of S-1`, "approved P-1 as one unit")
	if q := p.MustRun("query", "--type", "Ticket").Stdout; !strings.Contains(q, `T-2 Ticket "Reset endpoint, rate-limited"`) {
		t.Errorf("jfl query should list the edited T-2:\n%s", q)
	}
}

func TestAProposalEditedWhileItWasDecidedElsewhereIsRefusedSayingWhy(t *testing.T) {
	p := proposedBreakdown(t)
	ui := p.StartUI()
	page := get(t, ui, "/")

	// Rejected in a terminal while the person edits it in the Dashboard.
	p.MustRun("reject", "P-1")

	refused := submit(t, ui, section(t, page, "Pending Proposals"), "Approve all 4 changes", url.Values{
		"item-2-title": {"Reset endpoint, rate-limited"},
	})
	if refused.Status == http.StatusOK {
		t.Fatalf("approving P-1, rejected elsewhere, was done:\n%s", text(refused.HTML))
	}
	wantText(t, section(t, refused.HTML, "Not done"), "P-1 isn't pending")
	assertNothingApplied(t, p)
}

// askEvery is how often jfl ui asks a tracker Store for changes in these
// tests, instead of every 30 seconds, while a page listens.
const askEvery = 250 * time.Millisecond

// askedEvery makes jfl ui, started from now on, ask the project's tracker
// for changes every so often while a page listens.
func askedEvery(p *clitest.Project, every time.Duration) {
	p.Setenv(clitest.TrackerEveryEnv, every.String())
}

func TestTheStreamSignalsAnEditMadeInGitHubIssues(t *testing.T) {
	p, gh := githubPlaybook(t)
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket", "ready-for-agent"}})
	askedEvery(p, askEvery)
	ui := p.StartUI()
	s := listenAsPage(t, ui, get(t, ui, "/"))

	gh.Edit(41, func(i *fakegithub.Issue) { i.Labels = []string{"ticket", "status: doing"} })
	wantSignal(t, s, "an issue's labels edited in GitHub")
	gh.Add(fakegithub.Issue{Title: "Reset endpoint", Labels: []string{"ticket"}})
	wantSignal(t, s, "an issue filed in GitHub")
}

func TestTheStreamSignalsAnEditMadeInLinear(t *testing.T) {
	p, ln := linearPlaybook(t)
	ln.Add(fakelinear.Issue{Title: "Reset-token table", State: "Todo", Labels: []string{"ticket"}})
	askedEvery(p, askEvery)
	ui := p.StartUI()
	s := listenAsPage(t, ui, get(t, ui, "/"))

	ln.Edit(41, func(i *fakelinear.Issue) { i.State = "In Progress" })
	wantSignal(t, s, "an issue's workflow state changed in Linear")
	ln.Add(fakelinear.Issue{Title: "Reset endpoint", Labels: []string{"ticket"}})
	wantSignal(t, s, "an issue filed in Linear")
}

// lists counts the times the fake GitHub listed the repository's issues.
func lists(gh *fakegithub.Server) int {
	n := 0
	for _, r := range gh.Requests() {
		if r == "GET /repos/acme/shop/issues" {
			n++
		}
	}
	return n
}

func TestTheTrackerIsAskedOnceForEveryPageListeningAndNotWhileNoneIs(t *testing.T) {
	p, gh := githubPlaybook(t)
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket", "ready-for-agent"}})
	askedEvery(p, askEvery)
	ui := p.StartUI()

	before := lists(gh)
	time.Sleep(6 * askEvery)
	if n := lists(gh) - before; n != 0 {
		t.Errorf("GitHub was asked %d times while no page listened, want none", n)
	}

	// Pages shown one after another, as when each change reloads the page,
	// don't ask again each: the Status machines page lists no issues itself.
	before = lists(gh)
	for range 5 {
		get(t, ui, "/workflows")
	}
	if n := lists(gh) - before; n > 1 {
		t.Errorf("GitHub was asked %d times for 5 pages shown at once, want once at most", n)
	}

	var pages []*clitest.Stream
	for range 3 {
		pages = append(pages, listen(t, ui))
	}
	before = lists(gh)
	time.Sleep(12 * askEvery)
	// Once every askEvery for the three pages together, with one to spare
	// for timing, not once for each.
	if n := lists(gh) - before; n < 1 || n > 13 {
		t.Errorf("GitHub was asked %d times in %s with three pages listening, want once every %s", n, 12*askEvery, askEvery)
	}

	for _, s := range pages {
		s.Close()
	}
	time.Sleep(4 * askEvery) // for jfl ui to see the pages go
	before = lists(gh)
	time.Sleep(6 * askEvery)
	if n := lists(gh) - before; n != 0 {
		t.Errorf("GitHub was asked %d times once every page stopped listening, want none", n)
	}
}

// unreachable is a fake tracker that can be made unreachable, and edited.
type unreachable struct {
	name    string
	tracker func(t *testing.T) (p *clitest.Project, fail func(status int), edit func())
}

var trackers = []unreachable{
	{"GitHub", func(t *testing.T) (*clitest.Project, func(int), func()) {
		p, gh := githubPlaybook(t)
		gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket", "ready-for-agent"}})
		return p, gh.Fail, func() { gh.Edit(41, func(i *fakegithub.Issue) { i.Labels = []string{"ticket", "status: doing"} }) }
	}},
	{"Linear", func(t *testing.T) (*clitest.Project, func(int), func()) {
		p, ln := linearPlaybook(t)
		ln.Add(fakelinear.Issue{Title: "Reset-token table", State: "Todo", Labels: []string{"ticket"}})
		return p, ln.Fail, func() { ln.Edit(41, func(i *fakelinear.Issue) { i.State = "In Progress" }) }
	}},
}

func TestATrackerThatCantBeReachedSignalsNothingAndLeavesTheStreamOpen(t *testing.T) {
	for _, tr := range trackers {
		t.Run(tr.name, func(t *testing.T) {
			p, fail, edit := tr.tracker(t)
			askedEvery(p, askEvery)
			ui := p.StartUI()
			s := listenAsPage(t, ui, get(t, ui, "/"))

			fail(http.StatusBadGateway)
			if e, ok := s.Next(8 * askEvery); ok {
				t.Errorf("the stream said %q while %s couldn't be reached, want nothing", e, tr.name)
			}

			// The stream is still open, and signals the tracker's next edit.
			fail(0)
			edit()
			wantSignal(t, s, "an issue edited in "+tr.name+" once it could be reached")
		})
	}
}
func TestATrackerEditMadeBeforeThePageListensIsSignalledToo(t *testing.T) {
	p, gh := githubPlaybook(t)
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket", "ready-for-agent"}})
	askedEvery(p, askEvery)
	ui := p.StartUI()
	page := get(t, ui, "/")

	// A teammate edits the issue while the page loads, before it listens:
	// the page is told once the tracker is next asked.
	gh.Edit(41, func(i *fakegithub.Issue) { i.Labels = []string{"ticket", "status: doing"} })
	wantSignal(t, listenAsPage(t, ui, page), "an issue edited in GitHub since the page was shown")
}
