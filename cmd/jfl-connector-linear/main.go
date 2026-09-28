// Command jfl-connector-linear is the Linear Connector: it keeps the
// Artifacts of an Artifact Type as issues of a Linear team, speaking the
// Connector protocol (docs/connector-protocol.md) over stdio. It is a
// reference Connector, shipped alongside jfl; docs/linear-connector.md
// describes how to set it up.
//
// jfl maps Statuses and fields to labels and states (ADR 0011), so this
// Connector only reads and writes issues: their labels, workflow state,
// assignee, comments and blocking relations.
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
		Team         string `json:"team"`          // the team's key, e.g. ENG; required
		Label        string `json:"label"`         // the label that tells this Type's issues apart, if any
		CreateLabels bool   `json:"create_labels"` // create a label the team lacks, rather than refuse
		Assignee     string `json:"assignee"`      // the display name of whom a Claim assigns; the API key's user by default
	}
	// failure is a protocol error response.
	failure struct {
		Kind       string `json:"kind"`
		Message    string `json:"message"`
		RetryAfter int    `json:"retry_after,omitempty"`
	}
)

func (f *failure) Error() string { return f.Message }

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
		fmt.Fprintln(os.Stderr, "jfl-connector-linear:", err)
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
	var s settings
	if len(req.Settings) > 0 {
		if err := json.Unmarshal(req.Settings, &s); err != nil {
			return nil, &failure{Kind: "invalid", Message: "the settings are not the Linear Connector's: " + err.Error()}
		}
	}
	if s.Team == "" {
		return nil, &failure{Kind: "invalid", Message: "the setting team, the key of a Linear team, is required"}
	}
	key := os.Getenv("LINEAR_API_KEY")
	if key == "" {
		return nil, &failure{Kind: "auth", Message: "no Linear API key: set LINEAR_API_KEY in the environment jfl runs in"}
	}
	ln := &linear{client: &client{api: cmp.Or(os.Getenv("LINEAR_API_URL"), "https://api.linear.app/graphql"), key: key, http: &http.Client{Timeout: 30 * time.Second}}, s: s}
	switch req.Op {
	case "list":
		items, err := ln.list()
		return map[string]any{"items": items}, err
	case "get":
		i, err := ln.issue(req.ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"item": ln.item(i)}, nil
	case "create":
		it, err := ln.create(req.Item)
		return map[string]any{"item": it}, err
	case "status":
		return map[string]any{}, ln.status(req.ID, req.From, req.To)
	case "claim":
		return map[string]any{}, ln.claim(req.ID, req.Claim)
	case "comment":
		return map[string]any{}, ln.comment(req.ID, req.Body)
	}
	return nil, &failure{Kind: "invalid", Message: fmt.Sprintf("unknown op %q", req.Op)}
}

// classify is the protocol error of a failed Linear request.
func classify(err error) *failure {
	var e *apiError
	if !errors.As(err, &e) {
		return &failure{Kind: "internal", Message: err.Error()}
	}
	switch {
	case e.code == "RATELIMITED" || e.status == http.StatusTooManyRequests:
		return &failure{Kind: "rate_limit", Message: e.message, RetryAfter: e.retryAfter}
	case e.status == 0:
		return &failure{Kind: "network", Message: e.message}
	case e.code == "AUTHENTICATION_ERROR" || e.code == "FORBIDDEN" || e.status == http.StatusUnauthorized || e.status == http.StatusForbidden:
		return &failure{Kind: "auth", Message: e.message}
	case e.status == http.StatusBadGateway || e.status == http.StatusServiceUnavailable || e.status == http.StatusGatewayTimeout:
		return &failure{Kind: "network", Message: e.message}
	case e.code == "INTERNAL_SERVER_ERROR" || e.status >= 500:
		return &failure{Kind: "internal", Message: "Linear answered " + strconv.Itoa(e.status) + ": " + e.message}
	}
	return &failure{Kind: "invalid", Message: e.message}
}

// linear carries out requests against one team.
type linear struct {
	*client
	s    settings
	team *lnTeam // once looked up
}

// filter is the IssueFilter of the Artifact Type's issues, with more
// conditions. Looking the team up first makes a team Linear doesn't show
// an error, rather than a team without issues.
func (l *linear) filter(more map[string]any) (map[string]any, error) {
	t, err := l.getTeam()
	if err != nil {
		return nil, err
	}
	f := map[string]any{"team": map[string]any{"id": map[string]any{"eq": t.ID}}}
	if l.s.Label != "" {
		f["labels"] = map[string]any{"some": map[string]any{"name": map[string]any{"eq": l.s.Label}}}
	}
	for k, v := range more {
		f[k] = v
	}
	return f, nil
}

