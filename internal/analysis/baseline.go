package analysis

// BaselineMean computes the arithmetic mean of curve[start..end] (inclusive)
// using half-away-from-zero rounding. It returns ErrInvalidRange when the
// window is empty or out of bounds.
func BaselineMean(curve []int64, start, end int) (int64, error) {
	if start < 0 || end < start || end >= len(curve) {
		return 0, ErrInvalidRange
	}
	var sum int64
	n := end - start + 1
	for i := start; i <= end; i++ {
		if addOverflow(sum, curve[i]) {
			return 0, ErrOverflow
		}
		sum += curve[i]
	}
	return divRoundHalfAway(sum, int64(n))
}

// SubtractBaseline returns a new slice with baseline subtracted from every
// value, rejecting overflow.
func SubtractBaseline(curve []int64, baseline int64) ([]int64, error) {
	out := make([]int64, len(curve))
	for i, v := range curve {
		out[i] = v - baseline
	}
	return out, nil
}
