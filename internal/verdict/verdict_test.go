package verdict

import (
	"testing"

	"edna-contamination-verdict/internal/protocol"
)

func TestSortWellsOrdersByPlateRowCol(t *testing.T) {
	wells := []protocol.WellRef{
		{Plate: "B", Row: 1, Col: 1},
		{Plate: "A", Row: 2, Col: 1},
		{Plate: "A", Row: 1, Col: 2},
		{Plate: "A", Row: 1, Col: 1},
	}
	SortWells(wells)
	want := []protocol.WellRef{
		{Plate: "A", Row: 1, Col: 1},
		{Plate: "A", Row: 1, Col: 2},
		{Plate: "A", Row: 2, Col: 1},
		{Plate: "B", Row: 1, Col: 1},
	}
	for i := range want {
		if wells[i] != want[i] {
			t.Fatalf("index %d: got %+v want %+v", i, wells[i], want[i])
		}
	}
}

func TestOrderedClosureDoesNotMutate(t *testing.T) {
	c := ContaminationSet{Closure: []protocol.WellRef{
		{Plate: "B", Row: 1, Col: 1},
		{Plate: "A", Row: 1, Col: 1},
	}}
	got := c.OrderedClosure()
	if got[0] != (protocol.WellRef{Plate: "A", Row: 1, Col: 1}) {
		t.Fatalf("unexpected order %+v", got)
	}
	if c.Closure[0].Plate != "B" {
		t.Fatal("OrderedClosure must not mutate the receiver")
	}
}
