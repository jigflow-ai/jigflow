package main_test

import (
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// gitBaseAtV1 is a project whose Playbook file extends the git Base
// Playbook of gitUpstream at its tag v1, with comments of its own, and whose
// upstream has since tagged v2, which adds the Spec Artifact Type. It
// returns the upstream and the commits of v1 and v2.
func gitBaseAtV1(t *testing.T) (p *clitest.Project, u *upstream, v1, v2 string) {
	t.Helper()
	p = bin.NewProject(t)
	u = gitUpstream(t, p)
	v1 = u.git("rev-parse", "v1")
	p.Write(".jigflow/playbook.yaml", "# The shop's Playbook.\nname: mine\nextends:\n  git: "+u.URL+"\n  ref: v1 # upstream\n")
	p.MustRun("check")
	u.addSpec()
	v2 = u.commit("Add Spec")
	u.git("tag", "v2")
	return p, u, v1, v2
}

func TestAnAgentProposesAGitBasePlaybooksRefCheckChecksItAndApprovingPinsItsCommit(t *testing.T) {
	p, u, v1, v2 := gitBaseAtV1(t)
	p.Write("v2.yaml", "summary: move to v2\nitems:\n  - {base_ref: v2}\n")

	r := p.RunInSession("A", "propose", "v2.yaml")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "1. move the git Base Playbook to ref v2") {
		t.Fatalf("proposing the ref exited %d:\n%s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	want := "Playbook \"mine\", as P-1 would make it: no problems\nBase Playbook " + u.URL + "@v2, commit " + v2 + "\n"
	if got := p.MustRun("check", "--proposal", "P-1").Stdout; got != want {
		t.Errorf("check --proposal P-1 printed:\n%s\nwant:\n%s", got, want)
	}
	if got, want := lock(t, p), map[string]string{"git": u.URL, "ref": "v1", "commit": v1}; !maps.Equal(got, want) {
		t.Errorf("before approval, playbook.lock = %v, want %v", got, want)
	}
	if got := p.MustRun("show", "P-1").Stdout; !strings.Contains(got, "1. move the git Base Playbook to ref v2 (replacing v1)") {
		t.Errorf("jfl show P-1 should say the ref it replaces:\n%s", got)
	}

	approveInTerminal(t, p, "P-1")
	want = "# The shop's Playbook.\nname: mine\nextends:\n  git: " + u.URL + "\n  ref: v2 # upstream\n"
	if got := p.Read(".jigflow/playbook.yaml"); got != want {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, want)
	}
	if got, want := lock(t, p), map[string]string{"git": u.URL, "ref": "v2", "commit": v2}; !maps.Equal(got, want) {
		t.Errorf("playbook.lock = %v, want %v", got, want)
	}
	p.MustRun("create", "Spec", "--title", "Password reset by email")
}

func TestARefThatCantBeFetchedIsRefusedAndThePlaybookFileAndLockfileStayAsTheyWere(t *testing.T) {
	p, u, _, _ := gitBaseAtV1(t)
	p.Write("v9.yaml", "summary: move to v9\nitems:\n  - {base_ref: v9}\n")
	p.Write("v2.yaml", "summary: move to v2\nitems:\n  - {base_ref: v2}\n")
	file, lockfile := p.Read(".jigflow/playbook.yaml"), p.Read(".jigflow/playbook.lock")
	unchanged := func(when string) {
		t.Helper()
		if got := p.Read(".jigflow/playbook.yaml"); got != file {
			t.Errorf("%s, the Playbook file reads:\n%s\nwant:\n%s", when, got, file)
		}
		if got := p.Read(".jigflow/playbook.lock"); got != lockfile {
			t.Errorf("%s, playbook.lock reads:\n%s\nwant:\n%s", when, got, lockfile)
		}
	}

	r := p.RunInSession("A", "propose", "v9.yaml")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "not proposed") || !strings.Contains(r.Stderr, u.URL+"@v9") {
		t.Errorf("proposing a ref upstream lacks exited %d, want a refusal naming it: %s", r.ExitCode, r.Stderr)
	}
	unchanged("after proposing v9")

	p.MustRun("propose", "v2.yaml")
	u.git("tag", "-d", "v2")
	if r := p.Run("check", "--proposal", "P-1"); r.ExitCode != 1 || !strings.Contains(r.Stderr, u.URL+"@v2") {
		t.Errorf("check --proposal of a ref upstream no longer has exited %d, want a refusal naming it: %s", r.ExitCode, r.Stderr)
	}
	// It is refused before the person is asked.
	if r := p.Run("approve", "P-1"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "P-1 was not applied at all") || !strings.Contains(r.Stderr, u.URL+"@v2") {
		t.Errorf("approving a ref upstream no longer has exited %d, want a refusal naming it: %s", r.ExitCode, r.Stderr)
	}
	unchanged("after approving v2, gone upstream")
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 should still be pending:\n%s", got)
	}
}

