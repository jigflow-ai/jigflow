package main_test

import (
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// connectorFromBase is a project whose Tickets are kept in a tracker
// through the fake Connector tracker, which only its Base Playbook
// declares, with a yes/no setting, a Ticket setting and a Status mapped.
func connectorFromBase(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write("base/playbook.yaml", `name: base
connectors:
  tracker:
    command: `+fakeConnector+`
    settings: {tracker: tracker.json, log: calls.jsonl, verbose: false}
    types:
      Ticket:
        settings: {label: ticket}
        statuses:
          in-progress: "status: doing"
`)
	p.Write("base/types/ticket.yaml", strings.Replace(baseTicket, "prefix: T\n", "prefix: T\nstore: tracker\n", 1))
	writeSkillIn(p, "base", "implement", true)
	p.Write(".jigflow/playbook.yaml", "# The shop's Playbook.\nname: mine\nextends: {path: base} # ours\n")
	setTracker(t, p, map[string]any{"next": 41})
	return p
}

// playbookTree is every file of the project's Playbook and Proposals, by
// its path: the project as a change to the Playbook may change it, leaving
// out the fake tracker, which reading it rewrites.
func playbookTree(t *testing.T, p *clitest.Project) map[string]string {
	t.Helper()
	files := tree(t, p)
	maps.DeleteFunc(files, func(path string, _ string) bool {
		return !strings.HasPrefix(path, ".jigflow"+string(filepath.Separator))
	})
	return files
}

func TestAnAgentProposesAConnectorsSettingsCheckChecksThemAndApprovingCopiesTheBasesConnectorFirst(t *testing.T) {
	p := connectorFromBase(t)
	p.Write("verbose.yaml", "summary: a verbose tracker\nitems:\n  - {connector: tracker, settings: {verbose: true}, types: {Ticket: {settings: {label: tickets}}}}\n")

	r := p.RunInSession("A", "propose", "verbose.yaml")
	const change = `1. change Connector "tracker": setting verbose: true; Ticket setting label: tickets`
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, change) {
		t.Fatalf("proposing the Connector's settings exited %d:\n%s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if got := p.MustRun("check", "--proposal", "P-1").Stdout; !strings.Contains(got, `Playbook "mine", as P-1 would make it: no problems`) {
		t.Errorf("check --proposal P-1 printed:\n%s", got)
	}
	if got := p.MustRun("show", "P-1").Stdout; !strings.Contains(got, change+" (replacing setting verbose: false; Ticket setting label: ticket)") {
		t.Errorf("jfl show P-1 should say the values it replaces:\n%s", got)
	}
	if got := p.Read(".jigflow/playbook.yaml"); strings.Contains(got, "connectors") {
		t.Errorf("a pending Proposal changed the Playbook file:\n%s", got)
	}
	approveInTerminal(t, p, "P-1")
	got := p.Read(".jigflow/playbook.yaml")
	if !strings.HasPrefix(got, "# The shop's Playbook.\nname: mine\nextends: {path: base} # ours\nconnectors:\n  tracker:\n") {
		t.Errorf("the Playbook file should keep what it said and declare the Connector:\n%s", got)
	}
	// The project's declaration replaces the Base Playbook's whole, so it
	// is copied whole, mappings too, before it changes.
	for _, want := range []string{"command: " + fakeConnector, "tracker: tracker.json", "log: calls.jsonl", "verbose: true", "label: tickets", "in-progress: 'status: doing'"} {
		if !strings.Contains(got, want) {
			t.Errorf("the Playbook file should say %q:\n%s", want, got)
		}
	}
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("move", "T-41", "in-progress")
	if got := item(t, p, "41").Labels; !strings.Contains(strings.Join(got, ","), "status: doing") {
		t.Errorf("the copied Status mapping should label T-41 in the tracker: %v", got)
	}
	if c := calls(t, p, "create"); len(c) != 1 || c[0].Settings["verbose"] != true || c[0].Settings["label"] != "tickets" {
		t.Errorf("the Connector should be sent the new settings, as they are: %+v", c)
	}
}

// connectorOverBase is connectorFromBase with the project's own declaration
// of the Connector tracker, with comments, which replaces its Base
// Playbook's.
func connectorOverBase(t *testing.T) *clitest.Project {
	t.Helper()
	p := connectorFromBase(t)
	p.Write(".jigflow/playbook.yaml", `# The shop's Playbook.
name: mine
extends: {path: base} # ours
connectors:
  tracker:
    command: `+fakeConnector+` # the fake one
    args: [--slow]
    settings: {tracker: tracker.json, log: calls.jsonl, verbose: false}
    types:
      Ticket:
        settings: {label: ticket} # tells Tickets apart
`)
	return p
}

func TestAnAgentProposesAConnectorsCommandArgsAndMarkerAndApprovingKeepsTheRestOfThePlaybookFile(t *testing.T) {
	p := connectorOverBase(t)
	p.Write("marker.yaml", "summary: word the marker\nitems:\n  - {connector: tracker, args: [--quiet, --fast], marker: \"_Drafted by an agent._\", settings: {verbose: null}}\n")

	r := p.RunInSession("A", "propose", "marker.yaml")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, `1. change Connector "tracker": args: --quiet --fast; marker: _Drafted by an agent._; setting verbose: none`) {
		t.Fatalf("proposing the Connector's args and marker exited %d:\n%s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	approveInTerminal(t, p, "P-1")
	want := `# The shop's Playbook.
name: mine
extends: {path: base} # ours
connectors:
  tracker:
    command: ` + fakeConnector + ` # the fake one
    args: [--quiet, --fast]
    settings: {tracker: tracker.json, log: calls.jsonl}
    types:
      Ticket:
        settings: {label: ticket} # tells Tickets apart
    marker: _Drafted by an agent._
`
	if got := p.Read(".jigflow/playbook.yaml"); got != want {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, want)
	}
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	if r := p.RunInSession("A", "comment", "T-41", "Needs a design first."); r.ExitCode != 0 {
		t.Fatalf("agent comment exited %d: %s", r.ExitCode, r.Stderr)
	}
	if got := item(t, p, "41").Comments; len(got) != 1 || got[0] != "Needs a design first.\n\n_Drafted by an agent._" {
		t.Errorf("the agent's comment should carry the new marker: %q", got)
	}

	p.Write("args.yaml", "summary: no args\nitems:\n  - {connector: tracker, command: ./tracker, args: []}\n")
	p.MustRun("propose", "args.yaml")
	approveInTerminal(t, p, "P-2")
	if got := p.Read(".jigflow/playbook.yaml"); strings.Contains(got, "args") || !strings.Contains(got, "command: ./tracker # the fake one") {
		t.Errorf("the Playbook file should give the new command and no args:\n%s", got)
	}
}

func TestAnAgentProposesTakingAConnectorBackToTheBasesAndAPersonConfirmsIt(t *testing.T) {
	p := connectorOverBase(t)
	p.Write("back.yaml", "summary: the Base's tracker\nitems:\n  - {connector: tracker, remove: true}\n")

	r := p.RunInSession("A", "propose", "back.yaml")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, `1. remove the project's declaration of Connector "tracker"`) {
		t.Fatalf("proposing to remove the Connector exited %d:\n%s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if got := p.MustRun("show", "P-1").Stdout; !strings.Contains(got, `1. remove the project's declaration of Connector "tracker" (replacing command: `+fakeConnector+`; args: --slow; marker: _Written by an AI agent through JigFlow._; setting log: calls.jsonl; setting tracker: tracker.json; setting verbose: false; Ticket setting label: ticket)`) {
		t.Errorf("jfl show P-1 should say the declaration it removes:\n%s", got)
	}
	approveInTerminal(t, p, "P-1")
	want := "# The shop's Playbook.\nname: mine\nextends: {path: base} # ours\n"
	if got := p.Read(".jigflow/playbook.yaml"); got != want {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, want)
	}
	p.MustRun("create", "Ticket", "--title", "Reset-token table")
	p.MustRun("move", "T-41", "in-progress")
	if got := item(t, p, "41").Labels; !strings.Contains(strings.Join(got, ","), "status: doing") {
		t.Errorf("the Base Playbook's Connector should map in-progress again: %v", got)
	}

	for file, want := range map[string]string{
		"none.yaml:  - {connector: tracker, remove: true}\n":                  `.jigflow/playbook.yaml declares no Connector "tracker" to remove`,
		"both.yaml:  - {connector: tracker, remove: true, marker: \"_x_\"}\n": "removing a Connector's declaration from the Playbook file takes no other values",
		"what.yaml:  - {connector: tracker}\n":                                "changing a Connector needs its command, args, marker, settings or an Artifact Type's settings",
		"whose.yaml:  - {settings: {verbose: true}}\n":                        "changing a Connector needs the Connector's name",
	} {
		name, item, _ := strings.Cut(file, ":")
		p.Write(name, "summary: remove\nitems:\n"+item)
		if r := p.RunInSession("A", "propose", name); r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
			t.Errorf("proposing %s exited %d, want a refusal saying %q: %s", strings.TrimSpace(item), r.ExitCode, want, r.Stderr)
		}
	}
}

