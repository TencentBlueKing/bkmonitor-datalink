// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

// Each record's gate lands in exactly one count, or in none: held records by
// cause, a record sent past a Level without recovery on its own, and records
// that were neither add nothing.
func TestRecoveryGateCountsPartitionTheRecords(t *testing.T) {
	var counts execution.RecoveryGateCounts
	for _, gate := range []trigger.RecoveryGateV2{
		{Held: true, Cause: trigger.RecoveryHeldLevelUnavailable, LevelID: 1},
		{Held: true, Cause: trigger.RecoveryHeldLevelUnavailable, LevelID: 2},
		{Held: true, Cause: trigger.RecoveryHeldLevelRecovering, LevelID: 1},
		{PassedLevelWithoutRecovery: true},
		{PassedLevelWithoutRecovery: true},
		{PassedLevelWithoutRecovery: true},
		{},
		{},
	} {
		countRecoveryGate(&counts, gate)
	}
	want := execution.RecoveryGateCounts{HeldLevelUnavailable: 2, HeldLevelRecovering: 1, SentPastLevelWithoutRecovery: 3}
	if counts != want {
		t.Fatalf("counts = %+v, want %+v", counts, want)
	}
}
