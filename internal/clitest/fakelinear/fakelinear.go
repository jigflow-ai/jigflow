// Package fakelinear is an in-memory Linear for tests: an httptest server
// answering the GraphQL operations that the Linear Connector sends, for one
// workspace with one team, so no test reaches the real Linear.
//
// It tells operations apart by the name each query or mutation declares
// (query Issues(…)), and answers them with Linear's response shapes. Like
// Linear, it requires an API key, sends no Retry-After when rate limiting
// but the reset time in X-RateLimit-Requests-Reset, reports errors in the
// GraphQL errors list with an extensions code, keeps one assignee per
// issue, stores "A blocks B" as a relation of A, updates the attachment of
// an issue that already has one with the same URL, and paginates with
// cursors.
package fakelinear

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Key is the only API key the fake Linear accepts.
const Key = "lin_api_test"

// Issue is an issue of the fake team.
type Issue struct {
	Number      int
	Title       string
	Description string
	State       string // a workflow state's name; the team's default, Todo, when empty
	Labels      []string
	Assignee    string // the display name of its assignee, if any
	BlockedBy   []int  // numbers of the issues that block it
	Comments    []string
	Attachments []Attachment
}

// Attachment is an attachment of an issue.
type Attachment struct {
	ID       string
	URL      string
	Title    string
	Subtitle string
	Metadata map[string]any
}

// state is a workflow state of the team.
type state struct{ name, typ string }

// states are the fake team's workflow states, in Linear's default order.
var states = []state{
	{"Backlog", "backlog"}, {"Todo", "unstarted"}, {"In Progress", "started"},
	{"In Review", "started"}, {"Done", "completed"}, {"Canceled", "canceled"},
}

// defaultState is the state of a new issue created without one.
const defaultState = "Todo"

// Server is the fake Linear.
type Server struct {
	*httptest.Server
	Team   string   // the team's key
	Viewer string   // the display name of the API key's user
	Users  []string // the display names of the workspace's users

	mu          sync.Mutex
	issues      []*Issue
	labels      []string
	otherLabels []string // labels of another team
	next        int
	ids         int
	rateLimited bool
	failStatus  int
	pageSize    int
}

// New starts a fake Linear whose workspace has the team ENG and the users
// jfl-bot, the API key's, alice, bob and roberto, and stops it when the
// test ends. The team's issues are numbered from next.
func New(t testing.TB, next int) *Server {
	t.Helper()
	s := &Server{Team: "ENG", Viewer: "jfl-bot", Users: []string{"jfl-bot", "alice", "bob", "roberto"}, next: next, pageSize: 50}
	s.Server = httptest.NewServer(s.routes())
	t.Cleanup(s.Close)
	return s
}

// Add puts an issue in the team, with the next number unless it has one,
// and returns its number. Its labels are created in the team.
func (s *Server) Add(i Issue) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i.Number == 0 {
		i.Number = s.next
	}
	s.next = max(s.next, i.Number+1)
	if i.State == "" {
		i.State = defaultState
	}
	for _, l := range i.Labels {
		s.addLabel(l)
	}
	s.issues = append(s.issues, &i)
	return i.Number
}

// AddLabel creates a label in the team.
func (s *Server) AddLabel(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addLabel(name)
}

// AddOtherTeamsLabel creates a label in another team of the workspace,
// which the team's issues can't have.
func (s *Server) AddOtherTeamsLabel(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.otherLabels = append(s.otherLabels, name)
}

// Labels returns the team's labels.
func (s *Server) Labels() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.labels)
}

// Issue returns a copy of the issue with the given number.
func (s *Server) Issue(t testing.TB, number int) Issue {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(number)
	if i == nil {
		t.Fatalf("the fake Linear has no issue %d", number)
	}
	c := *i
	c.Labels, c.BlockedBy, c.Comments, c.Attachments = slices.Clone(i.Labels), slices.Clone(i.BlockedBy), slices.Clone(i.Comments), slices.Clone(i.Attachments)
	return c
}

// Edit changes an issue as a teammate would in Linear.
func (s *Server) Edit(number int, edit func(*Issue)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	edit(s.find(number))
}

