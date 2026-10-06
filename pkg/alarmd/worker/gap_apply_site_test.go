// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// Each of the Slot's two gap applies reports its own site.
//
// Driven through a whole Slot with one of the two refusing, because that is
// the only thing that reads which constant each call site actually passes. A
// case that drives applyGap directly proves the plumbing carries whatever it
// is handed, and a case that reads the constants proves they differ - neither
// notices the after-state call passing the before-events name, which is the
// mistake that would send a reader hunting another process for a write this
// Slot made itself.
func TestEachGapApplySiteReportsItsOwnName(t *testing.T) {
	for _, testCase := range []struct {
		stage, want string
		degraded    bool
	}{
		// A partial query is what makes the Slot write a gap before its
		// events; the ordinary Slot writes none, so a case built on it would
		// pass with the applies never reached.
		{stage: "gap_before", want: worker.GapSiteBeforeEvents, degraded: true},
		// The ordinary Slot writes its gap after the state, and only that
		// case reaches the second call: without it, the second site could be
		// handed the first site's name and every case above would stay green.
		{stage: "gap_after", want: worker.GapSiteAfterState, degraded: false},
	} {
		t.Run(testCase.stage, func(t *testing.T) {
			fixture := newFixture(t, true, "")
			fixture.ports.degraded = testCase.degraded
			fixture.ports.refuseGapStage = testCase.stage
			_, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err == nil {
				t.Fatalf("the Slot completed with %s refused", testCase.stage)
			}
			var refusal *worker.GapApplyRefusal
			if !errors.As(err, &refusal) {
				t.Fatalf("the refusal did not reach the caller as itself: %v", err)
			}
			if refusal.Site != testCase.want {
				t.Fatalf("%s refused and reported site %q, want %q", testCase.stage, refusal.Site, testCase.want)
			}
		})
	}
}
