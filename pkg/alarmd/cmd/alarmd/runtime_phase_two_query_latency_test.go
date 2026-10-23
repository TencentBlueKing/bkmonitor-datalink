// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
)

var _ lookbackReadings = (*lookback.Engine)(nil)

// quickGroup reads as a lookback group whose whole first reads take 300 ms:
// its reread bound is RecheckTimeout all the same.
type quickGroup struct{}

func (quickGroup) QueryDuration(execution.QueryGroupIdentity) (time.Duration, bool) {
	return 300 * time.Millisecond, true
}

func (quickGroup) RereadBound(execution.QueryGroupIdentity) (time.Duration, bool) {
	return lookback.RecheckTimeout, true
}

// The slot source judges whether a round still has time by what the group's
// query takes. A quick group with two seconds left reads in time and is not
// sent to replay; judged by the reread bound instead, every such round of it
// would be.
func TestAQuickGroupWithTwoSecondsLeftIsNotDiverted(t *testing.T) {
	latency := queryLatencyOf(quickGroup{})
	bound, ok := latency("qg")
	if !ok || bound != 300*time.Millisecond {
		t.Fatalf("the slot source reads %v (%v), want the 300ms query duration", bound, ok)
	}
	start := time.Unix(1_700_000_000, 0)
	if execution.StartsTooLate(start.UnixMilli(), start.Add(2*time.Second).UnixMilli(), bound) {
		t.Fatal("a round with two seconds left for a 300ms query is diverted")
	}
	if lookbackQueryLatency(nil) != nil {
		t.Fatal("no lookback has a reading")
	}
}
