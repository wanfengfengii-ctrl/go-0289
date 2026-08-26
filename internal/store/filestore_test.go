package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreReplayIgnoresUncommittedTail(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := s.Append("x", []byte("committed"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ev.Seq); err != nil {
		t.Fatal(err)
	}
	// Append a second, uncommitted record.
	if _, err := s.Append("x", []byte("uncommitted")); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := OpenFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	events, err := s2.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 committed event, got %d", len(events))
	}
	if string(events[0].Payload) != "committed" {
		t.Fatalf("unexpected payload %q", events[0].Payload)
	}
}

func TestFileStoreSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenFileStore(dir)
	ev, _ := s.Append("x", []byte("data"))
	_ = s.Commit(ev.Seq)
	if _, err := s.SaveSnapshot([]byte("hello-state"), ev.Seq); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := OpenFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	snap, err := s2.LoadSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if string(snap.Data) != "hello-state" {
		t.Fatalf("unexpected snapshot data %q", snap.Data)
	}
	if snap.Seq != ev.Seq {
		t.Fatalf("snapshot seq %d want %d", snap.Seq, ev.Seq)
	}
}

func TestFileStoreDetectsCorruptCommittedRecord(t *testing.T) {
	dir := t.TempDir()
	s, _ := OpenFileStore(dir)
	ev, _ := s.Append("x", []byte("payload"))
	_ = s.Commit(ev.Seq)
	_ = s.Close()

	// Corrupt the payload bytes of the committed record.
	path := filepath.Join(dir, "events.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The payload starts after the 45-byte header and kind bytes; flip a byte
	// near the end of the payload.
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := OpenFileStore(dir); err == nil {
		t.Fatal("expected corruption error, got nil")
	}
}

func TestChecksumDeterministic(t *testing.T) {
	a := Checksum([]byte("abc"))
	b := Checksum([]byte("abc"))
	if !bytes.Equal(a, b) {
		t.Fatal("checksum not deterministic")
	}
	if bytes.Equal(a, Checksum([]byte("abd"))) {
		t.Fatal("different payloads must differ")
	}
}
