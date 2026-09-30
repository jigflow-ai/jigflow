package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest"
)

// trackerPlaybook is a project whose Tickets are kept in a tracker reached
// through the fake Connector, which starts numbering its items at 41. The
// project settings map two Statuses and one category away from the
// Playbook's own names.
func trackerPlaybook(t *testing.T) *clitest.Project {
	t.Helper()
	p := bin.NewProject(t)
	p.Write(".jigflow/playbook.yaml", `name: team
connectors:
  tracker:
    command: `+fakeConnector+`
    settings: {tracker: tracker.json, log: calls.jsonl}
    types:
      Ticket:
        statuses:
          in-progress: "status: doing"
          done: {state: closed}
        fields:
          category:
            enhancement: {label: "kind: feature"}
`)
	p.Write(".jigflow/types/ticket.yaml", `name: Ticket
prefix: T
store: tracker
statuses: [needs-triage, ready-for-agent, in-progress, in-review, done]
initial: [needs-triage, ready-for-agent]
final: [done]
fields:
  category: [bug, enhancement]
links:
  blocked_by: Ticket
bindings:
  ready-for-agent: implement
  in-progress: implement
readiness:
  ready-for-agent:
    - {kind: linked-all-in, link: blocked_by, statuses: [done]}
transitions:
  - from: needs-triage
    to: ready-for-agent
    human: true
  - from: ready-for-agent
    to: in-progress
  - from: in-progress
    to: in-review
  - from: in-review
    to: done
`)
	writeSkill(p, "implement", true)
	setTracker(t, p, map[string]any{"next": 41})
	return p
}

// setTracker replaces the fake Connector's tracker.
func setTracker(t *testing.T, p *clitest.Project, tr map[string]any) {
	t.Helper()
	data, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	p.Write("tracker.json", string(data))
}

// trackerItem is an item as the fake Connector keeps it.
type trackerItem struct {
	ID       string                         `json:"id"`
	Title    string                         `json:"title"`
	Body     string                         `json:"body"`
	Labels   []string                       `json:"labels"`
	State    string                         `json:"state"`
	Claim    string                         `json:"claim"`
	Links    map[string][]map[string]string `json:"links"`
	Comments []string                       `json:"comments"`
}

// item returns the fake tracker's item with the given id.
func item(t *testing.T, p *clitest.Project, id string) trackerItem {
	t.Helper()
	var tr struct{ Items []trackerItem }
	if err := json.Unmarshal([]byte(p.Read("tracker.json")), &tr); err != nil {
		t.Fatal(err)
	}
	for _, it := range tr.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("the tracker has no item %s:\n%s", id, p.Read("tracker.json"))
	return trackerItem{}
}

// call is one request jfl made of the fake Connector.
type call struct {
	Protocol int            `json:"protocol"`
	Op       string         `json:"op"`
	Type     string         `json:"type"`
	Settings map[string]any `json:"settings"`
	ID       string         `json:"id"`
	Item     trackerItem    `json:"item"`
	From     map[string]any `json:"from"`
	To       map[string]any `json:"to"`
	Claim    *string        `json:"claim"`
	Body     string         `json:"body"`
}

// calls returns the requests of the given operation jfl made of the fake
// Connector, in order.
func calls(t *testing.T, p *clitest.Project, op string) []call {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.Dir, "calls.jsonl"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []call
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var c call
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("logged request is not JSON: %v\n%s", err, line)
		}
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

func TestAnArtifactTypeKeptByAConnectorIsCreatedInTheTracker(t *testing.T) {
	p := trackerPlaybook(t)

	r := p.MustRun("create", "Ticket", "--title", "Add login page")
	if !strings.Contains(r.Stdout, `created T-41 "Add login page" in needs-triage`) {
		t.Errorf("create output %q should name the Artifact by the tracker's id, T-41", r.Stdout)
	}
	creates := calls(t, p, "create")
	if len(creates) != 1 {
		t.Fatalf("jfl made %d create requests of the Connector, want 1", len(creates))
	}
	if c := creates[0]; c.Protocol != 1 || c.Type != "Ticket" || c.Item.Title != "Add login page" || strings.Join(c.Item.Labels, ",") != "needs-triage" {
		t.Errorf("create request = %+v, want protocol 1 creating Ticket %q labelled needs-triage", c, "Add login page")
	}
	if _, err := os.Stat(filepath.Join(p.Dir, ".jigflow/state")); !os.IsNotExist(err) {
		t.Errorf("a Ticket kept by a Connector was written to the file Store too (%v)", err)
	}
}