func TestANewBaseThatWouldLeaveArtifactsInUndeclaredStatusesIsRefusedNamingThemAndPlaybookMigrations(t *testing.T) {
	p, u, _, _ := gitBaseAtV1(t)
	// v3 renames the Ticket's Status in-progress to doing.
	p.Write(u.dir+"/types/ticket.yaml", strings.ReplaceAll(baseTicket, "in-progress", "doing"))
	u.commit("Rename in-progress to doing")
	u.git("tag", "v3")
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.Write("v3.yaml", "summary: move to v3\nitems:\n  - {base_ref: v3}\n")
	p.MustRun("propose", "v3.yaml")
	p.MustRun("move", "T-1", "in-progress")
	file, lockfile := p.Read(".jigflow/playbook.yaml"), p.Read(".jigflow/playbook.lock")

	const orphan = `T-1 "Reset-token table": Ticket has no Status "in-progress"`
	if r := p.RunInSession("A", "propose", "v3.yaml"); r.ExitCode != 1 || !strings.Contains(r.Stderr, orphan) || !strings.Contains(r.Stderr, "Playbook Migration") {
		t.Errorf("proposing v3 exited %d, want a refusal naming T-1 and Playbook Migrations: %s", r.ExitCode, r.Stderr)
	}
	if r := p.Run("approve", "P-1"); r.ExitCode != 1 || !strings.Contains(r.Stderr, orphan) || !strings.Contains(r.Stderr, "Playbook Migration") {
		t.Errorf("approving v3 exited %d, want a refusal naming T-1 and Playbook Migrations: %s", r.ExitCode, r.Stderr)
	}
	if got := p.Read(".jigflow/playbook.yaml"); got != file {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, file)
	}
	if got := p.Read(".jigflow/playbook.lock"); got != lockfile {
		t.Errorf("playbook.lock reads:\n%s\nwant:\n%s", got, lockfile)
	}
	p.MustRun("move", "T-1", "done")
}

func TestProposingARefIsRefusedWhenThePlaybookFileExtendsNoGitBasePlaybook(t *testing.T) {
	for name, extends := range map[string]string{"built in": "extends: {builtin: larapilot}\n", "at a local path": "extends: {path: base}\n", "none": ""} {
		t.Run(name, func(t *testing.T) {
			p := bin.NewProject(t)
			writeBase(p, "base")
			p.Write(".jigflow/playbook.yaml", "name: mine\n"+extends)
			if extends == "" {
				p.Write(".jigflow/types/ticket.yaml", baseTicket)
				writeSkillIn(p, ".jigflow", "implement", true)
			}
			p.Write("v2.yaml", "summary: move to v2\nitems:\n  - {base_ref: v2}\n")
			before := tree(t, p)
			if r := p.RunInSession("A", "propose", "v2.yaml"); r.ExitCode != 1 || !strings.Contains(r.Stderr, ".jigflow/playbook.yaml extends no git Base Playbook") {
				t.Errorf("proposing a ref exited %d, want a refusal: %s", r.ExitCode, r.Stderr)
			}
			if after := tree(t, p); !maps.Equal(after, before) {
				t.Errorf("a refused Proposal changed the project")
			}
		})
	}
}

// baseForm returns where the form in the Playbook page's Base Playbook
// section that a person reads as button posts to, and what it sends as it
// is.
func baseForm(t *testing.T, page, button string) (string, url.Values) {
	t.Helper()
	action, values, ok := findForm(section(t, page, "Base Playbook"), button)
	if !ok {
		t.Fatalf("no %q form for the Base Playbook in:\n%s", button, text(section(t, page, "Base Playbook")))
	}
	return action, values
}

func TestWithTheKeyThePlaybookPageShowsAGitBasesURLRefAndPinnedCommitAndEditsTheRefOnly(t *testing.T) {
	p, u, v1, _ := gitBaseAtV1(t)
	ui := p.StartUI()

	page := get(t, ui, "/playbook")
	_, form := baseForm(t, page, "Save")
	if form.Get("ref") != "v1" {
		t.Errorf("the ref field holds %q, want v1", form.Get("ref"))
	}
	if len(form) != 1 {
		t.Errorf("the Base Playbook's form sends %v, want its ref only", form)
	}
	base := section(t, page, "Base Playbook")
	wantText(t, base, "git "+u.URL, "ref Save", "commit "+v1)
	if strings.Count(base, "<form") != 1 {
		t.Errorf("the Base Playbook offers more than its ref's form:\n%s", base)
	}

	base = section(t, look(t, ui, "/playbook"), "Base Playbook")
	if strings.Contains(base, "<form") {
		t.Errorf("without the link, the Base Playbook offers a form:\n%s", text(base))
	}
	wantText(t, base, "git "+u.URL, "ref v1", "commit "+v1)
}

