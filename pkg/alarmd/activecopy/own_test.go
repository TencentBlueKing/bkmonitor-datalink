// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package activecopy

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The record of this process's own opens holds exactly the alerts this
// process opened on the consumer's protocol: not one it did not open, not
// one on the Python-compatible format. A RECOVERY at the level the alert
// stands at ends it; one without that level keeps it for RecoveredGrace;
// an ABNORMAL after a RECOVERY opens the alert again. The evaluator-driven
// cases are in package trigger (open_alert_gate_own_test.go).
func TestOwnPassesOnlyWhatThisProcessOpenedUntilItsRecovery(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	own := NewOwn(func() time.Time { return now })
	event := func(kind, fingerprint string) contract.TriggerEventV1 {
		result := contract.LevelResultAbnormal
		if kind == contract.TriggerEventRecovery {
			result = contract.LevelResultRecovery
		}
		return contract.TriggerEventV1{EventKind: kind, TenantID: "t", DedupeMD5: fingerprint, PrimaryLevelID: 1,
			LevelResults: []contract.LevelResultV1{{LevelID: 1, Priority: 1, Result: result}},
			StrategyRef:  &contract.StrategySnapshotRef{}, PlanRef: contract.RuntimePlanRefV1{StrategyID: "1"}}
	}
	// A RECOVERY that does not carry the standing level 1.
	beside := event(contract.TriggerEventRecovery, "a")
	beside.LevelResults[0].Result = contract.LevelResultUnavailable
	legacy := event(contract.TriggerEventAbnormal, "compat")
	legacy.LegacyOutput = &contract.LegacyEventContext{}
	own.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventAbnormal, "a"), legacy})
	if !own.Contains("t", "1", "a") || own.Contains("t", "1", "b") || own.Contains("t", "1", "compat") || own.Contains("t", "2", "a") {
		t.Fatal("the record must hold exactly the alert this process opened on the consumer's protocol")
	}
	own.Acknowledged([]contract.TriggerEventV1{beside})
	now = now.Add(RecoveredGrace)
	if !own.Contains("t", "1", "a") {
		t.Fatal("within the grace after its first RECOVERY the alert still passes")
	}
	now = now.Add(time.Second)
	if own.Contains("t", "1", "a") {
		t.Fatal("past the grace after its RECOVERY the alert is let go")
	}
	own.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventAbnormal, "a")})
	own.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventRecovery, "a")})
	if own.Contains("t", "1", "a") {
		t.Fatal("a RECOVERY at the standing level ends the alert")
	}
	own.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventAbnormal, "a")})
	now = now.Add(2 * RecoveredGrace)
	if !own.Contains("t", "1", "a") {
		t.Fatal("an ABNORMAL after a RECOVERY opens the alert again, with no grace running")
	}
	// A strategy tracked again after a gap starts from nothing.
	own.Track(StrategyKey{TenantID: "t", StrategyID: "1"})
	now = now.Add(untrackedGap + time.Second)
	own.Track(StrategyKey{TenantID: "t", StrategyID: "1"})
	if own.Contains("t", "1", "a") || own.Known() != 0 {
		t.Fatal("a strategy tracked again after the gap must start from nothing")
	}
}