func TestAConnectorChangeLeavingThePlaybookFailingItsChecksIsRefusedAndWritesNothing(t *testing.T) {
	p := connectorOverBase(t)
	p.Write("spec.yaml", "summary: label Specs\nitems:\n  - {connector: tracker, types: {Spec: {settings: {label: spec}}}}\n")
	before := playbookTree(t, p)

	const why = `Connector "tracker" maps Artifact Type "Spec", which it doesn't keep`
	if r := p.RunInSession("A", "propose", "spec.yaml"); r.ExitCode != 1 || !strings.Contains(r.Stderr, why) {
		t.Errorf("proposing settings for a Type the Connector doesn't keep exited %d, want a refusal saying %q: %s", r.ExitCode, why, r.Stderr)
	}
	if after := playbookTree(t, p); !maps.Equal(after, before) {
		t.Errorf("a refused change changed the project:\n%s", p.Read(".jigflow/playbook.yaml"))
	}
}

// connectorForm returns where the form in the Playbook page's Connectors
// section, for the Connector name, that a person reads as button posts to,
// and what it sends as it is.
func connectorForm(t *testing.T, page, name, button string) (string, url.Values) {
	t.Helper()
	connectors := section(t, page, "Connectors")
	for _, f := range forms.FindAllStringSubmatch(connectors, -1) {
		if !strings.HasPrefix(attrs(f[1])["action"], "/playbook/connectors/"+name) {
			continue
		}
		if action, values, ok := findForm(f[0], button); ok {
			return action, values
		}
	}
	t.Fatalf("no %q form for Connector %s in:\n%s", button, name, text(connectors))
	return "", nil
}

