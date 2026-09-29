package engine

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Unattributed is what agent session time with no Artifact in Focus is
// charged to.
const Unattributed = "unattributed"

// StatusChange is a Ledger entry: an Artifact leaving the Status From, none
// when it was created, and entering the Status To.
type StatusChange struct {
	At       time.Time
	Artifact string // its id
	Type     string
	Title    string
	From, To string
	// Via is the channel of the Confirmation behind the change, for a Human
	// Transition or an approved Proposal's; empty for a change that needed
	// none, and for every change recorded before channels were.
	Via Channel
}

// Channel is where a person gave a Confirmation: somewhere the agent can't
// answer for them.
type Channel string

// The channels a Confirmation comes through (ADR 0024).
const (
	ViaTerminal  Channel = "terminal"  // answered y at an interactive terminal
	ViaDashboard Channel = "dashboard" // clicked in the Dashboard
	ViaAgent     Channel = "agent"     // answered a form the agent's client showed only to the person
)

// FocusChange is a Ledger entry: an agent session's Focus becoming the
// Artifact Focus, or none when it is empty.
type FocusChange struct {
	At      time.Time
	Session string
	Focus   string
}

// Usage is a Ledger entry: the tokens one message of an agent session
// used, as the agent's own records say, read by an Adapter and never
// reported by the agent (ADR 0007).
type Usage struct {
	At      time.Time // when the agent's records say the message was sent
	Session string
	Agent   string // the coding agent whose records say so, e.g. claude-code
	Message string // its id in those records, which one message is recorded once under
	Tokens
}

// Tokens are the tokens of one or more messages to a model.
type Tokens struct {
	Input      int64 // not read from the cache
	Output     int64
	CacheRead  int64 // input read from the cache
	CacheWrite int64 // input written to the cache
}

func (t *Tokens) add(u Tokens) {
	t.Input += u.Input
	t.Output += u.Output
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
}

// Ledger is the record of time: every Status change and every Focus change,
// each in the order they happened, and the tokens agent sessions used
// where their agent's records expose them. The times of its changes come
// from jfl's own clock, those of usage from the agent's records, never from
// the agent itself (ADR 0007).
type Ledger struct {
	Statuses []StatusChange
	Focuses  []FocusChange
	Usages   []Usage
}

// LedgerSummary is the Ledger summed per Artifact and per Status.
type LedgerSummary struct {
	Artifacts []ArtifactTime // by Artifact Type, then id
	Statuses  []StatusTime   // by Artifact Type, then Status, in declaration order
	// Unattributed is the agent session time charged to no Artifact, spent
	// with nothing in Focus.
	Unattributed time.Duration
	// UnattributedTokens are the tokens used with nothing in Focus.
	UnattributedTokens Tokens
	// Usage is whether the Ledger records any tokens: where no agent's
	// records expose them, it records time only.
	Usage bool
	// Confirmations are the Status changes a Confirmation made, in the
	// order they happened.
	Confirmations []StatusChange
}

// ArtifactTime is the time one Artifact spent in each Status, and the agent
// session time charged to it.
type ArtifactTime struct {
	ID, Type, Title string
	Statuses        []TimeInStatus // in the Artifact Type's declaration order
	Agent           time.Duration
	Tokens          Tokens // used while it was in Focus
}

// TimeInStatus is the time an Artifact spent in one Status. When it is
// still there, Time is the time so far.
type TimeInStatus struct {
	Status string
	Time   time.Duration
	Now    bool // still in it
}

// StatusTime is the time every Artifact of one Artifact Type spent in one
// of its Statuses.
type StatusTime struct {
	Type, Status string
	Time         time.Duration
	Artifacts    int // how many spent time there
	Now          int // how many are there now
}