// list returns every issue of the Artifact Type.
func (l *linear) list() ([]item, error) {
	f, err := l.filter(nil)
	if err != nil {
		return nil, err
	}
	issues, err := l.issues(f)
	if err != nil {
		return nil, err
	}
	items := []item{}
	for _, i := range issues {
		items = append(items, l.item(i))
	}
	return items, nil
}

// issue returns the issue of the Artifact Type with the tracker id, its
// number in the team: not found when it lacks the Type's label.
func (l *linear) issue(id string) (lnIssue, error) {
	n, err := strconv.Atoi(id)
	if err != nil || n <= 0 {
		return lnIssue{}, &failure{Kind: "not_found", Message: fmt.Sprintf("%q is no issue number", id)}
	}
	f, err := l.filter(map[string]any{"number": map[string]any{"eq": n}})
	if err != nil {
		return lnIssue{}, err
	}
	issues, err := l.issues(f)
	if err != nil {
		return lnIssue{}, err
	}
	if len(issues) == 0 {
		what := "no issue"
		if l.s.Label != "" {
			what = fmt.Sprintf("no issue with the label %q", l.s.Label)
		}
		return lnIssue{}, &failure{Kind: "not_found", Message: fmt.Sprintf("team %s has %s numbered %d", l.s.Team, what, n)}
	}
	return issues[0], nil
}

// getTeam returns the team the settings name, looking it up once.
func (l *linear) getTeam() (*lnTeam, error) {
	if l.team != nil {
		return l.team, nil
	}
	var r struct {
		Teams struct {
			Nodes []lnTeam `json:"nodes"`
		} `json:"teams"`
	}
	q := `query Team($key: String!) {
  teams(filter: {key: {eq: $key}}) {
    nodes { id key states(first: 100) { nodes { id name } } defaultIssueState { id name } }
  }
}`
	if err := l.do(q, map[string]any{"key": l.s.Team}, &r); err != nil {
		return nil, err
	}
	if len(r.Teams.Nodes) == 0 {
		return nil, &failure{Kind: "invalid", Message: fmt.Sprintf("Linear has no team with the key %q, or the API key can't see it", l.s.Team)}
	}
	l.team = &r.Teams.Nodes[0]
	return l.team, nil
}

// stateID is the id of the team's workflow state with the name.
func (l *linear) stateID(name string) (string, error) {
	t, err := l.getTeam()
	if err != nil {
		return "", err
	}
	for _, st := range t.States.Nodes {
		if st.Name == name {
			return st.ID, nil
		}
	}
	return "", &failure{Kind: "invalid", Message: fmt.Sprintf("team %s has no workflow state %q", l.s.Team, name)}
}

