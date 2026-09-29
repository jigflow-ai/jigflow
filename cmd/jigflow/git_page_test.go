package main_test

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// gitProject makes the project a git repository on the branch main, with
// no commits yet, skipping the test when there is no git.
func gitProject(t *testing.T, p *clitest.Project) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	git(t, p, "init", "-q", "-b", "main")
}

// commit commits nothing but the subject given, in the project.
func commit(t *testing.T, p *clitest.Project, subject string) {
	t.Helper()
	git(t, p, "commit", "-q", "--allow-empty", "-m", subject)
}

// commitRows returns what a person reads of each commit the Git page lists,
// in order, with a linked id read as the subject it starts.
func commitRows(t *testing.T, page string) []string {
	t.Helper()
	var rows []string
	for _, r := range strings.Split(section(t, page, "Commits"), "<tr")[1:] {
		if !strings.Contains(r, "<td") {
			continue // the header
		}
		r, _, _ = strings.Cut(r, "</tr>")
		rows = append(rows, strings.ReplaceAll(text("<tr"+r), " : ", ": "))
	}
	return rows
}

func TestTheGitPageListsTheBranchsCommitsNewestFirstLinkedToTheirArtifacts(t *testing.T) {
	p := ticketPlaybook(t)
	gitProject(t, p)
	p.MustRun("create", "Ticket", "--title", "Login page")
	commit(t, p, "the project")
	commit(t, p, "T-1: Login page")
	commit(t, p, "T-9: an Artifact no Store keeps")
	commit(t, p, "T-10: not T-1's")
	ui := p.StartUI()

	if nav := get(t, ui, "/"); !strings.Contains(nav, `<a href="/git">Git</a>`) {
		t.Errorf("the navigation should link the Git page in a git repository:\n%s", nav)
	}
	page := get(t, ui, "/git")
	wantText(t, page, "main")
	rows := commitRows(t, page)
	want := []string{"T-10: not T-1's", "T-9: an Artifact no Store keeps", "T-1: Login page", "the project"}
	if len(rows) != len(want) {
		t.Fatalf("the Git page should list %d commits, lists %d:\n%s", len(want), len(rows), strings.Join(rows, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(rows[i], w) {
			t.Errorf("commit %d should be %q, newest first, is %q", i+1, w, rows[i])
		}
	}
	commits := section(t, page, "Commits")
	if n := strings.Count(commits, `href="/artifacts/`); n != 1 || !strings.Contains(commits, `<a class="id" href="/artifacts/T-1">T-1</a>`) {
		t.Errorf("only the commit of T-1, a known Artifact, should link to its page, %d links do:\n%s", n, commits)
	}
}

func TestTheGitPageListsFiftyCommitsAPageAndLinksToTheOthers(t *testing.T) {
	p := ticketPlaybook(t)
	gitProject(t, p)
	for i := 1; i <= 51; i++ {
		commit(t, p, fmt.Sprintf("change %d", i))
	}
	ui := p.StartUI()

	first := get(t, ui, "/git")
	rows := commitRows(t, first)
	if len(rows) != 50 || !strings.Contains(rows[0], "change 51") || !strings.Contains(rows[49], "change 2 ") {
		t.Fatalf("the first page should list the 50 newest commits, from change 51 to change 2, lists %d:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.Contains(first, `<a href="/git?page=2">Older →</a>`) || strings.Contains(first, "Newer") {
		t.Errorf("the first page should link only to older commits:\n%s", text(section(t, first, "Commits")))
	}
	second := get(t, ui, "/git?page=2")
	if rows := commitRows(t, second); len(rows) != 1 || !strings.Contains(rows[0], "change 1 ") {
		t.Errorf("the second page should list the oldest commit alone, lists:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(second, `<a href="/git?page=1">← Newer</a>`) || strings.Contains(second, "Older") {
		t.Errorf("the last page should link only to newer commits:\n%s", text(section(t, second, "Commits")))
	}
	if !strings.Contains(second, `var here = "/git?page=2"`) {
		t.Errorf("the second page should reload itself, not the first, when the project changes")
	}
}

func TestEachArtifactsPageListsTheCommitsWhoseSubjectStartsWithItsID(t *testing.T) {
	p := ticketPlaybook(t)
	gitProject(t, p)
	p.MustRun("create", "Ticket", "--title", "Login page")
	commit(t, p, "T-1: Login page")
	commit(t, p, "T-10: not T-1's")
	commit(t, p, "the project\n\nT-1: in the body, not the subject")
	commit(t, p, "T-1: Login page, sent back")
	ui := p.StartUI()

	rows := commitRows(t, get(t, ui, "/artifacts/T-1"))
	want := []string{"T-1: Login page, sent back", "T-1: Login page"}
	if len(rows) != len(want) {
		t.Fatalf("T-1's page should list its %d commits, lists %d:\n%s", len(want), len(rows), strings.Join(rows, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(rows[i], w) {
			t.Errorf("T-1's commit %d should be %q, newest first, is %q", i+1, w, rows[i])
		}
	}
	p.MustRun("create", "Ticket", "--title", "Signup page")
	wantText(t, section(t, get(t, ui, "/artifacts/T-2"), "Commits"), "No commit names T-2 yet.")
}

func TestOutsideAGitRepositoryTheGitPageAndTheCommitsListAreLeftOut(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Login page")
	ui := p.StartUI()

	for _, path := range []string{"/", "/artifacts/T-1", "/timeline"} {
		if page := get(t, ui, path); strings.Contains(page, `href="/git"`) {
			t.Errorf("%s shouldn't link a Git page outside a git repository:\n%s", path, text(page))
		}
	}
	if page := get(t, ui, "/artifacts/T-1"); strings.Contains(page, `id="commits"`) {
		t.Errorf("T-1's page shouldn't list commits outside a git repository:\n%s", text(page))
	}
	page := get(t, ui, "/git")
	wantText(t, page, "This project isn't in a git repository, so there is no history to show.")
}

func TestTheGitPageOfARepositoryWithNoCommitYetSaysSo(t *testing.T) {
	p := ticketPlaybook(t)
	gitProject(t, p)
	ui := p.StartUI()

	wantText(t, section(t, get(t, ui, "/git"), "Commits"), "No commit yet.")
}

func TestTheGitPageAnswersOnlyThisMachineAndUpdatesItselfOnACommit(t *testing.T) {
	p := ticketPlaybook(t)
	gitProject(t, p)
	commit(t, p, "the project")
	ui := p.StartUI()

	req := ui.NewRequest("GET", "/git", nil)
	req.Host = "attacker.example:80"
	if page := ui.Do(req); page.Status != http.StatusForbidden {
		t.Errorf("GET /git for attacker.example: status %d, want 403", page.Status)
	}
	page := get(t, ui, "/git")
	if !listens(page) || !strings.Contains(page, `var here = "/git"`) {
		t.Errorf("/git should reload itself when the project changes")
	}
	if strings.Contains(page, "<form") {
		t.Errorf("the Git page only lists commits, and offers no form:\n%s", text(page))
	}

	// A commit made outside jfl changes nothing under .jigflow/.
	s := listenAsPage(t, ui, page)
	commit(t, p, "a change made by hand")
	wantSignal(t, s, "a new commit")
	if rows := commitRows(t, get(t, ui, "/git")); len(rows) != 2 || !strings.Contains(rows[0], "a change made by hand") {
		t.Errorf("the Git page should list the new commit first:\n%s", strings.Join(rows, "\n"))
	}
}
