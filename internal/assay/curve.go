package assay

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"edna-contamination-verdict/internal/protocol"
)

// Sentinel errors produced while assembling or ingesting curves.
var (
	ErrCurveIncomplete = errors.New("curve incomplete")
	ErrRunMismatch     = errors.New("run mismatch")
	ErrChunkConflict   = errors.New("chunk content conflict")
	ErrStaleGeneration = errors.New("stale generation")
)

// Cursor is the deterministic upload cursor for one run.
type Cursor struct {
	RunID      string           `json:"run_id"`
	Well       protocol.WellRef `json:"well"`
	Generation int              `json:"generation"`
	NextSeq    int              `json:"next_seq"`
	Complete   bool             `json:"complete"`
}

// WellFromAssignment converts a locked protocol assignment into a concrete
// well, mapping control kinds to their well types.
func WellFromAssignment(a protocol.WellAssignment) Well {
	w := Well{Ref: a.Ref}
	if a.IsSample {
		w.Type = WellSample
		w.TubeCode = a.TubeCode
		return w
	}
	switch a.Control {
	case protocol.PositiveControl:
		w.Type = WellPositiveControl
	case protocol.NegativeControl:
		w.Type = WellNegativeControl
	case protocol.ExtractionBlank:
		w.Type = WellExtractionBlank
	case protocol.NoTemplateControl:
		w.Type = WellNoTemplateControl
	default:
		w.Type = WellNegativeControl
	}
	return w
}

// ChunkContentDigest returns a stable content digest of a chunk excluding the
// upload bookkeeping (run id and complete flag). Two chunks with the same
// content digest are considered identical for idempotent retries.
func ChunkContentDigest(c CurveChunk) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d|%d|%d|%d|", c.Well.Key(), c.Generation, c.Seq, c.CycleStart, c.CycleEnd)
	for _, v := range c.Fluorescence {
		fmt.Fprintf(h, "%d,", v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// AssembleCurve concatenates the chunks of one well's curve into a single
// fluorescence series ordered by cycle, validating that they form a contiguous
// prefix from sequence 1 and that cycle ranges do not overlap.
func AssembleCurve(chunks []CurveChunk) ([]int64, int, error) {
	if err := ValidateChunkPrefix(chunks); err != nil {
		return nil, 0, err
	}
	sorted := make([]CurveChunk, len(chunks))
	copy(sorted, chunks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })
	var out []int64
	start := sorted[0].CycleStart
	for i, c := range sorted {
		wantLen := c.CycleEnd - c.CycleStart + 1
		if len(c.Fluorescence) != wantLen {
			return nil, 0, ErrCurveIncomplete
		}
		if i > 0 && c.CycleStart != sorted[i-1].CycleEnd+1 {
			return nil, 0, ErrChunkGap
		}
		out = append(out, c.Fluorescence...)
	}
	return out, start, nil
}

// ComputeCursor derives the upload cursor for a set of chunks. NextSeq is the
// first missing sequence number; Complete is true once a complete chunk has
// been recorded.
func ComputeCursor(runID string, well protocol.WellRef, generation int, chunks []CurveChunk) Cursor {
	cur := Cursor{RunID: runID, Well: well, Generation: generation, NextSeq: 1}
	if len(chunks) == 0 {
		return cur
	}
	seqs := make([]int, len(chunks))
	for i, c := range chunks {
		seqs[i] = c.Seq
	}
	sort.Ints(seqs)
	next := 1
	for _, s := range seqs {
		if s == next {
			next++
		} else if s > next {
			break
		}
	}
	cur.NextSeq = next
	for _, c := range chunks {
		if c.Complete {
			cur.Complete = true
			break
		}
	}
	return cur
}
