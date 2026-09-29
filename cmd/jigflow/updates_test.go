package main_test

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jigflow-ai/jigflow/internal/clitest"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakegithub"
	"github.com/jigflow-ai/jigflow/internal/clitest/fakelinear"
)

// The Dashboard's pages update themselves (ADR 0025): each asks jfl ui,
// every 2 seconds while it is visible, whether the project changed since
// the version it shows (ADR 0029).

// signalWithin is how long a change jfl writes may take to show on
// /changes: about a second, with room for a slow machine.
const signalWithin = 3 * time.Second

// asking is a page asking /changes whether the project changed since the
// version it shows.
type asking struct {
	ui    *clitest.UI
	since string
}

// askOf is the version an inline script of a page asks /changes about.
var askOf = regexp.MustCompile(`(?s)<script>.*?var asks = "(/changes\?since=[^"]*)".*?</script>`)

// listens reports whether the page carries an inline script that asks
// /changes whether the project changed.
func listens(page string) bool { return askOf.MatchString(page) }

// listenAsPage asks /changes as the page's script does, about the version
// the page shows.
func listenAsPage(t *testing.T, ui *clitest.UI, page string) *asking {
	t.Helper()
	m := askOf.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("the page doesn't ask /changes whether the project changed")
	}
	u, err := url.Parse(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return &asking{ui: ui, since: u.Query().Get("since")}
}

// listen asks /changes as the backlog page, shown now, does.
func listen(t *testing.T, ui *clitest.UI) *asking {
	t.Helper()
	return listenAsPage(t, ui, get(t, ui, "/"))
}

// ask asks /changes once, as any program on this machine, with no key, and
// fails the test unless it answers at once.
func (a *asking) ask(t *testing.T) int {
	t.Helper()
	start := time.Now()
	page := a.ui.Curl(a.ui.NewRequest(http.MethodGet, "/changes?since="+url.QueryEscape(a.since), nil))
	if took := time.Since(start); took > time.Second {
		t.Errorf("/changes took %s to answer, want at once", took)
	}
	if page.Status != http.StatusOK && page.Status != http.StatusNoContent {
		t.Fatalf("GET /changes: status %d, want 200 or 204\n%s", page.Status, page.HTML)
	}
	return page.Status
}

// wantSignal fails the test unless /changes answers 200, the project
// changed, within signalWithin of the change, and then asks from what a
// page shown now shows, as the page does once it loaded again.
func wantSignal(t *testing.T, a *asking, change string) {
	t.Helper()
	deadline := time.Now().Add(signalWithin)
	for a.ask(t) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatalf("%s didn't show on /changes within %s", change, signalWithin)
		}
		time.Sleep(200 * time.Millisecond)
	}
	*a = *listenAsPage(t, a.ui, get(t, a.ui, "/"))
}

// wantNoSignal fails the test if /changes answers anything but 204, nothing
// changed, while it is asked for the time given.
func wantNoSignal(t *testing.T, a *asking, within time.Duration, why string) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if status := a.ask(t); status != http.StatusNoContent {
			t.Fatalf("/changes answered %d %s, want 204", status, why)
		}
	}
}

func TestChangesAnswersEachChangeJflWritesToTheProject(t *testing.T) {
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

func TestChangesAnswersNoContentWhileNothingChanges(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Login page")
	ui := p.StartUI()
	s := listenAsPage(t, ui, get(t, ui, "/"))

	// Reading the project changes nothing, and neither does time passing.
	get(t, ui, "/ledger")
	p.MustRun("query")
	wantNoSignal(t, s, 2500*time.Millisecond, "while nothing changed")
}

func TestChangesNeedsNoKeyAndServesOnlyThisMachine(t *testing.T) {
	p := ticketPlaybook(t)
	// A Dashboard an agent session started, only to look at, updates too.
	ui := p.StartUIInSession("A")
	s := listen(t, ui) // with no key, as any program on this machine

	p.MustRun("create", "Ticket", "--title", "Login page")
	wantSignal(t, s, "a new Artifact")

	req := ui.NewRequest(http.MethodGet, "/changes?since="+s.since, nil)
	req.Host = "attacker.example:80"
	if page := ui.Curl(req); page.Status != http.StatusForbidden {
		t.Errorf("/changes for attacker.example got status %d, want 403", page.Status)
	}
}

func TestAPageThatAsksForAStreamIsToldOnceToLoadAgain(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()

	// A page shown before jfl ui was upgraded listens to a stream: it is
	// told the project changed, so that it loads again into one that asks.
	req := ui.NewRequest(http.MethodGet, "/changes?since="+listen(t, ui).since, nil)
	req.Header.Set("Accept", "text/event-stream")
	s := ui.Listen(req)
	if s.Status != http.StatusOK || s.ContentType != "text/event-stream" {
		t.Fatalf("GET /changes as a stream: status %d, Content-Type %q, want 200 and an event stream", s.Status, s.ContentType)
	}
	if e, ok := s.Next(signalWithin); !ok || e != "changed" {
		t.Fatalf("the stream said %q (%v), want one changed event", e, ok)
	}
	if !s.Ended(time.Second) {
		t.Error("the stream stayed open after its changed event, want it closed")
	}
}

func TestJflUiStopsAtOnceWithPagesAsking(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()
	s := listen(t, ui)
	s.ask(t)

	start := time.Now()
	ui.Stop()
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("jfl ui took %s to stop with a page asking, want it to stop at once", took)
	}
}

