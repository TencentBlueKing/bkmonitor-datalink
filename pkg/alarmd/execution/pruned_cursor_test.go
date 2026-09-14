// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// A pruned skip leaves the cursor standing on its own: no continuity anchor
// is derived from Slots that no longer exist, while every other Progress
// keeps deriving its next Slot from the last completion it carries.
func TestScheduleProgressContinuityAnchorIgnoresAPrunedSkip(t *testing.T) {
	skipped := ScheduleProgress{Identity: ProgressIdentity{QueryGroup: "q"}, NextSlot: 600,
		LastCompletionKind: CompletionGapSkipped, CurrentOrRecentGap: PrunedSkipGap(120, 180)}
	if err := skipped.Validate(); err != nil {
		t.Fatalf("a pruned skip does not validate: %v", err)
	}
	if !skipped.SkippedPrunedRange() {
		t.Fatal("pruned skip not recognized")
	}
	if anchor, ok := skipped.ContinuityAnchor(); ok || anchor != 0 {
		t.Fatalf("pruned skip anchor = (%d, %v), want none", anchor, ok)
	}
	for _, test := range []struct {
		name     string
		progress ScheduleProgress
		want     EvaluationTime
		anchored bool
	}{
		{name: "a full completion anchors", progress: ScheduleProgress{NextSlot: 660, LastFullSlot: 600}, want: 600, anchored: true},
		{name: "a later gap anchors past the full completion", progress: ScheduleProgress{NextSlot: 780, LastFullSlot: 600,
			LastCompletionKind: CompletionGapSkipped, CurrentOrRecentGap: &ProgressGapSummary{Kind: CompletionGapSkipped,
				ReasonCode: ReasonCode(contract.ReasonGapSkipped), FirstSlot: 660, LastSlot: 720, Count: 2}}, want: 720, anchored: true},
		{name: "a completion after the skip anchors again", progress: ScheduleProgress{NextSlot: 660, LastFullSlot: 600,
			LastCompletionKind: CompletionFull, CurrentOrRecentGap: PrunedSkipGap(120, 180)}, want: 600, anchored: true},
		{name: "nothing completed yet has no anchor", progress: ScheduleProgress{NextSlot: 60}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, ok := test.progress.ContinuityAnchor(); got != test.want || ok != test.anchored {
				t.Fatalf("anchor = (%d, %v), want (%d, %v)", got, ok, test.want, test.anchored)
			}
		})
	}
}

func TestProgressSkipPrunedRequestValidation(t *testing.T) {
	valid := ProgressSkipPrunedRequest{Identity: ProgressIdentity{QueryGroup: "q"},
		OwnerFence:       OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
		ExpectedNextSlot: 120, ResumeAt: 600}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ProgressSkipPrunedRequest){
		"no identity":         func(r *ProgressSkipPrunedRequest) { r.Identity.QueryGroup = "" },
		"fence elsewhere":     func(r *ProgressSkipPrunedRequest) { r.OwnerFence.QueryGroup = "other" },
		"no lease":            func(r *ProgressSkipPrunedRequest) { r.OwnerFence.LeaseToken = "" },
		"resume not after":    func(r *ProgressSkipPrunedRequest) { r.ResumeAt = 120 },
		"cursor unset":        func(r *ProgressSkipPrunedRequest) { r.ExpectedNextSlot = 0 },
		"resume before start": func(r *ProgressSkipPrunedRequest) { r.ResumeAt = 60 },
	} {
		request := valid
		mutate(&request)
		if err := request.Validate(); err == nil {
			t.Fatalf("%s validated", name)
		}
	}
}

// A gap whose Slots cannot be counted does not report a count.
//
// Count means Slots in every other gap -- foldRecentGap adds one per Slot as
// consecutive completions arrive -- and a pruned skip has no way to know how
// many Slots were in the segments that were pruned. It used to record 1 there,
// meaning "one skip event", and a field carrying a count for every other
// producer is read as a count: an unknown number of object-windows that were
// never detected, and never will be, reported as the smallest non-zero amount
// of them by anything that adds these up or compares two gaps.
//
// The two shapes are mutually exclusive by construction, which is what stops a
// later edit from putting a plausible number back beside the flag.
func TestAPrunedSkipDoesNotReportASlotCountItCannotHave(t *testing.T) {
	gap := PrunedSkipGap(120, 600)
	if gap.Count != 0 || !gap.Uncounted {
		t.Fatalf("pruned skip = count %d uncounted %v, want no count and the flag: a number here is "+
			"read as Slots, and the Slots are exactly what cannot be known", gap.Count, gap.Uncounted)
	}
	// The extent is still stated. Both ends equal and nothing else would read as
	// a single instant, which is the same shape as a gap of one Slot -- the
	// reading the count used to give as well.
	if gap.FirstSlot != 120 || gap.ResumedAt != 600 {
		t.Fatalf("pruned skip spans %d..%d resumed %d, want the first skipped Slot and where the "+
			"cursor landed", gap.FirstSlot, gap.LastSlot, gap.ResumedAt)
	}
	// ResumedAt is not a skipped Slot, so it must not be LastSlot: the Progress
	// will evaluate it, and every other gap keeps the last skipped Slot before
	// the cursor.
	if gap.LastSlot >= gap.ResumedAt {
		t.Fatalf("last skipped Slot %d is not before the resume point %d, so the gap names a Slot "+
			"that was not skipped", gap.LastSlot, gap.ResumedAt)
	}
}

// Counted and uncounted are the only two shapes a gap summary may have.
//
// Without this, "uncounted" is a flag somebody can set beside a number, and the
// number then looks authoritative while the flag says it is not -- which is
// worse than either alone, because a reader who checks only one of the two gets
// a confident answer whichever one they check.
func TestAGapSummaryEitherCountsItsSlotsOrSaysItCannot(t *testing.T) {
	progress := ScheduleProgress{
		Identity: ProgressIdentity{QueryGroup: "q"}, NextSlot: 600, LastCompletionKind: CompletionGapSkipped,
	}
	for _, testCase := range []struct {
		name  string
		gap   ProgressGapSummary
		valid bool
	}{
		{"counted", ProgressGapSummary{Kind: CompletionGapSkipped,
			ReasonCode: ReasonCode(contract.ReasonGapSkipped), FirstSlot: 120, LastSlot: 180, Count: 2}, true},
		{"uncounted", *PrunedSkipGap(120, 600), true},
		{"a count beside the flag", ProgressGapSummary{Kind: CompletionGapSkipped,
			ReasonCode: ReasonCode(contract.ReasonSchedulePruned), FirstSlot: 120, LastSlot: 120,
			Count: 3, Uncounted: true}, false},
		{"no count and no flag", ProgressGapSummary{Kind: CompletionGapSkipped,
			ReasonCode: ReasonCode(contract.ReasonGapSkipped), FirstSlot: 120, LastSlot: 180}, false},
	} {
		candidate := progress
		gap := testCase.gap
		candidate.CurrentOrRecentGap = &gap
		err := candidate.Validate()
		if testCase.valid && err != nil {
			t.Errorf("%s was refused: %v", testCase.name, err)
		}
		if !testCase.valid && err == nil {
			t.Errorf("%s was accepted; a summary has to be exactly one of the two shapes, or a "+
				"reader checking only one of Count and Uncounted gets a confident wrong answer",
				testCase.name)
		}
	}
}
