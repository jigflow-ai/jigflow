package main_test

import (
	"maps"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// gatesOverBase is a project whose Playbook file gives the Gate tests a
// command over its Base Playbook's, and the Gate lint one the Base Playbook
// gives none, with comments of its own.
func gatesOverBase(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write("base/playbook.yaml", "name: base\ngates:\n  tests: make test\n")
	p.Write("base/types/ticket.yaml", strings.Replace(baseTicket, "  - {from: in-progress, to: done}\n",
		"  - from: in-progress\n    to: done\n    gates:\n      - {name: tests}\n      - {name: lint}\n", 1))
	writeSkillIn(p, "base", "implement", true)
	p.Write(".jigflow/playbook.yaml", "# The shop's Playbook.\nname: mine\nextends: {path: base} # ours\ngates:\n  tests: go test ./... # the fast ones\n  lint: go vet ./...\n")
	return p
}

// gateForm returns where the form in fragment that a person reads as
// button, for the Gate name, posts to, and what it sends as it is.
func gateForm(t *testing.T, fragment, name, button string) (string, url.Values) {
	t.Helper()
	for _, f := range forms.FindAllStringSubmatch(fragment, -1) {
		if !strings.HasPrefix(attrs(f[1])["action"], "/playbook/gates/"+name) {
			continue
		}
		if action, values, ok := findForm(f[0], button); ok {
			return action, values
		}
	}
	t.Fatalf("no %q form for Gate %s in:\n%s", button, name, fragment)
	return "", nil
}

// look fetches a page of the Dashboard as a program that never opened the
// link jfl ui printed, and fails the test unless it is served.
func look(t *testing.T, ui *clitest.UI, path string) string {
	t.Helper()
	page := ui.Curl(ui.NewRequest(http.MethodGet, path, nil))
	if page.Status != http.StatusOK {
		t.Fatalf("GET %s without the link: status %d\n%s", path, page.Status, text(page.HTML))
	}
	return page.HTML
}

func TestWithTheKeyEachGateOnThePlaybookPageHasAFieldWithItsCommandAndWhereItComesFrom(t *testing.T) {
	p := gatesOverBase(t)
	ui := p.StartUI()

	gates := section(t, get(t, ui, "/playbook"), "Gates")
	if _, form := gateForm(t, gates, "tests", "Save"); form.Get("cmd") != "go test ./..." {
		t.Errorf("the tests field holds %q, want its command", form.Get("cmd"))
	}
	if _, form := gateForm(t, gates, "lint", "Save"); form.Get("cmd") != "go vet ./..." {
		t.Errorf("the lint field holds %q, want its command", form.Get("cmd"))
	}
	wantText(t, gates,
		"tests Ticket in-progress → done Save Back to the Base's given by the project's Playbook file, over Base Playbook base's",
		"lint Ticket in-progress → done Save given by the project's Playbook file Base Playbook base")

	page := look(t, ui, "/playbook")
	if strings.Contains(page, "<form") {
		t.Errorf("without the link, the Playbook page offers a form:\n%s", text(page))
	}
	wantText(t, page, "open the link jfl ui printed in the terminal where you started it")
	wantText(t, section(t, page, "Gates"), "tests Ticket in-progress → done go test ./... given by the project's Playbook file, over Base Playbook base's")
}

func TestSavingAGatesCommandMakesAProposalApprovedAtOnceThatKeepsTheRestOfThePlaybookFile(t *testing.T) {
	p := gatesOverBase(t)
	ui := p.StartUI()

	action, form := gateForm(t, section(t, get(t, ui, "/playbook"), "Gates"), "tests", "Save")
	form.Set("cmd", "go test -race ./...")
	page := ui.Post(action, form)
	if page.Status != http.StatusOK {
		t.Fatalf("saving the tests command: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), `approved P-1 as one unit: give Gate "tests" the command go test -race ./...`)
	if _, form := gateForm(t, section(t, page.HTML, "Gates"), "tests", "Save"); form.Get("cmd") != "go test -race ./..." {
		t.Errorf("after saving, the tests field holds %q", form.Get("cmd"))
	}
	want := "# The shop's Playbook.\nname: mine\nextends: {path: base} # ours\ngates:\n  tests: go test -race ./... # the fast ones\n  lint: go vet ./...\n"
	if got := p.Read(".jigflow/playbook.yaml"); got != want {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, want)
	}
	proposal := p.Read(".jigflow/proposals/P-1.yaml")
	for _, want := range []string{"status: approved", "gate: tests", "cmd: go test -race ./..."} {
		if !strings.Contains(proposal, want) {
			t.Errorf("P-1 should say %q:\n%s", want, proposal)
		}
	}
	if strings.Contains(proposal, "by:") {
		t.Errorf("P-1 is the person's, not an agent session's:\n%s", proposal)
	}
}

func TestTheLedgerRecordsAGateCommandSavedInTheDashboardAsConfirmedThere(t *testing.T) {
	p := gatesOverBase(t)
	ui := p.StartUI()

	action, form := gateForm(t, section(t, get(t, ui, "/playbook"), "Gates"), "lint", "Save")
	form.Set("cmd", "golangci-lint run")
	if page := ui.Post(action, form); page.Status != http.StatusOK {
		t.Fatalf("saving the lint command: status %d\n%s", page.Status, text(page.HTML))
	}
	ledger := p.MustRun("ledger").Stdout
	_, confirmations, _ := strings.Cut(ledger, "Confirmations:\n")
	line, _, _ := strings.Cut(confirmations, "\n")
	for _, want := range []string{"P-1", `give Gate "lint" the command golangci-lint run`, "via dashboard"} {
		if !strings.Contains(line, want) {
			t.Errorf("the Ledger's Confirmations should read %q:\n%s", want, ledger)
		}
	}
}

func TestAGateCommandTheChecksRefuseChangesNoFileAndThePageSaysWhy(t *testing.T) {
	p := gatesOverBase(t)
	ui := p.StartUI()
	before := tree(t, p)

	action, form := gateForm(t, section(t, get(t, ui, "/playbook"), "Gates"), "tests", "Save")
	form.Set("cmd", "  ")
	page := ui.Post(action, form)
	if page.Status != http.StatusConflict {
		t.Fatalf("saving no command: status %d, want 409\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Not done"), "not proposed", "giving a Gate its command needs the Gate's name and a cmd")
	if after := tree(t, p); !maps.Equal(after, before) {
		t.Errorf("a refused change changed the project:\n%v", after)
	}
}

func TestOnlyTheBrowserThatOpenedTheLinkMayChangeThePlaybookFile(t *testing.T) {
	p := gatesOverBase(t)
	ui := p.StartUI()
	gates := section(t, get(t, ui, "/playbook"), "Gates")
	save, form := gateForm(t, gates, "tests", "Save")
	form.Set("cmd", "true")
	base, _ := gateForm(t, gates, "tests", "Back to the Base's")
	before := tree(t, p)

	for _, path := range []string{save, base} {
		// An agent's curl knows what the person's form sends...
		if page := ui.Curl(ui.NewRequest(http.MethodPost, path, form)); page.Status != http.StatusForbidden {
			t.Errorf("POST %s without the link: status %d, want 403", path, page.Status)
		}
		// ...and a page elsewhere can't make the person's browser send it.
		req := ui.NewRequest(http.MethodPost, path, form)
		req.Header.Set("Origin", "http://attacker.example")
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		if page := ui.Do(req); page.Status != http.StatusForbidden {
			t.Errorf("POST %s from another site: status %d, want 403", path, page.Status)
		}
	}
	if page := p.StartUIInSession("A").Post(save, form); page.Status != http.StatusForbidden {
		t.Errorf("saving in a Dashboard agent session A started: status %d, want 403", page.Status)
	}
	if after := tree(t, p); !maps.Equal(after, before) {
		t.Errorf("a refused change changed the project:\n%s", p.Read(".jigflow/playbook.yaml"))
	}
}

func TestBackToTheBasesRemovesTheProjectsGateCommandAndThePageShowsTheBasesAgain(t *testing.T) {
	p := gatesOverBase(t)
	ui := p.StartUI()

	action, form := gateForm(t, section(t, get(t, ui, "/playbook"), "Gates"), "tests", "Back to the Base's")
	page := ui.Post(action, form)
	if page.Status != http.StatusOK {
		t.Fatalf("going back to the Base's tests command: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), `approved P-1 as one unit: remove the project's command for Gate "tests"`)
	gates := section(t, page.HTML, "Gates")
	if _, form := gateForm(t, gates, "tests", "Save"); form.Get("cmd") != "make test" {
		t.Errorf("the tests field holds %q, want the Base Playbook's command", form.Get("cmd"))
	}
	wantText(t, gates, "tests Ticket in-progress → done Save given by the Playbook file of Base Playbook base")
	want := "# The shop's Playbook.\nname: mine\nextends: {path: base} # ours\ngates:\n  lint: go vet ./...\n"
	if got := p.Read(".jigflow/playbook.yaml"); got != want {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, want)
	}
}

func TestAnAgentProposesRemovingAGateCommandTheProjectGivesAndAPersonConfirmsIt(t *testing.T) {
	p := gatesOverBase(t)
	p.Write("back.yaml", "summary: run the Base Playbook's tests\nitems:\n  - {gate: tests, remove: true}\n")

	r := p.RunInSession("A", "propose", "back.yaml")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, `1. remove the project's command for Gate "tests"`) {
		t.Fatalf("proposing to remove the tests command exited %d:\n%s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if got := p.Read(".jigflow/playbook.yaml"); !strings.Contains(got, "tests: go test ./...") {
		t.Errorf("a pending Proposal changed the Playbook file:\n%s", got)
	}
	approveInTerminal(t, p, "P-1")
	want := "# The shop's Playbook.\nname: mine\nextends: {path: base} # ours\ngates:\n  lint: go vet ./...\n"
	if got := p.Read(".jigflow/playbook.yaml"); got != want {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, want)
	}
	if got := p.MustRun("simulate", "Ticket").Stdout; !strings.Contains(got, "make test") {
		t.Errorf("the tests Gate should run the Base Playbook's command again:\n%s", got)
	}
	if l := p.MustRun("ledger").Stdout; !strings.Contains(l, `remove the project's command for Gate "tests"`) || !strings.Contains(l, "via terminal") {
		t.Errorf("the Ledger should record the approval, via terminal:\n%s", l)
	}

	for file, want := range map[string]string{
		"none.yaml:  - {gate: build, remove: true}\n":               `.jigflow/playbook.yaml gives Gate "build" no command to remove`,
		"both.yaml:  - {gate: lint, remove: true, cmd: \"true\"}\n": "removing a Gate's command from the Playbook file takes no cmd",
		"what.yaml:  - {guideline: sql, remove: true}\n":            "an item either creates",
	} {
		name, item, _ := strings.Cut(file, ":")
		p.Write(name, "summary: remove\nitems:\n"+item)
		if r := p.RunInSession("A", "propose", name); r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
			t.Errorf("proposing %s exited %d, want a refusal saying %q: %s", strings.TrimSpace(item), r.ExitCode, want, r.Stderr)
		}
	}
}

func TestAChangeToThePlaybookFileMadeInTheDashboardIsNotCommitted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	p := gatesOverBase(t)
	git(t, p, "init", "-q")
	git(t, p, "add", "-A")
	git(t, p, "commit", "-q", "-m", "the shop")
	ui := p.StartUI()

	action, form := gateForm(t, section(t, get(t, ui, "/playbook"), "Gates"), "tests", "Save")
	form.Set("cmd", "go test -race ./...")
	if page := ui.Post(action, form); page.Status != http.StatusOK {
		t.Fatalf("saving the tests command: status %d\n%s", page.Status, text(page.HTML))
	}
	if log := git(t, p, "log", "--format=%s"); log != "the shop" {
		t.Errorf("the change was committed:\n%s", log)
	}
	if status := git(t, p, "status", "--porcelain"); !strings.Contains(status, "M .jigflow/playbook.yaml") {
		t.Errorf("the Playbook file should be changed and left uncommitted:\n%s", status)
	}
}