func TestTheProjectSettingsMapTrackerLabelsAndStatesToStatuses(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 44, "items": []map[string]any{
		{"id": "41", "title": "Filed with no label"},
		{"id": "42", "title": "Being worked on", "labels": []string{"status: doing"}},
		{"id": "43", "title": "Shipped", "state": "closed"},
	}})

	r := p.MustRun("query")
	for _, want := range []string{
		`T-41 Ticket "Filed with no label": needs-triage`,
		`T-42 Ticket "Being worked on": in-progress`,
		`T-43 Ticket "Shipped": done`,
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("query should list %q:\n%s", want, r.Stdout)
		}
	}
}

func TestAMoveChangesTheTrackersLabelsAndStateAsTheProjectSettingsMapThem(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent", "bug"}},
	}})

	p.MustRun("move", "T-41", "in-progress")
	if got := item(t, p, "41").Labels; strings.Join(got, ",") != "bug,status: doing" {
		t.Errorf("labels after moving into in-progress = %q, want the mapped label in place of ready-for-agent", got)
	}
	p.MustRun("move", "T-41", "in-review")
	p.MustRun("move", "T-41", "done")
	if it := item(t, p, "41"); it.State != "closed" || strings.Join(it.Labels, ",") != "bug" {
		t.Errorf("after moving into done the item is %q with labels %q, want it closed with in-review removed", it.State, it.Labels)
	}
	status := calls(t, p, "status")
	if len(status) != 3 || status[0].From["label"] != "ready-for-agent" || status[0].To["label"] != "status: doing" {
		t.Errorf("status requests = %+v, want 3, the first from label ready-for-agent to label %q", status, "status: doing")
	}
	if r := p.MustRun("query"); !strings.Contains(r.Stdout, `T-41 Ticket "Add login page": done`) {
		t.Errorf("query after the moves:\n%s", r.Stdout)
	}
}

func TestAnAgentsTransitionSetsTheClaimInTheTrackerAndEnteringHumanWorkClearsIt(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent"}},
	}})

	agentMove(t, p, "T-41", "in-progress", 0)
	if got := item(t, p, "41").Claim; got != "A" {
		t.Errorf("tracker claim = %q after agent session A's Transition, want A", got)
	}
	agentMove(t, p, "T-41", "in-review", 0)
	if got := item(t, p, "41").Claim; got != "" {
		t.Errorf("tracker claim = %q after entering in-review, which has no Binding, want it cleared", got)
	}
	claims := calls(t, p, "claim")
	if len(claims) != 2 || claims[0].Claim == nil || *claims[0].Claim != "A" || claims[1].Claim == nil || *claims[1].Claim != "" {
		t.Errorf("claim requests = %+v, want one setting A and one clearing it", claims)
	}
}

func TestAnotherSessionsClaimInTheTrackerRefusesAnAgentsTransition(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"status: doing"}, "claim": "B"},
	}})

	r := agentMove(t, p, "T-41", "in-review", 1)
	if want := "T-41 is claimed by agent session B."; !strings.Contains(r.Stderr, want) {
		t.Errorf("refusal %q should contain %q", r.Stderr, want)
	}
	if len(calls(t, p, "status")) != 0 || len(calls(t, p, "claim")) != 0 {
		t.Errorf("a refused move changed the tracker:\n%s", p.Read("calls.jsonl"))
	}
}

