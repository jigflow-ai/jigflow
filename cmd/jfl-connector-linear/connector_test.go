package main_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jigflow-ai/jigflow/internal/clitest/fakelinear"
)

// connector is the path of the built Linear Connector.
var connector string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "jfl-connector-linear-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	connector = filepath.Join(dir, "jfl-connector-linear")
	if runtime.GOOS == "windows" {
		connector += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-o", connector, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// response is the Connector's answer to one request.
type response struct {
	Items []item `json:"items"`
	Item  *item  `json:"item"`
	Error *struct {
		Kind       string `json:"kind"`
		Message    string `json:"message"`
		RetryAfter int    `json:"retry_after"`
	} `json:"error"`
}

type item struct {
	ID     string                         `json:"id"`
	Title  string                         `json:"title"`
	Body   string                         `json:"body"`
	Labels []string                       `json:"labels"`
	State  string                         `json:"state"`
	Claim  string                         `json:"claim"`
	Links  map[string][]map[string]string `json:"links"`
}

// request runs the Connector with one request for Tickets, kept in the fake
// Linear's team ENG with the label ticket, with the settings merged over
// those, and returns its response. The Connector must exit 0.
func request(t *testing.T, ln *fakelinear.Server, req map[string]any, settings ...map[string]any) response {
	t.Helper()
	s := map[string]any{"team": "ENG", "label": "ticket"}
	for _, more := range settings {
		for k, v := range more {
			s[k] = v
		}
	}
	req["protocol"], req["type"], req["settings"] = 1, "Ticket", s
	return run(t, req, "LINEAR_API_KEY="+fakelinear.Key, "LINEAR_API_URL="+ln.URL+"/graphql")
}

// run runs the Connector with the request and the environment given, on
// top of one without Linear credentials.
func run(t *testing.T, req map[string]any, env ...string) response {
	t.Helper()
	in, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(connector)
	cmd.Dir = t.TempDir()
	for _, kv := range os.Environ() {
		if k, _, _ := bytes.Cut([]byte(kv), []byte("=")); string(k) != "LINEAR_API_KEY" && string(k) != "LINEAR_API_URL" {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("the Connector failed on %v: %v\n%s", req["op"], err, stderr.String())
	}
	var resp response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("the Connector's response to %v is not JSON: %v\n%s", req["op"], err, stdout.String())
	}
	return resp
}

// ok fails the test if the Connector answered with an error.
func ok(t *testing.T, resp response) response {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("the Connector answered with an error: %+v", *resp.Error)
	}
	return resp
}

func TestListReturnsTheTeamsIssuesWithTheTypesLabel(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.Add(fakelinear.Issue{Title: "Add login page", Description: "As a user…", Labels: []string{"ticket", "ready-for-agent"}})
	ln.Add(fakelinear.Issue{Title: "Shipped", State: "Done", Labels: []string{"ticket"}})
	ln.Add(fakelinear.Issue{Title: "A spec", Labels: []string{"spec"}})

	resp := ok(t, request(t, ln, map[string]any{"op": "list"}))
	got := fmt.Sprintf("%+v", resp.Items)
	want := fmt.Sprintf("%+v", []item{
		{ID: "41", Title: "Add login page", Body: "As a user…", Labels: []string{"ticket", "ready-for-agent"}, State: "Todo"},
		{ID: "42", Title: "Shipped", Labels: []string{"ticket"}, State: "Done"},
	})
	if got != want {
		t.Errorf("list = %s\nwant %s", got, want)
	}
}

func TestGetReturnsOneIssueAndNotFoundForAnythingElse(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.Add(fakelinear.Issue{Title: "Add login page", Labels: []string{"ticket", "bug"}})
	ln.Add(fakelinear.Issue{Title: "A spec", Labels: []string{"spec"}})

	resp := ok(t, request(t, ln, map[string]any{"op": "get", "id": "41"}))
	if resp.Item == nil || resp.Item.ID != "41" || resp.Item.Title != "Add login page" || resp.Item.State != "Todo" || fmt.Sprint(resp.Item.Labels) != "[ticket bug]" {
		t.Errorf("get 41 = %+v, want issue 41", resp.Item)
	}
	for _, id := range []string{"42", "99", "x", "ENG-41"} {
		if resp := request(t, ln, map[string]any{"op": "get", "id": id}); resp.Error == nil || resp.Error.Kind != "not_found" {
			t.Errorf("get %s answered %+v, want a not_found error: it is no Ticket", id, resp)
		}
	}
}

