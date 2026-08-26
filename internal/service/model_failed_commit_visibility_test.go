package service_test

import (
	"errors"
	"testing"

	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/service"
	"edna-contamination-verdict/internal/store"
	"edna-contamination-verdict/internal/verdict"
)

func TestModel_FailedCommitIsNotVisibleToSameEngine(t *testing.T) {
	commitErr := errors.New("injected commit failure")
	well := func(row, col int) protocol.WellRef {
		return protocol.WellRef{Plate: "P", Row: row, Col: col}
	}
	spec := protocol.ProtocolSpec{
		ID:             "protocol-failed-commit",
		Target:         "target-A",
		Scale:          1000,
		Threshold:      10,
		BaselineStart:  0,
		BaselineEnd:    4,
		Window:         3,
		PositiveMin:    6000,
		PositiveMax:    8000,
		ReplicateCount: 1,
		ReagentLot:     "lot-1",
		Layout: protocol.LayoutSpec{
			PlateID: "P",
			Rows:    8,
			Cols:    12,
			Samples: []protocol.SampleSpec{{
				ReplicateGroup: "sample-1",
				Tubes: []protocol.TubePlacement{{
					TubeCode: "tube-1",
					Well:     well(1, 1),
				}},
			}},
			Controls: []protocol.ControlSpec{
				{Kind: protocol.PositiveControl, Well: well(8, 1)},
				{Kind: protocol.NegativeControl, Well: well(8, 2)},
			},
		},
	}

	tests := []struct {
		name string
		run  func(*testing.T, *service.Engine, *store.FileStore)
	}{
		{
			name: "protocol cannot be used to lock a batch",
			run: func(t *testing.T, engine *service.Engine, fileStore *store.FileStore) {
				fileStore.SetFailpoints(store.Failpoints{BeforeCommit: func() error { return commitErr }})
				if _, err := engine.CreateProtocol(spec); !errors.Is(err, commitErr) {
					t.Fatalf("CreateProtocol error = %v, want %v", err, commitErr)
				}
				fileStore.SetFailpoints(store.Failpoints{})

				if _, err := engine.LockBatch("batch-after-failed-protocol", spec.ID, ""); !errors.Is(err, protocol.ErrUnknownProtocol) {
					t.Fatalf("LockBatch error = %v, want %v", err, protocol.ErrUnknownProtocol)
				}
				if got := engine.ListBatches(); len(got) != 0 {
					t.Fatalf("ListBatches = %v, want no batches", got)
				}
			},
		},
		{
			name: "batch and generated wells remain absent",
			run: func(t *testing.T, engine *service.Engine, fileStore *store.FileStore) {
				if _, err := engine.CreateProtocol(spec); err != nil {
					t.Fatalf("CreateProtocol setup: %v", err)
				}
				fileStore.SetFailpoints(store.Failpoints{BeforeCommit: func() error { return commitErr }})
				if _, err := engine.LockBatch("failed-batch", spec.ID, ""); !errors.Is(err, commitErr) {
					t.Fatalf("LockBatch error = %v, want %v", err, commitErr)
				}
				fileStore.SetFailpoints(store.Failpoints{})

				if _, err := engine.GetBatch("failed-batch"); !errors.Is(err, service.ErrBatchNotFound) {
					t.Fatalf("GetBatch error = %v, want %v", err, service.ErrBatchNotFound)
				}
				if _, err := engine.GetWells("failed-batch"); !errors.Is(err, service.ErrBatchNotFound) {
					t.Fatalf("GetWells error = %v, want %v", err, service.ErrBatchNotFound)
				}
			},
		},
		{
			name: "run does not publish an upload cursor",
			run: func(t *testing.T, engine *service.Engine, fileStore *store.FileStore) {
				if _, err := engine.CreateProtocol(spec); err != nil {
					t.Fatalf("CreateProtocol setup: %v", err)
				}
				if _, err := engine.LockBatch("batch-with-run", spec.ID, ""); err != nil {
					t.Fatalf("LockBatch setup: %v", err)
				}
				fileStore.SetFailpoints(store.Failpoints{BeforeCommit: func() error { return commitErr }})
				if _, err := engine.CreateRun("batch-with-run", "failed-run", well(1, 1)); !errors.Is(err, commitErr) {
					t.Fatalf("CreateRun error = %v, want %v", err, commitErr)
				}
				fileStore.SetFailpoints(store.Failpoints{})

				if _, err := engine.GetCursor("failed-run"); !errors.Is(err, service.ErrRunNotFound) {
					t.Fatalf("GetCursor error = %v, want %v", err, service.ErrRunNotFound)
				}
			},
		},
		{
			name: "review can be retried as previously unseen",
			run: func(t *testing.T, engine *service.Engine, fileStore *store.FileStore) {
				if _, err := engine.CreateProtocol(spec); err != nil {
					t.Fatalf("CreateProtocol setup: %v", err)
				}
				if _, err := engine.LockBatch("batch-with-review", spec.ID, ""); err != nil {
					t.Fatalf("LockBatch setup: %v", err)
				}
				batch, err := engine.GetBatch("batch-with-review")
				if err != nil {
					t.Fatalf("GetBatch setup: %v", err)
				}
				review := verdict.Review{ReviewerID: "reviewer-1", Qualification: "operator", Digest: batch.Evidence}
				fileStore.SetFailpoints(store.Failpoints{BeforeCommit: func() error { return commitErr }})
				if err := engine.SubmitReview("batch-with-review", review); !errors.Is(err, commitErr) {
					t.Fatalf("SubmitReview error = %v, want %v", err, commitErr)
				}
				fileStore.SetFailpoints(store.Failpoints{})

				batch, err = engine.GetBatch("batch-with-review")
				if err != nil {
					t.Fatalf("GetBatch after failed review: %v", err)
				}
				if len(batch.Reviews) != 0 {
					t.Fatalf("published reviews = %v, want none", batch.Reviews)
				}
				if err := engine.SubmitReview("batch-with-review", review); err != nil {
					t.Fatalf("retry SubmitReview should see no prior review: %v", err)
				}
			},
		},
		{
			name: "terminal decision can be retried as undecided",
			run: func(t *testing.T, engine *service.Engine, fileStore *store.FileStore) {
				if _, err := engine.CreateProtocol(spec); err != nil {
					t.Fatalf("CreateProtocol setup: %v", err)
				}
				if _, err := engine.LockBatch("batch-with-decision", spec.ID, ""); err != nil {
					t.Fatalf("LockBatch setup: %v", err)
				}
				batch, err := engine.GetBatch("batch-with-decision")
				if err != nil {
					t.Fatalf("GetBatch setup: %v", err)
				}
				for _, review := range []verdict.Review{
					{ReviewerID: "reviewer-1", Qualification: "operator", Digest: batch.Evidence},
					{ReviewerID: "reviewer-2", Qualification: "scientist", Digest: batch.Evidence},
				} {
					if err := engine.SubmitReview("batch-with-decision", review); err != nil {
						t.Fatalf("SubmitReview setup: %v", err)
					}
				}
				decision := verdict.FinalDecision{Type: verdict.FinalQuarantine}
				fileStore.SetFailpoints(store.Failpoints{BeforeCommit: func() error { return commitErr }})
				if _, err := engine.Decide("batch-with-decision", decision); !errors.Is(err, commitErr) {
					t.Fatalf("Decide error = %v, want %v", err, commitErr)
				}
				fileStore.SetFailpoints(store.Failpoints{})

				batch, err = engine.GetBatch("batch-with-decision")
				if err != nil {
					t.Fatalf("GetBatch after failed decision: %v", err)
				}
				if batch.Final != nil {
					t.Fatalf("published final decision = %+v, want nil", batch.Final)
				}
				if _, err := engine.Decide("batch-with-decision", decision); err != nil {
					t.Fatalf("retry Decide should see an undecided batch: %v", err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fileStore, err := store.OpenFileStore(t.TempDir())
			if err != nil {
				t.Fatalf("OpenFileStore: %v", err)
			}
			t.Cleanup(func() {
				if err := fileStore.Close(); err != nil {
					t.Errorf("Close FileStore: %v", err)
				}
			})
			engine, err := service.NewEngine(fileStore)
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}
			test.run(t, engine, fileStore)
		})
	}
}
