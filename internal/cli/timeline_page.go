package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jigflow-ai/jigflow/internal/engine"
	"github.com/jigflow-ai/jigflow/internal/store"
)

// timelinePage draws how the work went, from the Ledger only: each
// Artifact a bar across time, split by the Statuses it passed through.
// There is no forecast.
type timelinePage struct {
	chrome
	Rows []timelineRow // the top-level rows, each with those nested under it
	// Start is when the first Status change in the Ledger happened, and
	// AsOf when the bars are drawn up to: the page updates only when the
	// project changes, not as time passes (ADR 0025).
	Start, AsOf time.Time
	// Tracked says which Artifact Types a tracker keeps, whose moves made
	// in the tracker itself the Ledger doesn't record; empty when none.
	Tracked string
	// Legend is the colour of each Status drawn, by Artifact Type.
	Legend []legendType
}

// legendType is the Statuses of one Artifact Type the Timeline draws, each
// with its colour.
type legendType struct {
	Type     string
	Statuses []segment
}

// timelineRow is one Artifact's bar, and the rows nested under it.
type timelineRow struct {
	ID, Title, Status string
	Depth             int // how many rows it is nested under
	Segments          []segment
	Agent             []segment // the agent session time charged to it
	Rows              []timelineRow
}

// segment is a stretch of a bar: the time an Artifact spent in one Status.
// X and W place it across the Timeline, in percent of its width.
type segment struct {
	Status string
	Class  string // its colour, by the Status's place in its Artifact Type
	// Hatched is whether the Status has no Binding: the time the Artifact
	// waited on a person.
	Hatched bool
	Hover   string // what it says on hover: the Artifact, the Status and dates
	X, W    float64
}

func (e *env) timelineView() (any, error) {
	pb, st, err := e.load()
	if err != nil {
		return nil, err
	}
	all, err := st.List()
	if err != nil {
		return nil, err
	}
	// Pages are served concurrently, so each reads the Ledger through its
	// own handle rather than the command's.
	l, err := store.NewLedger(e.dir).Read()
	if err != nil {
		return nil, err
	}
	now := e.now()
	v := timelinePage{chrome: chrome{Playbook: pb.Name, Page: "timeline"}, AsOf: now, Start: now}
	var tracked []string
	for _, t := range pb.Types {
		if t.Store != "" {
			tracked = append(tracked, t.Name)
		}
	}
	switch len(tracked) {
	case 0:
	case 1:
		v.Tracked = tracked[0] + " is kept in a tracker"
	default:
		v.Tracked = strings.Join(tracked[:len(tracked)-1], ", ") + " and " + tracked[len(tracked)-1] + " are kept in a tracker"
	}
	for _, c := range l.Statuses {
		v.Start = minTime(v.Start, c.At)
	}
	agent := map[string][]engine.AgentStretch{}
	for _, s := range l.AgentTime() {
		if s.Artifact != "" && s.Time() > 0 {
			agent[s.Artifact] = append(agent[s.Artifact], s)
			v.Start = minTime(v.Start, s.From)
		}
	}
	span := max(now.Sub(v.Start), time.Second)
	at := func(t time.Time) float64 { return 100 * float64(t.Sub(v.Start)) / float64(span) }

	kept := byID(all)
	var rows []timelineRow
	drawn := map[string]map[string]segment{} // Artifact Type -> Status -> how it is drawn
	// Every Artifact with Status changes in the Ledger, in the order jfl
	// ledger sums them: by Artifact Type, then id.
	for _, a := range engine.Summarise(pb, l, now).Artifacts {
		h := statusHistory(pb, l, a.ID, a.Type, now)
		if len(h) == 0 {
			continue
		}
		r := timelineRow{ID: a.ID, Title: a.Title, Status: h[len(h)-1].To}
		if k, ok := kept[a.ID]; ok {
			r.Title, r.Status = k.Title, k.Status
		}
		t := pb.Type(a.Type)
		for _, c := range h {
			if c.Time == 0 && !c.Now {
				continue
			}
			end := c.At.Add(c.Time)
			to := "now"
			if !c.Now {
				to = date(end)
			}
			spent := duration(c.Time)
			if c.Now {
				spent += " so far"
			}
			seg := segment{Status: c.To, Class: statusClass(t, c.To), Hatched: t == nil || t.Bindings[c.To] == ""}
			if drawn[a.Type] == nil {
				drawn[a.Type] = map[string]segment{}
			}
			drawn[a.Type][c.To] = seg
			waiting := ""
			if seg.Hatched {
				waiting = ", waiting on a person,"
			}
			seg.Hover = fmt.Sprintf("%s %s%s from %s to %s (%s)", a.ID, c.To, waiting, date(c.At), to, spent)
			seg.X, seg.W = at(c.At), at(end)-at(c.At)
			r.Segments = append(r.Segments, seg)
		}
		for _, s := range agent[a.ID] {
			r.Agent = append(r.Agent, segment{
				Hover: fmt.Sprintf("%s agent session %s from %s to %s (%s)", a.ID, s.Session, date(s.From), date(s.To), duration(s.Time())),
				X:     at(s.From), W: at(s.To) - at(s.From),
			})
		}
		rows = append(rows, r)
	}
	v.Rows = nest(pb, rows, kept)
	for _, t := range pb.Types {
		if len(drawn[t.Name]) == 0 {
			continue
		}
		lt := legendType{Type: t.Name}
		for _, name := range t.Statuses {
			if seg, ok := drawn[t.Name][name]; ok {
				lt.Statuses = append(lt.Statuses, seg)
			}
		}
		v.Legend = append(v.Legend, lt)
	}
	return v, nil
}