func TestCreateAddsAnIssueToTheTeamWithTheTypesLabelAndReturnsItsNumber(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.AddLabel("ticket")
	ln.AddLabel("needs-triage")

	resp := ok(t, request(t, ln, map[string]any{"op": "create", "item": map[string]any{
		"title": "Reset endpoint", "body": "_Written by an AI agent through JigFlow._", "labels": []string{"needs-triage"},
	}}))
	if resp.Item == nil || resp.Item.ID != "41" || resp.Item.Title != "Reset endpoint" || resp.Item.State != "Todo" {
		t.Fatalf("create answered %+v, want the new issue 41 in the team's default state", resp.Item)
	}
	i := ln.Issue(t, 41)
	if i.Title != "Reset endpoint" || i.Description != "_Written by an AI agent through JigFlow._" || i.State != "Todo" || fmt.Sprint(i.Labels) != "[needs-triage ticket]" {
		t.Errorf("created issue = %+v, want it in Todo, with the body and labels needs-triage and ticket", i)
	}
}

func TestCreateInAWorkflowStatePutsTheNewIssueInIt(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.AddLabel("ticket")

	ok(t, request(t, ln, map[string]any{"op": "create", "item": map[string]any{"title": "Already done", "state": "Done"}}))
	if i := ln.Issue(t, 41); i.State != "Done" {
		t.Errorf("issue created in the state Done is in %s", i.State)
	}
	resp := request(t, ln, map[string]any{"op": "create", "item": map[string]any{"title": "Nowhere", "state": "Shipped"}})
	if resp.Error == nil || resp.Error.Kind != "invalid" || resp.Error.Message != `team ENG has no workflow state "Shipped"` {
		t.Errorf("create in a state the team lacks answered %+v, want an invalid error naming the state", resp.Error)
	}
}

func TestALabelTheTeamLacksIsRefusedUnlessTheSettingsAskToCreateIt(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.AddLabel("ticket")
	ln.AddOtherTeamsLabel("kind: feature")

	resp := request(t, ln, map[string]any{"op": "create", "item": map[string]any{"title": "Dark mode", "labels": []string{"kind: feature"}}})
	if resp.Error == nil || resp.Error.Kind != "invalid" || resp.Error.Message != `team ENG has no label "kind: feature": create it in Linear, or set create_labels: true in the Connector's settings` {
		t.Errorf("create with a missing label answered %+v, want an invalid error naming the label", resp.Error)
	}
	if got := fmt.Sprint(ln.Labels()); got != "[ticket]" {
		t.Errorf("the team's labels are %s after refusing, want them unchanged", got)
	}

	ok(t, request(t, ln, map[string]any{"op": "create", "item": map[string]any{"title": "Dark mode", "labels": []string{"kind: feature"}}}, map[string]any{"create_labels": true}))
	if got := fmt.Sprint(ln.Labels()); got != "[ticket kind: feature]" {
		t.Errorf("the team's labels are %s, want kind: feature created", got)
	}
	if got := fmt.Sprint(ln.Issue(t, 41).Labels); got != "[kind: feature ticket]" {
		t.Errorf("the new issue's labels are %s", got)
	}
}