func TestThePlaybookPageListsEachConnectorWithItsValuesAndWhereTheyComeFrom(t *testing.T) {
	p := connectorOverBase(t)
	ui := p.StartUI()

	connectors := section(t, look(t, ui, "/playbook"), "Connectors")
	if strings.Contains(connectors, "<form") {
		t.Errorf("without the link, the Connectors offer a form:\n%s", text(connectors))
	}
	wantText(t, connectors, "tracker project, overriding Base Playbook base",
		"command "+fakeConnector+" args --slow marker _Written by an AI agent through JigFlow._ setting log calls.jsonl setting tracker tracker.json setting verbose false Ticket setting label ticket")

	p = connectorFromBase(t)
	wantText(t, section(t, look(t, p.StartUI(), "/playbook"), "Connectors"), "tracker Base Playbook base", "Ticket setting label ticket")
}

func TestWithTheKeyAConnectorsYesNoSettingIsAToggleAnyOtherATextFieldAndOneMayBeAdded(t *testing.T) {
	p := connectorOverBase(t)
	ui := p.StartUI()

	page := get(t, ui, "/playbook")
	action, form := connectorForm(t, page, "tracker", "Save")
	if action != "/playbook/connectors/tracker" {
		t.Errorf("the tracker form posts to %s", action)
	}
	for field, want := range map[string]string{
		"command": fakeConnector, "args": "--slow", "marker": "_Written by an AI agent through JigFlow._",
		"setting.log": "calls.jsonl", "setting.tracker": "tracker.json",
		"type": "Ticket", "type.Ticket.setting.label": "ticket",
		"new-setting": "", "new-value": "", "type.Ticket.new-setting": "", "type.Ticket.new-value": "",
	} {
		if got, ok := form[field]; !ok || got[0] != want {
			t.Errorf("the tracker form sends %s = %q, want %q", field, got, want)
		}
	}
	if form.Has("setting.verbose") {
		t.Errorf("the verbose toggle is on, but verbose is false")
	}
	if !strings.Contains(section(t, page, "Connectors"), `<input type="checkbox" name="setting.verbose" value="true"`) {
		t.Errorf("verbose, a yes/no setting, should be a toggle:\n%s", section(t, page, "Connectors"))
	}
	if _, form := connectorForm(t, page, "tracker", "Back to the Base's"); form == nil {
		t.Errorf("no Back to the Base's form")
	}
	if strings.Contains(page, "GH_TOKEN") || strings.Contains(page, "token:") {
		t.Errorf("the page shows a token")
	}
}

