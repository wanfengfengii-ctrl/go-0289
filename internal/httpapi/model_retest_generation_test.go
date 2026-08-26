package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"edna-contamination-verdict/internal/httpapi"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/service"
	"edna-contamination-verdict/internal/store"
)

func TestModel_CurrentGenerationReleaseEvidence(t *testing.T) {
	type batchProjection struct {
		Generation int    `json:"generation"`
		Evidence   string `json:"evidence_digest"`
	}
	type contaminationSet struct {
		Closure []protocol.WellRef `json:"closure"`
	}

	well := func(row, col int) protocol.WellRef {
		return protocol.WellRef{Plate: "P", Row: row, Col: col}
	}
	post := func(t *testing.T, h http.Handler, path string, body any, operationID string) *httptest.ResponseRecorder {
		t.Helper()
		var payload bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&payload).Encode(body); err != nil {
				t.Fatalf("encode %s: %v", path, err)
			}
		}
		req := httptest.NewRequest(http.MethodPost, path, &payload)
		req.Header.Set("Content-Type", "application/json")
		if operationID != "" {
			req.Header.Set("Operation-Id", operationID)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	getProjection := func(t *testing.T, h http.Handler) batchProjection {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/batches/batch-1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("get batch: status %d: %s", rec.Code, rec.Body.String())
		}
		var projection batchProjection
		if err := json.Unmarshal(rec.Body.Bytes(), &projection); err != nil {
			t.Fatalf("decode batch: %v", err)
		}
		return projection
	}

	earlyCurve := []int64{0, 0, 0, 0, 5, 15, 30, 60, 100, 150, 200, 260}
	validPositive := []int64{0, 0, 0, 0, 0, 5, 15, 30, 60, 100, 150, 200}
	validNegative := []int64{0, 0, 0, 0, 0, 0, 1, 2, 1, 0, 0, 0}

	cases := []struct {
		name             string
		reopenedToFinish int
		wantStatus       int
		wantCode         string
	}{
		{name: "no current-generation curves", reopenedToFinish: 0, wantStatus: http.StatusConflict, wantCode: httpapi.CodeNotReady},
		{name: "only part of the closure is current", reopenedToFinish: 1, wantStatus: http.StatusConflict, wantCode: httpapi.CodeNotReady},
		{name: "entire closure is current", reopenedToFinish: 2, wantStatus: http.StatusCreated},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, err := service.NewEngine(store.NewMemoryStore())
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}
			h := httpapi.NewServer(engine).Handler()
			spec := protocol.ProtocolSpec{
				ID: "protocol-1", Target: "target", Scale: 1000, Threshold: 10,
				BaselineStart: 0, BaselineEnd: 4, Window: 3,
				PositiveMin: 6000, PositiveMax: 8000, ReplicateCount: 2, ReagentLot: "lot",
				Layout: protocol.LayoutSpec{
					PlateID: "P", Rows: 8, Cols: 12,
					Samples: []protocol.SampleSpec{{ReplicateGroup: "sample", Tubes: []protocol.TubePlacement{
						{TubeCode: "tube-1", Well: well(1, 1)},
						{TubeCode: "tube-2", Well: well(1, 2)},
					}}},
					Controls: []protocol.ControlSpec{
						{Kind: protocol.PositiveControl, Well: well(8, 1)},
						{Kind: protocol.NegativeControl, Well: well(8, 2)},
					},
				},
				Edges: []protocol.PropagationEdge{{From: well(1, 1), To: well(1, 2)}},
			}
			if rec := post(t, h, "/api/v1/protocols", spec, ""); rec.Code != http.StatusCreated {
				t.Fatalf("create protocol: status %d: %s", rec.Code, rec.Body.String())
			}
			if rec := post(t, h, "/api/v1/batches/batch-1/lock", map[string]any{"protocol_id": spec.ID}, ""); rec.Code != http.StatusCreated {
				t.Fatalf("lock batch: status %d: %s", rec.Code, rec.Body.String())
			}

			ingestAndInterpret := func(runID string, ref protocol.WellRef, generation int, curve []int64) {
				t.Helper()
				if rec := post(t, h, "/api/v1/runs", map[string]any{"batch_id": "batch-1", "run_id": runID, "well": ref}, ""); rec.Code != http.StatusCreated {
					t.Fatalf("create run %s: status %d: %s", runID, rec.Code, rec.Body.String())
				}
				chunk := map[string]any{
					"batch_id": "batch-1",
					"chunk": map[string]any{
						"well": ref, "generation": generation, "seq": 1,
						"cycle_start": 1, "cycle_end": len(curve),
						"fluorescence": curve, "complete": true,
					},
				}
				if rec := post(t, h, "/api/v1/runs/"+runID+"/chunks", chunk, "upload-"+runID); rec.Code != http.StatusCreated {
					t.Fatalf("upload %s: status %d: %s", runID, rec.Code, rec.Body.String())
				}
				if rec := post(t, h, "/api/v1/batches/batch-1/interpret", map[string]any{"well": ref}, ""); rec.Code != http.StatusOK {
					t.Fatalf("interpret %s: status %d: %s", runID, rec.Code, rec.Body.String())
				}
			}

			initial := []struct {
				ref   protocol.WellRef
				curve []int64
			}{
				{well(1, 1), earlyCurve},
				{well(1, 2), earlyCurve},
				{well(8, 1), validPositive},
				{well(8, 2), validNegative},
			}
			for i, input := range initial {
				ingestAndInterpret(fmt.Sprintf("generation-1-%d", i), input.ref, 1, input.curve)
			}

			rec := post(t, h, "/api/v1/batches/batch-1/contamination/evaluate", nil, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("evaluate contamination: status %d: %s", rec.Code, rec.Body.String())
			}
			var contamination contaminationSet
			if err := json.Unmarshal(rec.Body.Bytes(), &contamination); err != nil {
				t.Fatalf("decode contamination: %v", err)
			}
			if len(contamination.Closure) != 2 {
				t.Fatalf("contamination closure has %d wells, want 2", len(contamination.Closure))
			}
			if rec := post(t, h, "/api/v1/batches/batch-1/retests", nil, ""); rec.Code != http.StatusCreated {
				t.Fatalf("create retest: status %d: %s", rec.Code, rec.Body.String())
			}
			if generation := getProjection(t, h).Generation; generation != 2 {
				t.Fatalf("generation = %d, want 2", generation)
			}

			for i := 0; i < tc.reopenedToFinish; i++ {
				ingestAndInterpret(fmt.Sprintf("generation-2-%d", i), contamination.Closure[i], 2, validPositive)
			}

			digest := getProjection(t, h).Evidence
			for i, qualification := range []string{"operator", "scientist"} {
				review := map[string]any{
					"reviewer_id":   fmt.Sprintf("reviewer-%d", i+1),
					"qualification": qualification,
					"digest":        digest,
				}
				if rec := post(t, h, "/api/v1/batches/batch-1/reviews", review, ""); rec.Code != http.StatusCreated {
					t.Fatalf("submit review %d: status %d: %s", i+1, rec.Code, rec.Body.String())
				}
			}

			rec = post(t, h, "/api/v1/batches/batch-1/final-decisions", map[string]any{"type": "release"}, "")
			if rec.Code != tc.wantStatus {
				t.Fatalf("release status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCode != "" {
				var apiErr httpapi.Error
				if err := json.Unmarshal(rec.Body.Bytes(), &apiErr); err != nil {
					t.Fatalf("decode release error: %v", err)
				}
				if apiErr.Code != tc.wantCode {
					t.Fatalf("release error code = %q, want %q", apiErr.Code, tc.wantCode)
				}
			}
		})
	}
}
