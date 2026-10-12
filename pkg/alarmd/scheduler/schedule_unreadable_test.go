// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A round blocked because the Query Group's schedule timeline does not
// decode - corrupt bytes, or a timeline a newer build wrote that this one
// cannot read - is named SCHEDULE_UNREADABLE; any other blocked read keeps
// BLOCKED_EXACT_SET_UNAVAILABLE. The schedule navigation's fail-closed path
// is where the decode error becomes a blocked read.
func TestARoundBlockedByAnUndecodableTimelineIsNamedSo(t *testing.T) {
	undecodable := failClosedScheduleNavigation(&controlplane.DeterministicScheduleError{Err: errors.New("unknown field")})
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"undecodable timeline", undecodable, contract.ReasonScheduleUnreadable},
		{"exact set unavailable", &SourceBlockedError{Err: errors.New("exact set unavailable")}, contract.ReasonBlockedExactSetUnavailable},
		{"schedule unavailable", failClosedScheduleNavigation(controlplane.ErrScheduleUnavailable), contract.ReasonBlockedExactSetUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := newMutableClock(time.Unix(200, 0))
			fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
			source := &fakeSlotSource{err: test.err}
			flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), clock.Now)
			if err != nil {
				t.Fatal(err)
			}
			runner, err := NewRunner("query-group-1", &fakeSession{fence: fence}, source, &blockingExecutor{}, flights, clock.Now)
			if err != nil {
				t.Fatal(err)
			}
			result, _, err := runner.RunOne(context.Background())
			if err != nil || string(result.ReasonCode) != test.want {
				t.Fatalf("RunOne() = (%+v, %v), want a round named %s", result, err, test.want)
			}
		})
	}
}
