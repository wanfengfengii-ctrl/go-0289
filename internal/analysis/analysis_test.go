package analysis

import "testing"

const maxInt = int64(^uint64(0) >> 1)

func TestInterpolateCtBoundary(t *testing.T) {
	got, err := InterpolateCt(0, 0, 10, 100, 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got != 10000 {
		t.Fatalf("want 10000, got %d", got)
	}
}

func TestInterpolateCtRoundHalfAway(t *testing.T) {
	got, err := InterpolateCt(0, 0, 1, 2, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}

func TestInterpolateCtDivideByZero(t *testing.T) {
	if _, err := InterpolateCt(0, 5, 10, 5, 7, 1); err != ErrDivideByZero {
		t.Fatalf("want ErrDivideByZero, got %v", err)
	}
}

func TestInterpolateCtInvalidRange(t *testing.T) {
	if _, err := InterpolateCt(10, 0, 10, 5, 7, 1); err != ErrInvalidRange {
		t.Fatalf("want ErrInvalidRange, got %v", err)
	}
}

func TestInterpolateCtOverflow(t *testing.T) {
	if _, err := InterpolateCt(0, 0, maxInt, 1, maxInt, 1); err != ErrOverflow {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
}
