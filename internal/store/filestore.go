package store

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// frameMagic prefixes every log record and lets recovery detect a truncated
// or foreign tail.
var frameMagic = [4]byte{'E', 'D', 'N', 'A'}

const (
	frameHeaderSize  = 4 + 4 + 4 + 32 + 1 // magic, kindLen, payloadLen, checksum, committed
	commitByteOffset = 4 + 4 + 4 + 32
)

// FileStore is a durable, file-backed implementation of Store.
//
// The event log is append-only; the committed flag of each record is patched
// in place when Commit is called and flushed with fsync. The snapshot is
// written to a temporary file and renamed into place so a reader never
// observes a half-written snapshot.
type FileStore struct {
	mu        sync.Mutex
	dir       string
	logPath   string
	snapPath  string
	f         *os.File
	events    []Event
	offsets   []int64
	seq       int64
	snapshot  Snapshot
	failpoint Failpoints
}

// OpenFileStore opens (creating if necessary) a store rooted at dir.
func OpenFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &FileStore{
		dir:      dir,
		logPath:  filepath.Join(dir, "events.log"),
		snapPath: filepath.Join(dir, "snapshot.bin"),
	}
	f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	s.f = f
	if err := s.recover(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return s, nil
}

// SetFailpoints installs optional test hooks.
func (s *FileStore) SetFailpoints(fp Failpoints) { s.failpoint = fp }

// recover loads the log and snapshot, dropping any uncommitted tail.
func (s *FileStore) recover() error {
	if err := s.readLog(); err != nil {
		return err
	}
	snap, err := s.readSnapshotFile()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		s.snapshot = snap
	}
	return nil
}

// readLog parses the log file, validates committed records and truncates any
// uncommitted tail from both memory and disk.
func (s *FileStore) readLog() error {
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, frameHeaderSize)
	var events []Event
	var offsets []int64
	var lastCommittedEnd int64
	var maxSeq int64
	for {
		start, err := s.f.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		n, err := io.ReadFull(s.f, buf)
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			// Partial frame at the tail: uncommitted, drop it.
			break
		}
		if err != nil {
			return err
		}
		if n != frameHeaderSize {
			break
		}
		if !bytes.Equal(buf[0:4], frameMagic[:]) {
			return fmt.Errorf("store: bad frame magic at offset %d", start)
		}
		kindLen := binary.BigEndian.Uint32(buf[4:8])
		payloadLen := binary.BigEndian.Uint32(buf[8:12])
		checksum := append([]byte(nil), buf[12:44]...)
		committed := buf[44] == 1

		kind := make([]byte, kindLen)
		if _, err := io.ReadFull(s.f, kind); err != nil {
			return err
		}
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(s.f, payload); err != nil {
			return err
		}
		ev := Event{
			Seq:       maxSeq + 1,
			Kind:      string(kind),
			Payload:   payload,
			Length:    int(payloadLen),
			Checksum:  checksum,
			Committed: committed,
		}
		maxSeq = ev.Seq
		if committed {
			if !bytes.Equal(checksum, Checksum(payload)) {
				return fmt.Errorf("%w at seq %d", ErrCorruptRecord, ev.Seq)
			}
			events = append(events, ev)
			offsets = append(offsets, start+commitByteOffset)
			lastCommittedEnd = start + int64(frameHeaderSize) + int64(kindLen) + int64(payloadLen)
		}
	}
	s.events = events
	s.offsets = offsets
	s.seq = maxSeq
	// Truncate any uncommitted tail so the file only contains durable records.
	if err := s.f.Truncate(lastCommittedEnd); err != nil {
		return err
	}
	if _, err := s.f.Seek(lastCommittedEnd, io.SeekStart); err != nil {
		return err
	}
	return nil
}

// Append writes an uncommitted record and returns its sequence.
func (s *FileStore) Append(kind string, payload []byte) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failpoint.BeforeAppend != nil {
		if err := s.failpoint.BeforeAppend(); err != nil {
			return Event{}, err
		}
	}
	s.seq++
	checksum := Checksum(payload)
	ev := Event{
		Seq:       s.seq,
		Kind:      kind,
		Payload:   append([]byte(nil), payload...),
		Length:    len(payload),
		Checksum:  checksum,
		Committed: false,
	}
	off, err := s.writeFrame(ev)
	if err != nil {
		return Event{}, err
	}
	s.events = append(s.events, ev)
	s.offsets = append(s.offsets, off)
	return ev, nil
}

// writeFrame writes a single framed record and returns the file offset of its
// committed-flag byte so Commit can patch it in place.
func (s *FileStore) writeFrame(ev Event) (int64, error) {
	off, err := s.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	commitOff := off + commitByteOffset
	header := make([]byte, frameHeaderSize)
	copy(header[0:4], frameMagic[:])
	binary.BigEndian.PutUint32(header[4:8], uint32(len(ev.Kind)))
	binary.BigEndian.PutUint32(header[8:12], uint32(len(ev.Payload)))
	copy(header[12:44], ev.Checksum)
	header[44] = 0
	if _, err := s.f.Write(header); err != nil {
		return 0, err
	}
	if _, err := s.f.WriteString(ev.Kind); err != nil {
		return 0, err
	}
	if _, err := s.f.Write(ev.Payload); err != nil {
		return 0, err
	}
	if err := s.f.Sync(); err != nil {
		return 0, err
	}
	return commitOff, nil
}

// Commit durably marks the record at seq as committed by patching its flag.
func (s *FileStore) Commit(seq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i := range s.events {
		if s.events[i].Seq == seq {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrUnknownSeq
	}
	if s.failpoint.BeforeCommit != nil {
		if err := s.failpoint.BeforeCommit(); err != nil {
			return err
		}
	}
	if _, err := s.f.Seek(s.offsets[idx], io.SeekStart); err != nil {
		return err
	}
	if _, err := s.f.Write([]byte{1}); err != nil {
		return err
	}
	if err := s.f.Sync(); err != nil {
		return err
	}
	// Restore the append position to the end of the log so the next Append
	// does not overwrite committed payload bytes.
	if _, err := s.f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	s.events[idx].Committed = true
	return nil
}

// Replay returns the committed records in order.
func (s *FileStore) Replay() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out, nil
}

// SaveSnapshot atomically persists a snapshot via temp-file rename.
func (s *FileStore) SaveSnapshot(data []byte, seq int64) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{Data: append([]byte(nil), data...), Seq: seq, Checksum: Checksum(data)}
	tmp := s.snapPath + ".tmp"
	full := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(full[:8], uint64(seq))
	copy(full[8:], data)
	if err := os.WriteFile(tmp, full, 0o644); err != nil {
		return Snapshot{}, err
	}
	if err := os.Rename(tmp, s.snapPath); err != nil {
		return Snapshot{}, err
	}
	s.snapshot = snap
	return snap, nil
}

// LoadSnapshot returns the latest persisted snapshot.
func (s *FileStore) LoadSnapshot() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot, nil
}

func (s *FileStore) readSnapshotFile() (Snapshot, error) {
	data, err := os.ReadFile(s.snapPath)
	if err != nil {
		return Snapshot{}, err
	}
	// The snapshot file is [seq int64][payload...]; the seq lets recovery know
	// which committed events must still be replayed.
	if len(data) < 8 {
		return Snapshot{}, fmt.Errorf("store: truncated snapshot")
	}
	seq := int64(binary.BigEndian.Uint64(data[:8]))
	payload := data[8:]
	return Snapshot{Data: payload, Seq: seq, Checksum: Checksum(payload)}, nil
}

// Close flushes and closes the log file.
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	return s.f.Close()
}
