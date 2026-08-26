package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"edna-contamination-verdict/internal/store"
)

func TestModel_FileStoreRecoveryDistinguishesIncompleteTailFromCommittedCorruption(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(t *testing.T, logPath string, committedEnd int64, fullLog []byte, kind, payload string)
		wantOpenErr bool
		wantCorrupt bool
	}{
		{
			name: "incomplete uncommitted kind is discarded",
			mutate: func(t *testing.T, logPath string, committedEnd int64, fullLog []byte, kind, payload string) {
				t.Helper()
				headerSize := len(fullLog) - int(committedEnd) - len(kind) - len(payload)
				if err := os.Truncate(logPath, committedEnd+int64(headerSize+len(kind)-1)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "incomplete uncommitted payload is discarded",
			mutate: func(t *testing.T, logPath string, committedEnd int64, fullLog []byte, kind, payload string) {
				t.Helper()
				headerSize := len(fullLog) - int(committedEnd) - len(kind) - len(payload)
				if err := os.Truncate(logPath, committedEnd+int64(headerSize+len(kind)+len(payload)-1)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "committed checksum mismatch remains an error",
			mutate: func(t *testing.T, logPath string, _ int64, fullLog []byte, _, _ string) {
				t.Helper()
				fullLog[len(fullLog)-1] ^= 0xff
				if err := os.WriteFile(logPath, fullLog, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantOpenErr: true,
			wantCorrupt: true,
		},
		{
			name: "committed frame protocol damage remains an error",
			mutate: func(t *testing.T, logPath string, _ int64, fullLog []byte, _, _ string) {
				t.Helper()
				fullLog[0] ^= 0xff
				if err := os.WriteFile(logPath, fullLog, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantOpenErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			logPath := filepath.Join(dir, "events.log")
			fs, err := store.OpenFileStore(dir)
			if err != nil {
				t.Fatal(err)
			}

			committed, err := fs.Append("batch", []byte("historical-batch"))
			if err != nil {
				t.Fatal(err)
			}
			if err := fs.Commit(committed.Seq); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(logPath)
			if err != nil {
				t.Fatal(err)
			}
			committedEnd := info.Size()

			kind, payload := "review-event", "unfinished-review-payload"
			if !tt.wantOpenErr {
				if _, err := fs.Append(kind, []byte(payload)); err != nil {
					t.Fatal(err)
				}
			}
			if err := fs.Close(); err != nil {
				t.Fatal(err)
			}
			fullLog, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			tt.mutate(t, logPath, committedEnd, fullLog, kind, payload)

			recovered, err := store.OpenFileStore(dir)
			if tt.wantOpenErr {
				if err == nil {
					recovered.Close()
					t.Fatal("expected recovery error")
				}
				if tt.wantCorrupt && !errors.Is(err, store.ErrCorruptRecord) {
					t.Fatalf("expected ErrCorruptRecord, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("recovery rejected an uncommitted partial tail: %v", err)
			}
			defer recovered.Close()

			events, err := recovered.Replay()
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Seq != committed.Seq || string(events[0].Payload) != "historical-batch" {
				t.Fatalf("committed history was not preserved: %#v", events)
			}
			info, err = os.Stat(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() != committedEnd {
				t.Fatalf("log size after recovery = %d, want last committed end %d", info.Size(), committedEnd)
			}
		})
	}
}