func TestSavingABaseConnectorsSettingsCopiesItIntoThePlaybookFileAndApprovesTheChangeAtOnce(t *testing.T) {
	p := connectorFromBase(t)
	ui := p.StartUI()

	action, form := connectorForm(t, get(t, ui, "/playbook"), "tracker", "Save")
	form.Set("setting.verbose", "true")
	form.Set("new-setting", "repo")
	form.Set("new-value", "acme/shop")
	form.Set("type.Ticket.setting.label", "tickets")
	page := ui.Post(action, form)
	if page.Status != http.StatusOK {
		t.Fatalf("saving the tracker Connector: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), `approved P-1 as one unit: change Connector "tracker": setting repo: acme/shop; setting verbose: true; Ticket setting label: tickets`)
	connectors := section(t, page.HTML, "Connectors")
	wantText(t, connectors, "tracker project, overriding Base Playbook base")
	if _, form := connectorForm(t, page.HTML, "tracker", "Save"); form.Get("setting.verbose") != "true" || form.Get("setting.repo") != "acme/shop" || form.Get("type.Ticket.setting.label") != "tickets" {
		t.Errorf("after saving, the tracker form sends %v", form)
	}
	got := p.Read(".jigflow/playbook.yaml")
	if !strings.HasPrefix(got, "# The shop's Playbook.\nname: mine\nextends: {path: base} # ours\nconnectors:\n  tracker:\n") {
		t.Errorf("the Playbook file should keep what it said and declare the Connector:\n%s", got)
	}
	for _, want := range []string{"repo: acme/shop", "verbose: true", "label: tickets", "in-progress: 'status: doing'", "log: calls.jsonl"} {
		if !strings.Contains(got, want) {
			t.Errorf("the Playbook file should say %q:\n%s", want, got)
		}
	}
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: approved") || strings.Contains(got, "by:") {
		t.Errorf("P-1 should be the person's, approved:\n%s", got)
	}
	ledger := p.MustRun("ledger").Stdout
	if _, confirmations, _ := strings.Cut(ledger, "Confirmations:\n"); !strings.Contains(confirmations, `change Connector "tracker"`) || !strings.Contains(confirmations, "via dashboard") {
		t.Errorf("the Ledger should record the change as confirmed in the Dashboard:\n%s", ledger)
	}
}

func TestATextSettingEmptiedIsRemovedAndATrueOrFalseOneAddedIsAToggle(t *testing.T) {
	p := connectorOverBase(t)
	ui := p.StartUI()

	action, form := connectorForm(t, get(t, ui, "/playbook"), "tracker", "Save")
	form.Set("type.Ticket.setting.label", " ")
	form.Set("type.Ticket.new-setting", "closes")
	form.Set("type.Ticket.new-value", "true")
	page := ui.Post(action, form)
	if page.Status != http.StatusOK {
		t.Fatalf("saving the tracker Connector: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), `change Connector "tracker": Ticket setting closes: true; Ticket setting label: none`)
	if got := p.Read(".jigflow/playbook.yaml"); strings.Contains(got, "label") || !strings.Contains(got, "settings: {closes: true}") {
		t.Errorf("the Playbook file should give Tickets closes, a yes/no setting, and no label:\n%s", got)
	}
	if !strings.Contains(section(t, page.HTML, "Connectors"), `<input type="checkbox" name="type.Ticket.setting.closes" value="true" checked`) {
		t.Errorf("closes, a yes/no setting, should be a toggle that is on:\n%s", section(t, page.HTML, "Connectors"))
	}
}

func TestBackToTheBasesRemovesTheProjectsConnectorAndThePageShowsTheBasesAgain(t *testing.T) {
	p := connectorOverBase(t)
	ui := p.StartUI()

	action, form := connectorForm(t, get(t, ui, "/playbook"), "tracker", "Back to the Base's")
	page := ui.Post(action, form)
	if page.Status != http.StatusOK {
		t.Fatalf("going back to the Base's tracker: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), `approved P-1 as one unit: remove the project's declaration of Connector "tracker"`)
	connectors := section(t, page.HTML, "Connectors")
	wantText(t, connectors, "tracker Base Playbook base")
	if strings.Contains(connectors, "Back to the Base's") {
		t.Errorf("the Base Playbook's Connector offers going back to it:\n%s", text(connectors))
	}
	want := "# The shop's Playbook.\nname: mine\nextends: {path: base} # ours\n"
	if got := p.Read(".jigflow/playbook.yaml"); got != want {
		t.Errorf("the Playbook file reads:\n%s\nwant:\n%s", got, want)
	}
}

func TestAConnectorChangeTheChecksRefuseChangesNoFileAndThePageSaysWhy(t *testing.T) {
	p := connectorOverBase(t)
	ui := p.StartUI()
	page := get(t, ui, "/playbook")
	before := playbookTree(t, p)

	action, form := connectorForm(t, page, "tracker", "Save")
	for edits, why := range map[string]string{
		"command=+": `Connector "tracker" needs a command`,
		"type=Spec&type.Spec.new-setting=label&type.Spec.new-value=spec": `Connector "tracker" maps Artifact Type "Spec", which it doesn't keep`,
		"new-setting=repo": `the new setting repo needs a value`,
		"":                 `nothing to change in Connector "tracker"`,
	} {
		edited := maps.Clone(form)
		changes, _ := url.ParseQuery(edits)
		for k, v := range changes {
			if k == "type" {
				edited[k] = append(slices.Clone(edited[k]), v...)
				continue
			}
			edited[k] = v
		}
		got := ui.Post(action, edited)
		if got.Status != http.StatusConflict {
			t.Errorf("saving %q: status %d, want 409\n%s", edits, got.Status, text(got.HTML))
			continue
		}
		wantText(t, section(t, got.HTML, "Not done"), why)
	}
	if after := playbookTree(t, p); !maps.Equal(after, before) {
		t.Errorf("a refused change changed the project:\n%s", p.Read(".jigflow/playbook.yaml"))
	}
}

func TestOnlyTheBrowserThatOpenedTheLinkMayChangeAConnector(t *testing.T) {
	p := connectorOverBase(t)
	ui := p.StartUI()
	page := get(t, ui, "/playbook")
	save, form := connectorForm(t, page, "tracker", "Save")
	form.Set("command", "true")
	base, _ := connectorForm(t, page, "tracker", "Back to the Base's")
	before := playbookTree(t, p)

	for _, path := range []string{save, base} {
		if got := ui.Curl(ui.NewRequest(http.MethodPost, path, form)); got.Status != http.StatusForbidden {
			t.Errorf("POST %s without the link: status %d, want 403", path, got.Status)
		}
		if got := p.StartUIInSession("A").Post(path, form); got.Status != http.StatusForbidden {
			t.Errorf("POST %s in a Dashboard agent session A started: status %d, want 403", path, got.Status)
		}
	}
	if after := playbookTree(t, p); !maps.Equal(after, before) {
		t.Errorf("a refused change changed the project:\n%s", p.Read(".jigflow/playbook.yaml"))
	}
}

func TestAConnectorAPendingProposalAlsoChangesNamesThatProposalWhichShowsTheValuesItReplaces(t *testing.T) {
	p := connectorOverBase(t)
	p.Write("verbose.yaml", "summary: a verbose tracker\nitems:\n  - {connector: tracker, settings: {verbose: true}}\n")
	if r := p.RunInSession("A", "propose", "verbose.yaml"); r.ExitCode != 0 {
		t.Fatalf("proposing the Connector's settings exited %d:\n%s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	ui := p.StartUI()

	for _, page := range []string{get(t, ui, "/playbook"), look(t, ui, "/playbook")} {
		connectors := section(t, page, "Connectors")
		wantText(t, connectors, "pending Proposal P-1 changes it too")
		if !strings.Contains(connectors, `href="/#P-1"`) {
			t.Errorf("the tracker Connector should link P-1:\n%s", connectors)
		}
	}
	wantText(t, section(t, get(t, ui, "/"), "Pending Proposals"), `change Connector "tracker": setting verbose: true replacing setting verbose: false`)

	action, form := connectorForm(t, get(t, ui, "/playbook"), "tracker", "Save")
	form.Set("marker", "_Drafted._")
	if page := ui.Post(action, form); page.Status != http.StatusOK {
		t.Fatalf("saving the tracker Connector: status %d\n%s", page.Status, text(page.HTML))
	}
	if got := p.Read(".jigflow/proposals/P-1.yaml"); !strings.Contains(got, "status: pending") {
		t.Errorf("P-1 should still be pending:\n%s", got)
	}
}
