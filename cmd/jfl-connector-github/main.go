// Command jfl-connector-github is the GitHub Issues Connector: it keeps the
// Artifacts of an Artifact Type as issues of a GitHub repository, speaking
// the Connector protocol (docs/connector-protocol.md) over stdio. It is the
// reference Connector, shipped alongside jfl; docs/github-connector.md
// describes how to set it up.
//
// jfl maps Statuses and fields to labels and states (ADR 0011), so this
// Connector only reads and writes issues: their labels, open or closed
// state, assignees, comments and blocked-by dependencies.
package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// protocol is the version of the Connector protocol this Connector speaks.
const protocol = 1

type (
	request struct {
		Protocol int             `json:"protocol"`
		Op       string          `json:"op"`
		Type     string          `json:"type"`
		Settings json.RawMessage `json:"settings"`
		ID       string          `json:"id"`
		Item     item            `json:"item"`
		From     term            `json:"from"`
		To       term            `json:"to"`
		Claim    string          `json:"claim"`
		Body     string          `json:"body"`
	}
	item struct {
		ID     string            `json:"id,omitempty"`
		Title  string            `json:"title"`
		Body   string            `json:"body,omitempty"`
		Labels []string          `json:"labels,omitempty"`
		State  string            `json:"state,omitempty"`
		Claim  string            `json:"claim,omitempty"`
		Links  map[string][]link `json:"links,omitempty"`
		// Comments are the issue's comments, oldest first, reported by get
		// only.
		Comments []string `json:"comments,omitempty"`
	}
	link struct {
		ID       string `json:"id,omitempty"`
		Artifact string `json:"artifact,omitempty"`
	}
	term struct {
		Label string `json:"label"`
		State string `json:"state"`
	}
	// settings are the Connector's, from the project's Playbook file.
	settings struct {
		Repo         string `json:"repo"`          // owner/name, required
		Label        string `json:"label"`         // the label that tells this Type's issues apart, if any
		CreateLabels bool   `json:"create_labels"` // create a label the repository lacks, rather than refuse
		Assignee     string `json:"assignee"`      // who a Claim assigns; the token's user by default
	}
	// described is one of the settings, as describe answers it.
	described struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"` // text, yesno or number
		Required bool   `json:"required,omitempty"`
		PerType  bool   `json:"per_type,omitempty"` // given per Artifact Type
		Help     string `json:"help"`
	}
	// failure is a protocol error response.
	failure struct {
		Kind       string `json:"kind"`
		Message    string `json:"message"`
		RetryAfter int    `json:"retry_after,omitempty"`
	}
)

func (f *failure) Error() string { return f.Message }

// describe is the settings, as describe answers them: it needs no token,
// so jfl can ask before the Connector is set up (ADR 0031).
var describe = []described{
	{Name: "repo", Kind: "text", Required: true, Help: "the repository that keeps the issues, as owner/name"},
	{Name: "label", Kind: "text", PerType: true, Help: "the label that tells this Artifact Type's issues apart, if any"},
	{Name: "create_labels", Kind: "yesno", Help: "create a label the repository lacks, rather than refuse"},
	{Name: "assignee", Kind: "text", Help: "the login a Claim assigns the issue to; the token's user by default"},
}

func main() {
	resp, err := serve(os.Stdin)
	if err != nil {
		var f *failure
		if !errors.As(err, &f) {
			f = classify(err)
		}
		resp = map[string]any{"error": f}
	}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		fmt.Fprintln(os.Stderr, "jfl-connector-github:", err)
		os.Exit(1)
	}
}