// Summarise sums the Ledger at the time now.
//
// An Artifact's time in a Status runs from the change that moved it in to
// the one that moved it out, or to now while it is still there, except in
// a final Status, where no work waits. An agent session's time runs from
// one Focus change to the next and is charged to the Artifact in Focus
// between them, or to Unattributed; the time after its last Focus change
// isn't charged, since jfl can't tell whether the session is still running.
// The tokens of a message are charged to the Artifact in its session's
// Focus when the message was sent, or to Unattributed.
func Summarise(pb *Playbook, l Ledger, now time.Time) LedgerSummary {
	var sum LedgerSummary
	byID := map[string]*ArtifactTime{}
	artifact := func(id string) *ArtifactTime {
		if a, ok := byID[id]; ok {
			return a
		}
		a := &ArtifactTime{ID: id}
		if t := pb.typeOfID(id); t != nil {
			a.Type = t.Name
		}
		byID[id] = a
		return a
	}
	type key struct{ typ, status string }
	statuses := map[key]*StatusTime{}
	status := func(typ, name string) *StatusTime {
		k := key{typ, name}
		if s, ok := statuses[k]; ok {
			return s
		}
		s := &StatusTime{Type: typ, Status: name}
		statuses[k] = s
		return s
	}

	// Each Artifact's changes, in the order they happened.
	changes := map[string][]StatusChange{}
	for _, c := range l.Statuses {
		changes[c.Artifact] = append(changes[c.Artifact], c)
		if c.Via != "" {
			sum.Confirmations = append(sum.Confirmations, c)
		}
	}
	for id, cs := range changes {
		a := artifact(id)
		spent := map[string]*TimeInStatus{}
		var order []string
		for i, c := range cs {
			if c.Type != "" {
				a.Type = c.Type
			}
			if c.Title != "" {
				a.Title = c.Title
			}
			end, still := now, i == len(cs)-1
			if !still {
				end = cs[i+1].At
			}
			t := pb.Type(a.Type)
			if still && t != nil && slices.Contains(t.Final, c.To) {
				continue
			}
			ts, ok := spent[c.To]
			if !ok {
				ts = &TimeInStatus{Status: c.To}
				spent[c.To] = ts
				order = append(order, c.To)
			}
			ts.Time += elapsed(c.At, end)
			ts.Now = still
		}
		for _, name := range statusOrder(pb.Type(a.Type), order) {
			ts := spent[name]
			a.Statuses = append(a.Statuses, *ts)
			s := status(a.Type, name)
			s.Time += ts.Time
			s.Artifacts++
			if ts.Now {
				s.Now++
			}
		}
	}

	sessions := l.sessions()
	for _, s := range l.AgentTime() {
		if s.Artifact == "" {
			sum.Unattributed += s.Time()
		} else {
			artifact(s.Artifact).Agent += s.Time()
		}
	}

	// Each message's tokens, once, charged to the Focus it was sent in.
	type message struct{ session, agent, id string }
	seen := map[message]bool{}
	for _, u := range l.Usages {
		sum.Usage = true
		m := message{u.Session, u.Agent, u.Message}
		if seen[m] {
			continue
		}
		seen[m] = true
		if focus := focusAt(sessions[u.Session], u.At); focus == "" {
			sum.UnattributedTokens.add(u.Tokens)
		} else {
			artifact(focus).Tokens.add(u.Tokens)
		}
	}

	for _, a := range byID {
		sum.Artifacts = append(sum.Artifacts, *a)
	}
	typeIndex := func(name string) int {
		i := slices.IndexFunc(pb.Types, func(t *ArtifactType) bool { return t.Name == name })
		if i < 0 {
			return len(pb.Types)
		}
		return i
	}
	slices.SortFunc(sum.Artifacts, func(x, y ArtifactTime) int {
		return cmp.Or(cmp.Compare(typeIndex(x.Type), typeIndex(y.Type)), compareIDs(x.ID, y.ID))
	})
	for _, t := range pb.Types {
		var names []string
		for k := range statuses {
			if k.typ == t.Name {
				names = append(names, k.status)
			}
		}
		slices.Sort(names)
		for _, name := range statusOrder(t, names) {
			sum.Statuses = append(sum.Statuses, *statuses[key{t.Name, name}])
		}
	}
	return sum
}

// AgentStretch is agent session time charged to one Artifact, or to none
// when nothing was in Focus: from one Focus change of the session to its
// next.
type AgentStretch struct {
	Session, Artifact string
	From, To          time.Time
}

// Time is how long the stretch lasted, or none when the clocks of the
// machines that recorded it disagree on which came first.
func (s AgentStretch) Time() time.Duration { return elapsed(s.From, s.To) }

// AgentTime returns the Ledger's agent session time, stretch by stretch,
// by session and in the order it happened. Time after a session's last
// Focus change isn't charged yet.
func (l Ledger) AgentTime() []AgentStretch {
	sessions := l.sessions()
	var out []AgentStretch
	for _, id := range slices.Sorted(maps.Keys(sessions)) {
		fs := sessions[id]
		for i := 0; i+1 < len(fs); i++ {
			out = append(out, AgentStretch{Session: id, Artifact: fs[i].Focus, From: fs[i].At, To: fs[i+1].At})
		}
	}
	return out
}

// sessions returns each session's Focus changes, in the order they
// happened.
func (l Ledger) sessions() map[string][]FocusChange {
	sessions := map[string][]FocusChange{}
	for _, f := range l.Focuses {
		sessions[f.Session] = append(sessions[f.Session], f)
	}
	return sessions
}

// focusAt is a session's Focus at the time at, given its Focus changes in
// the order they happened: none before the first.
func focusAt(fs []FocusChange, at time.Time) string {
	focus := ""
	for _, f := range fs {
		if f.At.After(at) {
			break
		}
		focus = f.Focus
	}
	return focus
}

// elapsed is the time from start to end, or none when the clocks of the
// machines that recorded them disagree on which came first.
func elapsed(start, end time.Time) time.Duration {
	return max(end.Sub(start), 0)
}

// statusOrder orders the named Statuses as the Artifact Type declares them,
// followed by those it no longer declares, in the order given.
func statusOrder(t *ArtifactType, names []string) []string {
	var declared, rest []string
	if t != nil {
		for _, s := range t.Statuses {
			if slices.Contains(names, s) {
				declared = append(declared, s)
			}
		}
	}
	for _, s := range names {
		if !slices.Contains(declared, s) {
			rest = append(rest, s)
		}
	}
	return append(declared, rest...)
}

// typeOfID is the Artifact Type whose prefix the Artifact id has.
func (pb *Playbook) typeOfID(id string) *ArtifactType {
	for _, t := range pb.Types {
		if strings.HasPrefix(id, t.Prefix+"-") {
			return t
		}
	}
	return nil
}

// compareIDs orders Artifact ids by prefix, then by number: T-2 before T-10.
func compareIDs(x, y string) int {
	xp, xn, _ := strings.Cut(x, "-")
	yp, yn, _ := strings.Cut(y, "-")
	if c := cmp.Compare(xp, yp); c != 0 {
		return c
	}
	xi, xerr := strconv.Atoi(xn)
	yi, yerr := strconv.Atoi(yn)
	if xerr == nil && yerr == nil {
		return cmp.Compare(xi, yi)
	}
	return cmp.Compare(xn, yn)
}