func TestConnectorFailuresAreReportedAsTrackerProblemsNotWorkflowRefusals(t *testing.T) {
	for _, tc := range []struct{ kind, failOn, want string }{
		{"auth", "", `Connector "tracker" could not authenticate with the tracker: the fake tracker says auth. Check the credentials it uses.`},
		{"rate_limit", "", `Connector "tracker" was rate-limited by the tracker: the fake tracker says rate_limit. Retry in 30s.`},
		{"network", "", `Connector "tracker" could not reach the tracker: the fake tracker says network. Check the network and retry.`},
		{"network", "status", `Connector "tracker" could not reach the tracker`},
		{"crash", "", `Connector "tracker" broke the Connector protocol on get: exit status 2: fakeconnector: crashed`},
	} {
		t.Run(tc.kind+"/"+tc.failOn, func(t *testing.T) {
			p := trackerPlaybook(t)
			setTracker(t, p, map[string]any{"next": 42, "fail": tc.kind, "fail_on": tc.failOn, "items": []map[string]any{
				{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent"}},
			}})

			r := p.Run("move", "T-41", "in-progress")
			if r.ExitCode != 3 {
				t.Fatalf("move with a failing Connector exited %d, want 3; stderr: %s", r.ExitCode, r.Stderr)
			}
			if want := "jfl move: tracker problem, not a workflow refusal: " + tc.want; !strings.HasPrefix(r.Stderr, want) {
				t.Errorf("stderr = %q, want it to start with %q", r.Stderr, want)
			}
		})
	}
}

func TestAWorkflowRefusalOnATrackerArtifactIsNotATrackerProblem(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent"}},
	}})

	r := p.Run("move", "T-41", "done")
	if r.ExitCode != 1 || strings.Contains(r.Stderr, "tracker problem") || !strings.Contains(r.Stderr, "is not a declared Transition") {
		t.Errorf("an undeclared Transition exited %d with %q, want 1 and a workflow refusal", r.ExitCode, r.Stderr)
	}
	r = p.Run("move", "T-99", "in-progress")
	if r.ExitCode != 1 || strings.Contains(r.Stderr, "tracker problem") || !strings.Contains(r.Stderr, "no such Artifact") {
		t.Errorf("moving an item the tracker doesn't have exited %d with %q, want 1 and no such Artifact", r.ExitCode, r.Stderr)
	}
}

func TestAnArtifactAnAgentCreatesInTheTrackerCarriesTheAIGeneratedMarker(t *testing.T) {
	p := trackerPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Filed by a person")
	if r := p.RunInSession("A", "create", "Ticket", "--title", "Filed by an agent"); r.ExitCode != 0 {
		t.Fatalf("agent create into needs-triage exited %d: %s", r.ExitCode, r.Stderr)
	}
	if got := item(t, p, "41").Body; got != "" {
		t.Errorf("a person's Artifact has body %q, want no marker", got)
	}
	if got := item(t, p, "42").Body; got != "_Written by an AI agent through JigFlow._" {
		t.Errorf("an agent's Artifact has body %q, want the default AI-generated marker", got)
	}
}

func TestAnAgentsCommentInTheTrackerCarriesTheMarkerTheSettingsWord(t *testing.T) {
	p := trackerPlaybook(t)
	p.Write(".jigflow/playbook.yaml", strings.Replace(p.Read(".jigflow/playbook.yaml"), "    types:\n", "    marker: \"-- drafted by an agent, check it\"\n    types:\n", 1))
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page"},
	}})

	if r := p.RunInSession("A", "comment", "T-41", "Needs a design first."); r.ExitCode != 0 || !strings.Contains(r.Stdout, "commented on T-41") {
		t.Fatalf("agent comment exited %d: %s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	p.MustRun("comment", "T-41", "Agreed.")
	got := item(t, p, "41").Comments
	want := []string{"Needs a design first.\n\n-- drafted by an agent, check it", "Agreed."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("comments = %q, want %q", got, want)
	}
}

func TestACommentOnAFileArtifactIsAppendedToItsBody(t *testing.T) {
	p := ticketPlaybook(t)
	p.MustRun("create", "Ticket", "--title", "Add login page")
	if r := p.RunInSession("A", "comment", "T-1", "Needs a design first."); r.ExitCode != 0 {
		t.Fatalf("comment exited %d: %s", r.ExitCode, r.Stderr)
	}
	if got := p.Read(".jigflow/state/T-1.md"); !strings.HasSuffix(got, "**Comment by agent session A:**\n\nNeeds a design first.\n") {
		t.Errorf("the comment isn't appended to the body:\n%s", got)
	}
	// jfl wrote the comment, so the move after it finds no edit outside jfl.
	if r := p.MustRun("move", "T-1", "in-progress"); strings.Contains(r.Stdout, "edited outside jfl") {
		t.Errorf("a comment made through jfl counts as an edit outside it:\n%s", r.Stdout)
	}
}

func TestTheProjectSettingsMapFieldValuesToTrackerLabels(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Crash on login", "labels": []string{"bug", "ready-for-agent"}},
	}})

	p.MustRun("create", "Ticket", "--title", "Dark mode", "--field", "category=enhancement")
	if got := item(t, p, "42").Labels; strings.Join(got, ",") != "needs-triage,kind: feature" {
		t.Errorf("labels of a new enhancement = %q, want the mapped label %q", got, "kind: feature")
	}
	r := p.MustRun("query")
	for _, want := range []string{`T-41 Ticket "Crash on login": ready-for-agent, category: bug`, `T-42 Ticket "Dark mode": needs-triage, category: enhancement`} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("query should list %q:\n%s", want, r.Stdout)
		}
	}
}

