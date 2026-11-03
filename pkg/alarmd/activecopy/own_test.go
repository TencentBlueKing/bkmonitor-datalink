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

// The record of this process's own opens answers as the self-maintained
// gate did: an alert this process opened passes its RECOVERY - every round
// of the grace after the first, so each Level's RECOVERY has its turn - and
// is let go RecoveredGrace after it; an alert it did not open, or one on the
// Python-compatible format, is never in it; an ABNORMAL after a RECOVERY
// opens the alert again.
func TestOwnPassesOnlyWhatThisProcessOpenedUntilTheGraceAfterItsRecovery(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	own := NewOwn(func() time.Time { return now })
	event := func(kind, fingerprint string) contract.TriggerEventV1 {
		return contract.TriggerEventV1{EventKind: kind, TenantID: "t", DedupeMD5: fingerprint,
			StrategyRef: &contract.StrategySnapshotRef{}, PlanRef: contract.RuntimePlanRefV1{StrategyID: "1"}}
	}
	legacy := event(contract.TriggerEventAbnormal, "compat")
	legacy.LegacyOutput = &contract.LegacyEventContext{}
	own.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventAbnormal, "a"), legacy})
	if !own.Contains("t", "1", "a") || own.Contains("t", "1", "b") || own.Contains("t", "1", "compat") || own.Contains("t", "2", "a") {
		t.Fatal("the record must hold exactly the alert this process opened on the consumer's protocol")
	}
	own.Acknowledged([]contract.TriggerEventV1{event(contract.TriggerEventRecovery, "a")})
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