// RateLimit makes every request fail as Linear does when the API key's
// rate limit is used up, until 42 seconds from now.
func (s *Server) RateLimit() { s.mu.Lock(); s.rateLimited = true; s.mu.Unlock() }

// Fail makes every request fail with the given HTTP status.
func (s *Server) Fail(status int) { s.mu.Lock(); s.failStatus = status; s.mu.Unlock() }

// PageSize sets how many issues a page of a list holds at most.
func (s *Server) PageSize(n int) { s.mu.Lock(); s.pageSize = n; s.mu.Unlock() }

func (s *Server) addLabel(name string) {
	if !slices.Contains(s.labels, name) {
		s.labels = append(s.labels, name)
	}
}

func (s *Server) find(number int) *Issue {
	for _, i := range s.issues {
		if i.Number == number {
			return i
		}
	}
	return nil
}

func (s *Server) byID(id string) *Issue {
	for _, i := range s.issues {
		if issueID(i.Number) == id {
			return i
		}
	}
	return nil
}

func (s *Server) newID(kind string) string {
	s.ids++
	return fmt.Sprintf("%s-%d", kind, s.ids)
}

// The ids of the team's objects, which the API uses in place of names.
func issueID(number int) string  { return fmt.Sprintf("issue-%d", number) }
func stateID(name string) string { return "state-" + name }
func labelID(name string) string { return "label-" + name }
func userID(name string) string  { return "user-" + name }

const teamID = "team-eng"

func stateByID(id string) (state, bool) {
	for _, st := range states {
		if stateID(st.name) == id {
			return st, true
		}
	}
	return state{}, false
}

var operation = regexp.MustCompile(`^\s*(query|mutation)\s+(\w+)`)

// request is a GraphQL request.
type request struct {
	Query     string                     `json:"query"`
	Variables map[string]json.RawMessage `json:"variables"`
}

func (s *Server) routes() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		data, _ := io.ReadAll(r.Body)
		var req request
		if err := json.Unmarshal(data, &req); err != nil {
			fail(w, 400, "GRAPHQL_PARSE_FAILED", "Syntax Error: the body is not JSON")
			return
		}
		m := operation.FindStringSubmatch(req.Query)
		if m == nil {
			fail(w, 400, "GRAPHQL_PARSE_FAILED", "Syntax Error: an operation must be named")
			return
		}
		switch {
		case r.Header.Get("Authorization") != Key:
			fail(w, 401, "AUTHENTICATION_ERROR", "Authentication required, not authenticated")
		case s.rateLimited:
			w.Header().Set("X-RateLimit-Requests-Remaining", "0")
			w.Header().Set("X-RateLimit-Requests-Reset", strconv.FormatInt(time.Now().Add(42*time.Second).UnixMilli(), 10))
			fail(w, 400, "RATELIMITED", "Rate limit exceeded")
		case s.failStatus != 0:
			w.WriteHeader(s.failStatus)
			fmt.Fprint(w, http.StatusText(s.failStatus))
		default:
			h, ok := s.handlers()[m[2]]
			if !ok {
				fail(w, 400, "GRAPHQL_VALIDATION_FAILED", fmt.Sprintf("Unknown operation %s", m[2]))
				return
			}
			h(w, req.Variables)
		}
	})
}

type handler func(http.ResponseWriter, map[string]json.RawMessage)

func (s *Server) handlers() map[string]handler {
	return map[string]handler{
		"Viewer": func(w http.ResponseWriter, _ map[string]json.RawMessage) {
			reply(w, map[string]any{"viewer": s.user(s.Viewer)})
		},
		"Team":             s.team,
		"User":             s.userByName,
		"Issues":           s.list,
		"Labels":           s.labelsByName,
		"CreateLabel":      s.createLabel,
		"CreateIssue":      s.createIssue,
		"UpdateIssue":      s.updateIssue,
		"CreateRelation":   s.createRelation,
		"SaveAttachment":   s.saveAttachment,
		"DeleteAttachment": s.deleteAttachment,
		"CreateComment":    s.createComment,
	}
}

