package main_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// trackerSettings is how the fake Connector describes its settings, when
// its args give it the file that holds them (ADR 0031).
const trackerSettings = `{"settings": [
  {"name": "tracker", "kind": "text", "required": true, "help": "the file that keeps the fake tracker"},
  {"name": "log", "kind": "text", "help": "the file every request is logged to"},
  {"name": "verbose", "kind": "yesno", "help": "say more"},
  {"name": "retries", "kind": "number", "help": "how often to retry"},
  {"name": "closes", "kind": "yesno", "per_type": true, "help": "close an item when it is done"},
  {"name": "project", "kind": "text", "required": true, "per_type": true, "help": "the project that keeps this Type's items"}
]}`

// describedConnector is connectorOverBase with the fake Connector
// describing its settings as trackerSettings says.
func describedConnector(t *testing.T) *clitest.Project {
	t.Helper()
	p := connectorOverBase(t)
	p.Write("describe.json", trackerSettings)
	p.Write(".jigflow/playbook.yaml", strings.Replace(p.Read(".jigflow/playbook.yaml"), "args: [--slow]", "args: [--slow, --describe=describe.json]", 1))
	return p
}

func TestThePlaybookPageShowsEverySettingAConnectorDescribesSetOrNot(t *testing.T) {
	p := describedConnector(t)
	ui := p.StartUI()

	page := get(t, ui, "/playbook")
	connectors := section(t, page, "Connectors")
	wantText(t, connectors,
		"setting tracker text, required: the file that keeps the fake tracker",
		"setting log text: the file every request is logged to",
		"setting verbose yes/no: say more",
		"setting retries number: how often to retry",
		"Ticket setting closes yes/no, per Artifact Type: close an item when it is done",
		"Ticket setting project text, required, per Artifact Type: the project that keeps this Type's items",
		// A setting the file gives and the Connector doesn't describe
		// is drawn from the file, as before.
		"Ticket setting label")
	if !strings.Contains(connectors, `<input type="checkbox" name="type.Ticket.setting.closes" value="true" aria-label`) {
		t.Errorf("closes, a yes/no setting not set yet, should be a toggle that is off:\n%s", connectors)
	}
	_, form := connectorForm(t, page, "tracker", "Save")
	for field, want := range map[string]string{
		"setting.tracker": "tracker.json", "setting.retries": "", "type.Ticket.setting.project": "", "type.Ticket.setting.label": "ticket",
	} {
		if got, ok := form[field]; !ok || got[0] != want {
			t.Errorf("the tracker form sends %s = %q, want %q", field, got, want)
		}
	}
	for _, field := range []string{"setting.closes", "setting.project", "type.Ticket.setting.tracker"} {
		if strings.Contains(connectors, `name="`+field+`"`) {
			t.Errorf("the form has a field %s, on the wrong side of per Artifact Type", field)
		}
	}

	wantText(t, section(t, look(t, ui, "/playbook"), "Connectors"), "setting retries not set number: how often to retry")
}

func TestADescribedSettingIsSavedAsItsKindAndARequiredOneLeftEmptyIsRefused(t *testing.T) {
	p := describedConnector(t)
	ui := p.StartUI()
	action, form := connectorForm(t, get(t, ui, "/playbook"), "tracker", "Save")
	before := playbookTree(t, p)

	refused := func(edit func(form map[string][]string), why string) {
		t.Helper()
		edited := map[string][]string{}
		for k, v := range form {
			edited[k] = append([]string(nil), v...)
		}
		edit(edited)
		got := ui.Post(action, edited)
		if got.Status != http.StatusConflict {
			t.Errorf("status %d, want 409\n%s", got.Status, text(got.HTML))
			return
		}
		wantText(t, section(t, got.HTML, "Not done"), why)
	}
	refused(func(f map[string][]string) { f["setting.retries"] = []string{"3"} },
		`not proposed: Connector "tracker" needs Ticket setting project: the project that keeps this Type's items`)
	refused(func(f map[string][]string) {
		f["setting.tracker"] = []string{" "}
		f["type.Ticket.setting.project"] = []string{"shop"}
	}, `not proposed: Connector "tracker" needs setting tracker: the file that keeps the fake tracker`)
	if after := playbookTree(t, p); len(after) != len(before) || after[".jigflow/playbook.yaml"] != before[".jigflow/playbook.yaml"] {
		t.Errorf("a refused change changed the Playbook file:\n%s", p.Read(".jigflow/playbook.yaml"))
	}

	form.Set("setting.retries", "3")
	form.Set("type.Ticket.setting.closes", "true")
	form.Set("type.Ticket.setting.project", "shop")
	page := ui.Post(action, form)
	if page.Status != http.StatusOK {
		t.Fatalf("saving the tracker Connector: status %d\n%s", page.Status, text(page.HTML))
	}
	wantText(t, section(t, page.HTML, "Done"), `change Connector "tracker": setting retries: 3; Ticket setting closes: true; Ticket setting project: shop`)
	got := p.Read(".jigflow/playbook.yaml")
	for _, want := range []string{"retries: 3", "closes: true", "project: shop"} {
		if !strings.Contains(got, want) {
			t.Errorf("the Playbook file should say %q:\n%s", want, got)
		}
	}
}

func TestTheGitHubConnectorsCreateLabelsIsAToggleBeforeItIsSet(t *testing.T) {
	p, _ := githubPlaybook(t)
	p.Write(".jigflow/playbook.yaml", strings.Replace(p.Read(".jigflow/playbook.yaml"), ", create_labels: true}", "}", 1))
	ui := p.StartUI()

	connectors := section(t, get(t, ui, "/playbook"), "Connectors")
	if !strings.Contains(connectors, `<input type="checkbox" name="setting.create_labels" value="true" aria-label`) {
		t.Errorf("create_labels, not set yet, should be a toggle that is off:\n%s", connectors)
	}
	wantText(t, connectors,
		"setting repo text, required: the repository that keeps the issues, as owner/name",
		"setting create_labels yes/no: create a label the repository lacks, rather than refuse",
		"setting assignee text: the login a Claim assigns the issue to; the token's user by default",
		// label is per Artifact Type, but this Playbook file gives it
		// for every one.
		"setting label text, per Artifact Type: the label that tells this Artifact Type's issues apart, if any")
}

func TestAConnectorAnsweringDescribeAsUnknownGetsTheFormBuiltFromThePlaybookFile(t *testing.T) {
	// Without --describe, the fake Connector answers describe as an
	// unknown operation, as a Connector written before it did.
	p := connectorOverBase(t)
	ui := p.StartUI()

	page := get(t, ui, "/playbook")
	connectors := section(t, page, "Connectors")
	wantText(t, connectors, "setting log setting tracker setting verbose new setting Ticket setting label new Ticket setting")
	if strings.Contains(connectors, "muted small\">text") || strings.Contains(text(connectors), "yes/no") {
		t.Errorf("a Connector that describes nothing has its settings described:\n%s", text(connectors))
	}
	if _, form := connectorForm(t, page, "tracker", "Save"); form.Get("setting.tracker") != "tracker.json" || form.Get("type.Ticket.setting.label") != "ticket" {
		t.Errorf("the tracker form sends %v", form)
	}
}
