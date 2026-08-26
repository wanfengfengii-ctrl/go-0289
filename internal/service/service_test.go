package service

import (
	"sync"
	"testing"

	"edna-contamination-verdict/internal/analysis"
	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/store"
	"edna-contamination-verdict/internal/verdict"
)

func well(r, c int) protocol.WellRef { return protocol.WellRef{Plate: "P", Row: r, Col: c} }

// testSpec builds a small protocol: two samples with two replicates each, one
// positive control, one negative control and one extraction blank.
func testSpec() protocol.ProtocolSpec {
	return protocol.ProtocolSpec{
		ID:             "proto-1",
		Target:         "target-A",
		Scale:          1000,
		Threshold:      10,
		BaselineStart:  0,
		BaselineEnd:    4,
		Window:         3,
		PositiveMin:    6000,
		PositiveMax:    8000,
		ReplicateCount: 2,
		ReagentLot:     "lot-1",
		Layout: protocol.LayoutSpec{
			PlateID: "P",
			Rows:    8,
			Cols:    12,
			Samples: []protocol.SampleSpec{
				{ReplicateGroup: "S1", Tubes: []protocol.TubePlacement{
					{TubeCode: "T1A", Well: well(1, 1)},
					{TubeCode: "T1B", Well: well(1, 2)},
				}},
				{ReplicateGroup: "S2", Tubes: []protocol.TubePlacement{
					{TubeCode: "T2A", Well: well(2, 1)},
					{TubeCode: "T2B", Well: well(2, 2)},
				}},
			},
			Controls: []protocol.ControlSpec{
				{Kind: protocol.PositiveControl, Well: well(8, 1)},
				{Kind: protocol.NegativeControl, Well: well(8, 2)},
				{Kind: protocol.ExtractionBlank, Well: well(8, 3)},
			},
		},
		Edges: []protocol.PropagationEdge{
			{From: well(1, 1), To: well(1, 2)},
			{From: well(2, 1), To: well(2, 2)},
		},
	}
}

// positiveCurve crosses threshold 10 at cycles 6->7.
func positiveCurve() []int64 {
	return []int64{0, 0, 0, 0, 0, 5, 15, 30, 60, 100, 150, 200, 260, 320, 380}
}

// negativeCurve stays below threshold.
func negativeCurve() []int64 {
	return []int64{0, 0, 0, 0, 0, 0, 1, 2, 1, 0, 0, 0, 0, 0, 0}
}

func lockTestBatch(t *testing.T, e *Engine) {
	t.Helper()
	if _, err := e.CreateProtocol(testSpec()); err != nil {
		t.Fatalf("create protocol: %v", err)
	}
	if _, err := e.LockBatch("batch-1", "proto-1", ""); err != nil {
		t.Fatalf("lock batch: %v", err)
	}
}

func ingestWell(t *testing.T, e *Engine, runID string, w protocol.WellRef, curve []int64) {
	t.Helper()
	if _, err := e.CreateRun("batch-1", runID, w); err != nil {
		t.Fatalf("create run: %v", err)
	}
	half := len(curve) / 2
	chunks := []assay.CurveChunk{
		{RunID: runID, Well: w, Generation: 1, Seq: 1, CycleStart: 1, CycleEnd: half, Fluorescence: curve[:half]},
		{RunID: runID, Well: w, Generation: 1, Seq: 2, CycleStart: half + 1, CycleEnd: len(curve), Fluorescence: curve[half:], Complete: true},
	}
	for i, c := range chunks {
		if _, err := e.IngestChunk("batch-1", runID+"-op-"+string(rune('a'+i)), c); err != nil {
			t.Fatalf("ingest chunk %d: %v", i, err)
		}
	}
}

