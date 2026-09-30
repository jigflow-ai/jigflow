package cli

import (
	"reflect"
	"strings"
	"testing"
)

// graph builds commits, newest first, from "hash:parent parent…" lines.
func graph(lines ...string) []laneCommit {
	var cs []laneCommit
	for _, l := range lines {
		hash, parents, _ := strings.Cut(l, ":")
		cs = append(cs, laneCommit{Hash: hash, Parents: strings.Fields(parents)})
	}
	return cs
}

func wantRows(t *testing.T, got, want []laneRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d rows, want %d:\n got %+v\nwant %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("row %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestALinearHistoryIsOneStraightLane(t *testing.T) {
	cs := graph("c:b", "b:a", "a:")
	wantRows(t, layOut(cs, []string{"c", "b", "a"}), []laneRow{
		{Col: 0, Out: []int{0}},
		{Col: 0, In: []int{0}, Out: []int{0}},
		{Col: 0, In: []int{0}},
	})
}

func TestABranchNotMergedYetRunsBesideTheDefaultBranchDownToItsBranchPoint(t *testing.T) {
	// feat split off main at m1 and has the newest commit; main is at m2.
	cs := graph("f1:m1", "m2:m1", "m1:")
	wantRows(t, layOut(cs, []string{"m2", "m1"}), []laneRow{
		{Col: 1, Out: []int{1}},
		{Col: 0, Out: []int{0}, Through: []int{1}},
		{Col: 0, In: []int{0, 1}},
	})
}

func TestABranchMergedBackRunsInItsOwnLaneFromItsBranchPointToItsMerge(t *testing.T) {
	// feat split off main at m1 with f1, and M merged it into main after m2.
	cs := graph("M:m2 f1", "f1:m1", "m2:m1", "m1:")
	wantRows(t, layOut(cs, []string{"M", "m2", "m1"}), []laneRow{
		{Col: 0, Out: []int{0, 1}},
		{Col: 1, In: []int{1}, Out: []int{1}, Through: []int{0}},
		{Col: 0, In: []int{0}, Out: []int{0}, Through: []int{1}},
		{Col: 0, In: []int{0, 1}},
	})
}

func TestAnOctopusMergeDrawsALineIntoTheLaneOfEachParent(t *testing.T) {
	cs := graph("O:a b c", "c:r", "b:r", "a:r", "r:")
	wantRows(t, layOut(cs, []string{"O", "a", "r"}), []laneRow{
		{Col: 0, Out: []int{0, 1, 2}},
		{Col: 2, In: []int{2}, Out: []int{2}, Through: []int{0, 1}},
		{Col: 1, In: []int{1}, Out: []int{1}, Through: []int{0, 2}},
		{Col: 0, In: []int{0}, Out: []int{0}, Through: []int{1, 2}},
		{Col: 0, In: []int{0, 1, 2}},
	})
}

func TestMergingACommitAnotherLaneRunsToJoinsThatLane(t *testing.T) {
	// main merged f1 while feat went on to f2 after it.
	cs := graph("f2:f1", "M:m1 f1", "f1:m0", "m1:m0", "m0:")
	wantRows(t, layOut(cs, []string{"M", "m1", "m0"}), []laneRow{
		{Col: 1, Out: []int{1}},
		{Col: 0, Out: []int{0, 1}, Through: []int{1}},
		{Col: 1, In: []int{1}, Out: []int{1}, Through: []int{0}},
		{Col: 0, In: []int{0}, Out: []int{0}, Through: []int{1}},
		{Col: 0, In: []int{0, 1}},
	})
}

func TestAFreedColumnIsReused(t *testing.T) {
	// a merged into main at M1, then b split off later and was merged at M2.
	cs := graph("M2:m2 b", "b:m2", "m2:M1", "M1:m1 a", "a:m1", "m1:")
	wantRows(t, layOut(cs, []string{"M2", "m2", "M1", "m1"}), []laneRow{
		{Col: 0, Out: []int{0, 1}},
		{Col: 1, In: []int{1}, Out: []int{1}, Through: []int{0}},
		{Col: 0, In: []int{0, 1}, Out: []int{0}},
		{Col: 0, In: []int{0}, Out: []int{0, 1}},
		{Col: 1, In: []int{1}, Out: []int{1}, Through: []int{0}},
		{Col: 0, In: []int{0, 1}},
	})
}

func TestWithoutASpineTheNewestCommitTakesColumnZero(t *testing.T) {
	cs := graph("b:a", "a:")
	wantRows(t, layOut(cs, nil), []laneRow{
		{Col: 0, Out: []int{0}},
		{Col: 0, In: []int{0}},
	})
}

func TestALaneCrossingAPageEdgeKeepsItsColumnWhereverThePageEnds(t *testing.T) {
	// feat has three commits newer than main's m1: laid out down to the end
	// of a first page of two, before any of main's commits, feat's lane is
	// in the column it is in when the whole history is laid out.
	cs := graph("f3:f2", "f2:f1", "f1:m0", "m1:m0", "m0:")
	spine := []string{"m1", "m0"}
	whole := layOut(cs, spine)
	wantRows(t, layOut(cs[:2], spine), whole[:2])
	wantRows(t, whole[:2], []laneRow{
		{Col: 1, Out: []int{1}},
		{Col: 1, In: []int{1}, Out: []int{1}},
	})
}
