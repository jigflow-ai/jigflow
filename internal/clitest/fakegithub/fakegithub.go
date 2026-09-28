// Package fakegithub is an in-memory GitHub for tests: an httptest server
// answering the part of GitHub's REST API that the GitHub Issues Connector
// uses, for one repository, so no test reaches the real GitHub.
//
// Like GitHub, it requires a token, adds a label to an issue even when the
// repository has no such label (creating it), reports an issue's
// dependencies summary, and paginates lists with a Link header.
package fakegithub

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Token is the only token the fake GitHub accepts.
const Token = "test-token"

// Issue is an issue, or a pull request, in the fake repository.
type Issue struct {
	Number      int
	ID          int64 // the database id, which the dependencies API uses
	Title       string
	Body        string
	State       string // open or closed
	Labels      []string
	Assignees   []string
	BlockedBy   []int // numbers of the issues blocking it
	Comments    []string
	PullRequest bool
}

// Server is the fake GitHub.
type Server struct {
	*httptest.Server
	Owner, Repo string
	Login       string // the login of the token's user

	mu          sync.Mutex
	issues      []*Issue
	labels      []string
	next        int
	noDeps      bool
	rateLimited bool
	failStatus  int
	pageSize    int
	requests    []string
}

// New starts a fake GitHub for the repository acme/shop, whose token's user
// is jfl-bot, and stops it when the test ends. Its issues are numbered from
// next.
func New(t testing.TB, next int) *Server {
	t.Helper()
	s := &Server{Owner: "acme", Repo: "shop", Login: "jfl-bot", next: next, pageSize: 100}
	s.Server = httptest.NewServer(s.routes())
	t.Cleanup(s.Close)
	return s
}

// Add puts an issue in the repository, with the next number unless it has
// one, and returns its number. Its labels are created in the repository.
func (s *Server) Add(i Issue) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i.Number == 0 {
		i.Number = s.next
	}
	s.next = max(s.next, i.Number+1)
	i.ID = int64(i.Number) * 1000
	if i.State == "" {
		i.State = "open"
	}
	for _, l := range i.Labels {
		s.addLabel(l)
	}
	s.issues = append(s.issues, &i)
	return i.Number
}

// AddLabel creates a label in the repository.
func (s *Server) AddLabel(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addLabel(name)
}

// Labels returns the repository's labels.
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
		t.Fatalf("the fake GitHub has no issue %d", number)
	}
	c := *i
	c.Labels, c.Assignees, c.BlockedBy, c.Comments = slices.Clone(i.Labels), slices.Clone(i.Assignees), slices.Clone(i.BlockedBy), slices.Clone(i.Comments)
	return c
}

// Edit changes an issue as a teammate would in GitHub.
func (s *Server) Edit(number int, edit func(*Issue)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	edit(s.find(number))
}

// WithoutDependencies makes the repository one where issue dependencies
// aren't available: the dependencies API answers 404 and issues have no
// dependencies summary.
func (s *Server) WithoutDependencies() { s.mu.Lock(); s.noDeps = true; s.mu.Unlock() }

// RateLimit makes every request fail as GitHub does when the token's rate
// limit is used up.
func (s *Server) RateLimit() { s.mu.Lock(); s.rateLimited = true; s.mu.Unlock() }

// Fail makes every request fail with the given HTTP status.
func (s *Server) Fail(status int) { s.mu.Lock(); s.failStatus = status; s.mu.Unlock() }

// PageSize sets how many items a page of a list holds at most.
func (s *Server) PageSize(n int) { s.mu.Lock(); s.pageSize = n; s.mu.Unlock() }

// Requests returns the requests the fake GitHub got, as "METHOD path".
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

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

