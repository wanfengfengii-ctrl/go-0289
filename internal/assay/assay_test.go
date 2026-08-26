package assay

import "testing"

func chunk(seq, start, end int) CurveChunk {
	return CurveChunk{Seq: seq, CycleStart: start, CycleEnd: end}
}

func TestValidateChunkPrefixAcceptsContiguous(t *testing.T) {
	chunks := []CurveChunk{chunk(1, 1, 5), chunk(2, 6, 10), chunk(3, 11, 15)}
	if err := ValidateChunkPrefix(chunks); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateChunkPrefixRejectsGap(t *testing.T) {
	chunks := []CurveChunk{chunk(1, 1, 5), chunk(3, 11, 15)}
	if err := ValidateChunkPrefix(chunks); err != ErrChunkGap {
		t.Fatalf("want ErrChunkGap, got %v", err)
	}
}

func TestValidateChunkPrefixRejectsOverlap(t *testing.T) {
	chunks := []CurveChunk{chunk(1, 1, 5), chunk(2, 5, 10)}
	if err := ValidateChunkPrefix(chunks); err != ErrChunkOverlap {
		t.Fatalf("want ErrChunkOverlap, got %v", err)
	}
}

func TestValidateChunkPrefixRejectsMissingFirst(t *testing.T) {
	chunks := []CurveChunk{chunk(2, 6, 10)}
	if err := ValidateChunkPrefix(chunks); err != ErrChunkOrder {
		t.Fatalf("want ErrChunkOrder, got %v", err)
	}
}

func TestValidateChunkPrefixRejectsEmpty(t *testing.T) {
	if err := ValidateChunkPrefix(nil); err != ErrChunkOrder {
		t.Fatalf("want ErrChunkOrder, got %v", err)
	}
}
