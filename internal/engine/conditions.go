package engine

import (
	"fmt"
	"slices"
	"strings"
)

// Condition kinds.
const (
	LinkedAllIn = "linked-all-in" // every Artifact a Link points to is in one of Statuses
	HasIncoming = "has-incoming"  // at least Min Artifacts point here through Link
)

// String describes the condition as something that must hold.
func (c Condition) String() string {
	switch c.Kind {
	case LinkedAllIn:
		return fmt.Sprintf("every %q item is %s", c.Link, strings.Join(c.Statuses, "/"))
	case HasIncoming:
		return fmt.Sprintf("at least %d item(s) link here via %q", c.Min, c.Link)
	}
	return c.Kind
}

// holds reports whether c holds for a, given every Artifact in the Store.
func (c Condition) holds(a Artifact, all []Artifact) bool {
	switch c.Kind {
	case LinkedAllIn:
		for _, id := range a.Links[c.Link] {
			i := slices.IndexFunc(all, func(o Artifact) bool { return o.ID == id })
			if i < 0 || !slices.Contains(c.Statuses, all[i].Status) {
				return false
			}
		}
		return true
	case HasIncoming:
		n := 0
		for _, o := range all {
			if slices.Contains(o.Links[c.Link], a.ID) {
				n++
			}
		}
		return n >= c.Min
	}
	return false
}

// failed returns the conditions that don't hold for a.
func failed(conds []Condition, a Artifact, all []Artifact) []Condition {
	var out []Condition
	for _, c := range conds {
		if !c.holds(a, all) {
			out = append(out, c)
		}
	}
	return out
}

// describe joins the descriptions of conds.
func describe(conds []Condition) string {
	s := make([]string, len(conds))
	for i, c := range conds {
		s[i] = c.String()
	}
	return strings.Join(s, "; ")
}
