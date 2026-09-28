package cli

import (
	"fmt"
	"slices"

	"github.com/jigflow-ai/jigflow/internal/engine"
)

// diagram is an Artifact Type's Status machine laid out for drawing: a box
// per Status and an arrow per Transition, in SVG coordinates.
//
// Statuses are placed in columns by how many Transitions it takes to reach
// them from an initial Status, in declaration order down a column; those
// no initial Status reaches come last. A Transition between neighbouring
// columns, or to the next box down or up a column, is a direct arrow; one
// past a box of its column goes around its right side; one along a row
// that skips columns or goes back arcs above or below the row; any other
// is an S-shaped curve from the side of one box to the side of the other.
type diagram struct {
	Width, Height int
	Nodes         []diagramNode
	Edges         []diagramEdge
}

type diagramNode struct {
	Status       string
	Skill        string // the Skill its Binding runs; empty for human work
	X, Y         int    // top left corner
	W, H         int
	TextX, TextY int // where the name is written
	SkillY       int // where the Skill is written
	Class        string
}

type diagramEdge struct {
	From, To string
	Human    bool
	Only     string // where only the Human Transition may be made, if the Playbook says
	Path     string // the SVG path data
}

// Sizes of the drawing, in pixels.
const (
	nodeH       = 46
	colGap      = 76
	rowGap      = 58
	margin      = 18
	arc         = 30 // how far an arc or a loop reaches beyond the boxes
	charWidth   = 7.6
	minNodeW    = 120
	nodePadding = 28
)

func layout(t *engine.ArtifactType) diagram {
	// The column of each Status: its distance from an initial Status.
	col := map[string]int{}
	queue := slices.Clone(t.Initial)
	for _, s := range t.Initial {
		col[s] = 0
	}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, tr := range t.Transitions {
			if _, seen := col[tr.To]; tr.From == s && !seen {
				col[tr.To] = col[s] + 1
				queue = append(queue, tr.To)
			}
		}
	}
	last := 0
	for _, c := range col {
		last = max(last, c)
	}
	row, rows := map[string]int{}, map[int]int{}
	for _, s := range t.Statuses {
		if _, ok := col[s]; !ok {
			col[s] = last + 1
		}
		row[s] = rows[col[s]]
		rows[col[s]]++
	}

	// Room above the first row for the arcs and loops drawn there.
	top := margin
	for _, tr := range t.Transitions {
		if row[tr.From] == 0 && row[tr.To] == 0 && (tr.From == tr.To || col[tr.To] > col[tr.From]+1) {
			top = margin + arc
		}
	}

	w := minNodeW
	for _, s := range t.Statuses {
		label := s
		if skill := t.Bindings[s]; len(skill)+1 > len(label) {
			label = "/" + skill
		}
		w = max(w, int(float64(len(label))*charWidth)+nodePadding)
	}

	var d diagram
	pos := map[string]diagramNode{}
	for _, s := range t.Statuses {
		n := diagramNode{
			Status: s,
			Skill:  t.Bindings[s],
			X:      margin + col[s]*(w+colGap),
			Y:      top + row[s]*(nodeH+rowGap),
			W:      w,
			H:      nodeH,
		}
		n.TextX = n.X + w/2
		n.TextY = n.Y + nodeH/2 + 5
		if n.Skill != "" {
			n.TextY = n.Y + 20
			n.SkillY = n.Y + 36
		}
		switch {
		case slices.Contains(t.Final, s):
			n.Class = "final"
		case n.Skill != "":
			n.Class = "agent"
		default:
			n.Class = "human"
		}
		if slices.Contains(t.Initial, s) {
			n.Class += " initial"
		}
		pos[s] = n
		d.Nodes = append(d.Nodes, n)
		d.Width = max(d.Width, n.X+w+arc+margin)
		d.Height = max(d.Height, n.Y+nodeH+arc+margin)
	}

	for _, tr := range t.Transitions {
		a, aok := pos[tr.From]
		b, bok := pos[tr.To]
		if !aok || !bok {
			continue
		}
		ca, cb := col[tr.From], col[tr.To]
		midA, midB := a.Y+nodeH/2, b.Y+nodeH/2
		var path string
		switch {
		case tr.From == tr.To:
			// A loop over the box.
			x := a.X + a.W - 24
			path = fmt.Sprintf("M %d %d C %d %d %d %d %d %d", x-10, a.Y, x-26, a.Y-arc, x+26, a.Y-arc, x+10, a.Y)
		case ca == cb && abs(row[tr.To]-row[tr.From]) > 1:
			// Past a box of the column: around its right side.
			x := a.X + a.W
			path = fmt.Sprintf("M %d %d C %d %d %d %d %d %d", x, midA, x+arc+10, midA, x+arc+10, midB, x, midB)
		case ca == cb:
			// Down or up to the next box of the column, beside the centre so a pair of
			// opposite Transitions stays apart.
			if b.Y > a.Y {
				path = fmt.Sprintf("M %d %d L %d %d", a.X+a.W/2-12, a.Y+nodeH, b.X+b.W/2-12, b.Y)
			} else {
				path = fmt.Sprintf("M %d %d L %d %d", a.X+a.W/2+12, a.Y, b.X+b.W/2+12, b.Y+nodeH)
			}
		case cb == ca+1:
			path = fmt.Sprintf("M %d %d L %d %d", a.X+a.W, midA, b.X, midB)
		case a.Y == b.Y && cb > ca && a.Y == top:
			// Forward past a column along the first row: over the row.
			x1, x2 := a.X+a.W/2+10, b.X+b.W/2-10
			path = fmt.Sprintf("M %d %d C %d %d %d %d %d %d", x1, a.Y, x1, a.Y-arc, x2, b.Y-arc, x2, b.Y)
		case a.Y == b.Y:
			// Back, or forward past a column, along a row: under the row.
			x1, x2 := a.X+a.W/2-10, b.X+b.W/2+10
			y := a.Y + nodeH
			path = fmt.Sprintf("M %d %d C %d %d %d %d %d %d", x1, y, x1, y+arc, x2, y+arc, x2, y)
		case cb > ca:
			// Forward to another row: from the right side to the left one.
			x1, x2 := a.X+a.W, b.X
			path = fmt.Sprintf("M %d %d C %d %d %d %d %d %d", x1, midA, x1+colGap/2, midA, x2-colGap/2, midB, x2, midB)
		default:
			// Back to another row: from the left side to the right one.
			x1, x2 := a.X, b.X+b.W
			path = fmt.Sprintf("M %d %d C %d %d %d %d %d %d", x1, midA, x1-colGap/2, midA, x2+colGap/2, midB, x2, midB)
		}
		d.Edges = append(d.Edges, diagramEdge{From: tr.From, To: tr.To, Human: tr.Human, Only: onlyIn(tr), Path: path})
	}
	return d
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