func TestSavingAGitBasesRefApprovesItAtOnceAndPinsTheNewCommit(t *testing.T) {
	p, u, _, v2 := gitBaseAtV1(t)
	ui := p.StartUI()

	action, form := baseForm(t, get(t, ui, "/playbook"), "Save")
	form.Set("ref", "v2")
	page := ui.Post(action, form)
	if page.Status != http.StatusOK {
		t.Fatalf("saving the ref: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), "approved P-1 as one unit: move the git Base Playbook to ref v2")
	if _, form := baseForm(t, page.HTML, "Save"); form.Get("ref") != "v2" {
		t.Errorf("after saving, the ref field holds %q", form.Get("ref"))
	}
	wantText(t, section(t, page.HTML, "Base Playbook"), "commit "+v2)
	wantText(t, section(t, page.HTML, "Artifact Types"), "Spec S files Base Playbook "+u.URL+"@v2")
	want := "# The shop's Playbook.\nname: mine\nextends:\n  git: " + u.URL + "\n  ref: v2 # upstream\n"
	if got := p.Read(".jigflow/playbook.yaml"); got != want {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, want)
	}
	if got, want := lock(t, p), map[string]string{"git": u.URL, "ref": "v2", "commit": v2}; !maps.Equal(got, want) {
		t.Errorf("playbook.lock = %v, want %v", got, want)
	}
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: approved") || !strings.Contains(got, "base_ref: v2") {
		t.Errorf("P-1 should be approved, moving the ref:\n%s", got)
	}
}

func TestSavingARefThatCantBeFetchedIsRefusedOnThePageAndChangesNeitherFile(t *testing.T) {
	p, u, _, _ := gitBaseAtV1(t)
	ui := p.StartUI()
	file, lockfile := p.Read(".jigflow/playbook.yaml"), p.Read(".jigflow/playbook.lock")

	action, form := baseForm(t, get(t, ui, "/playbook"), "Save")
	for ref, why := range map[string]string{"v9": u.URL + "@v9", " ": "the git Base Playbook needs a ref"} {
		form.Set("ref", ref)
		page := ui.Post(action, form)
		if page.Status != http.StatusConflict {
			t.Fatalf("saving the ref %q: status %d, want 409\n%s", ref, page.Status, text(page.HTML))
		}
		wantText(t, section(t, page.HTML, "Not done"), "not proposed", why)
	}
	if got := p.Read(".jigflow/playbook.yaml"); got != file {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, file)
	}
	if got := p.Read(".jigflow/playbook.lock"); got != lockfile {
		t.Errorf("playbook.lock reads:\n%s\nwant:\n%s", got, lockfile)
	}
}

func TestABuiltInOrLocalBasePlaybookIsShownWithNoForm(t *testing.T) {
	for extends, want := range map[string]string{"{builtin: larapilot}": "built into jfl larapilot", "{path: base}": "local path base"} {
		t.Run(extends, func(t *testing.T) {
			p := bin.NewProject(t)
			writeBase(p, "base")
			p.Write(".jigflow/playbook.yaml", "name: mine\nextends: "+extends+"\n")
			ui := p.StartUI()

			base := section(t, get(t, ui, "/playbook"), "Base Playbook")
			if strings.Contains(base, "<form") {
				t.Errorf("with the key, a Base Playbook other than a git one offers a form:\n%s", text(base))
			}
			wantText(t, base, want)
		})
	}
}

func TestTheRefAPendingProposalAlsoChangesNamesThatProposalWhichShowsTheRefItReplaces(t *testing.T) {
	p, _, _, _ := gitBaseAtV1(t)
	p.Write("v2.yaml", "summary: move to v2\nitems:\n  - {base_ref: v2}\n")
	if r := p.RunInSession("A", "propose", "v2.yaml"); r.ExitCode != 0 {
		t.Fatalf("proposing the ref exited %d:\n%s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	ui := p.StartUI()

	for _, page := range []string{get(t, ui, "/playbook"), look(t, ui, "/playbook")} {
		base := section(t, page, "Base Playbook")
		wantText(t, base, "pending Proposal P-1 changes it too")
		if !strings.Contains(base, `href="/#P-1"`) {
			t.Errorf("the ref should link P-1:\n%s", base)
		}
	}
	wantText(t, section(t, get(t, ui, "/"), "Pending Proposals"), "move the git Base Playbook to ref v2 replacing v1")
}