func (s *Server) user(name string) map[string]any {
	return map[string]any{"id": userID(name), "displayName": name}
}

func (s *Server) team(w http.ResponseWriter, v map[string]json.RawMessage) {
	var key string
	json.Unmarshal(v["key"], &key)
	nodes := []any{}
	if key == s.Team {
		st := []any{}
		for _, x := range states {
			st = append(st, map[string]any{"id": stateID(x.name), "name": x.name, "type": x.typ})
		}
		nodes = append(nodes, map[string]any{
			"id": teamID, "key": s.Team, "name": "Engineering",
			"states":            map[string]any{"nodes": st},
			"defaultIssueState": map[string]any{"id": stateID(defaultState), "name": defaultState},
		})
	}
	reply(w, map[string]any{"teams": map[string]any{"nodes": nodes}})
}

func (s *Server) userByName(w http.ResponseWriter, v map[string]json.RawMessage) {
	var name string
	json.Unmarshal(v["name"], &name)
	nodes := []any{}
	if slices.Contains(s.Users, name) {
		nodes = append(nodes, s.user(name))
	}
	reply(w, map[string]any{"users": map[string]any{"nodes": nodes}})
}

// filter is the part of Linear's IssueFilter the fake understands.
type filter struct {
	Team struct {
		ID struct{ Eq string } `json:"id"`
	} `json:"team"`
	Number *struct{ Eq float64 } `json:"number"`
	Labels *struct {
		Some struct {
			Name struct{ Eq string } `json:"name"`
		} `json:"some"`
	} `json:"labels"`
}

func (s *Server) list(w http.ResponseWriter, v map[string]json.RawMessage) {
	var f filter
	if err := json.Unmarshal(v["filter"], &f); err != nil {
		fail(w, 400, "GRAPHQL_VALIDATION_FAILED", "Variable \"$filter\" got an invalid value")
		return
	}
	var after string
	json.Unmarshal(v["after"], &after)
	var match []any
	for _, i := range s.issues {
		if f.Team.ID.Eq != teamID {
			continue
		}
		if f.Number != nil && float64(i.Number) != f.Number.Eq {
			continue
		}
		if f.Labels != nil && !slices.Contains(i.Labels, f.Labels.Some.Name.Eq) {
			continue
		}
		match = append(match, s.json(i))
	}
	from, _ := strconv.Atoi(after)
	from = min(from, len(match))
	to := min(from+s.pageSize, len(match))
	nodes := match[from:to]
	if nodes == nil {
		nodes = []any{}
	}
	reply(w, map[string]any{"issues": map[string]any{
		"nodes":    nodes,
		"pageInfo": map[string]any{"hasNextPage": to < len(match), "endCursor": strconv.Itoa(to)},
	}})
}

func (s *Server) labelsByName(w http.ResponseWriter, v map[string]json.RawMessage) {
	var f struct {
		Name struct{ In []string } `json:"name"`
	}
	json.Unmarshal(v["filter"], &f)
	nodes := []any{}
	for _, l := range s.labels {
		if slices.Contains(f.Name.In, l) {
			nodes = append(nodes, map[string]any{"id": labelID(l), "name": l, "team": map[string]any{"id": teamID}})
		}
	}
	for _, l := range s.otherLabels {
		if slices.Contains(f.Name.In, l) {
			nodes = append(nodes, map[string]any{"id": "other-" + labelID(l), "name": l, "team": map[string]any{"id": "team-other"}})
		}
	}
	reply(w, map[string]any{"issueLabels": map[string]any{"nodes": nodes}})
}

func (s *Server) createLabel(w http.ResponseWriter, v map[string]json.RawMessage) {
	var in struct {
		Name   string `json:"name"`
		TeamID string `json:"teamId"`
	}
	json.Unmarshal(v["input"], &in)
	if in.TeamID != teamID || in.Name == "" || slices.Contains(s.labels, in.Name) {
		fail(w, 400, "INVALID_INPUT", "Argument Validation Error")
		return
	}
	s.addLabel(in.Name)
	reply(w, map[string]any{"issueLabelCreate": map[string]any{"success": true, "issueLabel": map[string]any{"id": labelID(in.Name), "name": in.Name}}})
}

