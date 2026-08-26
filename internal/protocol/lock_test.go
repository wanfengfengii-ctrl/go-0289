package protocol

import "testing"

func wr(r, c int) WellRef { return WellRef{Plate: "P", Row: r, Col: c} }

func baseSpec() ProtocolSpec {
	return ProtocolSpec{
		ID: "p", Target: "t", Scale: 1000, Threshold: 10,
		BaselineStart: 0, BaselineEnd: 4, Window: 3,
		PositiveMin: 6000, PositiveMax: 8000, ReplicateCount: 2,
		ReagentLot: "L",
		Layout: LayoutSpec{
			PlateID: "P", Rows: 8, Cols: 12,
			Samples: []SampleSpec{
				{ReplicateGroup: "S1", Tubes: []TubePlacement{{TubeCode: "T1", Well: wr(1, 1)}, {TubeCode: "T2", Well: wr(1, 2)}}},
			},
			Controls: []ControlSpec{
				{Kind: PositiveControl, Well: wr(8, 1)},
				{Kind: NegativeControl, Well: wr(8, 2)},
			},
		},
	}
}

func TestLockSuccess(t *testing.T) {
	lr, err := Lock(baseSpec())
	if err != nil {
		t.Fatal(err)
	}
	if lr.Snapshot.Digest == "" {
		t.Fatal("empty digest")
	}
	if len(lr.Assignments) != 4 { // 2 samples + 2 controls
		t.Fatalf("expected 4 assignments, got %d", len(lr.Assignments))
	}
}

func TestLockMissingReplicates(t *testing.T) {
	s := baseSpec()
	s.Layout.Samples[0].Tubes = s.Layout.Samples[0].Tubes[:1]
	if _, err := Lock(s); err != ErrMissingReplicates {
		t.Fatalf("want ErrMissingReplicates, got %v", err)
	}
}

func TestLockDuplicateTube(t *testing.T) {
	s := baseSpec()
	s.Layout.Samples = append(s.Layout.Samples, SampleSpec{
		ReplicateGroup: "S2",
		Tubes:          []TubePlacement{{TubeCode: "T1", Well: wr(2, 1)}, {TubeCode: "T3", Well: wr(2, 2)}},
	})
	if _, err := Lock(s); err != ErrDuplicateTube {
		t.Fatalf("want ErrDuplicateTube, got %v", err)
	}
}

func TestLockControlShortage(t *testing.T) {
	s := baseSpec()
	s.Layout.Controls = []ControlSpec{{Kind: PositiveControl, Well: wr(8, 1)}}
	if _, err := Lock(s); err != ErrControlShortage {
		t.Fatalf("want ErrControlShortage, got %v", err)
	}
}

func TestLockInvalidEdge(t *testing.T) {
	s := baseSpec()
	s.Edges = []PropagationEdge{{From: wr(1, 1), To: wr(9, 9)}}
	if _, err := Lock(s); err != ErrInvalidEdge {
		t.Fatalf("want ErrInvalidEdge, got %v", err)
	}
}

func TestLockWellOutOfBounds(t *testing.T) {
	s := baseSpec()
	s.Layout.Controls[0].Well = wr(9, 1)
	if _, err := Lock(s); err != ErrWellOutOfBounds {
		t.Fatalf("want ErrWellOutOfBounds, got %v", err)
	}
}

func TestLockWellReused(t *testing.T) {
	s := baseSpec()
	s.Layout.Controls[0].Well = wr(1, 1) // collides with sample tube T1
	if _, err := Lock(s); err != ErrWellReused {
		t.Fatalf("want ErrWellReused, got %v", err)
	}
}