func TestFullFlowToRelease(t *testing.T) {
	e, err := NewEngine(store.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	lockTestBatch(t, e)

	// Load sample tubes.
	for _, l := range []struct {
		op, tube string
		w        protocol.WellRef
	}{
		{"op-1", "T1A", well(1, 1)},
		{"op-2", "T1B", well(1, 2)},
		{"op-3", "T2A", well(2, 1)},
		{"op-4", "T2B", well(2, 2)},
	} {
		if _, err := e.Load("batch-1", assay.LoadRequest{OperationID: l.op, TubeCode: l.tube, Well: l.w}); err != nil {
			t.Fatalf("load %s: %v", l.tube, err)
		}
	}

	// Ingest curves: samples positive, controls valid.
	ingestWell(t, e, "r1", well(1, 1), positiveCurve())
	ingestWell(t, e, "r2", well(1, 2), positiveCurve())
	ingestWell(t, e, "r3", well(2, 1), positiveCurve())
	ingestWell(t, e, "r4", well(2, 2), positiveCurve())
	ingestWell(t, e, "r5", well(8, 1), positiveCurve())
	ingestWell(t, e, "r6", well(8, 2), negativeCurve())
	ingestWell(t, e, "r7", well(8, 3), negativeCurve())

	for _, w := range []protocol.WellRef{well(1, 1), well(1, 2), well(2, 1), well(2, 2), well(8, 1), well(8, 2), well(8, 3)} {
		if _, err := e.Interpret("batch-1", w); err != nil {
			t.Fatalf("interpret %v: %v", w, err)
		}
	}

	// Positive control must pass and land in range.
	pc := e.batches["batch-1"].Interps[well(8, 1).Key()]
	if pc.Control != analysis.ControlPass {
		t.Fatalf("positive control should pass, got %s", pc.Control)
	}
	if pc.ScaledCt != 6500 {
		t.Fatalf("expected scaled Ct 6500, got %d", pc.ScaledCt)
	}

	// No contamination seeds -> empty closure.
	set, err := e.EvaluateContamination("batch-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Closure) != 0 {
		t.Fatalf("expected empty closure, got %v", set.Closure)
	}

	if _, err := e.CreateRetest("batch-1"); err != nil {
		t.Fatalf("retest: %v", err)
	}

	if err := e.SubmitReview("batch-1", verdict.Review{ReviewerID: "r1", Qualification: "operator", Digest: e.batches["batch-1"].evidenceDigest()}); err != nil {
		t.Fatalf("review 1: %v", err)
	}
	if err := e.SubmitReview("batch-1", verdict.Review{ReviewerID: "r2", Qualification: "scientist", Digest: e.batches["batch-1"].evidenceDigest()}); err != nil {
		t.Fatalf("review 2: %v", err)
	}

	dec, err := e.Decide("batch-1", verdict.FinalDecision{Type: verdict.FinalRelease})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if dec.Type != verdict.FinalRelease {
		t.Fatalf("expected release, got %s", dec.Type)
	}
}

func TestChunkIdempotencyAndConflict(t *testing.T) {
	e, _ := NewEngine(store.NewMemoryStore())
	lockTestBatch(t, e)
	if _, err := e.CreateRun("batch-1", "r1", well(1, 1)); err != nil {
		t.Fatal(err)
	}
	chunk := assay.CurveChunk{RunID: "r1", Well: well(1, 1), Generation: 1, Seq: 1, CycleStart: 1, CycleEnd: 5, Fluorescence: []int64{0, 0, 0, 0, 0}}
	txn1, err := e.IngestChunk("batch-1", "op-x", chunk)
	if err != nil {
		t.Fatal(err)
	}
	txn2, err := e.IngestChunk("batch-1", "op-x", chunk)
	if err != nil {
		t.Fatal(err)
	}
	if txn1 != txn2 {
		t.Fatalf("idempotent retry should return same txn: %s vs %s", txn1, txn2)
	}
	// Same op, different content -> conflict.
	chunk.Fluorescence = []int64{1, 1, 1, 1, 1}
	if _, err := e.IngestChunk("batch-1", "op-x", chunk); err != ErrIdempotentConflict {
		t.Fatalf("want conflict, got %v", err)
	}
}

func TestConcurrentRetestCreatesSingleGeneration(t *testing.T) {
	e, _ := NewEngine(store.NewMemoryStore())
	lockTestBatch(t, e)
	// Seed a contamination by making the negative control cross.
	ingestWell(t, e, "r1", well(1, 1), positiveCurve())
	ingestWell(t, e, "r2", well(1, 2), positiveCurve())
	ingestWell(t, e, "r3", well(2, 1), positiveCurve())
	ingestWell(t, e, "r4", well(2, 2), positiveCurve())
	ingestWell(t, e, "r5", well(8, 1), positiveCurve())
	ingestWell(t, e, "r6", well(8, 2), positiveCurve()) // negative control falsely positive
	ingestWell(t, e, "r7", well(8, 3), negativeCurve())
	for _, w := range []protocol.WellRef{well(1, 1), well(1, 2), well(2, 1), well(2, 2), well(8, 1), well(8, 2), well(8, 3)} {
		if _, err := e.Interpret("batch-1", w); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.EvaluateContamination("batch-1"); err != nil {
		t.Fatal(err)
	}
	if len(e.batches["batch-1"].Contam.Closure) == 0 {
		t.Fatal("expected non-empty contamination closure")
	}

	var wg sync.WaitGroup
	gens := make([]verdict.Generation, 16)
	errs := make([]error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			gens[i], errs[i] = e.CreateRetest("batch-1")
		}(i)
	}
	wg.Wait()
	gen := e.batches["batch-1"].Generation
	if gen != 2 {
		t.Fatalf("expected exactly one new generation (2), got %d", gen)
	}
}
