package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"edna-contamination-verdict/internal/analysis"
	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/verdict"
)

// Event payload shapes (mirror the anonymous structs in applyEvent).
type protocolEvent struct {
	Spec protocol.ProtocolSpec `json:"spec"`
}
type lockEvent struct {
	BatchID    string `json:"batch_id"`
	ProtocolID string `json:"protocol_id"`
}
type loadEvent struct {
	BatchID string            `json:"batch_id"`
	Request assay.LoadRequest `json:"request"`
}
type runEvent struct {
	BatchID string           `json:"batch_id"`
	RunID   string           `json:"run_id"`
	Well    protocol.WellRef `json:"well"`
}
type chunkEvent struct {
	BatchID     string           `json:"batch_id"`
	OperationID string           `json:"operation_id"`
	Chunk       assay.CurveChunk `json:"chunk"`
}
type interpretEvent struct {
	BatchID string           `json:"batch_id"`
	Well    protocol.WellRef `json:"well"`
}
type evaluateEvent struct {
	BatchID string `json:"batch_id"`
}
type retestEvent struct {
	BatchID      string `json:"batch_id"`
	SourceDigest string `json:"source_digest"`
	Generation   int    `json:"generation"`
}
type reviewEvent struct {
	BatchID string         `json:"batch_id"`
	Review  verdict.Review `json:"review"`
}
type decideEvent struct {
	BatchID  string                `json:"batch_id"`
	Decision verdict.FinalDecision `json:"decision"`
}

func txnString(seq int64) string { return strconv.FormatInt(seq, 10) }

func loadDigest(req assay.LoadRequest) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s", req.TubeCode, req.Well.Key())
	return hex.EncodeToString(h.Sum(nil))
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// CreateProtocol validates and records a new protocol, returning its preview
// snapshot (including the digest) for later lock and stale-digest checks.
func (e *Engine) CreateProtocol(spec protocol.ProtocolSpec) (protocol.ProtocolSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	lr, err := protocol.Lock(spec)
	if err != nil {
		return protocol.ProtocolSnapshot{}, err
	}
	if _, err := e.transact(evProtocolCreate, mustMarshal(protocolEvent{Spec: spec}), func(int64) error {
		return e.createProtocolApply(spec)
	}); err != nil {
		return protocol.ProtocolSnapshot{}, err
	}
	return lr.Snapshot, nil
}

// LockBatch locks a batch to a protocol, returning the locked snapshot and
// resolved wells. expectedDigest may be empty to skip the stale-digest check.
func (e *Engine) LockBatch(batchID, protocolID, expectedDigest string) (protocol.LockResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	spec, ok := e.protocols[protocolID]
	if !ok {
		return protocol.LockResult{}, protocol.ErrUnknownProtocol
	}
	lr, err := protocol.Lock(spec)
	if err != nil {
		return protocol.LockResult{}, err
	}
	if expectedDigest != "" && expectedDigest != lr.Snapshot.Digest {
		return protocol.LockResult{}, protocol.ErrStaleDigest
	}
	if _, exists := e.batches[batchID]; exists {
		return protocol.LockResult{}, ErrBatchLocked
	}
	if _, err := e.transact(evBatchLock, mustMarshal(lockEvent{BatchID: batchID, ProtocolID: protocolID}), func(int64) error {
		return e.lockBatchApply(batchID, protocolID)
	}); err != nil {
		return protocol.LockResult{}, err
	}
	return lr, nil
}

// Load scans a sample tube into its designated well (idempotent by OperationID).
func (e *Engine) Load(batchID string, req assay.LoadRequest) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.batches[batchID]
	if b == nil {
		return "", ErrBatchNotFound
	}
	if rec, ok := b.LoadOps[req.OperationID]; ok {
		if rec.Digest != loadDigest(req) {
			return "", ErrIdempotentConflict
		}
		return txnString(rec.Txn), nil
	}
	seq, err := e.transact(evBatchLoad, mustMarshal(loadEvent{BatchID: batchID, Request: req}), func(seq int64) error {
		_, err := e.loadApply(batchID, req, seq)
		return err
	})
	if err != nil {
		return "", err
	}
	return txnString(seq), nil
}

// CreateRun creates an instrument run for a well at the current generation.
func (e *Engine) CreateRun(batchID, runID string, well protocol.WellRef) (assay.InstrumentRun, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	seq, err := e.transact(evRunCreate, mustMarshal(runEvent{BatchID: batchID, RunID: runID, Well: well}), func(int64) error {
		return e.createRunApply(batchID, runID, well)
	})
	if err != nil {
		return assay.InstrumentRun{}, err
	}
	_ = seq
	return e.batches[batchID].Runs[runID], nil
}

// IngestChunk uploads one curve chunk (idempotent by OperationID).
func (e *Engine) IngestChunk(batchID, opID string, chunk assay.CurveChunk) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.batches[batchID]
	if b == nil {
		return "", ErrBatchNotFound
	}
	if rec, ok := b.ChunkOps[opID]; ok {
		if rec.Digest != assay.ChunkContentDigest(chunk) {
			return "", ErrIdempotentConflict
		}
		return txnString(rec.Txn), nil
	}
	seq, err := e.transact(evChunkIngest, mustMarshal(chunkEvent{BatchID: batchID, OperationID: opID, Chunk: chunk}), func(seq int64) error {
		_, err := e.ingestChunkApply(batchID, opID, chunk, seq)
		return err
	})
	if err != nil {
		return "", err
	}
	return txnString(seq), nil
}

