package service

import (
	"errors"
	"testing"

	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/store"
	"edna-contamination-verdict/internal/verdict"
)

func TestRestartRestoresFullState(t *testing.T) {
	dir := t.TempDir()
	fs, err := store.OpenFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(fs)
	if err != nil {
		t.Fatal(err)
	}
	lockTestBatch(t, e)
	if _, err := e.Load("batch-1", assay.LoadRequest{OperationID: "op-1", TubeCode: "T1A", Well: well(1, 1)}); err != nil {
		t.Fatal(err)
	}
	if err := e.SubmitReview("batch-1", verdict.Review{ReviewerID: "r1", Qualification: "operator", Digest: e.batches["batch-1"].evidenceDigest()}); err != nil {
		t.Fatal(err)
	}
	fs.Close()

	fs2, err := store.OpenFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fs2.Close()
	e2, err := NewEngine(fs2)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e2.protocols["proto-1"]; !ok {
		t.Fatal("protocol not restored")
	}
	b, err := e2.GetBatch("batch-1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Generation != 1 || !b.Locked {
		t.Fatalf("batch state wrong: gen=%d locked=%v", b.Generation, b.Locked)
	}
	if len(b.Reviews) != 1 {
		t.Fatalf("reviews not restored: %d", len(b.Reviews))
	}
	if e2.batches["batch-1"].LoadedTubes["T1A"] != well(1, 1).Key() {
		t.Fatal("loaded tube not restored")
	}
}

func TestFaultBeforeCommitLeavesNoPartialState(t *testing.T) {
	dir := t.TempDir()
	fs, _ := store.OpenFileStore(dir)
	e, err := NewEngine(fs)
	if err != nil {
		t.Fatal(err)
	}
	fs.SetFailpoints(store.Failpoints{BeforeCommit: func() error { return errors.New("boom") }})
	if _, err := e.CreateProtocol(testSpec()); err == nil {
		t.Fatal("expected commit failure")
	}
	fs.Close()

	fs2, err := store.OpenFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fs2.Close()
	e2, err := NewEngine(fs2)
	if err != nil {
		t.Fatal(err)
	}
	if len(e2.protocols) != 0 {
		t.Fatalf("partial protocol leaked after failed commit: %d", len(e2.protocols))
	}
}

func TestStaleGenerationChunkRejected(t *testing.T) {
	e, _ := NewEngine(store.NewMemoryStore())
	lockTestBatch(t, e)
	ingestWell(t, e, "r1", well(1, 1), positiveCurve())
	ingestWell(t, e, "r2", well(1, 2), positiveCurve())
	ingestWell(t, e, "r3", well(2, 1), positiveCurve())
	ingestWell(t, e, "r4", well(2, 2), positiveCurve())
	ingestWell(t, e, "r5", well(8, 1), positiveCurve())
	ingestWell(t, e, "r6", well(8, 2), positiveCurve())
	ingestWell(t, e, "r7", well(8, 3), negativeCurve())
	for _, w := range []protocol.WellRef{well(1, 1), well(1, 2), well(2, 1), well(2, 2), well(8, 1), well(8, 2), well(8, 3)} {
		if _, err := e.Interpret("batch-1", w); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.EvaluateContamination("batch-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.CreateRetest("batch-1"); err != nil {
		t.Fatal(err)
	}
	if e.batches["batch-1"].Generation != 2 {
		t.Fatalf("expected generation 2, got %d", e.batches["batch-1"].Generation)
	}

	old := assay.CurveChunk{RunID: "r1", Well: well(1, 1), Generation: 1, Seq: 1, CycleStart: 1, CycleEnd: 5, Fluorescence: []int64{0, 0, 0, 0, 0}}
	if _, err := e.IngestChunk("batch-1", "old-op", old); err == nil {
		t.Fatal("expected stale generation error")
	}
}
