package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"edna-contamination-verdict/internal/analysis"
	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/verdict"
)

// loadRecord is the idempotency record for a sample load.
type loadRecord struct {
	Txn    int64  `json:"txn"`
	Digest string `json:"digest"`
}

// chunkRecord is the idempotency record for a curve chunk.
type chunkRecord struct {
	Txn    int64  `json:"txn"`
	Digest string `json:"digest"`
}

// batchState is the in-memory aggregate for one locked batch.
type batchState struct {
	ID          string
	Snapshot    protocol.ProtocolSnapshot
	Wells       []assay.Well
	Generation  int
	Locked      bool
	TubeWell    map[string]string // tube code -> well key
	GroupByWell map[string]string // well key -> replicate group
	LoadedTubes map[string]string // tube code -> well key (physically loaded)
	LoadOps     map[string]loadRecord
	Runs        map[string]assay.InstrumentRun
	Chunks      map[string][]assay.CurveChunk // runID -> chunks
	ChunkOps    map[string]chunkRecord
	Interps     map[string]analysis.Interpretation // well key -> interpretation
	Contam      *verdict.ContaminationSet
	Retests     []verdict.Generation
	Reviews     []verdict.Review
	Final       *verdict.FinalDecision
}

func newBatchState(id string, snap protocol.ProtocolSnapshot, assignments []protocol.WellAssignment) *batchState {
	b := &batchState{
		ID:          id,
		Snapshot:    snap,
		Generation:  1,
		Locked:      true,
		TubeWell:    map[string]string{},
		GroupByWell: map[string]string{},
		LoadedTubes: map[string]string{},
		LoadOps:     map[string]loadRecord{},
		Runs:        map[string]assay.InstrumentRun{},
		Chunks:      map[string][]assay.CurveChunk{},
		ChunkOps:    map[string]chunkRecord{},
		Interps:     map[string]analysis.Interpretation{},
	}
	for _, a := range assignments {
		w := assay.WellFromAssignment(a)
		b.Wells = append(b.Wells, w)
		if a.IsSample {
			b.TubeWell[a.TubeCode] = a.Ref.Key()
			b.GroupByWell[a.Ref.Key()] = a.ReplicateGroup
		}
	}
	return b
}

// wellByKey returns the well at the given key, or false.
func (b *batchState) wellByKey(key string) (assay.Well, bool) {
	for _, w := range b.Wells {
		if w.Ref.Key() == key {
			return w, true
		}
	}
	return assay.Well{}, false
}

// setWellTube records a tube as physically loaded into a well.
func (b *batchState) setWellTube(key, tube string) {
	for i := range b.Wells {
		if b.Wells[i].Ref.Key() == key {
			b.Wells[i].TubeCode = tube
			return
		}
	}
}

// runForWell returns the run for a well at the current generation.
func (b *batchState) runForWell(well protocol.WellRef) (assay.InstrumentRun, bool) {
	for _, r := range b.Runs {
		if r.Well == well && r.Generation == b.Generation {
			return r, true
		}
	}
	return assay.InstrumentRun{}, false
}