func TestStatusSwapsTheIssuesLabelsAndMovesItBetweenWorkflowStates(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.AddLabel("status: doing")
	ln.Add(fakelinear.Issue{Title: "Add login page", Labels: []string{"ticket", "bug", "ready-for-agent"}})

	ok(t, request(t, ln, map[string]any{"op": "status", "id": "41", "from": map[string]any{"label": "ready-for-agent"}, "to": map[string]any{"label": "status: doing", "state": "In Progress"}}))
	if i := ln.Issue(t, 41); fmt.Sprint(i.Labels) != "[ticket bug status: doing]" || i.State != "In Progress" {
		t.Errorf("after the first move the issue is in %s with %q, want it In Progress with status: doing in place of ready-for-agent", i.State, i.Labels)
	}
	ok(t, request(t, ln, map[string]any{"op": "status", "id": "41", "from": map[string]any{"label": "status: doing", "state": "In Progress"}, "to": map[string]any{"state": "Done"}}))
	if i := ln.Issue(t, 41); fmt.Sprint(i.Labels) != "[ticket bug]" || i.State != "Done" {
		t.Errorf("after moving into Done the issue is in %s with %q, want it Done without status: doing", i.State, i.Labels)
	}
	ok(t, request(t, ln, map[string]any{"op": "status", "id": "41", "from": map[string]any{"state": "Done"}, "to": map[string]any{"label": "ready-for-agent"}}))
	if i := ln.Issue(t, 41); fmt.Sprint(i.Labels) != "[ticket bug ready-for-agent]" || i.State != "Todo" {
		t.Errorf("after moving out of Done into a Status with no state the issue is in %s with %q, want it back in the team's default state, Todo", i.State, i.Labels)
	}

	for _, tc := range []struct {
		name string
		req  map[string]any
		kind string
	}{
		{"a missing issue", map[string]any{"op": "status", "id": "99", "to": map[string]any{"label": "ready-for-agent"}}, "not_found"},
		{"a state the team lacks", map[string]any{"op": "status", "id": "41", "to": map[string]any{"state": "Shipped"}}, "invalid"},
		{"a label the team lacks", map[string]any{"op": "status", "id": "41", "to": map[string]any{"label": "status: blocked"}}, "invalid"},
	} {
		if resp := request(t, ln, tc.req); resp.Error == nil || resp.Error.Kind != tc.kind {
			t.Errorf("status to %s answered %+v, want %s", tc.name, resp.Error, tc.kind)
		}
	}
	if i := ln.Issue(t, 41); fmt.Sprint(i.Labels) != "[ticket bug ready-for-agent]" || i.State != "Todo" {
		t.Errorf("after the refused moves the issue is in %s with %q, want it unchanged", i.State, i.Labels)
	}
}

func TestCommentAddsTheBodyAsItIsAsAnIssueComment(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.Add(fakelinear.Issue{Title: "Add login page", Labels: []string{"ticket"}})

	ok(t, request(t, ln, map[string]any{"op": "comment", "id": "41", "body": "Needs a design first.\n\n_Written by an AI agent through JigFlow._"}))
	if got := ln.Issue(t, 41).Comments; len(got) != 1 || got[0] != "Needs a design first.\n\n_Written by an AI agent through JigFlow._" {
		t.Errorf("comments = %q, want the body as it was sent", got)
	}
	if resp := request(t, ln, map[string]any{"op": "comment", "id": "99", "body": "Hello"}); resp.Error == nil || resp.Error.Kind != "not_found" {
		t.Errorf("comment on a missing issue answered %+v, want not_found", resp.Error)
	}
}

func TestAClaimAssignsTheAPIKeysUserAndIsReadBackAsTheSession(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.Add(fakelinear.Issue{Title: "Add login page", Description: "As a user…", Labels: []string{"ticket"}})

	ok(t, request(t, ln, map[string]any{"op": "claim", "id": "41", "claim": "agent-1727000000"}))
	i := ln.Issue(t, 41)
	if i.Assignee != "jfl-bot" || len(i.Attachments) != 1 || i.Attachments[0].Subtitle != "Claimed by agent session agent-1727000000" {
		t.Errorf("after the Claim the issue is %+v, want it assigned to the API key's user jfl-bot, with an attachment showing the session", i)
	}
	got := ok(t, request(t, ln, map[string]any{"op": "get", "id": "41"})).Item
	if got.Claim != "agent-1727000000" || got.Body != "As a user…" {
		t.Errorf("get after the Claim = %+v, want claim agent-1727000000 and the body as it was", got)
	}
	if items := ok(t, request(t, ln, map[string]any{"op": "list"})).Items; len(items) != 1 || items[0].Claim != "agent-1727000000" {
		t.Errorf("list after the Claim = %+v, want claim agent-1727000000", items)
	}

	ok(t, request(t, ln, map[string]any{"op": "claim", "id": "41", "claim": ""}))
	if i := ln.Issue(t, 41); i.Assignee != "" || len(i.Attachments) != 0 || i.Description != "As a user…" {
		t.Errorf("after clearing the Claim the issue is %+v, want no assignee, no attachment and the body as it was", i)
	}
	if got := ok(t, request(t, ln, map[string]any{"op": "get", "id": "41"})).Item; got.Claim != "" {
		t.Errorf("claim after clearing it = %q, want none", got.Claim)
	}
}

