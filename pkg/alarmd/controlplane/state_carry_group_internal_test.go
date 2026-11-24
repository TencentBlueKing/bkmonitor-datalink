// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Plan whose previous version is not under the Query Group it is in now is
// one of two things, and the carries say which: the group was
// not in the previous content at all - the edit moved the Plan, a group
// change - or the group was there with no object named for it, as a blocked
// group that never activated is, and the previous version cannot be read.
// Neither carries anything; only the outcome differs.
func TestAPlanMissingFromItsGroupIsAGroupChangeOnlyWhenTheGroupIsNew(t *testing.T) {
	const group = execution.QueryGroupIdentity("qg-now")
	previous := PlanActivationRecord{}
	current := changedPlan{group: group}
	reconciler := &ScheduleActivationReconciler{}
	for _, test := range []struct {
		name    string
		content activatedContent
		state   string
	}{
		{name: "the group is new", content: activatedContent{groups: map[execution.QueryGroupIdentity]QueryGroup{"qg-before": {Identity: "qg-before"}},
			digests: map[execution.QueryGroupIdentity]execution.ObjectDigest{"qg-before": "digest-before"}},
			state: StateCarryGroupChanged},
		{name: "the group was there without its object", content: activatedContent{groups: map[execution.QueryGroupIdentity]QueryGroup{group: {Identity: group}},
			digests: map[execution.QueryGroupIdentity]execution.ObjectDigest{}},
			state: StateCarryPreviousUnreadable},
	} {
		t.Run(test.name, func(t *testing.T) {
			if carry, outcome := reconciler.stateCarry(context.Background(), previous, test.content, current); carry != nil || outcome != test.state {
				t.Fatalf("state carry %+v %s, want nothing carried and %s", carry, outcome, test.state)
			}
			if _, _, _, err := reconciler.previousPlan(context.Background(), previous, test.content, group); err == nil {
				t.Fatal("previousPlan found a Plan in content that holds none")
			}
		})
	}
}
