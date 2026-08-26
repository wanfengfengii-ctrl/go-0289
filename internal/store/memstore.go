package store

import "sync"

// MemoryStore is an in-process Store used by tests and by the server when no
// data directory is configured. It honours the same transactional ordering and
// fault-injection hooks as FileStore so the engine is agnostic to the backend.
type MemoryStore struct {
	mu        sync.Mutex
	events    []Event
	seq       int64
	snapshot  Snapshot
	failpoint Failpoints
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// SetFailpoints installs optional test hooks.
func (m *MemoryStore) SetFailpoints(fp Failpoints) { m.failpoint = fp }

// Append records an uncommitted event.
func (m *MemoryStore) Append(kind string, payload []byte) (Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failpoint.BeforeAppend != nil {
		if err := m.failpoint.BeforeAppend(); err != nil {
			return Event{}, err
		}
	}
	m.seq++
	ev := Event{
		Seq:       m.seq,
		Kind:      kind,
		Payload:   append([]byte(nil), payload...),
		Length:    len(payload),
		Checksum:  Checksum(payload),
		Committed: false,
	}
	m.events = append(m.events, ev)
	return ev, nil
}

// Commit marks the record at seq as committed.
func (m *MemoryStore) Commit(seq int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failpoint.BeforeCommit != nil {
		if err := m.failpoint.BeforeCommit(); err != nil {
			return err
		}
	}
	for i := range m.events {
		if m.events[i].Seq == seq {
			m.events[i].Committed = true
			return nil
		}
	}
	return ErrUnknownSeq
}

// Replay returns committed records only, in order.
func (m *MemoryStore) Replay() ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, 0, len(m.events))
	for _, e := range m.events {
		if e.Committed {
			out = append(out, e)
		}
	}
	return out, nil
}

// SaveSnapshot stores an in-memory snapshot.
func (m *MemoryStore) SaveSnapshot(data []byte, seq int64) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{Data: append([]byte(nil), data...), Seq: seq, Checksum: Checksum(data)}
	m.snapshot = s
	return s, nil
}

// LoadSnapshot returns the latest snapshot.
func (m *MemoryStore) LoadSnapshot() (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot, nil
}

// Close is a no-op for the in-memory store.
func (m *MemoryStore) Close() error { return nil }