// labelIDs returns the ids of the labels the team's issues can have with
// the names: the team's own, or the workspace's. It creates a label the
// team lacks when the settings ask for that, and refuses otherwise, so a
// mistyped mapping doesn't fill the team with labels.
func (l *linear) labelIDs(names ...string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	t, err := l.getTeam()
	if err != nil {
		return nil, err
	}
	var r struct {
		IssueLabels struct {
			Nodes []lnLabel `json:"nodes"`
		} `json:"issueLabels"`
	}
	q := `query Labels($filter: IssueLabelFilter) {
  issueLabels(filter: $filter, first: 250) { nodes { id name team { id } } }
}`
	if err := l.do(q, map[string]any{"filter": map[string]any{"name": map[string]any{"in": names}}}, &r); err != nil {
		return nil, err
	}
	var ids []string
	for _, name := range names {
		id := ""
		for _, lb := range r.IssueLabels.Nodes {
			if lb.Name == name && (lb.Team == nil || lb.Team.ID == t.ID) {
				id = lb.ID
				break
			}
		}
		if id == "" {
			if !l.s.CreateLabels {
				return nil, &failure{Kind: "invalid", Message: fmt.Sprintf("team %s has no label %q: create it in Linear, or set create_labels: true in the Connector's settings", l.s.Team, name)}
			}
			var c struct {
				IssueLabel lnLabel `json:"issueLabel"`
			}
			q := `mutation CreateLabel($input: IssueLabelCreateInput!) {
  issueLabelCreate(input: $input) { success issueLabel { id name } }
}`
			if err := l.mutate(q, map[string]any{"input": map[string]any{"name": name, "teamId": t.ID}}, fmt.Sprintf("create the label %q", name), &c); err != nil {
				return nil, err
			}
			id = c.IssueLabel.ID
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// create adds an issue for the item to the team, with the Artifact Type's
// label, in the item's workflow state or else the team's default one. Its
// blocked_by Links to other issues become Linear's blocking relations; its
// other Links are kept in its metadata.
func (l *linear) create(it item) (item, error) {
	t, err := l.getTeam()
	if err != nil {
		return item{}, err
	}
	labels := it.Labels
	if l.s.Label != "" && !slices.Contains(labels, l.s.Label) {
		labels = append(labels, l.s.Label)
	}
	input := map[string]any{"teamId": t.ID, "title": it.Title, "description": it.Body}
	if it.State != "" {
		id, err := l.stateID(it.State)
		if err != nil {
			return item{}, err
		}
		input["stateId"] = id
	}
	var blockers []string // their Linear ids
	m := meta{}
	for _, name := range slices.Sorted(maps.Keys(it.Links)) {
		for _, lk := range it.Links[name] {
			if name != blockingLink || lk.ID == "" {
				m.addLink(name, lk)
				continue
			}
			b, err := l.issue(lk.ID)
			var f *failure
			if errors.As(err, &f) && f.Kind == "not_found" {
				return item{}, &failure{Kind: "invalid", Message: fmt.Sprintf("the issue can't be blocked by %s: %s", lk.ID, f.Message)}
			}
			if err != nil {
				return item{}, err
			}
			blockers = append(blockers, b.ID)
		}
	}
	ids, err := l.labelIDs(labels...)
	if err != nil {
		return item{}, err
	}
	if len(ids) > 0 {
		input["labelIds"] = ids
	}
	var r struct {
		Issue lnIssue `json:"issue"`
	}
	q := `mutation CreateIssue($input: IssueCreateInput!) {
  issueCreate(input: $input) { success issue { ` + issueFields + ` } }
}`
	if err := l.mutate(q, map[string]any{"input": input}, "create the issue", &r); err != nil {
		return item{}, err
	}
	created := r.Issue
	if len(blockers) == 0 && m.empty() {
		return l.item(created), nil
	}
	for _, b := range blockers {
		q := `mutation CreateRelation($input: IssueRelationCreateInput!) { issueRelationCreate(input: $input) { success } }`
		// Linear keeps "A blocks B" as a relation of A.
		if err := l.mutate(q, map[string]any{"input": map[string]any{"issueId": b, "relatedIssueId": created.ID, "type": "blocks"}}, "add the blocking relation", nil); err != nil {
			return item{}, err
		}
	}
	if err := l.setMeta(created.ID, "", m); err != nil {
		return item{}, err
	}
	again, err := l.issue(strconv.Itoa(created.Number))
	if err != nil {
		return item{}, err
	}
	return l.item(again), nil
}

// blockingLink is the Link kept as Linear's blocking relations.
const blockingLink = "blocked_by"

// status moves the issue from one label and state to another, in one
// update: it adds the to label, removes the from label, and sets the to
// state. With no to state, an issue leaving a state is moved to the team's
// default state for new issues, where an issue created without a state
// would be.
func (l *linear) status(id string, from, to term) error {
	i, err := l.issue(id)
	if err != nil {
		return err
	}
	input := map[string]any{}
	state := to.State
	if state == "" && from.State != "" {
		t, err := l.getTeam()
		if err != nil {
			return err
		}
		if t.DefaultIssueState != nil {
			state = t.DefaultIssueState.Name
		}
	}
	if state != "" && state != i.State.Name {
		sid, err := l.stateID(state)
		if err != nil {
			return err
		}
		input["stateId"] = sid
	}
	if to.Label != "" && !i.hasLabel(to.Label) {
		ids, err := l.labelIDs(to.Label)
		if err != nil {
			return err
		}
		input["addedLabelIds"] = ids
	}
	if from.Label != "" && from.Label != to.Label && i.hasLabel(from.Label) {
		input["removedLabelIds"] = []string{i.labelID(from.Label)}
	}
	if len(input) == 0 {
		return nil
	}
	return l.update(i.ID, input)
}

// update applies the IssueUpdateInput to the issue with the Linear id.
func (l *linear) update(id string, input map[string]any) error {
	q := `mutation UpdateIssue($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) { success }
}`
	return l.mutate(q, map[string]any{"id": id, "input": input}, "update the issue", nil)
}

// claim records the agent session's Claim on the issue: it assigns the
// issue to the settings' assignee, or the API key's user, and keeps the
// session in the issue's metadata, since sessions aren't Linear users. An
// empty session clears the Claim jfl recorded, leaving an assignee a
// person set in Linear.
func (l *linear) claim(id, session string) error {
	i, err := l.issue(id)
	if err != nil {
		return err
	}
	m, attachment := metaOf(i)
	if session == "" {
		if m.Claim == nil {
			return nil
		}
		if i.Assignee != nil && i.Assignee.ID == m.Claim.Assignee {
			if err := l.update(i.ID, map[string]any{"assigneeId": nil}); err != nil {
				return err
			}
		}
		m.Claim = nil
		return l.setMeta(i.ID, attachment, m)
	}
	user, err := l.assignee()
	if err != nil {
		return err
	}
	m.Claim = &claimMeta{Session: session, Assignee: user.ID}
	if err := l.setMeta(i.ID, attachment, m); err != nil {
		return err
	}
	if i.Assignee != nil && i.Assignee.ID == user.ID {
		return nil
	}
	return l.update(i.ID, map[string]any{"assigneeId": user.ID})
}

// assignee is the user a Claim assigns: the one the settings name, or the
// API key's.
func (l *linear) assignee() (lnUser, error) {
	if l.s.Assignee == "" {
		var r struct {
			Viewer lnUser `json:"viewer"`
		}
		err := l.do(`query Viewer { viewer { id displayName } }`, nil, &r)
		return r.Viewer, err
	}
	var r struct {
		Users struct {
			Nodes []lnUser `json:"nodes"`
		} `json:"users"`
	}
	q := `query User($name: String!) {
  users(filter: {displayName: {eq: $name}}) { nodes { id displayName } }
}`
	if err := l.do(q, map[string]any{"name": l.s.Assignee}, &r); err != nil {
		return lnUser{}, err
	}
	if len(r.Users.Nodes) == 0 {
		return lnUser{}, &failure{Kind: "invalid", Message: fmt.Sprintf("the workspace has no user with the display name %q: set assignee in the Connector's settings to one", l.s.Assignee)}
	}
	return r.Users.Nodes[0], nil
}

// setMeta keeps the metadata in the issue's attachment, whose id is
// attachment when it has one, removing the attachment when there is
// nothing to keep.
func (l *linear) setMeta(issue, attachment string, m meta) error {
	if m.empty() {
		if attachment == "" {
			return nil
		}
		q := `mutation DeleteAttachment($id: String!) { attachmentDelete(id: $id) { success } }`
		return l.mutate(q, map[string]any{"id": attachment}, "remove the issue's JigFlow attachment", nil)
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	// Linear updates the issue's attachment with the same URL.
	q := `mutation SaveAttachment($input: AttachmentCreateInput!) { attachmentCreate(input: $input) { success } }`
	input := map[string]any{"issueId": issue, "url": metaURL, "title": "JigFlow", "subtitle": m.subtitle(), "metadata": json.RawMessage(data)}
	return l.mutate(q, map[string]any{"input": input}, "save the issue's JigFlow attachment", nil)
}

// comment adds a comment to the issue.
func (l *linear) comment(id, body string) error {
	i, err := l.issue(id)
	if err != nil {
		return err
	}
	q := `mutation CreateComment($input: CommentCreateInput!) {
  commentCreate(input: $input) { success }
}`
	return l.mutate(q, map[string]any{"input": map[string]any{"issueId": i.ID, "body": body}}, "add the comment", nil)
}

// item is the protocol's item for the issue.
func (l *linear) item(i lnIssue) item {
	m, _ := metaOf(i)
	it := item{ID: strconv.Itoa(i.Number), Title: i.Title, Body: i.description(), State: i.State.Name}
	switch {
	case i.Assignee == nil:
	case m.Claim != nil && m.Claim.Assignee == i.Assignee.ID:
		it.Claim = m.Claim.Session
	default:
		// Someone took the issue in Linear: it is theirs.
		it.Claim = i.Assignee.DisplayName
	}
	for _, r := range i.InverseRelations.Nodes {
		if r.Type == "blocks" && r.Issue.Team.Key == l.s.Team {
			it.addLink(blockingLink, link{ID: strconv.Itoa(r.Issue.Number)})
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m.Links)) {
		for _, lk := range m.Links[name] {
			it.addLink(name, lk)
		}
	}
	for _, lb := range i.Labels.Nodes {
		it.Labels = append(it.Labels, lb.Name)
	}
	return it
}

// addLink adds the Link to the item, unless it has it.
func (it *item) addLink(name string, l link) {
	for _, x := range it.Links[name] {
		if x == l {
			return
		}
	}
	if it.Links == nil {
		it.Links = map[string][]link{}
	}
	it.Links[name] = append(it.Links[name], l)
}
