// Package store provides the checksummed event log, atomic snapshots and
// transactional commit/replay used for crash recovery and final arbitration.
//
// The store is an append-only journal of framed records. Each record carries a
// length, a SHA-256 checksum and a committed flag. A transaction is published
// only after its committed flag is durably persisted, so a crash before commit
// leaves an uncommitted tail that recovery ignores. A committed record whose
// checksum no longer matches is a hard error that must prevent startup.
package store

import (
	"crypto/sha256"
	"errors"
)

// ComponentName is the stable identity of this component.
const ComponentName = "event-store-and-final-arbitration"

// Event is a single transactional log record.
type Event struct {
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`
	Payload   []byte `json:"payload"`
	Length    int    `json:"length"`
	Checksum  []byte `json:"checksum"`
	Committed bool   `json:"committed"`
}

// Snapshot is an atomic serialized projection of visible state tied to the
// committed sequence it reflects.
type Snapshot struct {
	Data     []byte `json:"data"`
	Seq      int64  `json:"seq"`
	Checksum []byte `json:"checksum"`
}

var (
	// ErrCorruptRecord indicates a committed record failed checksum validation.
	ErrCorruptRecord = errors.New("corrupt committed record")
	// ErrUnknownSeq indicates Commit targeted a sequence that does not exist.
	ErrUnknownSeq = errors.New("unknown sequence")
)

// Store is the persistence boundary for events and snapshots.
//
// The engine follows a strict ordering per transaction: Append (uncommitted),
// apply to memory, Commit (durable committed flag), then SaveSnapshot. This
// guarantees that a crash never publishes a transaction whose effect was not
// committed, and that any committed effect not yet snapshotted is replayed on
// recovery.
type Store interface {
	// Append writes an uncommitted record and returns its sequence.
	Append(kind string, payload []byte) (Event, error)
	// Commit durably marks the record at seq as committed.
	Commit(seq int64) error
	// Replay returns committed records in sequence order, ignoring any
	// uncommitted tail. It returns ErrCorruptRecord on a damaged committed
	// record.
	Replay() ([]Event, error)
	// SaveSnapshot atomically persists a snapshot reflecting seq.
	SaveSnapshot(data []byte, seq int64) (Snapshot, error)
	// LoadSnapshot returns the latest persisted snapshot.
	LoadSnapshot() (Snapshot, error)
	// Close flushes and releases the underlying files.
	Close() error
}

// Failpoints are optional test hooks that inject faults around the durable
// boundaries of a transaction. They are nil in production.
type Failpoints struct {
	BeforeAppend func() error
	BeforeCommit func() error
}

// Checksum computes the SHA-256 digest of payload.
func Checksum(payload []byte) []byte {
	h := sha256.Sum256(payload)
	return h[:]
}