// labelNames returns the names of the team's labels with the ids, or false
// if an id is no label the team's issues can have.
func (s *Server) labelNames(ids []string) ([]string, bool) {
	var out []string
	for _, id := range ids {
		i := slices.IndexFunc(s.labels, func(l string) bool { return labelID(l) == id })
		if i < 0 {
			return nil, false
		}
		out = append(out, s.labels[i])
	}
	return out, true
}

func (s *Server) createIssue(w http.ResponseWriter, v map[string]json.RawMessage) {
	var in struct {
		TeamID      string   `json:"teamId"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		LabelIDs    []string `json:"labelIds"`
		StateID     string   `json:"stateId"`
	}
	json.Unmarshal(v["input"], &in)
	labels, ok := s.labelNames(in.LabelIDs)
	if in.TeamID != teamID || in.Title == "" || !ok {
		fail(w, 400, "INVALID_INPUT", "Argument Validation Error")
		return
	}
	i := &Issue{Number: s.next, Title: in.Title, Description: in.Description, State: defaultState, Labels: labels}
	if in.StateID != "" {
		st, ok := stateByID(in.StateID)
		if !ok {
			fail(w, 400, "INVALID_INPUT", "Argument Validation Error: stateId")
			return
		}
		i.State = st.name
	}
	s.next++
	s.issues = append(s.issues, i)
	reply(w, map[string]any{"issueCreate": map[string]any{"success": true, "issue": s.json(i)}})
}

func (s *Server) updateIssue(w http.ResponseWriter, v map[string]json.RawMessage) {
	var id string
	json.Unmarshal(v["id"], &id)
	i := s.byID(id)
	if i == nil {
		fail(w, 400, "INVALID_INPUT", "Entity not found: Issue")
		return
	}
	var in map[string]json.RawMessage
	json.Unmarshal(v["input"], &in)
	var add, remove []string
	var desc, stID string
	json.Unmarshal(in["addedLabelIds"], &add)
	json.Unmarshal(in["removedLabelIds"], &remove)
	json.Unmarshal(in["stateId"], &stID)
	added, ok := s.labelNames(add)
	removed, ok2 := s.labelNames(remove)
	st, ok3 := stateByID(stID)
	if !ok || !ok2 || (stID != "" && !ok3) {
		fail(w, 400, "INVALID_INPUT", "Argument Validation Error")
		return
	}
	if raw, ok := in["assigneeId"]; ok {
		var a *string
		json.Unmarshal(raw, &a)
		switch {
		case a == nil:
			i.Assignee = ""
		default:
			j := slices.IndexFunc(s.Users, func(u string) bool { return userID(u) == *a })
			if j < 0 {
				fail(w, 400, "INVALID_INPUT", "Argument Validation Error: assigneeId")
				return
			}
			i.Assignee = s.Users[j]
		}
	}
	if json.Unmarshal(in["description"], &desc) == nil {
		i.Description = desc
	}
	i.Labels = slices.DeleteFunc(i.Labels, func(l string) bool { return slices.Contains(removed, l) })
	for _, l := range added {
		if !slices.Contains(i.Labels, l) {
			i.Labels = append(i.Labels, l)
		}
	}
	if stID != "" {
		i.State = st.name
	}
	reply(w, map[string]any{"issueUpdate": map[string]any{"success": true, "issue": s.json(i)}})
}

func (s *Server) createRelation(w http.ResponseWriter, v map[string]json.RawMessage) {
	var in struct {
		IssueID        string `json:"issueId"`
		RelatedIssueID string `json:"relatedIssueId"`
		Type           string `json:"type"`
	}
	json.Unmarshal(v["input"], &in)
	blocker, blocked := s.byID(in.IssueID), s.byID(in.RelatedIssueID)
	if blocker == nil || blocked == nil || in.Type != "blocks" {
		fail(w, 400, "INVALID_INPUT", "Argument Validation Error")
		return
	}
	blocked.BlockedBy = append(blocked.BlockedBy, blocker.Number)
	reply(w, map[string]any{"issueRelationCreate": map[string]any{"success": true}})
}

func (s *Server) saveAttachment(w http.ResponseWriter, v map[string]json.RawMessage) {
	var in struct {
		IssueID  string         `json:"issueId"`
		URL      string         `json:"url"`
		Title    string         `json:"title"`
		Subtitle string         `json:"subtitle"`
		Metadata map[string]any `json:"metadata"`
	}
	json.Unmarshal(v["input"], &in)
	i := s.byID(in.IssueID)
	if i == nil || in.URL == "" || in.Title == "" {
		fail(w, 400, "INVALID_INPUT", "Argument Validation Error")
		return
	}
	a := Attachment{URL: in.URL, Title: in.Title, Subtitle: in.Subtitle, Metadata: in.Metadata}
	if j := slices.IndexFunc(i.Attachments, func(x Attachment) bool { return x.URL == in.URL }); j >= 0 {
		a.ID = i.Attachments[j].ID
		i.Attachments[j] = a
	} else {
		a.ID = s.newID("attachment")
		i.Attachments = append(i.Attachments, a)
	}
	reply(w, map[string]any{"attachmentCreate": map[string]any{"success": true, "attachment": map[string]any{"id": a.ID}}})
}

func (s *Server) deleteAttachment(w http.ResponseWriter, v map[string]json.RawMessage) {
	var id string
	json.Unmarshal(v["id"], &id)
	for _, i := range s.issues {
		if j := slices.IndexFunc(i.Attachments, func(a Attachment) bool { return a.ID == id }); j >= 0 {
			i.Attachments = slices.Delete(i.Attachments, j, j+1)
			reply(w, map[string]any{"attachmentDelete": map[string]any{"success": true}})
			return
		}
	}
	fail(w, 400, "INVALID_INPUT", "Entity not found: Attachment")
}

func (s *Server) createComment(w http.ResponseWriter, v map[string]json.RawMessage) {
	var in struct {
		IssueID string `json:"issueId"`
		Body    string `json:"body"`
	}
	json.Unmarshal(v["input"], &in)
	i := s.byID(in.IssueID)
	if i == nil {
		fail(w, 400, "INVALID_INPUT", "Entity not found: Issue")
		return
	}
	i.Comments = append(i.Comments, in.Body)
	reply(w, map[string]any{"commentCreate": map[string]any{"success": true}})
}

// json is the issue as Linear's GraphQL API shows it.
func (s *Server) json(i *Issue) map[string]any {
	var st state
	for _, x := range states {
		if x.name == i.State {
			st = x
		}
	}
	labels := []any{}
	for _, l := range i.Labels {
		labels = append(labels, map[string]any{"id": labelID(l), "name": l})
	}
	relations := []any{}
	for _, n := range i.BlockedBy {
		relations = append(relations, map[string]any{"type": "blocks", "issue": map[string]any{"id": issueID(n), "number": n, "team": map[string]any{"key": s.Team}}})
	}
	attachments := []any{}
	for _, a := range i.Attachments {
		attachments = append(attachments, map[string]any{"id": a.ID, "url": a.URL, "title": a.Title, "subtitle": a.Subtitle, "metadata": a.Metadata})
	}
	out := map[string]any{
		"id": issueID(i.Number), "identifier": fmt.Sprintf("%s-%d", s.Team, i.Number), "number": i.Number,
		"title": i.Title, "description": i.Description,
		"state":            map[string]any{"id": stateID(st.name), "name": st.name, "type": st.typ},
		"team":             map[string]any{"id": teamID, "key": s.Team},
		"assignee":         nil,
		"labels":           map[string]any{"nodes": labels},
		"inverseRelations": map[string]any{"nodes": relations},
		"attachments":      map[string]any{"nodes": attachments},
	}
	if i.Description == "" {
		out["description"] = nil
	}
	if i.Assignee != "" {
		out["assignee"] = s.user(i.Assignee)
	}
	return out
}

func reply(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": data})
}

// fail answers with a GraphQL error, as Linear does.
func fail(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]any{
		"message": message, "extensions": map[string]any{"code": code, "userPresentableMessage": message},
	}}})
}