func TestAClaimAssignsTheAssigneeTheSettingsName(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.Add(fakelinear.Issue{Title: "Add login page", Labels: []string{"ticket"}})

	ok(t, request(t, ln, map[string]any{"op": "claim", "id": "41", "claim": "agent-1"}, map[string]any{"assignee": "roberto"}))
	if got := ln.Issue(t, 41).Assignee; got != "roberto" {
		t.Errorf("assignee = %q, want roberto", got)
	}
	resp := request(t, ln, map[string]any{"op": "claim", "id": "41", "claim": "agent-1"}, map[string]any{"assignee": "nobody"})
	if resp.Error == nil || resp.Error.Kind != "invalid" || resp.Error.Message != `the workspace has no user with the display name "nobody": set assignee in the Connector's settings to one` {
		t.Errorf("a Claim for an unknown assignee answered %+v, want an invalid error naming them", resp.Error)
	}
}

func TestAnIssueSomeoneAssignedInLinearIsClaimedByThem(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.Add(fakelinear.Issue{Title: "Add login page", Labels: []string{"ticket"}, Assignee: "alice"})
	ln.Add(fakelinear.Issue{Title: "Reset endpoint", Labels: []string{"ticket"}})
	ok(t, request(t, ln, map[string]any{"op": "claim", "id": "42", "claim": "agent-1"}))
	// A teammate takes the issue over in Linear.
	ln.Edit(42, func(i *fakelinear.Issue) { i.Assignee = "bob" })

	for id, want := range map[string]string{"41": "alice", "42": "bob"} {
		if got := ok(t, request(t, ln, map[string]any{"op": "get", "id": id})).Item.Claim; got != want {
			t.Errorf("claim of %s = %q, want its assignee %s", id, got, want)
		}
	}
	// Clearing the agent's Claim leaves the teammate's assignment.
	ok(t, request(t, ln, map[string]any{"op": "claim", "id": "42", "claim": ""}))
	if got := ln.Issue(t, 42).Assignee; got != "bob" {
		t.Errorf("assignee after clearing the agent's Claim = %q, want bob", got)
	}
}

func TestBlockedByLinksAreLinearBlockingRelations(t *testing.T) {
	ln := fakelinear.New(t, 41)
	ln.AddLabel("ticket")
	ln.Add(fakelinear.Issue{Title: "Reset-token table", Labels: []string{"ticket"}})
	ln.Add(fakelinear.Issue{Title: "Mail templates", Labels: []string{"ticket"}})

	resp := ok(t, request(t, ln, map[string]any{"op": "create", "item": map[string]any{
		"title": "Reset endpoint", "body": "Marked.",
		"links": map[string]any{"blocked_by": []map[string]string{{"id": "41"}, {"id": "42"}}, "part_of": []map[string]string{{"artifact": "S-3"}}},
	}}))
	if got := ln.Issue(t, 43).BlockedBy; fmt.Sprint(got) != "[41 42]" {
		t.Errorf("Linear's blocking relations of the new issue = %v, want it blocked by 41 and 42", got)
	}
	if got := fmt.Sprint(resp.Item.Links); got != "map[blocked_by:[map[id:41] map[id:42]] part_of:[map[artifact:S-3]]]" {
		t.Errorf("create answered the links %s", got)
	}
	// A teammate marks the issue blocked by another in Linear.
	ln.Add(fakelinear.Issue{Number: 50, Title: "Rate limiter", Labels: []string{"ticket"}})
	ln.Edit(43, func(i *fakelinear.Issue) { i.BlockedBy = append(i.BlockedBy, 50) })

	want := "map[blocked_by:[map[id:41] map[id:42] map[id:50]] part_of:[map[artifact:S-3]]]"
	if got := ok(t, request(t, ln, map[string]any{"op": "get", "id": "43"})).Item; fmt.Sprint(got.Links) != want || got.Body != "Marked." {
		t.Errorf("get answered the links %v and body %q, want %s and the body as created", got.Links, got.Body, want)
	}
	items := ok(t, request(t, ln, map[string]any{"op": "list"})).Items
	if len(items) != 4 || fmt.Sprint(items[2].Links) != want || items[0].Links != nil {
		t.Errorf("list answered %+v, want 43 with the links %s", items, want)
	}
	if resp := request(t, ln, map[string]any{"op": "create", "item": map[string]any{"title": "Orphan", "links": map[string]any{"blocked_by": []map[string]string{{"id": "99"}}}}}); resp.Error == nil || resp.Error.Kind != "invalid" {
		t.Errorf("create blocked by a missing issue answered %+v, want invalid", resp.Error)
	}
}

