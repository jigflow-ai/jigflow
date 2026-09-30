package cli

import (
	"fmt"
	"strconv"
	"strings"
)

// laneCommit is a commit as the Git page lays it out in lanes: its hash
// and its parents', first parent first.
type laneCommit struct {
	Hash    string
	Parents []string
}

// laneRow is how a commit's row of the Git page draws its lanes, each lane
// a column, from 0 at the left. A line either passes through the row, or
// ends at the commit's dot coming from the row above, or starts at it
// going to the row below.
type laneRow struct {
	Col int // the column of the commit's dot
	// In are the columns at the row's top edge whose line ends at the dot,
	// one for each child drawn above; Out, those at its bottom edge a line
	// from the dot runs to, one for each parent; Through, those a line
	// passes straight through, to a commit further down.
	In, Out, Through []int
}

// layOut lays out commits, newest first with each parent after its
// children, in lanes: a lane runs from a commit down to its parent, so a
// branch that splits off gets a lane of its own from its branch point up,
// and a merge draws a line into the lane of each parent it merges. Each
// lane keeps its column while it runs, a column freed is reused, and
// column 0 is kept for spine, the default branch's first-parent chain,
// so it runs straight down the left. spine is the whole chain, not only
// the part of it among commits, so that laying out only the commits down
// to a page's end gives them the columns the whole history does.
func layOut(cs []laneCommit, spine []string) []laneRow {
	onSpine := make(map[string]bool, len(spine))
	for _, h := range spine {
		onSpine[h] = true
	}
	// lanes[i] is the commit the lane in column i runs down to, or "" if
	// column i is free; column 0 is never free while there is a spine.
	lanes := []string{""}
	first := 0
	if len(onSpine) > 0 {
		first = 1
	}
	free := func() int {
		for i := first; i < len(lanes); i++ {
			if lanes[i] == "" {
				return i
			}
		}
		lanes = append(lanes, "")
		return len(lanes) - 1
	}
	rows := make([]laneRow, 0, len(cs))
	for _, c := range cs {
		r := laneRow{Col: -1}
		if onSpine[c.Hash] {
			r.Col = 0
		}
		for i, h := range lanes {
			switch {
			case h == c.Hash:
				r.In = append(r.In, i)
				if r.Col < 0 {
					r.Col = i
				}
				lanes[i] = ""
			case h != "":
				r.Through = append(r.Through, i)
			}
		}
		if r.Col < 0 {
			r.Col = free()
		}
		for k, p := range c.Parents {
			// The first parent carries the commit's own lane on, down to
			// the parent even when another lane runs there too: lanes meet
			// at a branch point, not before it. A merged parent joins the
			// lane already running to it, if any.
			col := -1
			if k == 0 {
				col = r.Col
			} else {
				for i, h := range lanes {
					if h == p {
						col = i
						break
					}
				}
			}
			if col < 0 {
				col = free()
			}
			lanes[col] = p
			r.Out = append(r.Out, col)
		}
		rows = append(rows, r)
	}
	return rows
}

// width is how many columns the row's lanes take.
func (r laneRow) width() int {
	w := r.Col + 1
	for _, cols := range [][]int{r.In, r.Out, r.Through} {
		for _, c := range cols {
			w = max(w, c+1)
		}
	}
	return w
}

// laneStep is how many pixels apart the Git page draws its lanes, laneDot
// how far down a row its commit's dot is, level with the subject's first
// line, and laneColors how many colours it draws lanes in, one per column
// in turn.
const (
	laneStep   = 14
	laneDot    = 19
	laneColors = 8
)

// laneCell is a row's graph as the Git page draws it, in pixels from the
// row's top left: lines bending into the commit's dot from the row above
// and out of it into the lanes of its parents, level with the dot, and
// runs going straight on down to the row's bottom edge, however tall the
// row is.
type laneCell struct {
	Col, Color int  // the dot's column and colour
	Merge      bool // whether it is a merge, drawn as a hollow dot
	Width      int  // the same for every row of a page
	Left, Top  int  // where the dot's centre is
	Bends      []laneLine
	Runs       []laneRun
	Through    string // the columns a line passes straight through, as "1 2"
}

// laneLine is an SVG path of a row's graph, in the colour of the lane it
// belongs to.
type laneLine struct {
	D     string
	Color int
}

// laneRun is a line of a row's graph going straight down from Y to the
// row's bottom edge, at X.
type laneRun struct{ X, Y, Color int }

// cellOf draws a row, a merge's when merge, in a graph cols lanes wide.
func cellOf(r laneRow, merge bool, cols int) laneCell {
	x := func(col int) int { return laneStep/2 + col*laneStep }
	color := func(col int) int { return col % laneColors }
	c := laneCell{Col: r.Col, Color: color(r.Col), Merge: merge, Width: cols * laneStep, Left: x(r.Col), Top: laneDot}
	var through []string
	for _, col := range r.Through {
		c.Runs = append(c.Runs, laneRun{x(col), 0, color(col)})
		through = append(through, strconv.Itoa(col))
	}
	c.Through = strings.Join(through, " ")
	// A line from a child above ends at the dot, bending in from its own
	// lane; one to a parent below leaves it, bending out into the parent's
	// lane as far below the dot as the dot is below the top, and runs on.
	bend := func(x1, y1, x2, y2 int) string {
		m := (y1 + y2) / 2
		return fmt.Sprintf("M%d %dC%d %d %d %d %d %d", x1, y1, x1, m, x2, m, x2, y2)
	}
	for _, col := range r.In {
		c.Bends = append(c.Bends, laneLine{bend(x(col), 0, x(r.Col), laneDot), color(col)})
	}
	for _, col := range r.Out {
		c.Bends = append(c.Bends, laneLine{bend(x(r.Col), laneDot, x(col), 2*laneDot), color(col)})
		c.Runs = append(c.Runs, laneRun{x(col), 2 * laneDot, color(col)})
	}
	return c
}
