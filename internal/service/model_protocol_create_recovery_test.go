package service_test

import (
	"errors"
	"testing"

	"edna-contamination-verdict/internal/protocol"
	"edna-contamination-verdict/internal/service"
	"edna-contamination-verdict/internal/store"
)

var errModelSnapshotInterrupted = errors.New("snapshot interrupted")

type modelRecoveryStore struct {
	events       []store.Event
	snapshot     store.Snapshot
	failSnapshot bool
}

func (s *modelRecoveryStore) Append(kind string, payload []byte) (store.Event, error) {
	ev := store.Event{
		Seq:      int64(len(s.events) + 1),
		Kind:     kind,
		Payload:  append([]byte(nil), payload...),
		Length:   len(payload),
		Checksum: store.Checksum(payload),
	}
	s.events = append(s.events, ev)
	return ev, nil
}

func (s *modelRecoveryStore) Commit(seq int64) error {
	for i := range s.events {
		if s.events[i].Seq == seq {
			s.events[i].Committed = true
			return nil
		}
	}
	return store.ErrUnknownSeq
}

func (s *modelRecoveryStore) Replay() ([]store.Event, error) {
	var committed []store.Event
	for _, ev := range s.events {
		if ev.Committed {
			committed = append(committed, ev)
		}
	}
	return committed, nil
}

func (s *modelRecoveryStore) SaveSnapshot(data []byte, seq int64) (store.Snapshot, error) {
	if s.failSnapshot {
		return store.Snapshot{}, errModelSnapshotInterrupted
	}
	s.snapshot = store.Snapshot{
		Data:     append([]byte(nil), data...),
		Seq:      seq,
		Checksum: store.Checksum(data),
	}
	return s.snapshot, nil
}

func (s *modelRecoveryStore) LoadSnapshot() (store.Snapshot, error) {
	return s.snapshot, nil
}

func (s *modelRecoveryStore) Close() error { return nil }

func modelProtocolSpec(id string) protocol.ProtocolSpec {
	well := func(row, col int) protocol.WellRef {
		return protocol.WellRef{Plate: "P1", Row: row, Col: col}
	}
	return protocol.ProtocolSpec{
		ID:             id,
		Target:         "target-A",
		Scale:          1000,
		Threshold:      10,
		BaselineStart:  0,
		BaselineEnd:    4,
		Window:         3,
		PositiveMin:    5000,
		PositiveMax:    9000,
		ReplicateCount: 1,
		ReagentLot:     "lot-A",
		Layout: protocol.LayoutSpec{
			PlateID: "P1",
			Rows:    2,
			Cols:    2,
			Samples: []protocol.SampleSpec{{
				ReplicateGroup: "sample-1",
				Tubes: []protocol.TubePlacement{{
					TubeCode: "tube-1",
					Well:     well(1, 1),
				}},
			}},
			Controls: []protocol.ControlSpec{
				{Kind: protocol.PositiveControl, Well: well(2, 1)},
				{Kind: protocol.NegativeControl, Well: well(2, 2)},
			},
		},
	}
}

func TestModel_ProtocolCreateRecoveryFromCommittedEvent(t *testing.T) {
	cases := []struct {
		name         string
		withSnapshot bool
	}{
		{name: "missing snapshot"},
		{name: "snapshot behind committed protocol", withSnapshot: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &modelRecoveryStore{}
			engine, err := service.NewEngine(st)
			if err != nil {
				t.Fatalf("open initial engine: %v", err)
			}

			if tc.withSnapshot {
				if _, err := engine.CreateProtocol(modelProtocolSpec("snapshotted-protocol")); err != nil {
					t.Fatalf("create snapshotted protocol: %v", err)
				}
			}

			recoveredSpec := modelProtocolSpec("replayed-protocol")
			expected, err := protocol.Lock(recoveredSpec)
			if err != nil {
				t.Fatalf("test protocol is invalid: %v", err)
			}
			st.failSnapshot = true
			if _, err := engine.CreateProtocol(recoveredSpec); !errors.Is(err, errModelSnapshotInterrupted) {
				t.Fatalf("create protocol after simulated crash: got %v, want snapshot interruption", err)
			}

			st.failSnapshot = false
			restarted, err := service.NewEngine(st)
			if err != nil {
				t.Fatalf("restart from committed protocol event: %v", err)
			}
			locked, err := restarted.LockBatch("batch-after-restart", recoveredSpec.ID, expected.Snapshot.Digest)
			if err != nil {
				t.Fatalf("lock batch with replayed protocol: %v", err)
			}
			if locked.Snapshot.Digest != expected.Snapshot.Digest {
				t.Fatalf("locked digest = %q, want %q", locked.Snapshot.Digest, expected.Snapshot.Digest)
			}
		})
	}
}
