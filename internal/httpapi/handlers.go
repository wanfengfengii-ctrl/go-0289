package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"edna-contamination-verdict/internal/analysis"
	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/service"
	"edna-contamination-verdict/internal/store"
	"edna-contamination-verdict/internal/verdict"
)

// mapError translates a domain error into a stable {code,message,reasons}.
func mapError(err error) *Error {
	switch {
	case errors.Is(err, protocol.ErrMissingReplicates):
		return NewError(CodeLayoutMissing, "sample missing replicate wells")
	case errors.Is(err, protocol.ErrDuplicateTube):
		return NewError(CodeTubeDuplicate, "duplicate tube code")
	case errors.Is(err, protocol.ErrControlShortage):
		return NewError(CodeControlShortage, "control shortage")
	case errors.Is(err, protocol.ErrStaleDigest):
		return NewError(CodeStaleDigest, "stale protocol digest")
	case errors.Is(err, protocol.ErrInvalidEdge):
		return NewError(CodeInvalidEdge, "invalid propagation edge")
	case errors.Is(err, protocol.ErrWellOutOfBounds):
		return NewError(CodeWellOutOfBounds, "well out of plate bounds")
	case errors.Is(err, protocol.ErrWellReused):
		return NewError(CodeWellReused, "well assigned more than once")
	case errors.Is(err, protocol.ErrInvalidScale):
		return NewError(CodeInvalidScale, "invalid scale factor")
	case errors.Is(err, protocol.ErrInvalidBaseline):
		return NewError(CodeInvalidBaseline, "invalid baseline range")
	case errors.Is(err, protocol.ErrInvalidWindow):
		return NewError(CodeInvalidWindow, "invalid crossing window")
	case errors.Is(err, protocol.ErrInvalidPositiveRange):
		return NewError(CodeInvalidRange, "invalid positive control range")
	case errors.Is(err, protocol.ErrUnknownProtocol):
		return NewError(CodeUnknownProtocol, "unknown protocol")
	case errors.Is(err, assay.ErrChunkOrder):
		return NewError(CodeCurveIncomplete, "chunk sequence must start at 1")
	case errors.Is(err, assay.ErrChunkGap):
		return NewError(CodeChunkGap, "chunk sequence gap")
	case errors.Is(err, assay.ErrChunkOverlap):
		return NewError(CodeChunkOverlap, "chunk cycle overlap")
	case errors.Is(err, assay.ErrStaleGeneration):
		return NewError(CodeGenerationStale, "stale generation")
	case errors.Is(err, assay.ErrRunMismatch):
		return NewError(CodeRunMismatch, "run mismatch")
	case errors.Is(err, assay.ErrCurveIncomplete):
		return NewError(CodeCurveIncomplete, "curve incomplete")
	case errors.Is(err, analysis.ErrDivideByZero):
		return NewError(CodeDivideByZero, "division by zero")
	case errors.Is(err, analysis.ErrOverflow):
		return NewError(CodeFixedOverflow, "fixed-point overflow")
	case errors.Is(err, analysis.ErrInvalidRange):
		return NewError(CodeInvalidRange, "invalid interpolation range")
	case errors.Is(err, analysis.ErrInvalidScale):
		return NewError(CodeInvalidScale, "invalid scale factor")
	case errors.Is(err, verdict.ErrRetestConflict):
		return NewError(CodeRetestConflict, "retest conflict")
	case errors.Is(err, verdict.ErrReviewerDuplicate):
		return NewError(CodeReviewerDuplicate, "duplicate reviewer")
	case errors.Is(err, verdict.ErrReviewerUnqualified):
		return NewError(CodeReviewerUnqualified, "reviewer not qualified")
	case errors.Is(err, verdict.ErrDigestMismatch):
		return NewError(CodeDigestMismatch, "evidence digest mismatch")
	case errors.Is(err, verdict.ErrDecisionExists):
		return NewError(CodeDecisionExists, "final decision already exists")
	case errors.Is(err, verdict.ErrNotReady):
		return NewError(CodeNotReady, "batch not ready")
	case errors.Is(err, service.ErrBatchNotFound):
		return NewError(CodeBatchNotFound, "batch not found")
	case errors.Is(err, service.ErrBatchLocked):
		return NewError(CodeBatchLocked, "batch already locked")
	case errors.Is(err, service.ErrBatchNotLocked):
		return NewError(CodeBatchNotLocked, "batch not locked")
	case errors.Is(err, service.ErrRunNotFound):
		return NewError(CodeRunNotFound, "run not found")
	case errors.Is(err, service.ErrWellNotFound):
		return NewError(CodeWellNotFound, "well not found")
	case errors.Is(err, service.ErrNotSampleWell):
		return NewError(CodeNotSampleWell, "well is not a sample well")
	case errors.Is(err, service.ErrUnknownTube):
		return NewError(CodeUnknownTube, "unknown tube code")
	case errors.Is(err, service.ErrLoadConflict):
		return NewError(CodeLoadConflict, "tube/well load conflict")
	case errors.Is(err, service.ErrIdempotentConflict):
		return NewError(CodeIdempotentConflict, "idempotent operation content conflict")
	case errors.Is(err, service.ErrInsufficientReview):
		return NewError(CodeInsufficientReview, "insufficient review quorum")
	case errors.Is(err, service.ErrNotComplete):
		return NewError(CodeCurveIncomplete, "curve not complete")
	case errors.Is(err, store.ErrCorruptRecord):
		return NewError("store_corrupt", "corrupt committed record")
	default:
		return NewError("internal", "internal error")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func (s *Server) handleCreateProtocol(w http.ResponseWriter, r *http.Request) {
	var spec protocol.ProtocolSpec
	if !decodeJSON(w, r, &spec) {
		return
	}
	snap, err := s.engine.CreateProtocol(spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": spec.ID, "snapshot": snap})
}

func (s *Server) handleLockBatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		ProtocolID     string `json:"protocol_id"`
		ExpectedDigest string `json:"expected_digest"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	lr, err := s.engine.LockBatch(id, body.ProtocolID, body.ExpectedDigest)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          id,
		"generation":  1,
		"digest":      lr.Snapshot.Digest,
		"snapshot":    lr.Snapshot,
		"assignments": lr.Assignments,
	})
}

func (s *Server) handleLoad(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req assay.LoadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.OperationID == "" {
		req.OperationID = r.Header.Get("Operation-Id")
	}
	txn, err := s.engine.Load(id, req)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"txn": txn, "tube_code": req.TubeCode, "well": req.Well})
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BatchID string           `json:"batch_id"`
		RunID   string           `json:"run_id"`
		Well    protocol.WellRef `json:"well"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	run, err := s.engine.CreateRun(body.BatchID, body.RunID, body.Well)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) handleIngestChunk(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	var body struct {
		BatchID string           `json:"batch_id"`
		Chunk   assay.CurveChunk `json:"chunk"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Chunk.RunID = runID
	opID := r.Header.Get("Operation-Id")
	txn, err := s.engine.IngestChunk(body.BatchID, opID, body.Chunk)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"txn": txn, "seq": body.Chunk.Seq})
}

func (s *Server) handleCursor(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	cur, err := s.engine.GetCursor(runID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, cur)
}

func (s *Server) handleInterpret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Well protocol.WellRef `json:"well"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	interp, err := s.engine.Interpret(id, body.Well)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, interp)
}

func (s *Server) handleWells(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wells, err := s.engine.GetWells(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"wells": wells})
}

func (s *Server) handleEvaluate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	set, err := s.engine.EvaluateContamination(id)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *Server) handleRetest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	gen, err := s.engine.CreateRetest(id)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, gen)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var review verdict.Review
	if !decodeJSON(w, r, &review) {
		return
	}
	if err := s.engine.SubmitReview(id, review); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"reviewer_id": review.ReviewerID})
}

func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Type verdict.FinalType `json:"type"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	decision, err := s.engine.Decide(id, verdict.FinalDecision{Type: body.Type})
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, decision)
}

func (s *Server) handleGetBatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := s.engine.GetBatch(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleListBatches(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"batches": s.engine.ListBatches()})
}
