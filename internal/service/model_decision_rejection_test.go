package service_test

import (
	"errors"
	"testing"

	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/service"
	"edna-contamination-verdict/internal/store"
	"edna-contamination-verdict/internal/verdict"
)

func TestModel_RejectedDecisionDoesNotPersist(t *testing.T) {
	cases := []struct {
		name       string
		preReviews bool
		wantErr    error
	}{
		{name: "insufficient review", wantErr: service.ErrInsufficientReview},
		{name: "release not ready", preReviews: true, wantErr: verdict.ErrNotReady},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := func(row, col int) protocol.WellRef {
				return protocol.WellRef{Plate: "P", Row: row, Col: col}
			}
			spec := protocol.ProtocolSpec{
				ID:             "protocol-1",
				Target:         "target-1",
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
					Samples: []protocol.SampleSpec{{
						ReplicateGroup: "sample-1",
						Tubes: []protocol.TubePlacement{
							{TubeCode: "tube-1", Well: ref(1, 1)},
							{TubeCode: "tube-2", Well: ref(1, 2)},
						},
					}},
					Controls: []protocol.ControlSpec{
						{Kind: protocol.PositiveControl, Well: ref(8, 1)},
						{Kind: protocol.NegativeControl, Well: ref(8, 2)},
					},
				},
			}

			engine, err := service.NewEngine(store.NewMemoryStore())
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}
			if _, err := engine.CreateProtocol(spec); err != nil {
				t.Fatalf("create protocol: %v", err)
			}
			if _, err := engine.LockBatch("batch-1", spec.ID, ""); err != nil {
				t.Fatalf("lock batch: %v", err)
			}

			projection, err := engine.GetBatch("batch-1")
			if err != nil {
				t.Fatalf("get batch: %v", err)
			}
			reviews := []verdict.Review{
				{ReviewerID: "reviewer-1", Qualification: verdict.QualificationOperator, Digest: projection.Evidence},
				{ReviewerID: "reviewer-2", Qualification: verdict.QualificationScientist, Digest: projection.Evidence},
			}
			if tc.preReviews {
				for _, review := range reviews {
					if err := engine.SubmitReview("batch-1", review); err != nil {
						t.Fatalf("submit initial review: %v", err)
					}
				}
			}

			if _, err := engine.Decide("batch-1", verdict.FinalDecision{Type: verdict.FinalRelease}); !errors.Is(err, tc.wantErr) {
				t.Fatalf("release error = %v, want %v", err, tc.wantErr)
			}
			projection, err = engine.GetBatch("batch-1")
			if err != nil {
				t.Fatalf("get batch after rejected release: %v", err)
			}
			if projection.Final != nil {
				t.Fatalf("rejected release persisted final decision: %+v", projection.Final)
			}

			if !tc.preReviews {
				for _, review := range reviews {
					if err := engine.SubmitReview("batch-1", review); err != nil {
						t.Fatalf("submit review after rejection: %v", err)
					}
				}
			}
			final, err := engine.Decide("batch-1", verdict.FinalDecision{Type: verdict.FinalVoid})
			if err != nil {
				t.Fatalf("void after rejected release: %v", err)
			}
			if final.Type != verdict.FinalVoid || final.Digest != projection.Evidence {
				t.Fatalf("final decision = %+v, want void bound to evidence %q", final, projection.Evidence)
			}
			if _, err := engine.Decide("batch-1", verdict.FinalDecision{Type: verdict.FinalQuarantine}); !errors.Is(err, verdict.ErrDecisionExists) {
				t.Fatalf("decision after successful void error = %v, want %v", err, verdict.ErrDecisionExists)
			}
		})
	}
}
