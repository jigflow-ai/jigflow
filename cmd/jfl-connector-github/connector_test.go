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

	"github.com/jigflow-ai/jigflow/internal/clitest/fakegithub"
)

// connector is the path of the built GitHub Issues Connector.
var connector string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "jfl-connector-github-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	connector = filepath.Join(dir, "jfl-connector-github")
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
// GitHub's acme/shop with the label ticket, with the settings merged over
// those, and returns its response. The Connector must exit 0.
func request(t *testing.T, gh *fakegithub.Server, req map[string]any, settings ...map[string]any) response {
	t.Helper()
	s := map[string]any{"repo": "acme/shop", "label": "ticket"}
	for _, more := range settings {
		for k, v := range more {
			s[k] = v
		}
	}
	req["protocol"], req["type"], req["settings"] = 1, "Ticket", s
	return run(t, req, "GH_TOKEN="+fakegithub.Token, "GITHUB_API_URL="+gh.URL)
}

// run runs the Connector with the request and the environment given, on
// top of one without GitHub credentials.
func run(t *testing.T, req map[string]any, env ...string) response {
	t.Helper()
	in, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(connector)
	cmd.Dir = t.TempDir()
	for _, kv := range os.Environ() {
		if k, _, _ := bytes.Cut([]byte(kv), []byte("=")); string(k) != "GH_TOKEN" && string(k) != "GITHUB_TOKEN" && string(k) != "GITHUB_API_URL" {
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

func TestListReturnsTheIssuesWithTheTypesLabelAndNoPullRequests(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.Add(fakegithub.Issue{Title: "Add login page", Body: "As a user…", Labels: []string{"ticket", "ready-for-agent"}})
	gh.Add(fakegithub.Issue{Title: "Shipped", State: "closed", Labels: []string{"ticket"}})
	gh.Add(fakegithub.Issue{Title: "A spec", Labels: []string{"spec"}})
	gh.Add(fakegithub.Issue{Title: "A pull request", Labels: []string{"ticket"}, PullRequest: true})

	resp := ok(t, request(t, gh, map[string]any{"op": "list"}))
	got := fmt.Sprintf("%+v", resp.Items)
	want := fmt.Sprintf("%+v", []item{
		{ID: "41", Title: "Add login page", Body: "As a user…", Labels: []string{"ticket", "ready-for-agent"}, State: "open"},
		{ID: "42", Title: "Shipped", Labels: []string{"ticket"}, State: "closed"},
	})
	if got != want {
		t.Errorf("list = %s\nwant %s", got, want)
	}
}

func TestGetReturnsOneIssueAndNotFoundForAnythingElse(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.Add(fakegithub.Issue{Title: "Add login page", Labels: []string{"ticket", "bug"}})
	gh.Add(fakegithub.Issue{Title: "A spec", Labels: []string{"spec"}})
	gh.Add(fakegithub.Issue{Title: "A pull request", Labels: []string{"ticket"}, PullRequest: true})

	resp := ok(t, request(t, gh, map[string]any{"op": "get", "id": "41"}))
	if resp.Item == nil || resp.Item.ID != "41" || resp.Item.Title != "Add login page" || resp.Item.State != "open" {
		t.Errorf("get 41 = %+v, want issue 41", resp.Item)
	}
	for _, id := range []string{"42", "43", "99", "x"} {
		if resp := request(t, gh, map[string]any{"op": "get", "id": id}); resp.Error == nil || resp.Error.Kind != "not_found" {
			t.Errorf("get %s answered %+v, want a not_found error: it is no Ticket", id, resp)
		}
	}
}

func TestCreateOpensAnIssueWithTheTypesLabelAndReturnsItsNumber(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.AddLabel("ticket")
	gh.AddLabel("needs-triage")

	resp := ok(t, request(t, gh, map[string]any{"op": "create", "item": map[string]any{
		"title": "Reset endpoint", "body": "_Written by an AI agent through JigFlow._", "labels": []string{"needs-triage"},
	}}))
	if resp.Item == nil || resp.Item.ID != "41" || resp.Item.Title != "Reset endpoint" {
		t.Fatalf("create answered %+v, want the new issue 41", resp.Item)
	}
	i := gh.Issue(t, 41)
	if i.Title != "Reset endpoint" || i.Body != "_Written by an AI agent through JigFlow._" || i.State != "open" || fmt.Sprint(i.Labels) != "[needs-triage ticket]" {
		t.Errorf("created issue = %+v, want it open, with the body and labels needs-triage and ticket", i)
	}
}

func TestCreateInAClosedStateClosesTheNewIssue(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.AddLabel("ticket")

	ok(t, request(t, gh, map[string]any{"op": "create", "item": map[string]any{"title": "Already done", "state": "closed"}}))
	if i := gh.Issue(t, 41); i.State != "closed" {
		t.Errorf("issue created in state closed is %s", i.State)
	}
}

func TestALabelTheRepositoryLacksIsRefusedUnlessTheSettingsAskToCreateIt(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.AddLabel("ticket")
	gh.Add(fakegithub.Issue{Title: "Add login page", Labels: []string{"ticket", "ready-for-agent"}})

	resp := request(t, gh, map[string]any{"op": "create", "item": map[string]any{"title": "Dark mode", "labels": []string{"kind: feature"}}})
	if resp.Error == nil || resp.Error.Kind != "invalid" || resp.Error.Message != `acme/shop has no label "kind: feature": create it in GitHub, or set create_labels: true in the Connector's settings` {
		t.Errorf("create with a missing label answered %+v, want an invalid error naming the label", resp.Error)
	}
	resp = request(t, gh, map[string]any{"op": "status", "id": "41", "from": map[string]any{"label": "ready-for-agent"}, "to": map[string]any{"label": "status: doing"}})
	if resp.Error == nil || resp.Error.Kind != "invalid" {
		t.Errorf("status to a missing label answered %+v, want an invalid error", resp.Error)
	}
	if got := fmt.Sprint(gh.Labels()); got != "[ticket ready-for-agent]" {
		t.Errorf("the repository's labels are %s after refusing, want them unchanged", got)
	}

	create := map[string]any{"create_labels": true}
	ok(t, request(t, gh, map[string]any{"op": "create", "item": map[string]any{"title": "Dark mode", "labels": []string{"kind: feature"}}}, create))
	ok(t, request(t, gh, map[string]any{"op": "status", "id": "41", "from": map[string]any{"label": "ready-for-agent"}, "to": map[string]any{"label": "status: doing"}}, create))
	if got := fmt.Sprint(gh.Labels()); got != "[ticket ready-for-agent kind: feature status: doing]" {
		t.Errorf("the repository's labels are %s, want kind: feature and status: doing created", got)
	}
}

func TestStatusSwapsTheIssuesLabelsAndClosesAndReopensIt(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.AddLabel("status: doing")
	gh.Add(fakegithub.Issue{Title: "Add login page", Labels: []string{"ticket", "bug", "ready-for-agent"}})

	ok(t, request(t, gh, map[string]any{"op": "status", "id": "41", "from": map[string]any{"label": "ready-for-agent"}, "to": map[string]any{"label": "status: doing"}}))
	if i := gh.Issue(t, 41); fmt.Sprint(i.Labels) != "[ticket bug status: doing]" || i.State != "open" {
		t.Errorf("after the first move the issue is %s with %q, want it open with status: doing in place of ready-for-agent", i.State, i.Labels)
	}
	ok(t, request(t, gh, map[string]any{"op": "status", "id": "41", "from": map[string]any{"label": "status: doing"}, "to": map[string]any{"state": "closed"}}))
	if i := gh.Issue(t, 41); fmt.Sprint(i.Labels) != "[ticket bug]" || i.State != "closed" {
		t.Errorf("after moving into a closed state the issue is %s with %q, want it closed without status: doing", i.State, i.Labels)
	}
	ok(t, request(t, gh, map[string]any{"op": "status", "id": "41", "from": map[string]any{"state": "closed"}, "to": map[string]any{"label": "status: doing"}}))
	if i := gh.Issue(t, 41); fmt.Sprint(i.Labels) != "[ticket bug status: doing]" || i.State != "open" {
		t.Errorf("after moving out of the closed state the issue is %s with %q, want it reopened with status: doing", i.State, i.Labels)
	}
	if resp := request(t, gh, map[string]any{"op": "status", "id": "99", "to": map[string]any{"label": "status: doing"}}); resp.Error == nil || resp.Error.Kind != "not_found" {
		t.Errorf("status of a missing issue answered %+v, want not_found", resp.Error)
	}
}

func TestCommentAddsTheBodyAsItIsAsAnIssueComment(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.Add(fakegithub.Issue{Title: "Add login page", Labels: []string{"ticket"}})

	ok(t, request(t, gh, map[string]any{"op": "comment", "id": "41", "body": "Needs a design first.\n\n_Written by an AI agent through JigFlow._"}))
	if got := gh.Issue(t, 41).Comments; len(got) != 1 || got[0] != "Needs a design first.\n\n_Written by an AI agent through JigFlow._" {
		t.Errorf("comments = %q, want the body as it was sent", got)
	}
}

func TestAClaimAssignsTheTokensUserAndIsReadBackAsTheSession(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.Add(fakegithub.Issue{Title: "Add login page", Body: "As a user…", Labels: []string{"ticket"}})

	ok(t, request(t, gh, map[string]any{"op": "claim", "id": "41", "claim": "agent-1727000000"}))
	if got := gh.Issue(t, 41).Assignees; fmt.Sprint(got) != "[jfl-bot]" {
		t.Errorf("assignees after the Claim = %q, want the token's user jfl-bot", got)
	}
	got := ok(t, request(t, gh, map[string]any{"op": "get", "id": "41"})).Item
	if got.Claim != "agent-1727000000" || got.Body != "As a user…" {
		t.Errorf("get after the Claim = %+v, want claim agent-1727000000 and the body as it was", got)
	}
	if items := ok(t, request(t, gh, map[string]any{"op": "list"})).Items; len(items) != 1 || items[0].Claim != "agent-1727000000" {
		t.Errorf("list after the Claim = %+v, want claim agent-1727000000", items)
	}

	ok(t, request(t, gh, map[string]any{"op": "claim", "id": "41", "claim": ""}))
	i := gh.Issue(t, 41)
	if len(i.Assignees) != 0 || i.Body != "As a user…" {
		t.Errorf("after clearing the Claim the issue is %+v, want no assignee and the body as it was", i)
	}
	if got := ok(t, request(t, gh, map[string]any{"op": "get", "id": "41"})).Item; got.Claim != "" {
		t.Errorf("claim after clearing it = %q, want none", got.Claim)
	}
}

func TestAClaimAssignsTheAssigneeTheSettingsName(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.Add(fakegithub.Issue{Title: "Add login page", Labels: []string{"ticket"}})

	ok(t, request(t, gh, map[string]any{"op": "claim", "id": "41", "claim": "agent-1"}, map[string]any{"assignee": "roberto"}))
	if got := gh.Issue(t, 41).Assignees; fmt.Sprint(got) != "[roberto]" {
		t.Errorf("assignees = %q, want roberto", got)
	}
}

func TestAnIssueSomeoneAssignedInGitHubIsClaimedByThem(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.Add(fakegithub.Issue{Title: "Add login page", Labels: []string{"ticket"}, Assignees: []string{"alice"}})
	gh.Add(fakegithub.Issue{Title: "Reset endpoint", Labels: []string{"ticket"}})
	ok(t, request(t, gh, map[string]any{"op": "claim", "id": "42", "claim": "agent-1"}))
	// A teammate takes the issue over in GitHub.
	gh.Edit(42, func(i *fakegithub.Issue) { i.Assignees = []string{"bob"} })

	for id, want := range map[string]string{"41": "alice", "42": "bob"} {
		if got := ok(t, request(t, gh, map[string]any{"op": "get", "id": id})).Item.Claim; got != want {
			t.Errorf("claim of %s = %q, want its assignee %s", id, got, want)
		}
	}
}

func TestBlockedByLinksAreGitHubIssueDependencies(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.AddLabel("ticket")
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket"}})
	gh.Add(fakegithub.Issue{Title: "Mail templates", Labels: []string{"ticket"}})

	resp := ok(t, request(t, gh, map[string]any{"op": "create", "item": map[string]any{
		"title": "Reset endpoint",
		"links": map[string]any{"blocked_by": []map[string]string{{"id": "41"}, {"id": "42"}}, "part_of": []map[string]string{{"artifact": "S-3"}}},
	}}))
	if got := gh.Issue(t, 43).BlockedBy; fmt.Sprint(got) != "[41 42]" {
		t.Errorf("GitHub's blocked-by dependencies of the new issue = %v, want 41 and 42", got)
	}
	// A teammate adds a dependency in GitHub.
	gh.Add(fakegithub.Issue{Number: 50, Title: "Rate limiter", Labels: []string{"ticket"}})
	gh.Edit(43, func(i *fakegithub.Issue) { i.BlockedBy = append(i.BlockedBy, 50) })

	want := "map[blocked_by:[map[id:41] map[id:42] map[id:50]] part_of:[map[artifact:S-3]]]"
	if got := fmt.Sprint(resp.Item.Links); got != "map[blocked_by:[map[id:41] map[id:42]] part_of:[map[artifact:S-3]]]" {
		t.Errorf("create answered the links %s", got)
	}
	if got := ok(t, request(t, gh, map[string]any{"op": "get", "id": "43"})).Item; fmt.Sprint(got.Links) != want || got.Body != "" {
		t.Errorf("get answered the links %v and body %q, want %s and no body", got.Links, got.Body, want)
	}
	items := ok(t, request(t, gh, map[string]any{"op": "list"})).Items
	if len(items) != 4 || fmt.Sprint(items[2].Links) != want || items[0].Links != nil {
		t.Errorf("list answered %+v, want 43 with the links %s", items, want)
	}
}

func TestBlockedByLinksAreKeptInTheBodyWhereGitHubHasNoIssueDependencies(t *testing.T) {
	gh := fakegithub.New(t, 41)
	gh.WithoutDependencies()
	gh.AddLabel("ticket")
	gh.Add(fakegithub.Issue{Title: "Reset-token table", Labels: []string{"ticket"}})

	ok(t, request(t, gh, map[string]any{"op": "create", "item": map[string]any{
		"title": "Reset endpoint", "body": "Marked.", "links": map[string]any{"blocked_by": []map[string]string{{"id": "41"}}},
	}}))
	got := ok(t, request(t, gh, map[string]any{"op": "get", "id": "42"})).Item
	if fmt.Sprint(got.Links) != "map[blocked_by:[map[id:41]]]" || got.Body != "Marked." {
		t.Errorf("get answered the links %v and body %q, want blocked_by 41 and the body as created", got.Links, got.Body)
	}
}

func TestListFollowsGitHubsPages(t *testing.T) {
	gh := fakegithub.New(t, 1)
	gh.PageSize(2)
	for n := range 5 {
		gh.Add(fakegithub.Issue{Title: fmt.Sprint("Issue ", n+1), Labels: []string{"ticket"}})
	}
	if items := ok(t, request(t, gh, map[string]any{"op": "list"})).Items; len(items) != 5 || items[4].ID != "5" {
		t.Errorf("list answered %d items, want all 5 across 3 pages: %+v", len(items), items)
	}
}

func TestGitHubFailuresAreReportedByKind(t *testing.T) {
	get := map[string]any{"op": "get", "id": "41"}
	for _, tc := range []struct {
		name  string
		setup func(*fakegithub.Server)
		env   []string
		req   map[string]any
		kind  string
		retry int
	}{
		{name: "no token", env: []string{}, req: get, kind: "auth"},
		{name: "a token GitHub refuses", env: []string{"GH_TOKEN=wrong"}, req: get, kind: "auth"},
		{name: "rate limit", setup: (*fakegithub.Server).RateLimit, req: get, kind: "rate_limit", retry: 42},
		{name: "bad gateway", setup: func(s *fakegithub.Server) { s.Fail(502) }, req: get, kind: "network"},
		{name: "server error", setup: func(s *fakegithub.Server) { s.Fail(500) }, req: get, kind: "internal"},
		{name: "unreachable", setup: (*fakegithub.Server).Close, req: get, kind: "network"},
		{name: "a repository GitHub doesn't show", req: map[string]any{"op": "list", "settings": map[string]any{"repo": "acme/other"}}, kind: "invalid"},
		{name: "no repository", req: map[string]any{"op": "list", "settings": map[string]any{}}, kind: "invalid"},
		{name: "another protocol", req: map[string]any{"op": "list", "protocol": 2}, kind: "invalid"},
		{name: "an unknown op", req: map[string]any{"op": "delete"}, kind: "invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh := fakegithub.New(t, 41)
			gh.Add(fakegithub.Issue{Title: "Add login page", Labels: []string{"ticket"}})
			if tc.setup != nil {
				tc.setup(gh)
			}
			req := map[string]any{"protocol": 1, "type": "Ticket", "settings": map[string]any{"repo": "acme/shop"}}
			for k, v := range tc.req {
				req[k] = v
			}
			env := []string{"GH_TOKEN=" + fakegithub.Token}
			if tc.env != nil {
				env = tc.env
			}
			resp := run(t, req, append(env, "GITHUB_API_URL="+gh.URL)...)
			if resp.Error == nil || resp.Error.Kind != tc.kind || resp.Error.RetryAfter != tc.retry || resp.Error.Message == "" {
				t.Errorf("answered %+v, want a %s error with a message and retry_after %d", resp.Error, tc.kind, tc.retry)
			}
		})
	}
}
