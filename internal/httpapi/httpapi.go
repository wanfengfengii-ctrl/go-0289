// Package httpapi exposes the versioned JSON API and the embedded frontend.
package httpapi

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sort"

	"edna-contamination-verdict/internal/service"
)

// ComponentName is the stable identity of this component.
const ComponentName = "http-api-and-embedded-frontend"

//go:embed index.html
var indexHTML []byte

// Error is the stable API error structure {code, message, reasons[]}.
type Error struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Reasons []string `json:"reasons"`
}

// Stable error codes covering the documented failure boundaries.
const (
	CodeLayoutMissing       = "layout_missing"
	CodeTubeDuplicate       = "tube_duplicate"
	CodeControlShortage     = "control_shortage"
	CodeStaleDigest         = "stale_digest"
	CodeChunkGap            = "chunk_gap"
	CodeChunkOverlap        = "chunk_overlap"
	CodeIdempotentConflict  = "idempotent_conflict"
	CodeFixedOverflow       = "fixed_overflow"
	CodeCurveIncomplete     = "curve_incomplete"
	CodeGenerationStale     = "generation_stale"
	CodeRetestConflict      = "retest_conflict"
	CodeReviewerDuplicate   = "reviewer_duplicate"
	CodeDecisionExists      = "decision_exists"
	CodeInvalidEdge         = "invalid_edge"
	CodeWellOutOfBounds     = "well_out_of_bounds"
	CodeWellReused          = "well_reused"
	CodeInvalidScale        = "invalid_scale"
	CodeInvalidBaseline     = "invalid_baseline"
	CodeInvalidWindow       = "invalid_window"
	CodeInvalidRange        = "invalid_range"
	CodeDivideByZero        = "divide_by_zero"
	CodeRunMismatch         = "run_mismatch"
	CodeRunNotFound         = "run_not_found"
	CodeBatchNotFound       = "batch_not_found"
	CodeBatchLocked         = "batch_locked"
	CodeBatchNotLocked      = "batch_not_locked"
	CodeWellNotFound        = "well_not_found"
	CodeNotSampleWell       = "not_sample_well"
	CodeUnknownTube         = "unknown_tube"
	CodeLoadConflict        = "load_conflict"
	CodeUnknownProtocol     = "unknown_protocol"
	CodeReviewerUnqualified = "reviewer_unqualified"
	CodeDigestMismatch      = "digest_mismatch"
	CodeInsufficientReview  = "insufficient_review"
	CodeNotReady            = "not_ready"
	CodeMethodNotAllowed    = "method_not_allowed"
	CodeBadRequest          = "bad_request"
)

// NewError builds a stable error with a sorted, de-duplicated reason list.
func NewError(code, message string, reasons ...string) *Error {
	seen := map[string]bool{}
	rs := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		rs = append(rs, r)
	}
	sort.Strings(rs)
	return &Error{Code: code, Message: message, Reasons: rs}
}

// Error implements the error interface.
func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

// Server exposes the documented HTTP API and the embedded frontend.
type Server struct {
	engine   *service.Engine
	frontend string
}

// Option configures the Server.
type Option func(*Server)

// WithFrontendDir serves frontend assets from a directory instead of the
// embedded page.
func WithFrontendDir(dir string) Option {
	return func(s *Server) { s.frontend = dir }
}

// NewServer constructs a Server backed by the given engine.
func NewServer(engine *service.Engine, opts ...Option) *Server {
	s := &Server{engine: engine}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Handler builds the routing table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("POST /api/v1/protocols", s.handleCreateProtocol)
	mux.HandleFunc("POST /api/v1/batches/{id}/lock", s.handleLockBatch)
	mux.HandleFunc("POST /api/v1/batches/{id}/loads", s.handleLoad)
	mux.HandleFunc("POST /api/v1/runs", s.handleCreateRun)
	mux.HandleFunc("POST /api/v1/runs/{id}/chunks", s.handleIngestChunk)
	mux.HandleFunc("GET /api/v1/runs/{id}/cursor", s.handleCursor)
	mux.HandleFunc("POST /api/v1/batches/{id}/interpret", s.handleInterpret)
	mux.HandleFunc("GET /api/v1/batches/{id}/wells", s.handleWells)
	mux.HandleFunc("POST /api/v1/batches/{id}/contamination/evaluate", s.handleEvaluate)
	mux.HandleFunc("POST /api/v1/batches/{id}/retests", s.handleRetest)
	mux.HandleFunc("POST /api/v1/batches/{id}/reviews", s.handleReview)
	mux.HandleFunc("POST /api/v1/batches/{id}/final-decisions", s.handleDecide)
	mux.HandleFunc("GET /api/v1/batches/{id}", s.handleGetBatch)
	mux.HandleFunc("GET /api/v1/batches", s.handleListBatches)
	mux.HandleFunc("/", s.handleFrontend)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"components": []string{
			"experiment-rules-and-plate-lock",
			"sample-loading-and-curve-ingest",
			"fixed-point-interpretation-and-quality-gating",
			"contamination-closure-and-retest-generation",
			"event-store-and-final-arbitration",
			service.ComponentName,
			ComponentName,
		},
		"service": service.ComponentName,
	})
}

func (s *Server) handleFrontend(w http.ResponseWriter, r *http.Request) {
	if s.frontend != "" {
		http.FileServer(http.Dir(s.frontend)).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, mapError(err))
}
