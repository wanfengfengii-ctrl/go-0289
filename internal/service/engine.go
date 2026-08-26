// Package service is the application engine that composes the domain
// packages (protocol, assay, analysis, verdict) over a durable store. It owns
// the transactional boundary: every mutation appends an event, applies the
// change to memory, durably commits, and only then snapshots. On startup it
// restores the snapshot and replays any committed events that followed it, so
// a crash never publishes a partial transaction.
package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"edna-contamination-verdict/internal/assay"
	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/store"
	"edna-contamination-verdict/internal/verdict"
)

// ComponentName is the stable identity of the application engine.
const ComponentName = "application-engine"

// Event kinds written to the durable log.
const (
	evProtocolCreate = "protocol.create"
	evBatchLock      = "batch.lock"
	evBatchLoad      = "batch.load"
	evRunCreate      = "run.create"
	evChunkIngest    = "chunk.ingest"
	evInterpret      = "batch.interpret"
	evContaminate    = "contamination.evaluate"
	evRetestCreate   = "retest.create"
	evReviewSubmit   = "review.submit"
	evFinalDecide    = "final.decide"
)

// Sentinel engine-level errors.
var (
	ErrBatchNotFound  = errors.New("batch not found")
	ErrBatchLocked    = errors.New("batch already locked")
	ErrBatchNotLocked = errors.New("batch not locked")
	ErrRunNotFound    = errors.New("run not found")
)

// Engine is the thread-safe application service.
type Engine struct {
	mu        sync.Mutex
	store     store.Store
	protocols map[string]protocol.ProtocolSpec
	batches   map[string]*batchState
}

// NewEngine opens an engine over the given store and restores any persisted
// state.
func NewEngine(st store.Store) (*Engine, error) {
	e := &Engine{
		store:     st,
		protocols: map[string]protocol.ProtocolSpec{},
		batches:   map[string]*batchState{},
	}
	if err := e.restore(); err != nil {
		return nil, err
	}
	return e, nil
}

// restore loads the snapshot then replays committed events after its sequence.
func (e *Engine) restore() error {
	snap, err := e.store.LoadSnapshot()
	if err != nil {
		return err
	}
	if len(snap.Data) > 0 {
		if err := e.deserialize(snap.Data); err != nil {
			return err
		}
	}
	events, err := e.store.Replay()
	if err != nil {
		return err
	}
	for _, ev := range events {
		if ev.Seq <= snap.Seq {
			continue
		}
		if err := e.applyEvent(ev); err != nil {
			return fmt.Errorf("replay seq %d: %w", ev.Seq, err)
		}
	}
	return nil
}

// serialize produces a deterministic JSON snapshot of the full engine state.
func (e *Engine) serialize() ([]byte, error) {
	p := persistState{
		Protocols: e.protocols,
		Batches:   map[string]persistBatch{},
	}
	for id, b := range e.batches {
		p.Batches[id] = b.export()
	}
	return json.Marshal(p)
}

// deserialize restores engine state from a snapshot.
func (e *Engine) deserialize(data []byte) error {
	var p persistState
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	e.protocols = p.Protocols
	if e.protocols == nil {
		e.protocols = map[string]protocol.ProtocolSpec{}
	}
	e.batches = map[string]*batchState{}
	for id, pb := range p.Batches {
		b, err := pb.importState()
		if err != nil {
			return err
		}
		e.batches[id] = b
	}
	return nil
}

// transact runs one mutation under the (already held) engine lock: append,
// apply, commit, snapshot.
//
// A transaction must not become visible in the running process until it is
// durably committed. The apply step mutates the live in-memory state directly,
// so if apply or Commit fails we roll the in-memory state back to the
// pre-transaction checkpoint — mirroring the crash-recovery guarantee that an
// uncommitted tail is dropped. Once Commit succeeds the transaction is durable
// (and is replayed from the log on the next restart whenever the snapshot
// lags), so a later snapshot failure must not roll back: doing so would hide a
// committed transaction.
func (e *Engine) transact(kind string, payload []byte, apply func(seq int64) error) (int64, error) {
	ev, err := e.store.Append(kind, payload)
	if err != nil {
		return 0, err
	}
	checkpoint, err := e.serialize()
	if err != nil {
		return 0, err
	}
	if err := apply(ev.Seq); err != nil {
		e.rollback(checkpoint)
		return 0, err
	}
	if err := e.store.Commit(ev.Seq); err != nil {
		e.rollback(checkpoint)
		return 0, err
	}
	data, err := e.serialize()
	if err != nil {
		return 0, err
	}
	if _, err := e.store.SaveSnapshot(data, ev.Seq); err != nil {
		return 0, err
	}
	return ev.Seq, nil
}

// rollback discards any in-memory mutation from a transaction that did not
// durably commit, restoring the pre-transaction visible state. It is infallible
// for a checkpoint produced by serialize.
func (e *Engine) rollback(checkpoint []byte) {
	_ = e.deserialize(checkpoint)
}

// applyEvent re-applies a committed event during replay (no persistence).
func (e *Engine) applyEvent(ev store.Event) error {
	switch ev.Kind {
	case evProtocolCreate:
		var spec protocol.ProtocolSpec
		if err := json.Unmarshal(ev.Payload, &spec); err != nil {
			return err
		}
		return e.createProtocolApply(spec)
	case evBatchLock:
		var p struct {
			BatchID    string `json:"batch_id"`
			ProtocolID string `json:"protocol_id"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		return e.lockBatchApply(p.BatchID, p.ProtocolID)
	case evBatchLoad:
		var p struct {
			BatchID string            `json:"batch_id"`
			Request assay.LoadRequest `json:"request"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		_, err := e.loadApply(p.BatchID, p.Request, ev.Seq)
		return err
	case evRunCreate:
		var p struct {
			BatchID string           `json:"batch_id"`
			RunID   string           `json:"run_id"`
			Well    protocol.WellRef `json:"well"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		return e.createRunApply(p.BatchID, p.RunID, p.Well)
	case evChunkIngest:
		var p struct {
			BatchID     string           `json:"batch_id"`
			OperationID string           `json:"operation_id"`
			Chunk       assay.CurveChunk `json:"chunk"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		_, err := e.ingestChunkApply(p.BatchID, p.OperationID, p.Chunk, ev.Seq)
		return err
	case evInterpret:
		var p struct {
			BatchID string           `json:"batch_id"`
			Well    protocol.WellRef `json:"well"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		return e.interpretApply(p.BatchID, p.Well)
	case evContaminate:
		var p struct {
			BatchID string `json:"batch_id"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		return e.evaluateApply(p.BatchID)
	case evRetestCreate:
		var p struct {
			BatchID      string `json:"batch_id"`
			SourceDigest string `json:"source_digest"`
			Generation   int    `json:"generation"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		return e.retestApply(p.BatchID, p.SourceDigest, p.Generation)
	case evReviewSubmit:
		var p struct {
			BatchID string         `json:"batch_id"`
			Review  verdict.Review `json:"review"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		return e.reviewApply(p.BatchID, p.Review)
	case evFinalDecide:
		var p struct {
			BatchID  string                `json:"batch_id"`
			Decision verdict.FinalDecision `json:"decision"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		return e.decideApply(p.BatchID, p.Decision)
	default:
		return fmt.Errorf("unknown event kind %q", ev.Kind)
	}
}