func TestCreateRefusesAFieldValueTheTypeDoesntDeclare(t *testing.T) {
	p := trackerPlaybook(t)
	r := p.Run("create", "Ticket", "--title", "Dark mode", "--field", "category=chore")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, `a Ticket's category can't be "chore". Declared values: bug, enhancement`) {
		t.Errorf("create with an undeclared value exited %d: %s", r.ExitCode, r.Stderr)
	}
	r = p.Run("create", "Ticket", "--title", "Dark mode", "--field", "size=small")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, `a Ticket has no field "size". Declared fields: category`) {
		t.Errorf("create with an undeclared field exited %d: %s", r.ExitCode, r.Stderr)
	}
	if len(calls(t, p, "create")) != 0 {
		t.Errorf("a refused create reached the tracker:\n%s", p.Read("calls.jsonl"))
	}
}

func TestLinksBetweenTrackerArtifactsUseTheTrackersIds(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Reset-token table", "labels": []string{"ready-for-agent"}},
	}})

	p.MustRun("create", "Ticket", "--title", "Reset endpoint", "--status", "ready-for-agent", "--link", "blocked_by=T-41")
	if got := item(t, p, "42").Links["blocked_by"]; len(got) != 1 || got[0]["id"] != "41" {
		t.Errorf("the tracker's blocked_by of T-42 = %v, want the tracker's id 41", got)
	}
	_, all := agentNext(t, p)
	if want := `T-42: not ready: waiting until every "blocked_by" item is done`; !strings.Contains(all, want) {
		t.Errorf("next should follow the tracker's Link and say %q:\n%s", want, all)
	}
}

func TestApprovingAProposalCreatesItsArtifactsInTheTrackerAndLinksThemByTheTrackersIds(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 50, "items": []map[string]any{
		{"id": "41", "title": "Unrelated", "labels": []string{"needs-triage"}},
	}})
	p.Write("breakdown.yaml", `summary: break the reset flow into tickets
items:
  - {create: Ticket, ref: table, title: Reset-token table, status: ready-for-agent}
  - {create: Ticket, title: Reset endpoint, status: ready-for-agent, links: {blocked_by: [table]}}
  - {move: table, to: in-progress}
`)
	if r := p.RunInSession("A", "propose", "breakdown.yaml"); r.ExitCode != 0 {
		t.Fatalf("propose exited %d: %s", r.ExitCode, r.Stderr)
	}

	r := approveInTerminal(t, p, "P-1")
	for _, want := range []string{`created T-50 "Reset-token table" in ready-for-agent`, `created T-51 "Reset endpoint" in ready-for-agent`, "T-50: ready-for-agent → in-progress"} {
		if !strings.Contains(r.Output, want) {
			t.Errorf("approve should report %q:\n%s", want, r.Output)
		}
	}
	if got := item(t, p, "51").Links["blocked_by"]; len(got) != 1 || got[0]["id"] != "50" {
		t.Errorf("the tracker's blocked_by of T-51 = %v, want the tracker's id 50", got)
	}
	table := item(t, p, "50")
	if strings.Join(table.Labels, ",") != "status: doing" || table.Body != "_Written by an AI agent through JigFlow._" {
		t.Errorf("T-50 is %+v, want it in-progress and marked as written by an agent, whose Proposal it was", table)
	}
}

