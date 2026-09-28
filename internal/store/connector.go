package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// Protocol is the version of the Connector protocol jfl speaks, sent in
// every request. docs/connector-protocol.md describes it.
const Protocol = 1

// Connector is the Connector Store of one Connector: it keeps the Artifacts
// of the Artifact Types that name it in an outside tracker, and reaches the
// tracker only by running the Connector, one request per run (ADR 0008).
//
// An Artifact's id is its Artifact Type's prefix and the tracker's own id
// for it, so T-41 is the tracker's item 41. The project settings map its
// Status and fields to the tracker's labels or states, and its Claim is the
// tracker's to record, in its assignee.
//
// The tracker is the source of truth for its Artifacts: teammates change
// them in it freely, so there is no content hash to hold them to. Instead a
// change jfl makes is checked against what jfl last read, so a Status or
// Claim changed in the tracker while a move waited on its Gates refuses the
// move rather than being overwritten.
type Connector struct {
	root string
	pb   *engine.Playbook
	c    *engine.Connector
	seen map[string]engine.Artifact // by id, as jfl last read or wrote it
}

// NewConnector returns the Connector Store of the Connector c, for the
// project rooted at root.
func NewConnector(root string, pb *engine.Playbook, c *engine.Connector) *Connector {
	return &Connector{root: root, pb: pb, c: c, seen: map[string]engine.Artifact{}}
}

// Kinds of Connector failure. A Connector reports the first six; jfl
// reports KindProtocol when a Connector can't be run or doesn't answer as
// the protocol says.
const (
	KindAuth      = "auth"       // the tracker didn't accept the Connector's credentials
	KindRateLimit = "rate_limit" // the tracker is limiting the Connector's requests
	KindNetwork   = "network"    // the tracker couldn't be reached
	KindNotFound  = "not_found"  // the tracker has no such item
	KindInvalid   = "invalid"    // the tracker, or the Connector, refused the request as made
	KindInternal  = "internal"   // anything else that went wrong in the Connector
	KindProtocol  = "protocol"
)

// ConnectorError is a Connector failing: a problem reaching or using the
// tracker, which is not a workflow refusal.
type ConnectorError struct {
	Connector  string
	Op         string
	Kind       string
	Message    string
	RetryAfter int // seconds, for KindRateLimit, when the Connector says
}

func (e *ConnectorError) Error() string {
	c := fmt.Sprintf("Connector %q", e.Connector)
	switch e.Kind {
	case KindAuth:
		return fmt.Sprintf("%s could not authenticate with the tracker: %s. Check the credentials it uses.", c, e.Message)
	case KindRateLimit:
		retry := "Retry later."
		if e.RetryAfter > 0 {
			retry = fmt.Sprintf("Retry in %ds.", e.RetryAfter)
		}
		return fmt.Sprintf("%s was rate-limited by the tracker: %s. %s", c, e.Message, retry)
	case KindNetwork:
		return fmt.Sprintf("%s could not reach the tracker: %s. Check the network and retry.", c, e.Message)
	case KindInvalid:
		return fmt.Sprintf("the tracker refused %s's %s request: %s", c, e.Op, e.Message)
	case KindProtocol:
		return fmt.Sprintf("%s broke the Connector protocol on %s: %s", c, e.Op, e.Message)
	}
	return fmt.Sprintf("%s failed on %s (%s): %s", c, e.Op, e.Kind, e.Message)
}

