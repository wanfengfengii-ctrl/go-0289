package service

import (
	"errors"

	"edna-contamination-verdict/internal/analysis"
	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/verdict"
)

// Domain-level sentinel errors surfaced by the engine.
var (
	ErrWellNotFound       = errors.New("well not found")
	ErrNotSampleWell      = errors.New("well is not a sample well")
	ErrUnknownTube        = errors.New("unknown tube code")
	ErrLoadConflict       = errors.New("tube/well load conflict")
	ErrIdempotentConflict = errors.New("idempotent operation content conflict")
	ErrInsufficientReview = errors.New("insufficient review quorum")
	ErrNotComplete        = errors.New("curve not complete")
)

// createProtocolApply validates and records a protocol definition.
func (e *Engine) createProtocolApply(spec protocol.ProtocolSpec) error {
	if _, err := protocol.Lock(spec); err != nil {
		return err
	}
	e.protocols[spec.ID] = spec
	return nil
}

// lockBatchApply locks a batch to an already-created protocol.
func (e *Engine) lockBatchApply(batchID, protocolID string) error {
	spec, ok := e.protocols[protocolID]
	if !ok {
		return protocol.ErrUnknownProtocol
	}
	if _, exists := e.batches[batchID]; exists {
		return ErrBatchLocked
	}
	lr, err := protocol.Lock(spec)
	if err != nil {
		return err
	}
	e.batches[batchID] = newBatchState(batchID, lr.Snapshot, lr.Assignments)
	return nil
}

// loadApply records a physical tube scan into its designated well.
func (e *Engine) loadApply(batchID string, req assay.LoadRequest, seq int64) (string, error) {
	b := e.batches[batchID]
	if b == nil {
		return "", ErrBatchNotFound
	}
	if !b.Locked {
		return "", ErrBatchNotLocked
	}
	w, ok := b.wellByKey(req.Well.Key())
	if !ok {
		return "", ErrWellNotFound
	}
	if w.Type != assay.WellSample {
		return "", ErrNotSampleWell
	}
	expected, ok := b.TubeWell[req.TubeCode]
	if !ok {
		return "", ErrUnknownTube
	}
	if expected != req.Well.Key() {
		return "", ErrLoadConflict
	}
	b.LoadedTubes[req.TubeCode] = req.Well.Key()
	b.setWellTube(req.Well.Key(), req.TubeCode)
	b.LoadOps[req.OperationID] = loadRecord{Txn: seq, Digest: loadDigest(req)}
	return txnString(seq), nil
}

// createRunApply creates an instrument run for a well at the current generation.
func (e *Engine) createRunApply(batchID, runID string, well protocol.WellRef) error {
	b := e.batches[batchID]
	if b == nil {
		return ErrBatchNotFound
	}
	if !b.Locked {
		return ErrBatchNotLocked
	}
	if _, ok := b.wellByKey(well.Key()); !ok {
		return ErrWellNotFound
	}
	b.Runs[runID] = assay.InstrumentRun{ID: runID, Well: well, Generation: b.Generation}
	return nil
}

// ingestChunkApply records one curve chunk after validating the prefix.
func (e *Engine) ingestChunkApply(batchID, opID string, chunk assay.CurveChunk, seq int64) (string, error) {
	b := e.batches[batchID]
	if b == nil {
		return "", ErrBatchNotFound
	}
	run, ok := b.Runs[chunk.RunID]
	if !ok {
		return "", ErrRunNotFound
	}
	if run.Well != chunk.Well {
		return "", assay.ErrRunMismatch
	}
	if chunk.Generation != b.Generation {
		return "", assay.ErrStaleGeneration
	}
	existing := b.Chunks[chunk.RunID]
	next := make([]assay.CurveChunk, 0, len(existing)+1)
	next = append(next, existing...)
	next = append(next, chunk)
	if err := assay.ValidateChunkPrefix(next); err != nil {
		return "", err
	}
	b.Chunks[chunk.RunID] = next
	b.ChunkOps[opID] = chunkRecord{Txn: seq, Digest: assay.ChunkContentDigest(chunk)}
	return txnString(seq), nil
}

// interpretApply runs the fixed-point pipeline for a complete well curve.
func (e *Engine) interpretApply(batchID string, well protocol.WellRef) error {
	b := e.batches[batchID]
	if b == nil {
		return ErrBatchNotFound
	}
	if !b.Locked {
		return ErrBatchNotLocked
	}
	w, ok := b.wellByKey(well.Key())
	if !ok {
		return ErrWellNotFound
	}
	run, ok := b.runForWell(well)
	if !ok {
		return ErrRunNotFound
	}
	chunks := b.Chunks[run.ID]
	if !curveComplete(chunks) {
		return ErrNotComplete
	}
	curve, cycleStart, err := assay.AssembleCurve(chunks)
	if err != nil {
		return err
	}
	interp, err := analysis.Interpret(curve, cycleStart, b.Snapshot)
	if err != nil {
		return err
	}
	interp.Well = well
	if w.Type != assay.WellSample {
		positive := w.Type == assay.WellPositiveControl
		interp.Control = analysis.ControlVerdictFor(interp, b.Snapshot, positive)
	}
	b.Interps[well.Key()] = interp
	b.refreshReplicates(well.Key())
	return nil
}

