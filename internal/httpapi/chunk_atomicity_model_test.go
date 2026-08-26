package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/httpapi"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/service"
	"edna-contamination-verdict/internal/store"
)

func TestModel_RejectedChunkLeavesCursorAndSnapshotUnchanged(t *testing.T) {
	well := protocol.WellRef{Plate: "P", Row: 1, Col: 1}
	otherWell := protocol.WellRef{Plate: "P", Row: 1, Col: 2}
	tests := []struct {
		name     string
		badChunk assay.CurveChunk
		wantCode string
	}{
		{
			name: "gap marked complete",
			badChunk: assay.CurveChunk{Well: well, Generation: 1, Seq: 3, CycleStart: 5, CycleEnd: 6,
				Fluorescence: []int64{901, 902}, Complete: true},
			wantCode: httpapi.CodeChunkGap,
		},
		{
			name: "overlap marked complete",
			badChunk: assay.CurveChunk{Well: well, Generation: 1, Seq: 2, CycleStart: 2, CycleEnd: 3,
				Fluorescence: []int64{903, 904}, Complete: true},
			wantCode: httpapi.CodeChunkOverlap,
		},
		{
			name: "well mismatch marked complete",
			badChunk: assay.CurveChunk{Well: otherWell, Generation: 1, Seq: 2, CycleStart: 3, CycleEnd: 4,
				Fluorescence: []int64{905, 906}, Complete: true},
			wantCode: httpapi.CodeRunMismatch,
		},
		{
			name: "generation mismatch marked complete",
			badChunk: assay.CurveChunk{Well: well, Generation: 2, Seq: 2, CycleStart: 3, CycleEnd: 4,
				Fluorescence: []int64{907, 908}, Complete: true},
			wantCode: httpapi.CodeGenerationStale,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			memory := store.NewMemoryStore()
			engine, err := service.NewEngine(memory)
			if err != nil {
				t.Fatalf("new engine: %v", err)
			}
			spec := protocol.ProtocolSpec{
				ID: "protocol", Target: "target", Scale: 1000, Threshold: 10,
				BaselineStart: 0, BaselineEnd: 4, Window: 3,
				PositiveMin: 6000, PositiveMax: 8000, ReplicateCount: 2, ReagentLot: "lot",
				Layout: protocol.LayoutSpec{
					PlateID: "P", Rows: 8, Cols: 12,
					Samples: []protocol.SampleSpec{{ReplicateGroup: "sample", Tubes: []protocol.TubePlacement{
						{TubeCode: "tube-1", Well: well}, {TubeCode: "tube-2", Well: otherWell},
					}}},
					Controls: []protocol.ControlSpec{
						{Kind: protocol.PositiveControl, Well: protocol.WellRef{Plate: "P", Row: 8, Col: 1}},
						{Kind: protocol.NegativeControl, Well: protocol.WellRef{Plate: "P", Row: 8, Col: 2}},
					},
				},
			}
			if _, err := engine.CreateProtocol(spec); err != nil {
				t.Fatalf("create protocol: %v", err)
			}
			if _, err := engine.LockBatch("batch", spec.ID, ""); err != nil {
				t.Fatalf("lock batch: %v", err)
			}
			if _, err := engine.CreateRun("batch", "run", well); err != nil {
				t.Fatalf("create run: %v", err)
			}

			handler := httpapi.NewServer(engine).Handler()
			postChunk := func(opID string, chunk assay.CurveChunk) *httptest.ResponseRecorder {
				var body bytes.Buffer
				if err := json.NewEncoder(&body).Encode(map[string]any{"batch_id": "batch", "chunk": chunk}); err != nil {
					t.Fatalf("encode chunk: %v", err)
				}
				req := httptest.NewRequest(http.MethodPost, "/api/v1/runs/run/chunks", &body)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Operation-Id", opID)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				return rec
			}
			getCursor := func(h http.Handler) assay.Cursor {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/runs/run/cursor", nil))
				if rec.Code != http.StatusOK {
					t.Fatalf("get cursor: status %d: %s", rec.Code, rec.Body.String())
				}
				var cursor assay.Cursor
				if err := json.Unmarshal(rec.Body.Bytes(), &cursor); err != nil {
					t.Fatalf("decode cursor: %v", err)
				}
				return cursor
			}

			first := assay.CurveChunk{Well: well, Generation: 1, Seq: 1, CycleStart: 1, CycleEnd: 2,
				Fluorescence: []int64{101, 102}}
			if rec := postChunk("first-op", first); rec.Code != http.StatusCreated {
				t.Fatalf("first chunk: status %d: %s", rec.Code, rec.Body.String())
			}
			beforeCursor := getCursor(handler)
			beforeSnapshot, err := memory.LoadSnapshot()
			if err != nil {
				t.Fatalf("load snapshot before rejection: %v", err)
			}

			rejected := postChunk("reused-op", tt.badChunk)
			if rejected.Code != http.StatusConflict {
				t.Fatalf("rejected chunk: status %d: %s", rejected.Code, rejected.Body.String())
			}
			var apiErr httpapi.Error
			if err := json.Unmarshal(rejected.Body.Bytes(), &apiErr); err != nil {
				t.Fatalf("decode rejection: %v", err)
			}
			if apiErr.Code != tt.wantCode {
				t.Fatalf("rejection code = %q, want %q", apiErr.Code, tt.wantCode)
			}
			if after := getCursor(handler); !reflect.DeepEqual(after, beforeCursor) {
				t.Fatalf("cursor changed after rejection: before %+v, after %+v", beforeCursor, after)
			}
			afterSnapshot, err := memory.LoadSnapshot()
			if err != nil {
				t.Fatalf("load snapshot after rejection: %v", err)
			}
			if !reflect.DeepEqual(afterSnapshot, beforeSnapshot) {
				t.Fatal("durable snapshot changed after rejected chunk")
			}

			valid := assay.CurveChunk{Well: well, Generation: 1, Seq: 2, CycleStart: 3, CycleEnd: 4,
				Fluorescence: []int64{201, 202}}
			accepted := postChunk("reused-op", valid)
			if accepted.Code != http.StatusCreated {
				t.Fatalf("valid reuse of rejected operation id: status %d: %s", accepted.Code, accepted.Body.String())
			}
			retried := postChunk("reused-op", valid)
			if retried.Code != http.StatusCreated {
				t.Fatalf("idempotent retry: status %d: %s", retried.Code, retried.Body.String())
			}
			var acceptedBody, retriedBody struct {
				Txn string `json:"txn"`
			}
			if err := json.Unmarshal(accepted.Body.Bytes(), &acceptedBody); err != nil {
				t.Fatalf("decode accepted response: %v", err)
			}
			if err := json.Unmarshal(retried.Body.Bytes(), &retriedBody); err != nil {
				t.Fatalf("decode retry response: %v", err)
			}
			if acceptedBody.Txn == "" || retriedBody.Txn != acceptedBody.Txn {
				t.Fatalf("idempotent txn changed: accepted %q, retry %q", acceptedBody.Txn, retriedBody.Txn)
			}

			wantCursor := assay.Cursor{RunID: "run", Well: well, Generation: 1, NextSeq: 3, Complete: false}
			if got := getCursor(handler); !reflect.DeepEqual(got, wantCursor) {
				t.Fatalf("cursor after valid continuation = %+v, want %+v", got, wantCursor)
			}
			restored, err := service.NewEngine(memory)
			if err != nil {
				t.Fatalf("restore from subsequent snapshot: %v", err)
			}
			if got := getCursor(httpapi.NewServer(restored).Handler()); !reflect.DeepEqual(got, wantCursor) {
				t.Fatalf("restored cursor = %+v, want %+v", got, wantCursor)
			}
		})
	}
}