// serve reads one request and carries it out, returning the response.
func serve(in io.Reader) (any, error) {
	raw, err := io.ReadAll(in)
	if err != nil {
		return nil, &failure{Kind: "internal", Message: err.Error()}
	}
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, &failure{Kind: "invalid", Message: "the request is not JSON: " + err.Error()}
	}
	if req.Protocol != protocol {
		return nil, &failure{Kind: "invalid", Message: fmt.Sprintf("this Connector speaks protocol %d, not %d", protocol, req.Protocol)}
	}
	if req.Op == "describe" {
		return map[string]any{"settings": describe}, nil
	}
	var s settings
	if len(req.Settings) > 0 {
		if err := json.Unmarshal(req.Settings, &s); err != nil {
			return nil, &failure{Kind: "invalid", Message: "the settings are not the GitHub Connector's: " + err.Error()}
		}
	}
	if owner, name, ok := strings.Cut(s.Repo, "/"); !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return nil, &failure{Kind: "invalid", Message: fmt.Sprintf("the setting repo must be owner/name, not %q", s.Repo)}
	}
	token := cmp.Or(os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return nil, &failure{Kind: "auth", Message: "no GitHub token: set GH_TOKEN or GITHUB_TOKEN in the environment jfl runs in"}
	}
	gh := &github{
		client: &client{
			api:   strings.TrimRight(cmp.Or(os.Getenv("GITHUB_API_URL"), "https://api.github.com"), "/"),
			token: token,
			repo:  s.Repo,
			http:  &http.Client{Timeout: 30 * time.Second},
		},
		s: s,
	}
	switch req.Op {
	case "list":
		items, err := gh.list()
		return map[string]any{"items": items}, err
	case "get":
		i, err := gh.issue(req.ID)
		if err != nil {
			return nil, err
		}
		it, err := gh.item(i)
		if err != nil {
			return nil, err
		}
		it.Comments, err = gh.comments(i)
		return map[string]any{"item": it}, err
	case "create":
		it, err := gh.create(req.Item)
		return map[string]any{"item": it}, err
	case "status":
		return map[string]any{}, gh.status(req.ID, req.From, req.To)
	case "claim":
		return map[string]any{}, gh.claim(req.ID, req.Claim)
	case "comment":
		return map[string]any{}, gh.comment(req.ID, req.Body)
	}
	return nil, &failure{Kind: "invalid", Message: fmt.Sprintf("unknown op %q", req.Op)}
}

// classify is the protocol error of a failed GitHub request.
func classify(err error) *failure {
	var e *apiError
	if !errors.As(err, &e) {
		return &failure{Kind: "internal", Message: err.Error()}
	}
	switch {
	case e.rateLimit:
		return &failure{Kind: "rate_limit", Message: e.message, RetryAfter: e.retryAfter}
	case e.status == 0:
		return &failure{Kind: "network", Message: e.message}
	case e.status == http.StatusUnauthorized || e.status == http.StatusForbidden:
		return &failure{Kind: "auth", Message: e.message}
	case e.status == http.StatusNotFound:
		// A missing issue is not_found, answered where it is looked up;
		// anything else missing is the repository, or the token's access.
		return &failure{Kind: "invalid", Message: e.message + ": check that the repository exists and the token can see it"}
	case e.status == http.StatusBadGateway || e.status == http.StatusServiceUnavailable || e.status == http.StatusGatewayTimeout:
		return &failure{Kind: "network", Message: e.message}
	case e.status < 500:
		return &failure{Kind: "invalid", Message: e.message}
	}
	return &failure{Kind: "internal", Message: "GitHub answered " + strconv.Itoa(e.status) + ": " + e.message}
}

// github carries out requests against one repository.
type github struct {
	*client
	s settings
}