// nest returns the rows as a tree: each under the row of the target of its
// Artifact's first declared Link to a different Artifact Type, a Task under
// its Story, a Story under its Requirement, and at the top when that target
// has no row. Links to the same Type, such as blocked_by, don't nest, so the
// rows stay a tree.
func nest(pb *engine.Playbook, rows []timelineRow, kept map[string]engine.Artifact) []timelineRow {
	has := map[string]bool{}
	for _, r := range rows {
		has[r.ID] = true
	}
	parent := map[string]string{}
	for _, r := range rows {
		a, ok := kept[r.ID]
		t := pb.Type(a.Type)
		if !ok || t == nil {
			continue
		}
		for _, name := range t.DeclaredLinks() {
			if t.Links[name] == a.Type || len(a.Links[name]) == 0 {
				continue
			}
			if to := a.Links[name][0]; has[to] {
				parent[r.ID] = to
			}
			break
		}
	}
	// A loop of Links across Types would leave its rows under nothing: the
	// row that closes it stays at the top.
	for _, r := range rows {
		id := parent[r.ID]
		for range rows {
			if id == "" {
				break
			}
			if id == r.ID {
				delete(parent, r.ID)
				break
			}
			id = parent[id]
		}
	}
	children := map[string][]timelineRow{}
	for _, r := range rows {
		children[parent[r.ID]] = append(children[parent[r.ID]], r)
	}
	var grow func(rs []timelineRow, depth int) []timelineRow
	grow = func(rs []timelineRow, depth int) []timelineRow {
		for i := range rs {
			rs[i].Depth = depth
			rs[i].Rows = grow(children[rs[i].ID], depth+1)
		}
		return rs
	}
	return grow(children[""], 0)
}

// statusClasses are the colours of Statuses on the Timeline.
var statusClasses = []string{"s0", "s1", "s2", "s3", "s4", "s5", "s6", "s7", "s-other"}

// Classes are the colours of Statuses on the Timeline, each of which has
// a hatched pattern.
func (timelinePage) Classes() []string { return statusClasses }

// statusClass is the colour of the Status s of the Artifact Type t, by
// its place among t's Statuses.
func statusClass(t *engine.ArtifactType, s string) string {
	i := -1
	if t != nil {
		i = slices.Index(t.Statuses, s)
	}
	if i < 0 {
		return "s-other"
	}
	return fmt.Sprintf("s%d", i%8)
}

// date is the time t as the Timeline says it.
func date(t time.Time) string {
	return t.Local().Format("2 Jan 2006 15:04")
}

func minTime(x, y time.Time) time.Time {
	if y.Before(x) {
		return y
	}
	return x
}
