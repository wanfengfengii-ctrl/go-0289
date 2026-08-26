package protocol

import "testing"

func TestWellRefKeyIsCanonical(t *testing.T) {
	a := WellRef{Plate: "P1", Row: 1, Col: 12}
	if got := a.Key(); got != "P1:1:12" {
		t.Fatalf("unexpected key %q", got)
	}
}

func TestComputeDigestIsStable(t *testing.T) {
	s := ProtocolSnapshot{
		Target:     "target-1",
		Scale:      1000000,
		Window:     3,
		ReagentLot: "L1",
		Edges: []PropagationEdge{
			{From: WellRef{Plate: "P", Row: 1, Col: 1}, To: WellRef{Plate: "P", Row: 1, Col: 2}},
		},
	}
	if d1, d2 := s.ComputeDigest(), s.ComputeDigest(); d1 == "" || d1 != d2 {
		t.Fatalf("digest not stable: %q vs %q", d1, d2)
	}
}

func TestComputeDigestIsEdgeOrderInsensitive(t *testing.T) {
	a := ProtocolSnapshot{
		Target: "t", Scale: 1,
		Edges: []PropagationEdge{
			{From: WellRef{Plate: "P", Row: 1, Col: 1}, To: WellRef{Plate: "P", Row: 1, Col: 2}},
			{From: WellRef{Plate: "P", Row: 2, Col: 1}, To: WellRef{Plate: "P", Row: 2, Col: 2}},
		},
	}
	b := ProtocolSnapshot{
		Target: "t", Scale: 1,
		Edges: []PropagationEdge{
			{From: WellRef{Plate: "P", Row: 2, Col: 1}, To: WellRef{Plate: "P", Row: 2, Col: 2}},
			{From: WellRef{Plate: "P", Row: 1, Col: 1}, To: WellRef{Plate: "P", Row: 1, Col: 2}},
		},
	}
	if a.ComputeDigest() != b.ComputeDigest() {
		t.Fatal("digest must be insensitive to edge order")
	}
}
