// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package linkdoutput

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// consumerOutcome is what the alert consumer does with one message against
// the alert it holds open on the series, reduced to the three outcomes this
// side has to reason about. It transcribes the consumer's lifecycle rule as
// read in its source: an evaluation ends the active alert only when its
// severity is the alert's own and its action is not triggered; a triggered
// evaluation more severe than the alert moves the alert up; every other
// evaluation is recorded as orphaned and changes nothing. It is a model of a
// peer's rule, kept to the one question the per-Level RECOVERY rests on:
// can a message end an alert of a severity it did not name.
type consumerOutcome string

const (
	consumerEnded     consumerOutcome = "ended"
	consumerUpgraded  consumerOutcome = "upgraded"
	consumerUnchanged consumerOutcome = "unchanged"
)

var severityRank = map[string]int{"critical": 0, "warning": 1, "info": 2}

func applyToActiveAlert(t *testing.T, payload []byte, active string) consumerOutcome {
	t.Helper()
	var message struct {
		Evaluations []wireEvaluation `json:"evaluations"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatal(err)
	}
	for _, evaluation := range message.Evaluations {
		if evaluation.Action == ActionTriggered && severityRank[evaluation.Severity] < severityRank[active] {
			return consumerUpgraded
		}
	}
	for _, evaluation := range message.Evaluations {
		if evaluation.Severity == active && evaluation.Action != ActionTriggered {
			return consumerEnded
		}
	}
	return consumerUnchanged
}

// A RECOVERY decided for one Level beside another that is unavailable, NORMAL
// with a triggering window in its recovery span, or ABNORMAL: the message
// names the recovered Level alone as resolved, so against an open alert at the
// other Level's severity it ends nothing, and against one at its own severity
// it ends that alert. Beside an ABNORMAL Level the same message also carries
// the trigger, and the open alert at the recovered Level is moved up rather
// than ended. This is the whole safety argument for releasing a RECOVERY
// without waiting for the other Levels.
func TestARecoveryForOneLevelEndsOnlyThatLevelsAlert(t *testing.T) {
	recovered := contract.LevelResultV1{LevelID: 2, Priority: 1, Result: contract.LevelResultRecovery}
	for _, test := range []struct {
		name   string
		other  contract.LevelResultV1
		kind   string
		atL1   consumerOutcome // the open alert stands at the other Level
		atL2   consumerOutcome // the open alert stands at the recovered Level
		wanted string
	}{
		{
			name: "beside an unavailable Level", kind: contract.TriggerEventRecovery,
			other: contract.LevelResultV1{LevelID: 1, Priority: 0, Result: contract.LevelResultUnavailable},
			atL1:  consumerUnchanged, atL2: consumerEnded,
			wanted: `[{"severity":"warning","action":"resolved","action_reason":""}]`,
		},
		{
			name: "beside a NORMAL Level still inside its recovery span", kind: contract.TriggerEventRecovery,
			other: contract.LevelResultV1{LevelID: 1, Priority: 0, Result: contract.LevelResultNormal},
			atL1:  consumerUnchanged, atL2: consumerEnded,
			wanted: `[{"severity":"warning","action":"resolved","action_reason":""}]`,
		},
		{
			name: "beside an ABNORMAL Level", kind: contract.TriggerEventAbnormal,
			other: contract.LevelResultV1{LevelID: 1, Priority: 0, Result: contract.LevelResultAbnormal,
				DecisionWindow: contract.DecisionWindowV1{Trigger: contract.TriggerWindowEvidenceV1{
					WindowSize: 5, RequiredAnomalies: 2, ObservedAnomalies: 2, AnomalyBeginTime: 1756684740,
				}}},
			atL1: consumerUnchanged, atL2: consumerUpgraded,
			wanted: `[{"severity":"critical","action":"triggered","action_reason":""},` +
				`{"severity":"warning","action":"resolved","action_reason":""}]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := decision(func(event *contract.TriggerEventV1) {
				event.EventKind = test.kind
				event.PrimaryLevelID = 2
				if test.kind == contract.TriggerEventAbnormal {
					event.PrimaryLevelID = 1
				}
				event.LevelResults = []contract.LevelResultV1{test.other, recovered}
			})
			written := convertRaw(t, event)
			message := convert(t, event)
			if string(message["evaluations"]) != test.wanted {
				t.Fatalf("evaluations = %s, want %s", message["evaluations"], test.wanted)
			}
			if got := applyToActiveAlert(t, written.Payload, "critical"); got != test.atL1 {
				t.Fatalf("against an open alert at the other Level: %s, want %s", got, test.atL1)
			}
			if got := applyToActiveAlert(t, written.Payload, "warning"); got != test.atL2 {
				t.Fatalf("against an open alert at the recovered Level: %s, want %s", got, test.atL2)
			}
		})
	}
}