// evaluateApply computes the contamination closure from the current seeds.
func (e *Engine) evaluateApply(batchID string) error {
	b := e.batches[batchID]
	if b == nil {
		return ErrBatchNotFound
	}
	if !b.Locked {
		return ErrBatchNotLocked
	}
	closure := verdict.ComputeClosure(b.collectSeeds(), b.Snapshot.Edges)
	b.Contam = &closure
	return nil
}

// retestApply creates the next generation re-opening only the affected wells.
func (e *Engine) retestApply(batchID, sourceDigest string, generation int) error {
	b := e.batches[batchID]
	if b == nil {
		return ErrBatchNotFound
	}
	if b.Contam == nil {
		return verdict.ErrNotReady
	}
	if b.Contam.SourceDigest != sourceDigest {
		return verdict.ErrRetestConflict
	}
	if generation != b.Generation+1 {
		return verdict.ErrRetestConflict
	}
	if _, exists := verdict.FindRetest(b.Retests, b.Generation, sourceDigest); exists {
		return verdict.ErrRetestConflict
	}
	b.Retests = append(b.Retests, verdict.NewGeneration(generation, sourceDigest, b.Contam.Closure))
	b.Generation = generation
	return nil
}

// reviewApply records a qualified review over the current evidence digest.
func (e *Engine) reviewApply(batchID string, review verdict.Review) error {
	b := e.batches[batchID]
	if b == nil {
		return ErrBatchNotFound
	}
	if err := verdict.ValidateReview(b.Reviews, review, b.evidenceDigest()); err != nil {
		return err
	}
	b.Reviews = append(b.Reviews, review)
	return nil
}

// decideApply persists the terminal decision with a single-writer barrier.
func (e *Engine) decideApply(batchID string, decision verdict.FinalDecision) error {
	b := e.batches[batchID]
	if b == nil {
		return ErrBatchNotFound
	}
	if b.Final != nil {
		return verdict.ErrDecisionExists
	}
	digest := b.evidenceDigest()
	if !verdict.HasQuorum(b.Reviews, digest) {
		return ErrInsufficientReview
	}
	if decision.Type == verdict.FinalRelease {
		if !b.releaseReady() {
			return verdict.ErrNotReady
		}
	}
	decision.Digest = digest
	b.Final = &decision
	return nil
}

// releaseReady reports whether a batch may be released under rule 8.
func (b *batchState) releaseReady() bool {
	return b.allInterpreted() && b.controlsValid() && b.replicatesConsistent() &&
		b.Contam != nil && len(b.Retests) > 0
}

// curveComplete reports whether chunks form a complete curve (contiguous
// prefix from sequence 1 terminated by a complete flag).
func curveComplete(chunks []assay.CurveChunk) bool {
	if len(chunks) == 0 {
		return false
	}
	if err := assay.ValidateChunkPrefix(chunks); err != nil {
		return false
	}
	last := chunks[0]
	for _, c := range chunks {
		if c.Seq > last.Seq {
			last = c
		}
	}
	return last.Complete
}

// refreshReplicates recomputes the replicate verdict for a sample group.
func (b *batchState) refreshReplicates(key string) {
	group := b.GroupByWell[key]
	if group == "" {
		return
	}
	var keys []string
	for k, g := range b.GroupByWell {
		if g == group {
			keys = append(keys, k)
		}
	}
	var calls []bool
	for _, k := range keys {
		if i, ok := b.Interps[k]; ok {
			calls = append(calls, i.Positive)
		}
	}
	v := analysis.ReplicateVerdictFor(calls)
	for _, k := range keys {
		if i, ok := b.Interps[k]; ok {
			i.Replicate = v
			i.Digest = analysis.InterpretationDigest(i)
			b.Interps[k] = i
		}
	}
}

// collectSeeds derives contamination seeds from control failures, replicate
// mismatches and suspicious high-concentration samples.
func (b *batchState) collectSeeds() []protocol.WellRef {
	var seeds []protocol.WellRef
	for _, w := range b.Wells {
		i, ok := b.Interps[w.Ref.Key()]
		if !ok {
			continue
		}
		switch w.Type {
		case assay.WellSample:
			if i.Replicate == analysis.ReplicateMismatch {
				seeds = append(seeds, w.Ref)
			}
			if i.Positive && i.ScaledCt < b.Snapshot.PositiveMin {
				seeds = append(seeds, w.Ref)
			}
		default:
			if i.Control == analysis.ControlFail {
				seeds = append(seeds, w.Ref)
			}
		}
	}
	return seeds
}
