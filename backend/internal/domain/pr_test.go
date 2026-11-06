package domain

import (
	"slices"
	"testing"
)

// LADO fork, #21: Blockers names every reason ReadyToMerge refuses, and is
// empty exactly when ReadyToMerge holds, over every combination of inputs.
func TestMergeReadinessBlockersMatchReadyToMerge(t *testing.T) {
	bools := []bool{false, true}
	cis := []CIState{"", CIUnknown, CIPending, CIPassing, CIFailing, "something_else"}
	reviews := []ReviewDecision{"", ReviewNone, ReviewApproved, ReviewChangesRequest, ReviewRequired}
	merges := []Mergeability{"", MergeUnknown, MergeMergeable, MergeConflicting, MergeBlocked, MergeUnstable}
	cases := 0
	for _, merged := range bools {
		for _, closed := range bools {
			for _, draft := range bools {
				for _, unresolved := range bools {
					for _, ci := range cis {
						for _, review := range reviews {
							for _, mergeability := range merges {
								r := MergeReadiness{Draft: draft, Merged: merged, Closed: closed, CI: ci, Review: review, Mergeability: mergeability, UnresolvedComments: unresolved}
								blockers := r.Blockers()
								if r.ReadyToMerge() != (len(blockers) == 0) {
									t.Fatalf("%+v: ReadyToMerge = %v, Blockers = %v", r, r.ReadyToMerge(), blockers)
								}
								cases++
							}
						}
					}
				}
			}
		}
	}
	if cases != 2*2*2*2*len(cis)*len(reviews)*len(merges) {
		t.Fatalf("ran %d cases", cases)
	}
}

func TestMergeReadinessBlockersNamesEachReason(t *testing.T) {
	ready := MergeReadiness{CI: CIPassing, Review: ReviewApproved, Mergeability: MergeMergeable}
	cases := []struct {
		name   string
		mutate func(*MergeReadiness)
		want   []string
	}{
		{"ready", func(*MergeReadiness) {}, nil},
		{"merged", func(r *MergeReadiness) { r.Merged = true }, []string{MergeBlockerMerged}},
		{"closed", func(r *MergeReadiness) { r.Closed = true }, []string{MergeBlockerClosed}},
		{"draft", func(r *MergeReadiness) { r.Draft = true }, []string{MergeBlockerDraft}},
		{"ci failing", func(r *MergeReadiness) { r.CI = CIFailing }, []string{MergeBlockerCIFailing}},
		{"ci pending", func(r *MergeReadiness) { r.CI = CIPending }, []string{MergeBlockerCIPending}},
		{"ci unknown", func(r *MergeReadiness) { r.CI = CIUnknown }, []string{MergeBlockerCIUnknown}},
		{"ci empty", func(r *MergeReadiness) { r.CI = "" }, []string{MergeBlockerCIUnknown}},
		{"changes requested", func(r *MergeReadiness) { r.Review = ReviewChangesRequest }, []string{MergeBlockerChangesRequested}},
		{"unresolved comments", func(r *MergeReadiness) { r.UnresolvedComments = true }, []string{MergeBlockerUnresolvedComments}},
		{"not mergeable", func(r *MergeReadiness) { r.Mergeability = MergeBlocked }, []string{MergeBlockerNotMergeable}},
		{"several", func(r *MergeReadiness) { r.Draft, r.CI, r.Mergeability = true, CIUnknown, MergeUnknown }, []string{MergeBlockerDraft, MergeBlockerCIUnknown, MergeBlockerNotMergeable}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ready
			c.mutate(&r)
			if got := r.Blockers(); !slices.Equal(got, c.want) {
				t.Errorf("Blockers = %v, want %v", got, c.want)
			}
		})
	}
}