func TestListFollowsLinearsPages(t *testing.T) {
	ln := fakelinear.New(t, 1)
	ln.PageSize(2)
	for n := range 5 {
		ln.Add(fakelinear.Issue{Title: fmt.Sprint("Issue ", n+1), Labels: []string{"ticket"}})
	}
	if items := ok(t, request(t, ln, map[string]any{"op": "list"})).Items; len(items) != 5 || items[4].ID != "5" {
		t.Errorf("list answered %d items, want all 5 across 3 pages: %+v", len(items), items)
	}
}

func TestLinearFailuresAreReportedByKind(t *testing.T) {
	get := map[string]any{"op": "get", "id": "41"}
	for _, tc := range []struct {
		name  string
		setup func(*fakelinear.Server)
		env   []string
		req   map[string]any
		kind  string
		retry int
	}{
		{name: "no API key", env: []string{}, req: get, kind: "auth"},
		{name: "an API key Linear refuses", env: []string{"LINEAR_API_KEY=wrong"}, req: get, kind: "auth"},
		{name: "rate limit", setup: (*fakelinear.Server).RateLimit, req: get, kind: "rate_limit", retry: 42},
		{name: "bad gateway", setup: func(s *fakelinear.Server) { s.Fail(502) }, req: get, kind: "network"},
		{name: "server error", setup: func(s *fakelinear.Server) { s.Fail(500) }, req: get, kind: "internal"},
		{name: "unreachable", setup: (*fakelinear.Server).Close, req: get, kind: "network"},
		{name: "a team Linear doesn't show", req: map[string]any{"op": "list", "settings": map[string]any{"team": "OPS"}}, kind: "invalid"},
		{name: "a team Linear doesn't show, on get", req: map[string]any{"op": "get", "id": "41", "settings": map[string]any{"team": "OPS"}}, kind: "invalid"},
		{name: "no team", req: map[string]any{"op": "list", "settings": map[string]any{}}, kind: "invalid"},
		{name: "another protocol", req: map[string]any{"op": "list", "protocol": 2}, kind: "invalid"},
		{name: "an unknown op", req: map[string]any{"op": "delete"}, kind: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln := fakelinear.New(t, 41)
			ln.Add(fakelinear.Issue{Title: "Add login page", Labels: []string{"ticket"}})
			if tc.setup != nil {
				tc.setup(ln)
			}
			req := map[string]any{"protocol": 1, "type": "Ticket", "settings": map[string]any{"team": "ENG"}}
			for k, v := range tc.req {
				req[k] = v
			}
			env := []string{"LINEAR_API_KEY=" + fakelinear.Key}
			if tc.env != nil {
				env = tc.env
			}
			resp := run(t, req, append(env, "LINEAR_API_URL="+ln.URL+"/graphql")...)
			if resp.Error == nil || resp.Error.Kind != tc.kind || resp.Error.RetryAfter != tc.retry || resp.Error.Message == "" {
				t.Errorf("answered %+v, want a %s error with a message and retry_after %d", resp.Error, tc.kind, tc.retry)
			}
		})
	}
}