// askScript is the inline script of a page, which asks /changes.
var askScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

func TestPagesAskEveryTwoSecondsOnlyWhileVisible(t *testing.T) {
	p := ticketPlaybook(t)
	ui := p.StartUI()
	m := askScript.FindStringSubmatch(get(t, ui, "/"))
	if m == nil {
		t.Fatal("the backlog page carries no script")
	}
	script := m[1]
	for _, want := range []string{
		"setTimeout(ask, 2000)", // every 2 seconds
		"visibilitychange",      // once it becomes visible
		"document.hidden",       // never while hidden
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the page's script has no %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "EventSource") {
		t.Errorf("the page's script still holds a stream:\n%s", script)
	}
}

func TestFilesAndGitAreLookedAtAtMostOnceASecondHoweverManyPagesAsk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("counts git's runs through a shell script")
	}
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	p := ticketPlaybook(t)
	// A git that notes each time it is asked where HEAD is.
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "git.log")
	script := "#!/bin/sh\ncase \"$*\" in *absolute-git-dir*) echo x >> " + log + ";; esac\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ui := p.StartUI()
	s := listen(t, ui)
	looks := func() int { b, _ := os.ReadFile(log); return strings.Count(string(b), "x") }

	// Ten pages, each asking many times a second, for 3 seconds.
	before := looks()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
				s.ask(t)
			}
		})
	}
	wg.Wait()
	if n := looks() - before; n < 1 || n > 5 {
		t.Errorf("git was asked %d times in 3 seconds by ten pages, want once a second at most", n)
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

	for _, path := range []string{"/", "/timeline", "/workflows", "/ledger"} {
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
// tests, instead of every 30 seconds, while pages ask it.
const askEvery = 250 * time.Millisecond

// askedEvery makes jfl ui, started from now on, ask the project's tracker
// for changes at most every so often.
func askedEvery(p *clitest.Project, every time.Duration) {
	p.Setenv(clitest.TrackerEveryEnv, every.String())
}

func TestChangesAnswersAnEditMadeInGitHubIssues(t *testing.T) {
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

func TestChangesAnswersAnEditMadeInLinear(t *testing.T) {
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

func TestTheTrackerIsAskedAtMostEverySoOftenAndNotWhileNoPageAsks(t *testing.T) {
	p, gh := githubPlaybook(t)
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket", "ready-for-agent"}})
	askedEvery(p, askEvery)
	ui := p.StartUI()
	s := listen(t, ui)

	before := lists(gh)
	time.Sleep(6 * askEvery)
	if n := lists(gh) - before; n != 0 {
		t.Errorf("GitHub was asked %d times while no page asked, want none", n)
	}

	// Pages shown one after another, as when each change reloads the page,
	// don't ask again each: the Status machines page lists no issues itself.
	time.Sleep(askEvery)
	before = lists(gh)
	for range 5 {
		get(t, ui, "/workflows")
	}
	if n := lists(gh) - before; n > 1 {
		t.Errorf("GitHub was asked %d times for 5 pages shown at once, want once at most", n)
	}

	// Three pages asking again and again ask it once every askEvery
	// together, with one to spare for timing, not once each.
	before = lists(gh)
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			for deadline := time.Now().Add(12 * askEvery); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
				s.ask(t)
			}
		})
	}
	wg.Wait()
	if n := lists(gh) - before; n < 1 || n > 13 {
		t.Errorf("GitHub was asked %d times in %s with three pages asking, want once every %s", n, 12*askEvery, askEvery)
	}

	before = lists(gh)
	time.Sleep(6 * askEvery)
	if n := lists(gh) - before; n != 0 {
		t.Errorf("GitHub was asked %d times once every page stopped asking, want none", n)
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

func TestATrackerThatCantBeReachedChangesNothing(t *testing.T) {
	for _, tr := range trackers {
		t.Run(tr.name, func(t *testing.T) {
			p, fail, edit := tr.tracker(t)
			askedEvery(p, askEvery)
			ui := p.StartUI()
			s := listenAsPage(t, ui, get(t, ui, "/"))

			fail(http.StatusBadGateway)
			wantNoSignal(t, s, 8*askEvery, "while "+tr.name+" couldn't be reached")

			// The tracker's next edit shows once it can be reached.
			fail(0)
			edit()
			wantSignal(t, s, "an issue edited in "+tr.name+" once it could be reached")
		})
	}
}

func TestATrackerEditMadeBeforeThePageAsksIsAnsweredToo(t *testing.T) {
	p, gh := githubPlaybook(t)
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket", "ready-for-agent"}})
	askedEvery(p, askEvery)
	ui := p.StartUI()
	page := get(t, ui, "/")

	// A teammate edits the issue while the page loads, before it asks:
	// the page is told once the tracker is next asked.
	gh.Edit(41, func(i *fakegithub.Issue) { i.Labels = []string{"ticket", "status: doing"} })
	wantSignal(t, listenAsPage(t, ui, page), "an issue edited in GitHub since the page was shown")
}
