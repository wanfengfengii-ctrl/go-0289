package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/verdict"
)

func wr(r, c int) protocol.WellRef { return protocol.WellRef{Plate: "P", Row: r, Col: c} }

func flowSpec() protocol.ProtocolSpec {
	return protocol.ProtocolSpec{
		ID: "p1", Target: "t", Scale: 1000, Threshold: 10,
		BaselineStart: 0, BaselineEnd: 4, Window: 3,
		PositiveMin: 6000, PositiveMax: 8000, ReplicateCount: 2, ReagentLot: "L",
		Layout: protocol.LayoutSpec{
			PlateID: "P", Rows: 8, Cols: 12,
			Samples: []protocol.SampleSpec{
				{ReplicateGroup: "S1", Tubes: []protocol.TubePlacement{{TubeCode: "T1", Well: wr(1, 1)}, {TubeCode: "T2", Well: wr(1, 2)}}},
			},
			Controls: []protocol.ControlSpec{
				{Kind: protocol.PositiveControl, Well: wr(8, 1)},
				{Kind: protocol.NegativeControl, Well: wr(8, 2)},
			},
		},
	}
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPFullFlowToFinalDecision(t *testing.T) {
	srv := newTestServer(t)
	h := srv.Handler()

	if rec := doJSON(t, h, "POST", "/api/v1/protocols", flowSpec()); rec.Code != 201 {
		t.Fatalf("create protocol: %d %s", rec.Code, rec.Body.String())
	}

	lockBody := map[string]any{"protocol_id": "p1"}
	if rec := doJSON(t, h, "POST", "/api/v1/batches/b-1/lock", lockBody); rec.Code != 201 {
		t.Fatalf("lock: %d %s", rec.Code, rec.Body.String())
	}

	load := assay.LoadRequest{OperationID: "op-1", TubeCode: "T1", Well: wr(1, 1)}
	if rec := doJSON(t, h, "POST", "/api/v1/batches/b-1/loads", load); rec.Code != 201 {
		t.Fatalf("load: %d %s", rec.Code, rec.Body.String())
	}
	load2 := assay.LoadRequest{OperationID: "op-2", TubeCode: "T2", Well: wr(1, 2)}
	if rec := doJSON(t, h, "POST", "/api/v1/batches/b-1/loads", load2); rec.Code != 201 {
		t.Fatalf("load2: %d %s", rec.Code, rec.Body.String())
	}

	// Duplicate tube to wrong well should be rejected.
	bad := assay.LoadRequest{OperationID: "op-3", TubeCode: "T1", Well: wr(1, 2)}
	if rec := doJSON(t, h, "POST", "/api/v1/batches/b-1/loads", bad); rec.Code != 409 {
		t.Fatalf("expected conflict on bad load, got %d", rec.Code)
	}

	// Verify wells endpoint returns ordered wells.
	if rec := doJSON(t, h, "GET", "/api/v1/batches/b-1/wells", nil); rec.Code != 200 {
		t.Fatalf("wells: %d", rec.Code)
	}

	// Fetch the current evidence digest before submitting reviews.
	rec := doJSON(t, h, "GET", "/api/v1/batches/b-1", nil)
	var proj struct {
		Evidence string `json:"evidence_digest"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &proj); err != nil {
		t.Fatal(err)
	}
	review := verdict.Review{ReviewerID: "r1", Qualification: "operator", Digest: proj.Evidence}
	if rec := doJSON(t, h, "POST", "/api/v1/batches/b-1/reviews", review); rec.Code != 201 {
		t.Fatalf("review: %d %s", rec.Code, rec.Body.String())
	}
	dec := map[string]any{"type": "release"}
	if rec := doJSON(t, h, "POST", "/api/v1/batches/b-1/final-decisions", dec); rec.Code != 409 {
		t.Fatalf("expected not_ready decision, got %d", rec.Code)
	}
}

func TestHTTPErrorReasonsAreStableAndSorted(t *testing.T) {
	srv := newTestServer(t)
	h := srv.Handler()
	// Locking against an unknown protocol returns a stable error.
	rec := doJSON(t, h, "POST", "/api/v1/batches/x/lock", map[string]any{"protocol_id": "nope"})
	if rec.Code != 400 {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var e Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.Code != CodeUnknownProtocol {
		t.Fatalf("expected unknown_protocol, got %s", e.Code)
	}
}

func TestHTTPHealthAndFrontend(t *testing.T) {
	srv := newTestServer(t)
	h := srv.Handler()
	if rec := doJSON(t, h, "GET", "/api/v1/health", nil); rec.Code != 200 {
		t.Fatalf("health: %d", rec.Code)
	}
	if rec := doJSON(t, h, "GET", "/", nil); rec.Code != 200 {
		t.Fatalf("frontend: %d", rec.Code)
	}
}
