package service

import (
	"errors"
	"reflect"
	"testing"

	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/store"
)

func TestModel_FailedApplyTailDoesNotPoisonRecovery(t *testing.T) {
	cases := []struct {
		name      string
		rejected  assay.CurveChunk
		wantError error
	}{
		{
			name: "stale generation curve upload",
			rejected: assay.CurveChunk{
				RunID:        "r1",
				Well:         well(1, 1),
				Generation:   1,
				Seq:          3,
				CycleStart:   16,
				CycleEnd:     16,
				Fluorescence: []int64{0},
			},
			wantError: assay.ErrStaleGeneration,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fs, err := store.OpenFileStore(dir)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			e, err := NewEngine(fs)
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}
			lockTestBatch(t, e)

			for i, item := range []struct {
				run   string
				well  protocol.WellRef
				curve []int64
			}{
				{"r1", well(1, 1), positiveCurve()},
				{"r2", well(1, 2), positiveCurve()},
				{"r3", well(2, 1), positiveCurve()},
				{"r4", well(2, 2), positiveCurve()},
				{"r5", well(8, 1), positiveCurve()},
				{"r6", well(8, 2), positiveCurve()},
				{"r7", well(8, 3), negativeCurve()},
			} {
				ingestWell(t, e, item.run, item.well, item.curve)
				if _, err := e.Interpret("batch-1", item.well); err != nil {
					t.Fatalf("interpret well %d: %v", i, err)
				}
			}
			if _, err := e.EvaluateContamination("batch-1"); err != nil {
				t.Fatalf("evaluate contamination: %v", err)
			}
			if _, err := e.CreateRetest("batch-1"); err != nil {
				t.Fatalf("create retest: %v", err)
			}

			before, err := e.GetBatch("batch-1")
			if err != nil {
				t.Fatalf("get batch before rejection: %v", err)
			}
			committedBefore, err := fs.Replay()
			if err != nil {
				t.Fatalf("replay before rejection: %v", err)
			}
			if _, err := e.IngestChunk("batch-1", "rejected-op", tc.rejected); !errors.Is(err, tc.wantError) {
				t.Fatalf("rejected upload error = %v, want %v", err, tc.wantError)
			}
			if err := fs.Close(); err != nil {
				t.Fatalf("close store: %v", err)
			}

			fs, err = store.OpenFileStore(dir)
			if err != nil {
				t.Fatalf("reopen store after rejected upload: %v", err)
			}
			e, err = NewEngine(fs)
			if err != nil {
				fs.Close()
				t.Fatalf("recover engine after rejected upload: %v", err)
			}
			after, err := e.GetBatch("batch-1")
			if err != nil {
				fs.Close()
				t.Fatalf("get recovered batch: %v", err)
			}
			if !reflect.DeepEqual(after, before) {
				fs.Close()
				t.Fatalf("rejected upload changed recovered batch\nbefore: %#v\nafter:  %#v", before, after)
			}
			committedAfter, err := fs.Replay()
			if err != nil {
				fs.Close()
				t.Fatalf("replay after recovery: %v", err)
			}
			if len(committedAfter) != len(committedBefore) {
				fs.Close()
				t.Fatalf("rejected operation was published: committed events = %d, want %d", len(committedAfter), len(committedBefore))
			}

			if _, err := e.CreateRun("batch-1", "generation-2-run", well(1, 1)); err != nil {
				fs.Close()
				t.Fatalf("create valid run after recovery: %v", err)
			}
			valid := assay.CurveChunk{
				RunID:        "generation-2-run",
				Well:         well(1, 1),
				Generation:   2,
				Seq:          1,
				CycleStart:   1,
				CycleEnd:     1,
				Fluorescence: []int64{0},
				Complete:     true,
			}
			txn, err := e.IngestChunk("batch-1", "rejected-op", valid)
			if err != nil {
				fs.Close()
				t.Fatalf("valid upload after recovery: %v", err)
			}
			if err := fs.Close(); err != nil {
				t.Fatalf("close recovered store: %v", err)
			}

			fs, err = store.OpenFileStore(dir)
			if err != nil {
				t.Fatalf("reopen after valid upload: %v", err)
			}
			defer fs.Close()
			e, err = NewEngine(fs)
			if err != nil {
				t.Fatalf("recover valid upload: %v", err)
			}
			retryTxn, err := e.IngestChunk("batch-1", "rejected-op", valid)
			if err != nil {
				t.Fatalf("idempotent retry after restart: %v", err)
			}
			if retryTxn != txn {
				t.Fatalf("idempotent transaction changed across restart: got %q, want %q", retryTxn, txn)
			}
		})
	}
}