// list returns every issue of the Artifact Type.
func (g *github) list() ([]item, error) {
	issues, err := g.issues(g.s.Label)
	if err != nil {
		return nil, err
	}
	items := []item{}
	for _, i := range issues {
		it, err := g.item(i)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

// issue returns the issue of the Artifact Type with the tracker id: not
// found when it is a pull request or lacks the Type's label.
func (g *github) issue(id string) (ghIssue, error) {
	n, err := strconv.Atoi(id)
	if err != nil || n <= 0 {
		return ghIssue{}, &failure{Kind: "not_found", Message: fmt.Sprintf("%q is no issue number", id)}
	}
	var i ghIssue
	if _, err := g.do("GET", g.repoPath("issues", id), nil, &i); err != nil {
		if notFound(err) {
			return ghIssue{}, &failure{Kind: "not_found", Message: fmt.Sprintf("%s has no issue %d", g.repo, n)}
		}
		return ghIssue{}, err
	}
	if i.PullRequest != nil {
		return ghIssue{}, &failure{Kind: "not_found", Message: fmt.Sprintf("%s#%d is a pull request, not an issue", g.repo, n)}
	}
	if g.s.Label != "" && !i.hasLabel(g.s.Label) {
		return ghIssue{}, &failure{Kind: "not_found", Message: fmt.Sprintf("%s#%d has no label %q", g.repo, n, g.s.Label)}
	}
	return i, nil
}

// create opens an issue for the item, with the Artifact Type's label, and
// closes it when the item is closed. Its blocked_by Links to other issues
// become GitHub issue dependencies where the repository has them; its
// other Links are kept in its metadata.
func (g *github) create(it item) (item, error) {
	if err := checkState(it.State); err != nil {
		return item{}, err
	}
	labels := it.Labels
	if g.s.Label != "" && !slices.Contains(labels, g.s.Label) {
		labels = append(labels, g.s.Label)
	}
	if err := g.ensureLabels(labels...); err != nil {
		return item{}, err
	}
	var blockers []link
	m := meta{}
	for name, targets := range it.Links {
		for _, l := range targets {
			if name == dependencyLink && l.ID != "" {
				blockers = append(blockers, l)
				continue
			}
			m.addLink(name, l)
		}
	}
	var i ghIssue
	if _, err := g.do("POST", g.repoPath("issues"), map[string]any{"title": it.Title, "body": joinBody(it.Body, m), "labels": labels}, &i); err != nil {
		return item{}, err
	}
	id := strconv.Itoa(i.Number)
	if it.State != "" && it.State != i.State {
		if _, err := g.do("PATCH", g.repoPath("issues", id), map[string]any{"state": it.State}, nil); err != nil {
			return item{}, err
		}
	}
	for n, b := range blockers {
		added, err := g.addDependency(id, b.ID)
		if err != nil {
			return item{}, err
		}
		if !added {
			// No issue dependencies here: keep the rest in the metadata.
			for _, rest := range blockers[n:] {
				m.addLink(dependencyLink, rest)
			}
			if err := g.setBody(id, joinBody(it.Body, m)); err != nil {
				return item{}, err
			}
			break
		}
	}
	created, err := g.issue(id)
	if err != nil {
		return item{}, err
	}
	return g.item(created)
}

// dependencyLink is the Link kept as GitHub's blocked-by issue
// dependencies.
const dependencyLink = "blocked_by"

// addDependency records that the issue is blocked by the issue blocker. It
// reports false, adding nothing, when the repository has no issue
// dependencies, as on older GitHub Enterprise Servers.
func (g *github) addDependency(id, blocker string) (bool, error) {
	b, err := g.issue(blocker)
	var f *failure
	if errors.As(err, &f) && f.Kind == "not_found" {
		return false, &failure{Kind: "invalid", Message: fmt.Sprintf("the issue can't be blocked by %s: %s", blocker, f.Message)}
	}
	if err != nil {
		return false, err
	}
	_, err = g.do("POST", g.repoPath("issues", id, "dependencies", "blocked_by"), map[string]any{"issue_id": b.ID}, nil)
	if notFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// blockedBy returns the numbers of the issues GitHub has blocking the
// issue, or none where the repository has no issue dependencies.
func (g *github) blockedBy(i ghIssue) ([]string, error) {
	if i.Dependencies != nil && i.Dependencies.TotalBlockedBy == 0 {
		return nil, nil
	}
	var out []string
	for page := g.repoPath("issues", strconv.Itoa(i.Number), "dependencies", "blocked_by") + "?per_page=100"; page != ""; {
		var batch []ghIssue
		next, err := g.do("GET", page, nil, &batch)
		if notFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, b := range batch {
			out = append(out, strconv.Itoa(b.Number))
		}
		page = next
	}
	return out, nil
}

// comments returns the text of the issue's comments, oldest first.
func (g *github) comments(i ghIssue) ([]string, error) {
	var out []string
	for page := g.repoPath("issues", strconv.Itoa(i.Number), "comments") + "?per_page=100"; page != ""; {
		var batch []struct {
			Body string `json:"body"`
		}
		next, err := g.do("GET", page, nil, &batch)
		if err != nil {
			return nil, err
		}
		for _, c := range batch {
			out = append(out, c.Body)
		}
		page = next
	}
	return out, nil
}

// status moves the issue from one label and state to another: it adds the
// to label, removes the from label, and sets the to state. With no to
// state, an issue leaving a closed state is reopened, since an open issue
// is GitHub's default.
func (g *github) status(id string, from, to term) error {
	if err := checkState(to.State); err != nil {
		return err
	}
	i, err := g.issue(id)
	if err != nil {
		return err
	}
	if to.Label != "" && !i.hasLabel(to.Label) {
		if err := g.ensureLabels(to.Label); err != nil {
			return err
		}
		if _, err := g.do("POST", g.repoPath("issues", id, "labels"), map[string]any{"labels": []string{to.Label}}, nil); err != nil {
			return err
		}
	}
	if from.Label != "" && from.Label != to.Label && i.hasLabel(from.Label) {
		if _, err := g.do("DELETE", g.repoPath("issues", id, "labels", from.Label), nil, nil); err != nil && !notFound(err) {
			return err
		}
	}
	state := to.State
	if state == "" && from.State != "" && from.State != "open" {
		state = "open"
	}
	if state != "" && state != i.State {
		if _, err := g.do("PATCH", g.repoPath("issues", id), map[string]any{"state": state}, nil); err != nil {
			return err
		}
	}
	return nil
}

// claim records the agent session's Claim on the issue: it assigns the
// issue to the settings' assignee, or the token's user, and keeps the
// session in the issue's hidden metadata, since sessions aren't GitHub
// users. An empty session clears the Claim jfl recorded, leaving any
// assignee a person set in GitHub.
func (g *github) claim(id, session string) error {
	i, err := g.issue(id)
	if err != nil {
		return err
	}
	text, m := splitBody(i.body())
	old := m.Claim
	if session == "" {
		if old == nil {
			return nil
		}
		if err := g.unassign(id, old.Assignee); err != nil {
			return err
		}
		m.Claim = nil
		return g.setBody(id, joinBody(text, m))
	}
	login := g.s.Assignee
	if login == "" {
		var user struct {
			Login string `json:"login"`
		}
		if _, err := g.do("GET", "/user", nil, &user); err != nil {
			return err
		}
		login = user.Login
	}
	if old != nil && old.Assignee != login {
		if err := g.unassign(id, old.Assignee); err != nil {
			return err
		}
	}
	m.Claim = &claimMeta{Session: session, Assignee: login}
	if err := g.setBody(id, joinBody(text, m)); err != nil {
		return err
	}
	var after ghIssue
	if _, err := g.do("POST", g.repoPath("issues", id, "assignees"), map[string]any{"assignees": []string{login}}, &after); err != nil {
		return err
	}
	if !slices.Contains(after.logins(), login) {
		return &failure{Kind: "invalid", Message: fmt.Sprintf("%s can't be assigned issues in %s; set assignee in the Connector's settings to a collaborator", login, g.repo)}
	}
	return nil
}

func (g *github) unassign(id, login string) error {
	_, err := g.do("DELETE", g.repoPath("issues", id, "assignees"), map[string]any{"assignees": []string{login}}, nil)
	return err
}

func (g *github) setBody(id, body string) error {
	_, err := g.do("PATCH", g.repoPath("issues", id), map[string]any{"body": body}, nil)
	return err
}

// comment adds a comment to the issue.
func (g *github) comment(id, body string) error {
	if _, err := g.issue(id); err != nil {
		return err
	}
	_, err := g.do("POST", g.repoPath("issues", id, "comments"), map[string]any{"body": body}, nil)
	return err
}

// addLink adds the Link to the item, unless it has it.
func (it *item) addLink(name string, l link) {
	if slices.Contains(it.Links[name], l) {
		return
	}
	if it.Links == nil {
		it.Links = map[string][]link{}
	}
	it.Links[name] = append(it.Links[name], l)
}

// checkState refuses a state GitHub issues can't be in.
func checkState(state string) error {
	if state != "" && state != "open" && state != "closed" {
		return &failure{Kind: "invalid", Message: fmt.Sprintf("a GitHub issue is open or closed, not %q", state)}
	}
	return nil
}

// ensureLabels makes sure the repository has the labels: it creates one
// it lacks when the settings ask for that, and refuses otherwise, so a
// mistyped mapping doesn't fill the repository with labels. (GitHub itself
// would create a missing label silently on adding it to an issue.)
func (g *github) ensureLabels(labels ...string) error {
	for _, l := range labels {
		_, err := g.do("GET", g.repoPath("labels", l), nil, nil)
		if !notFound(err) {
			if err != nil {
				return err
			}
			continue
		}
		if !g.s.CreateLabels {
			return &failure{Kind: "invalid", Message: fmt.Sprintf("%s has no label %q: create it in GitHub, or set create_labels: true in the Connector's settings", g.repo, l)}
		}
		if _, err := g.do("POST", g.repoPath("labels"), map[string]any{"name": l, "color": "ededed"}, nil); err != nil {
			return err
		}
	}
	return nil
}

// item is the protocol's item for the issue.
func (g *github) item(i ghIssue) (item, error) {
	text, m := splitBody(i.body())
	it := item{ID: strconv.Itoa(i.Number), Title: i.Title, Body: text, State: i.State}
	logins := i.logins()
	if m.Claim != nil && slices.Contains(logins, m.Claim.Assignee) {
		it.Claim = m.Claim.Session
	} else if len(logins) > 0 {
		// Someone took the issue in GitHub: it is theirs.
		it.Claim = logins[0]
	}
	blockers, err := g.blockedBy(i)
	if err != nil {
		return item{}, err
	}
	for _, b := range blockers {
		it.addLink(dependencyLink, link{ID: b})
	}
	for _, name := range slices.Sorted(maps.Keys(m.Links)) {
		for _, l := range m.Links[name] {
			it.addLink(name, l)
		}
	}
	for _, l := range i.Labels {
		it.Labels = append(it.Labels, l.Name)
	}
	return it, nil
}