// The wire format of the protocol.
type (
	request struct {
		Protocol int            `json:"protocol"`
		Op       string         `json:"op"`
		Type     string         `json:"type"`
		Settings map[string]any `json:"settings"`
		ID       string         `json:"id,omitempty"`
		Item     *item          `json:"item,omitempty"`
		From     *term          `json:"from,omitempty"`
		To       *term          `json:"to,omitempty"`
		Claim    *string        `json:"claim,omitempty"`
		Body     string         `json:"body,omitempty"`
	}
	response struct {
		Items []item `json:"items"`
		Item  *item  `json:"item"`
		Error *struct {
			Kind       string `json:"kind"`
			Message    string `json:"message"`
			RetryAfter int    `json:"retry_after"`
		} `json:"error"`
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
	// link is one target of a Link: an item of the same tracker, by the
	// tracker's id, or an Artifact kept elsewhere, by its Artifact id.
	link struct {
		ID       string `json:"id,omitempty"`
		Artifact string `json:"artifact,omitempty"`
	}
	term struct {
		Label string `json:"label,omitempty"`
		State string `json:"state,omitempty"`
	}
)

// List returns every Artifact of the Artifact Types the Connector keeps.
func (s *Connector) List() ([]engine.Artifact, error) {
	var out []engine.Artifact
	for _, t := range s.types() {
		resp, err := s.call(t, request{Op: "list"})
		if err != nil {
			return nil, err
		}
		for _, it := range resp.Items {
			a := s.artifact(t, it)
			// What a command first read of an Artifact is what it decided
			// on, so a later List doesn't replace it.
			if _, ok := s.seen[a.ID]; !ok {
				s.seen[a.ID] = a
			}
			out = append(out, a)
		}
	}
	return out, nil
}

// Get returns the Artifact with the given id.
func (s *Connector) Get(id string) (engine.Artifact, error) {
	t, tid, ok := s.split(id)
	if !ok {
		return engine.Artifact{}, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	resp, err := s.call(t, request{Op: "get", ID: tid})
	if errors.Is(err, ErrNotFound) {
		return engine.Artifact{}, fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	if err != nil {
		return engine.Artifact{}, err
	}
	if resp.Item == nil {
		return engine.Artifact{}, s.broken("get", "the response has no item")
	}
	a := s.artifact(t, *resp.Item)
	s.seen[id] = a
	return a, nil
}

// Create creates the Artifact in the tracker, which gives it its id. The
// body of one an agent creates is the AI-generated marker.
func (s *Connector) Create(a engine.Artifact, by engine.Actor) (engine.Artifact, error) {
	t := s.pb.Type(a.Type)
	m := s.c.Types[t.Name]
	status := statusTerm(m, a.Status)
	it := &item{Title: a.Title, State: status.State, Links: s.wireLinks(t, a.Links)}
	if status.Label != "" {
		it.Labels = append(it.Labels, status.Label)
	}
	for _, field := range slices.Sorted(maps.Keys(a.Fields)) {
		ft := fieldTerm(m, field, a.Fields[field])
		if ft.Label != "" {
			it.Labels = append(it.Labels, ft.Label)
		}
		if ft.State != "" {
			it.State = ft.State
		}
	}
	if by.Agent() {
		it.Body = s.c.Marker
	}
	resp, err := s.call(t, request{Op: "create", Item: it})
	if err != nil {
		return engine.Artifact{}, err
	}
	if resp.Item == nil || resp.Item.ID == "" {
		return engine.Artifact{}, s.broken("create", "the response has no item with an id")
	}
	created := s.artifact(t, *resp.Item)
	s.seen[created.ID] = created
	return created, nil
}

// Save changes the Artifact's Status and Claim in the tracker to its own,
// where they differ from what jfl last read.
func (s *Connector) Save(a engine.Artifact) error {
	before, ok := s.seen[a.ID]
	if !ok {
		var err error
		if before, err = s.Get(a.ID); err != nil {
			return err
		}
	}
	t, tid, _ := s.split(a.ID)
	m := s.c.Types[t.Name]
	if a.Status != before.Status {
		from, to := term(statusTerm(m, before.Status)), term(statusTerm(m, a.Status))
		if _, err := s.call(t, request{Op: "status", ID: tid, From: &from, To: &to}); err != nil {
			return err
		}
	}
	if a.Claim != before.Claim {
		if _, err := s.call(t, request{Op: "claim", ID: tid, Claim: &a.Claim}); err != nil {
			return err
		}
	}
	s.seen[a.ID] = a
	return nil
}

// Verify reads the Artifact from the tracker again, and refuses a change
// to it if its Status or Claim changed there since jfl last read it. A
// tracker's body is its own, so no body edit is reported.
func (s *Connector) Verify(id string) (bool, error) {
	before, ok := s.seen[id]
	now, err := s.Get(id)
	if err != nil || !ok {
		return false, err
	}
	if now.Status != before.Status {
		return false, fmt.Errorf("%s: its Status changed in the tracker from %q to %q while jfl was changing it; nothing was changed, run the command again", id, before.Status, now.Status)
	}
	if now.Claim != before.Claim {
		return false, fmt.Errorf("%s: its Claim changed in the tracker while jfl was changing it; nothing was changed, run the command again", id)
	}
	return false, nil
}

// Comment adds a comment to the Artifact in the tracker. A comment by an
// agent ends with the AI-generated marker.
func (s *Connector) Comment(id, text string, by engine.Actor) error {
	t, tid, ok := s.split(id)
	if !ok {
		return fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	if by.Agent() {
		text = strings.TrimRight(text, "\n") + "\n\n" + s.c.Marker
	}
	_, err := s.call(t, request{Op: "comment", ID: tid, Body: text})
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%s: %w", id, ErrNotFound)
	}
	return err
}

// types returns the Artifact Types the Connector keeps, in declaration
// order.
func (s *Connector) types() []*engine.ArtifactType {
	var ts []*engine.ArtifactType
	for _, t := range s.pb.Types {
		if t.Store == s.c.Name {
			ts = append(ts, t)
		}
	}
	return ts
}

// split returns the Artifact Type the Artifact id names and the tracker's
// id for it.
func (s *Connector) split(id string) (*engine.ArtifactType, string, bool) {
	for _, t := range s.types() {
		if tid, ok := strings.CutPrefix(id, t.Prefix+"-"); ok && tid != "" {
			return t, tid, true
		}
	}
	return nil, "", false
}

// artifact is the Artifact of Artifact Type t that the tracker's item is.
// Its Status is the first of t's whose label or state it has, or, for an
// item a teammate filed with none, t's first initial Status; each field is
// the first of its values whose label or state it has.
func (s *Connector) artifact(t *engine.ArtifactType, it item) engine.Artifact {
	m := s.c.Types[t.Name]
	a := engine.Artifact{ID: t.Prefix + "-" + it.ID, Type: t.Name, Title: it.Title, Claim: it.Claim}
	if i := slices.IndexFunc(t.Statuses, func(st string) bool { return it.has(statusTerm(m, st)) }); i >= 0 {
		a.Status = t.Statuses[i]
	} else if len(t.Initial) > 0 {
		a.Status = t.Initial[0]
	}
	for _, field := range slices.Sorted(maps.Keys(t.Fields)) {
		values := t.Fields[field]
		if i := slices.IndexFunc(values, func(v string) bool { return it.has(fieldTerm(m, field, v)) }); i >= 0 {
			if a.Fields == nil {
				a.Fields = map[string]string{}
			}
			a.Fields[field] = values[i]
		}
	}
	for name, targets := range it.Links {
		target := s.pb.Type(t.Links[name])
		for _, l := range targets {
			id := l.Artifact
			if l.ID != "" && target != nil {
				id = target.Prefix + "-" + l.ID
			}
			if id == "" {
				continue
			}
			if a.Links == nil {
				a.Links = map[string][]string{}
			}
			a.Links[name] = append(a.Links[name], id)
		}
	}
	return a
}

// wireLinks are an Artifact of Artifact Type t's Links as the protocol
// sends them: by the tracker's id when the target is kept by the same
// Connector, by the Artifact id otherwise.
func (s *Connector) wireLinks(t *engine.ArtifactType, links map[string][]string) map[string][]link {
	var out map[string][]link
	for name, ids := range links {
		target := s.pb.Type(t.Links[name])
		for _, id := range ids {
			l := link{Artifact: id}
			if target != nil && target.Store == s.c.Name {
				l = link{ID: strings.TrimPrefix(id, target.Prefix+"-")}
			}
			if out == nil {
				out = map[string][]link{}
			}
			out[name] = append(out[name], l)
		}
	}
	return out
}

// has reports whether the item has the label and the state of tt.
func (it item) has(tt engine.TrackerTerm) bool {
	return (tt.Label == "" || slices.Contains(it.Labels, tt.Label)) && (tt.State == "" || it.State == tt.State)
}

// statusTerm is the label or state the project settings map a Status to:
// by default, a label named after it.
func statusTerm(m engine.TrackerMapping, status string) engine.TrackerTerm {
	if tt, ok := m.Statuses[status]; ok {
		return tt
	}
	return engine.TrackerTerm{Label: status}
}

// fieldTerm is the label or state the project settings map a field's value
// to: by default, a label named after the value.
func fieldTerm(m engine.TrackerMapping, field, value string) engine.TrackerTerm {
	if tt, ok := m.Fields[field][value]; ok {
		return tt
	}
	return engine.TrackerTerm{Label: value}
}

// call runs the Connector with one request for an Artifact of Artifact
// Type t, and returns its response. A not_found error is ErrNotFound; any
// other is a *ConnectorError.
func (s *Connector) call(t *engine.ArtifactType, req request) (response, error) {
	req.Protocol = Protocol
	req.Type = t.Name
	req.Settings = maps.Clone(s.c.Settings)
	if ts := s.c.Types[t.Name].Settings; len(ts) > 0 {
		if req.Settings == nil {
			req.Settings = map[string]any{}
		}
		maps.Copy(req.Settings, ts)
	}
	in, err := json.Marshal(req)
	if err != nil {
		return response{}, err
	}
	command := s.c.Command
	if !filepath.IsAbs(command) && strings.ContainsAny(command, `/\`) {
		command = filepath.Join(s.root, command)
	}
	cmd := exec.Command(command, s.c.Args...)
	cmd.Dir = s.root
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var resp response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil || runErr != nil {
		why := fmt.Sprintf("its response is not JSON (%v)", err)
		if runErr != nil {
			why = runErr.Error()
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			why += ": " + msg
		}
		return response{}, s.broken(req.Op, why)
	}
	if e := resp.Error; e != nil {
		if e.Kind == KindNotFound {
			return response{}, fmt.Errorf("%s: %w", e.Message, ErrNotFound)
		}
		return response{}, &ConnectorError{Connector: s.c.Name, Op: req.Op, Kind: e.Kind, Message: e.Message, RetryAfter: e.RetryAfter}
	}
	return resp, nil
}

// broken is the error of a Connector that didn't answer op as the protocol
// says.
func (s *Connector) broken(op, why string) error {
	return &ConnectorError{Connector: s.c.Name, Op: op, Kind: KindProtocol, Message: why}
}