func (s *Server) byID(id int64) *Issue {
	for _, i := range s.issues {
		if i.ID == id {
			return i
		}
	}
	return nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	repo := "/repos/{owner}/{repo}"
	issue := repo + "/issues/{number}"
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"login": s.Login})
	})
	mux.HandleFunc("GET "+repo+"/issues", s.list)
	mux.HandleFunc("POST "+repo+"/issues", s.create)
	mux.HandleFunc("GET "+issue, s.withIssue(func(w http.ResponseWriter, r *http.Request, i *Issue) {
		reply(w, 200, s.json(i))
	}))
	mux.HandleFunc("PATCH "+issue, s.withIssue(func(w http.ResponseWriter, r *http.Request, i *Issue) {
		var in struct{ Title, Body, State *string }
		if !decode(w, r, &in) {
			return
		}
		if in.Body != nil {
			i.Body = *in.Body
		}
		if in.State != nil {
			if *in.State != "open" && *in.State != "closed" {
				reply(w, 422, map[string]any{"message": "Validation Failed: state"})
				return
			}
			i.State = *in.State
		}
		reply(w, 200, s.json(i))
	}))
	mux.HandleFunc("POST "+issue+"/labels", s.withIssue(func(w http.ResponseWriter, r *http.Request, i *Issue) {
		var in struct{ Labels []string }
		if !decode(w, r, &in) {
			return
		}
		for _, l := range in.Labels {
			s.addLabel(l)
			if !slices.Contains(i.Labels, l) {
				i.Labels = append(i.Labels, l)
			}
		}
		reply(w, 200, labelsJSON(i.Labels))
	}))
	mux.HandleFunc("DELETE "+issue+"/labels/{name}", s.withIssue(func(w http.ResponseWriter, r *http.Request, i *Issue) {
		n := r.PathValue("name")
		if !slices.Contains(i.Labels, n) {
			reply(w, 404, map[string]any{"message": "Label does not exist"})
			return
		}
		i.Labels = slices.DeleteFunc(i.Labels, func(l string) bool { return l == n })
		reply(w, 200, labelsJSON(i.Labels))
	}))
	mux.HandleFunc("POST "+issue+"/assignees", s.withIssue(func(w http.ResponseWriter, r *http.Request, i *Issue) {
		var in struct{ Assignees []string }
		if !decode(w, r, &in) {
			return
		}
		for _, a := range in.Assignees {
			if !slices.Contains(i.Assignees, a) {
				i.Assignees = append(i.Assignees, a)
			}
		}
		reply(w, 201, s.json(i))
	}))
	mux.HandleFunc("DELETE "+issue+"/assignees", s.withIssue(func(w http.ResponseWriter, r *http.Request, i *Issue) {
		var in struct{ Assignees []string }
		if !decode(w, r, &in) {
			return
		}
		i.Assignees = slices.DeleteFunc(i.Assignees, func(a string) bool { return slices.Contains(in.Assignees, a) })
		reply(w, 200, s.json(i))
	}))
	mux.HandleFunc("GET "+issue+"/comments", s.withIssue(func(w http.ResponseWriter, r *http.Request, i *Issue) {
		out := []any{}
		for _, c := range i.Comments {
			out = append(out, map[string]any{"body": c})
		}
		reply(w, 200, out)
	}))
	mux.HandleFunc("POST "+issue+"/comments", s.withIssue(func(w http.ResponseWriter, r *http.Request, i *Issue) {
		var in struct{ Body string }
		if !decode(w, r, &in) {
			return
		}
		i.Comments = append(i.Comments, in.Body)
		reply(w, 201, map[string]any{"body": in.Body})
	}))
	mux.HandleFunc("GET "+issue+"/dependencies/blocked_by", s.withIssue(func(w http.ResponseWriter, r *http.Request, i *Issue) {
		if s.noDeps {
			reply(w, 404, map[string]any{"message": "Not Found"})
			return
		}
		out := []any{}
		for _, n := range i.BlockedBy {
			out = append(out, s.json(s.find(n)))
		}
		reply(w, 200, out)
	}))
	mux.HandleFunc("POST "+issue+"/dependencies/blocked_by", s.withIssue(func(w http.ResponseWriter, r *http.Request, i *Issue) {
		if s.noDeps {
			reply(w, 404, map[string]any{"message": "Not Found"})
			return
		}
		var in struct {
			IssueID int64 `json:"issue_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		b := s.byID(in.IssueID)
		if b == nil {
			reply(w, 422, map[string]any{"message": "Validation Failed: issue_id"})
			return
		}
		i.BlockedBy = append(i.BlockedBy, b.Number)
		reply(w, 201, s.json(i))
	}))
	mux.HandleFunc("GET "+repo+"/labels/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(s.labels, r.PathValue("name")) {
			reply(w, 404, map[string]any{"message": "Not Found"})
			return
		}
		reply(w, 200, map[string]any{"name": r.PathValue("name")})
	})
	mux.HandleFunc("POST "+repo+"/labels", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Name, Color string }
		if !decode(w, r, &in) {
			return
		}
		if slices.Contains(s.labels, in.Name) {
			reply(w, 422, map[string]any{"message": "Validation Failed: already_exists"})
			return
		}
		s.addLabel(in.Name)
		reply(w, 201, map[string]any{"name": in.Name, "color": in.Color})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, r.Method+" "+r.URL.EscapedPath())
		switch {
		case r.Header.Get("Authorization") != "Bearer "+Token:
			reply(w, 401, map[string]any{"message": "Bad credentials"})
		case s.rateLimited:
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("Retry-After", "42")
			reply(w, 403, map[string]any{"message": "API rate limit exceeded for user ID 1."})
		case s.failStatus != 0:
			reply(w, s.failStatus, map[string]any{"message": http.StatusText(s.failStatus)})
		default:
			if owner, repo, ok := repoOf(r.URL.Path); ok && (owner != s.Owner || repo != s.Repo) {
				reply(w, 404, map[string]any{"message": "Not Found"})
				return
			}
			mux.ServeHTTP(w, r)
		}
	})
}

// repoOf returns the repository a /repos/ path names.
func repoOf(path string) (owner, repo string, ok bool) {
	rest, ok := strings.CutPrefix(path, "/repos/")
	parts := strings.SplitN(rest, "/", 3)
	if !ok || len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var match []any
	for _, i := range s.issues {
		if st := q.Get("state"); st != "all" && i.State != cmp.Or(st, "open") {
			continue
		}
		if l := q.Get("labels"); l != "" && !slices.Contains(i.Labels, l) {
			continue
		}
		match = append(match, s.json(i))
	}
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(page, 1)
	size, _ := strconv.Atoi(q.Get("per_page"))
	size = min(cmp.Or(size, 30), s.pageSize)
	from, to := min((page-1)*size, len(match)), min(page*size, len(match))
	if to < len(match) {
		next := *r.URL
		nq := next.Query()
		nq.Set("page", strconv.Itoa(page+1))
		next.RawQuery = nq.Encode()
		w.Header().Set("Link", fmt.Sprintf(`<%s%s>; rel="next"`, s.URL, next.RequestURI()))
	}
	out := match[from:to]
	if out == nil {
		out = []any{}
	}
	reply(w, 200, out)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title     string
		Body      string
		Labels    []string
		Assignees []string
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Title == "" {
		reply(w, 422, map[string]any{"message": "Validation Failed: title"})
		return
	}
	i := &Issue{Number: s.next, ID: int64(s.next) * 1000, Title: in.Title, Body: in.Body, State: "open", Labels: in.Labels, Assignees: in.Assignees}
	s.next++
	for _, l := range in.Labels {
		s.addLabel(l)
	}
	s.issues = append(s.issues, i)
	reply(w, 201, s.json(i))
}

func (s *Server) withIssue(h func(http.ResponseWriter, *http.Request, *Issue)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.PathValue("number"))
		i := s.find(n)
		if i == nil {
			reply(w, 404, map[string]any{"message": "Not Found"})
			return
		}
		h(w, r, i)
	}
}

// json is the issue as GitHub's REST API shows it.
func (s *Server) json(i *Issue) map[string]any {
	assignees := []any{}
	for _, a := range i.Assignees {
		assignees = append(assignees, map[string]any{"login": a})
	}
	out := map[string]any{
		"id": i.ID, "number": i.Number, "title": i.Title, "body": i.Body, "state": i.State,
		"labels": labelsJSON(i.Labels), "assignees": assignees,
	}
	if i.Body == "" {
		out["body"] = nil
	}
	if i.PullRequest {
		out["pull_request"] = map[string]any{"url": "https://example.test/pull"}
	}
	if !s.noDeps {
		out["issue_dependencies_summary"] = map[string]any{"blocked_by": len(i.BlockedBy), "total_blocked_by": len(i.BlockedBy), "blocking": 0, "total_blocking": 0}
	}
	return out
}

func labelsJSON(labels []string) []any {
	out := []any{}
	for _, l := range labels {
		out = append(out, map[string]any{"name": l})
	}
	return out
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		reply(w, 400, map[string]any{"message": "Problems parsing JSON"})
		return false
	}
	return true
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