// evidenceDigest computes a stable content digest over all interpreted wells,
// the current contamination set and the retest history. Two different
// reviewers must sign this exact digest for a final decision.
func (b *batchState) evidenceDigest() string {
	h := sha256.New()
	keys := make([]string, 0, len(b.Interps))
	for k := range b.Interps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		i := b.Interps[k]
		fmt.Fprintf(h, "%s|%d|%t|%s|%s;",
			k, i.ScaledCt, i.Positive, i.Control, i.Replicate)
	}
	if b.Contam != nil {
		fmt.Fprintf(h, "contam:%s;", b.Contam.SourceDigest)
	}
	for _, g := range b.Retests {
		fmt.Fprintf(h, "retest:%d:%s;", g.Number, g.ParentDigest)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// allInterpreted reports whether every sample and control well has an
// interpretation in the current generation.
func (b *batchState) allInterpreted() bool {
	for _, w := range b.Wells {
		if _, ok := b.Interps[w.Ref.Key()]; !ok {
			return false
		}
	}
	return true
}

// controlsValid reports whether every control well passed its gating.
func (b *batchState) controlsValid() bool {
	for _, w := range b.Wells {
		if w.Type == assay.WellSample {
			continue
		}
		i, ok := b.Interps[w.Ref.Key()]
		if !ok || i.Control != analysis.ControlPass {
			return false
		}
	}
	return true
}

// replicatesConsistent reports whether every replicate group is consistent.
func (b *batchState) replicatesConsistent() bool {
	groups := map[string][]bool{}
	for key, group := range b.GroupByWell {
		i, ok := b.Interps[key]
		if ok {
			groups[group] = append(groups[group], i.Positive)
		}
	}
	for _, calls := range groups {
		if analysis.ReplicateVerdictFor(calls) == analysis.ReplicateMismatch {
			return false
		}
	}
	return true
}

// persistBatch is the serializable form of a batch.
type persistBatch struct {
	ID          string                             `json:"id"`
	Snapshot    protocol.ProtocolSnapshot          `json:"snapshot"`
	Wells       []assay.Well                       `json:"wells"`
	Generation  int                                `json:"generation"`
	Locked      bool                               `json:"locked"`
	TubeWell    map[string]string                  `json:"tube_well"`
	GroupByWell map[string]string                  `json:"group_by_well"`
	LoadedTubes map[string]string                  `json:"loaded_tubes"`
	LoadOps     map[string]loadRecord              `json:"load_ops"`
	Runs        map[string]assay.InstrumentRun     `json:"runs"`
	Chunks      map[string][]assay.CurveChunk      `json:"chunks"`
	ChunkOps    map[string]chunkRecord             `json:"chunk_ops"`
	Interps     map[string]analysis.Interpretation `json:"interps"`
	Contam      *verdict.ContaminationSet          `json:"contam,omitempty"`
	Retests     []verdict.Generation               `json:"retests"`
	Reviews     []verdict.Review                   `json:"reviews"`
	Final       *verdict.FinalDecision             `json:"final,omitempty"`
}

// persistState is the serializable engine state.
type persistState struct {
	Protocols map[string]protocol.ProtocolSpec `json:"protocols"`
	Batches   map[string]persistBatch          `json:"batches"`
}

func (b *batchState) export() persistBatch {
	return persistBatch{
		ID:          b.ID,
		Snapshot:    b.Snapshot,
		Wells:       append([]assay.Well(nil), b.Wells...),
		Generation:  b.Generation,
		Locked:      b.Locked,
		TubeWell:    b.TubeWell,
		GroupByWell: b.GroupByWell,
		LoadedTubes: b.LoadedTubes,
		LoadOps:     b.LoadOps,
		Runs:        b.Runs,
		Chunks:      b.Chunks,
		ChunkOps:    b.ChunkOps,
		Interps:     b.Interps,
		Contam:      b.Contam,
		Retests:     append([]verdict.Generation(nil), b.Retests...),
		Reviews:     append([]verdict.Review(nil), b.Reviews...),
		Final:       b.Final,
	}
}

func (p persistBatch) importState() (*batchState, error) {
	b := &batchState{
		ID:          p.ID,
		Snapshot:    p.Snapshot,
		Wells:       p.Wells,
		Generation:  p.Generation,
		Locked:      p.Locked,
		TubeWell:    p.TubeWell,
		GroupByWell: p.GroupByWell,
		LoadedTubes: p.LoadedTubes,
		LoadOps:     p.LoadOps,
		Runs:        p.Runs,
		Chunks:      p.Chunks,
		ChunkOps:    p.ChunkOps,
		Interps:     p.Interps,
		Contam:      p.Contam,
		Retests:     p.Retests,
		Reviews:     p.Reviews,
		Final:       p.Final,
	}
	if b.TubeWell == nil {
		b.TubeWell = map[string]string{}
	}
	if b.GroupByWell == nil {
		b.GroupByWell = map[string]string{}
	}
	if b.LoadedTubes == nil {
		b.LoadedTubes = map[string]string{}
	}
	if b.LoadOps == nil {
		b.LoadOps = map[string]loadRecord{}
	}
	if b.Runs == nil {
		b.Runs = map[string]assay.InstrumentRun{}
	}
	if b.Chunks == nil {
		b.Chunks = map[string][]assay.CurveChunk{}
	}
	if b.ChunkOps == nil {
		b.ChunkOps = map[string]chunkRecord{}
	}
	if b.Interps == nil {
		b.Interps = map[string]analysis.Interpretation{}
	}
	return b, nil
}
