package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/protocol"
)

func TestModel_RunIDRemainsBoundToItsOriginalCurveOwner(t *testing.T) {
	cases := []struct {
		name                string
		secondWell          protocol.WellRef
		wantCreateStatus    int
		wantErrorCode       string
		wantSecondInterpret int
	}{
		{
			name:                "same owner is an idempotent retry",
			secondWell:          wr(1, 1),
			wantCreateStatus:    http.StatusCreated,
			wantSecondInterpret: http.StatusOK,
		},
		{
			name:                "different owner cannot steal uploaded curve",
			secondWell:          wr(1, 2),
			wantCreateStatus:    http.StatusBadRequest,
			wantErrorCode:       "run_reused",
			wantSecondInterpret: http.StatusConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			h := srv.Handler()
			if rec := doJSON(t, h, http.MethodPost, "/api/v1/protocols", flowSpec()); rec.Code != http.StatusCreated {
				t.Fatalf("create protocol: status %d body %s", rec.Code, rec.Body.String())
			}
			if rec := doJSON(t, h, http.MethodPost, "/api/v1/batches/b-1/lock", map[string]any{"protocol_id": "p1"}); rec.Code != http.StatusCreated {
				t.Fatalf("lock batch: status %d body %s", rec.Code, rec.Body.String())
			}

			owner := wr(1, 1)
			create := map[string]any{"batch_id": "b-1", "run_id": "dup", "well": owner}
			if rec := doJSON(t, h, http.MethodPost, "/api/v1/runs", create); rec.Code != http.StatusCreated {
				t.Fatalf("create unique run: status %d body %s", rec.Code, rec.Body.String())
			}
			chunk := assay.CurveChunk{
				Well: owner, Generation: 1, Seq: 1, CycleStart: 1, CycleEnd: 9,
				Fluorescence: []int64{0, 0, 0, 0, 0, 5, 15, 20, 20}, Complete: true,
			}
			chunkBody := map[string]any{"batch_id": "b-1", "chunk": chunk}
			for attempt := 0; attempt < 2; attempt++ {
				if rec := doJSON(t, h, http.MethodPost, "/api/v1/runs/dup/chunks", chunkBody); rec.Code != http.StatusCreated {
					t.Fatalf("idempotent chunk upload %d: status %d body %s", attempt+1, rec.Code, rec.Body.String())
				}
			}

			beforeInterpret := doJSON(t, h, http.MethodPost, "/api/v1/batches/b-1/interpret", map[string]any{"well": owner})
			if beforeInterpret.Code != http.StatusOK {
				t.Fatalf("interpret original owner before retry: status %d body %s", beforeInterpret.Code, beforeInterpret.Body.String())
			}
			var beforeResult struct {
				Positive bool `json:"positive"`
			}
			if err := json.Unmarshal(beforeInterpret.Body.Bytes(), &beforeResult); err != nil || !beforeResult.Positive {
				t.Fatalf("expected uploaded curve to be positive, result=%+v err=%v", beforeResult, err)
			}
			beforeBatch := doJSON(t, h, http.MethodGet, "/api/v1/batches/b-1", nil)
			var beforeProjection struct {
				Evidence string `json:"evidence_digest"`
			}
			if err := json.Unmarshal(beforeBatch.Body.Bytes(), &beforeProjection); err != nil {
				t.Fatalf("decode projection before retry: %v", err)
			}

			create["well"] = tc.secondWell
			reused := doJSON(t, h, http.MethodPost, "/api/v1/runs", create)
			if reused.Code != tc.wantCreateStatus {
				t.Errorf("reuse status = %d, want %d; body %s", reused.Code, tc.wantCreateStatus, reused.Body.String())
			}
			if tc.wantErrorCode != "" {
				var apiErr Error
				if err := json.Unmarshal(reused.Body.Bytes(), &apiErr); err != nil || apiErr.Code != tc.wantErrorCode {
					t.Errorf("reuse error = %+v, want code %q (decode err %v)", apiErr, tc.wantErrorCode, err)
				}
			}

			cursorRec := doJSON(t, h, http.MethodGet, "/api/v1/runs/dup/cursor", nil)
			var cursor assay.Cursor
			if err := json.Unmarshal(cursorRec.Body.Bytes(), &cursor); err != nil {
				t.Fatalf("decode cursor: %v", err)
			}
			if cursorRec.Code != http.StatusOK || cursor.Well != owner || cursor.NextSeq != 2 || !cursor.Complete {
				t.Errorf("cursor was changed after run reuse: status=%d cursor=%+v", cursorRec.Code, cursor)
			}

			afterOwner := doJSON(t, h, http.MethodPost, "/api/v1/batches/b-1/interpret", map[string]any{"well": owner})
			var ownerResult struct {
				Positive bool `json:"positive"`
			}
			_ = json.Unmarshal(afterOwner.Body.Bytes(), &ownerResult)
			if afterOwner.Code != http.StatusOK || !ownerResult.Positive {
				t.Errorf("original owner lost its positive curve: status=%d body=%s", afterOwner.Code, afterOwner.Body.String())
			}

			second := doJSON(t, h, http.MethodPost, "/api/v1/batches/b-1/interpret", map[string]any{"well": tc.secondWell})
			if second.Code != tc.wantSecondInterpret {
				t.Errorf("second owner interpretation status = %d, want %d; body %s", second.Code, tc.wantSecondInterpret, second.Body.String())
			}
			if tc.secondWell != owner && second.Code == http.StatusOK {
				var stolen struct {
					Positive bool `json:"positive"`
				}
				_ = json.Unmarshal(second.Body.Bytes(), &stolen)
				t.Errorf("different owner interpreted the original curve (positive=%v)", stolen.Positive)
			}

			afterBatch := doJSON(t, h, http.MethodGet, "/api/v1/batches/b-1", nil)
			var afterProjection struct {
				Evidence string `json:"evidence_digest"`
			}
			if err := json.Unmarshal(afterBatch.Body.Bytes(), &afterProjection); err != nil {
				t.Fatalf("decode projection after retry: %v", err)
			}
			if afterProjection.Evidence != beforeProjection.Evidence {
				t.Errorf("interpretation projection changed: before=%s after=%s", beforeProjection.Evidence, afterProjection.Evidence)
			}
		})
	}
}