// Interpret runs the fixed-point pipeline for one well and returns the result.
func (e *Engine) Interpret(batchID string, well protocol.WellRef) (analysis.Interpretation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.transact(evInterpret, mustMarshal(interpretEvent{BatchID: batchID, Well: well}), func(int64) error {
		return e.interpretApply(batchID, well)
	}); err != nil {
		return analysis.Interpretation{}, err
	}
	return e.batches[batchID].Interps[well.Key()], nil
}

// EvaluateContamination computes and stores the contamination closure.
func (e *Engine) EvaluateContamination(batchID string) (verdict.ContaminationSet, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.transact(evContaminate, mustMarshal(evaluateEvent{BatchID: batchID}), func(int64) error {
		return e.evaluateApply(batchID)
	}); err != nil {
		return verdict.ContaminationSet{}, err
	}
	return *e.batches[batchID].Contam, nil
}

// CreateRetest creates the next generation re-opening the contamination
// closure, using compare-and-swap on the current generation and defect digest.
func (e *Engine) CreateRetest(batchID string) (verdict.Generation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.batches[batchID]
	if b == nil {
		return verdict.Generation{}, ErrBatchNotFound
	}
	if b.Contam == nil {
		return verdict.Generation{}, verdict.ErrNotReady
	}
	if existing, ok := verdict.FindRetest(b.Retests, b.Generation, b.Contam.SourceDigest); ok {
		return existing, nil
	}
	next := b.Generation + 1
	if _, err := e.transact(evRetestCreate, mustMarshal(retestEvent{BatchID: batchID, SourceDigest: b.Contam.SourceDigest, Generation: next}), func(int64) error {
		return e.retestApply(batchID, b.Contam.SourceDigest, next)
	}); err != nil {
		return verdict.Generation{}, err
	}
	g, _ := verdict.FindRetest(e.batches[batchID].Retests, next, b.Contam.SourceDigest)
	return g, nil
}

// SubmitReview records a qualified review over the current evidence digest.
func (e *Engine) SubmitReview(batchID string, review verdict.Review) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, err := e.transact(evReviewSubmit, mustMarshal(reviewEvent{BatchID: batchID, Review: review}), func(int64) error {
		return e.reviewApply(batchID, review)
	})
	return err
}

// Decide persists the terminal decision with a single-writer barrier.
func (e *Engine) Decide(batchID string, decision verdict.FinalDecision) (verdict.FinalDecision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.transact(evFinalDecide, mustMarshal(decideEvent{BatchID: batchID, Decision: decision}), func(int64) error {
		return e.decideApply(batchID, decision)
	}); err != nil {
		return verdict.FinalDecision{}, err
	}
	return *e.batches[batchID].Final, nil
}

// GetCursor returns the deterministic upload cursor for a run.
func (e *Engine) GetCursor(runID string) (assay.Cursor, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, b := range e.batches {
		if run, ok := b.Runs[runID]; ok {
			return assay.ComputeCursor(runID, run.Well, run.Generation, b.Chunks[runID]), nil
		}
	}
	return assay.Cursor{}, ErrRunNotFound
}

// BatchProjection is the read-side projection returned by GetBatch.
type BatchProjection struct {
	ID         string                    `json:"id"`
	Target     string                    `json:"target"`
	Generation int                       `json:"generation"`
	Locked     bool                      `json:"locked"`
	Digest     string                    `json:"digest"`
	Wells      []assay.Well              `json:"wells"`
	Evidence   string                    `json:"evidence_digest"`
	Contam     *verdict.ContaminationSet `json:"contamination,omitempty"`
	Retests    []verdict.Generation      `json:"retests"`
	Reviews    []verdict.Review          `json:"reviews"`
	Final      *verdict.FinalDecision    `json:"final,omitempty"`
}

// GetBatch returns the current projection of a batch.
func (e *Engine) GetBatch(batchID string) (BatchProjection, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.batches[batchID]
	if b == nil {
		return BatchProjection{}, ErrBatchNotFound
	}
	return BatchProjection{
		ID:         b.ID,
		Target:     b.Snapshot.Target,
		Generation: b.Generation,
		Locked:     b.Locked,
		Digest:     b.Snapshot.Digest,
		Wells:      append([]assay.Well(nil), b.Wells...),
		Evidence:   b.evidenceDigest(),
		Contam:     b.Contam,
		Retests:    append([]verdict.Generation(nil), b.Retests...),
		Reviews:    append([]verdict.Review(nil), b.Reviews...),
		Final:      b.Final,
	}, nil
}

// GetWells returns the batch wells ordered by plate, row and column.
func (e *Engine) GetWells(batchID string) ([]assay.Well, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b := e.batches[batchID]
	if b == nil {
		return nil, ErrBatchNotFound
	}
	out := append([]assay.Well(nil), b.Wells...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ref.Plate != out[j].Ref.Plate {
			return out[i].Ref.Plate < out[j].Ref.Plate
		}
		if out[i].Ref.Row != out[j].Ref.Row {
			return out[i].Ref.Row < out[j].Ref.Row
		}
		return out[i].Ref.Col < out[j].Ref.Col
	})
	return out, nil
}

// ListBatches returns the ids of all locked batches in stable order.
func (e *Engine) ListBatches() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.batches))
	for id := range e.batches {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
