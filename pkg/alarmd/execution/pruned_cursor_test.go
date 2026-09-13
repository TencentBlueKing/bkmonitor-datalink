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
		LastCompletionKind: CompletionGapSkipped, CurrentOrRecentGap: PrunedSkipGap(120)}
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
			LastCompletionKind: CompletionFull, CurrentOrRecentGap: PrunedSkipGap(120)}, want: 600, anchored: true},
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
