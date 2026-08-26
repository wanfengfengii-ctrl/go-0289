package verdict

// ValidateReview checks a single review against the already-submitted reviews
// and the current evidence digest. Two different qualified reviewers must sign
// the same digest; a duplicate reviewer, an unqualified reviewer or a stale
// digest is rejected.
func ValidateReview(existing []Review, review Review, digest string) error {
	if !IsQualified(review.Qualification) {
		return ErrReviewerUnqualified
	}
	if review.Digest != digest {
		return ErrDigestMismatch
	}
	for _, r := range existing {
		if r.ReviewerID == review.ReviewerID {
			return ErrReviewerDuplicate
		}
	}
	return nil
}

// HasQuorum reports whether two distinct qualified reviewers have signed the
// same digest.
func HasQuorum(reviews []Review, digest string) bool {
	ids := map[string]bool{}
	for _, r := range reviews {
		if r.Digest == digest && IsQualified(r.Qualification) {
			ids[r.ReviewerID] = true
		}
	}
	return len(ids) >= 2
}

// Decide performs the single-writer arbitration for the terminal decision. It
// returns the winning decision and whether it was newly persisted.
func Decide(existing *FinalDecision, decision FinalDecision) (FinalDecision, bool, error) {
	if existing != nil {
		return *existing, false, ErrDecisionExists
	}
	return decision, true, nil
}