func TestAStatusChangedInTheTrackerWhileTheGatesRunRefusesTheMove(t *testing.T) {
	p := trackerPlaybook(t)
	// The Gate stands in for a teammate relabelling the item in the tracker
	// while the move waits on it.
	p.Write(".jigflow/types/ticket.yaml", strings.Replace(p.Read(".jigflow/types/ticket.yaml"), "    to: in-review\n",
		"    to: in-review\n    gates:\n      - {name: relabel, cmd: \"sed -i.bak 's/status: doing/ready-for-agent/' tracker.json\"}\n", 1))
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"status: doing"}},
	}})

	r := p.Run("move", "T-41", "in-review")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, `T-41: its Status changed in the tracker from "in-progress" to "ready-for-agent" while jfl was changing it`) {
		t.Errorf("move exited %d with %q, want a refusal saying the tracker changed", r.ExitCode, r.Stderr)
	}
	if len(calls(t, p, "status")) != 0 {
		t.Errorf("the move overwrote the tracker's change:\n%s", p.Read("calls.jsonl"))
	}
}

func TestAStatusChangedInTheTrackerBeforeAMoveIsWhereTheMoveStarts(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"ready-for-agent"}},
	}})
	p.MustRun("move", "T-41", "in-progress")
	// A teammate moves it on in the tracker: the tracker is the source of
	// truth for its Artifacts, so jfl doesn't hold it to what it wrote.
	setTracker(t, p, map[string]any{"next": 42, "items": []map[string]any{
		{"id": "41", "title": "Add login page", "labels": []string{"in-review"}},
	}})

	r := p.MustRun("move", "T-41", "done")
	if !strings.Contains(r.Stdout, "T-41: in-review → done") {
		t.Errorf("move output %q, want it to move from the tracker's in-review", r.Stdout)
	}
}

func TestAnArtifactTypeDeclaringTheFileStoreKeepsItsArtifactsInFiles(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".jigflow/types/ticket.yaml", strings.Replace(p.Read(".jigflow/types/ticket.yaml"), "prefix: T\n", "prefix: T\nstore: files\n", 1))
	p.MustRun("create", "Ticket", "--title", "Add login page")
	if got := frontmatter(t, p.Read(".jigflow/state/T-1.md"))["title"]; got != "Add login page" {
		t.Errorf("title in the file Store = %q", got)
	}
}

func TestCheckRefusesStoresAndProjectSettingsThatDontMatchThePlaybook(t *testing.T) {
	p := trackerPlaybook(t)
	p.Write(".jigflow/types/spec.yaml", "name: Spec\nprefix: S\nstore: jira\nstatuses: [open]\ninitial: [open]\nfinal: [open]\n")
	p.Write(".jigflow/playbook.yaml", strings.Replace(p.Read(".jigflow/playbook.yaml"), "          done: {state: closed}\n",
		"          done: {state: closed}\n          shipped: shipped\n          in-review: {}\n", 1)+`      Spec:
        statuses: {open: open}
  empty: {}
`)

	r := p.Run("check")
	for _, want := range []string{
		`.jigflow/types/spec.yaml: its Store "jira" is neither files nor a Connector the Playbook file declares`,
		`.jigflow/playbook.yaml: Connector "tracker" maps Status "shipped", which a Ticket doesn't declare`,
		`.jigflow/playbook.yaml: Connector "tracker" maps Status "in-review" to no label or state`,
		`.jigflow/playbook.yaml: Connector "tracker" maps Artifact Type "Spec", which it doesn't keep`,
		`.jigflow/playbook.yaml: Connector "empty" needs a command`,
	} {
		if !strings.Contains(r.Stderr, want) {
			t.Errorf("check should report %q:\n%s", want, r.Stderr)
		}
	}
	if r.ExitCode != 1 {
		t.Errorf("check exited %d, want 1", r.ExitCode)
	}
}

func TestCheckDoesntNeedTheTracker(t *testing.T) {
	p := trackerPlaybook(t)
	setTracker(t, p, map[string]any{"fail": "network"})
	if r := p.Run("check"); r.ExitCode != 0 {
		t.Errorf("check with the tracker unreachable exited %d: %s", r.ExitCode, r.Stderr)
	}
}

func TestAFileArtifactKeepsItsFields(t *testing.T) {
	p := ticketPlaybook(t)
	p.Write(".jigflow/types/ticket.yaml", strings.Replace(p.Read(".jigflow/types/ticket.yaml"), "prefix: T\n", "prefix: T\nfields: {category: [bug, enhancement]}\n", 1))
	p.MustRun("create", "Ticket", "--title", "Crash on login", "--field", "category=bug")
	if r := p.MustRun("query"); !strings.Contains(r.Stdout, `T-1 Ticket "Crash on login": ready-for-agent, category: bug`) {
		t.Errorf("query should list T-1's category:\n%s", r.Stdout)
	}
}
