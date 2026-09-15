// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package shadow

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type goldenCase struct {
	Name           string           `json:"name"`
	Python         EventView        `json:"python"`
	Go             EventView        `json:"go"`
	WantVerdict    Verdict          `json:"want_verdict"`
	WantReason     DifferenceReason `json:"want_reason"`
	WantGoSiblings int              `json:"want_go_siblings"`
	WantPrimaryID  uint32           `json:"want_primary_level_id"`
}

func TestG1CrossChainProjectionGolden(t *testing.T) {
	payload, err := os.ReadFile("testdata/g1_projection_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(payload, &cases); err != nil {
		t.Fatal(err)
	}

	for _, testCase := range cases {
		t.Run(testCase.Name, func(t *testing.T) {
			pythonProjection, err := Project(testCase.Python)
			if err != nil {
				t.Fatalf("project Python event: %v", err)
			}
			goProjection, err := Project(testCase.Go)
			if err != nil {
				t.Fatalf("project Go event: %v", err)
			}
			comparison, err := CompareAbnormal(pythonProjection, goProjection)
			if err != nil {
				t.Fatalf("compare abnormal: %v", err)
			}
			if comparison.Verdict != testCase.WantVerdict || comparison.Reason != testCase.WantReason {
				t.Fatalf("comparison = (%s, %s), want (%s, %s)", comparison.Verdict, comparison.Reason, testCase.WantVerdict, testCase.WantReason)
			}
			if len(goProjection.Siblings) != testCase.WantGoSiblings {
				t.Fatalf("Go sibling diagnostics = %d, want %d", len(goProjection.Siblings), testCase.WantGoSiblings)
			}
			if goProjection.Primary.LevelID != testCase.WantPrimaryID {
				t.Fatalf("Go primary level = %d, want %d", goProjection.Primary.LevelID, testCase.WantPrimaryID)
			}
		})
	}
}

func TestProjectTriggerEventV1AcceptsUnknownDynamicLevel(t *testing.T) {
	payload, err := os.ReadFile("../contract/testdata/go-v2/trigger_event_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	event, err := contract.DecodeTriggerEventV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := ProjectTriggerEventV1(ChainGo, *event)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Primary.LevelID != 5 || len(projection.Siblings) != 1 {
		t.Fatalf("projection primary/siblings = %d/%d, want 5/1", projection.Primary.LevelID, len(projection.Siblings))
	}
}

func TestMatchedEpisodeUsesOnlyEnvelopePrimary(t *testing.T) {
	opening, err := Project(eventView(ChainGo, contract.TriggerEventAbnormal, 100, []LevelView{
		{LevelID: 1, Priority: 1, Result: contract.LevelResultAbnormal},
	}, 1))
	if err != nil {
		t.Fatal(err)
	}
	episode, transition, err := ApplyMatchedEvent(Episode{}, opening)
	if err != nil {
		t.Fatal(err)
	}
	if transition != EpisodeOpened || !episode.Open {
		t.Fatalf("opening transition/state = %s/%t, want OPENED/true", transition, episode.Open)
	}

	repeatedWithSiblingRecovery, err := Project(eventView(ChainGo, contract.TriggerEventAbnormal, 160, []LevelView{
		{LevelID: 1, Priority: 1, Result: contract.LevelResultAbnormal},
		{LevelID: 5, Priority: 5, Result: contract.LevelResultRecovery},
	}, 1))
	if err != nil {
		t.Fatal(err)
	}
	next, transition, err := ApplyMatchedEvent(episode, repeatedWithSiblingRecovery)
	if err != nil {
		t.Fatal(err)
	}
	if transition != EpisodeUnchanged || !next.Open {
		t.Fatalf("repeat transition/state = %s/%t, want UNCHANGED/true", transition, next.Open)
	}
	if next.Anchor != episode.Anchor || next.PrimaryLevelID != episode.PrimaryLevelID {
		t.Fatalf("repeat changed episode anchor: %#v -> %#v", episode, next)
	}
}

func TestCompareAbnormalLeavesPriorityToConfigEligibility(t *testing.T) {
	python, err := Project(eventView(ChainPython, contract.TriggerEventAbnormal, 100, []LevelView{
		{LevelID: 1, Priority: 10, Result: contract.LevelResultAbnormal},
	}, 1))
	if err != nil {
		t.Fatal(err)
	}
	goEvent, err := Project(eventView(ChainGo, contract.TriggerEventAbnormal, 100, []LevelView{
		{LevelID: 1, Priority: 20, Result: contract.LevelResultAbnormal},
	}, 1))
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := CompareAbnormal(python, goEvent)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Verdict != VerdictMatch || comparison.Reason != "" {
		t.Fatalf("comparison = (%s, %s), want priority left to config eligibility", comparison.Verdict, comparison.Reason)
	}
}

func eventView(chain Chain, kind string, sourceTime int64, levels []LevelView, primary uint32) EventView {
	return EventView{
		Chain:                chain,
		NativeEventID:        string(chain) + "-event",
		NativeSemanticDigest: string(chain) + "-digest",
		Subject: Subject{
			TenantID: "default", BusinessID: "2", StrategyID: "1001",
			DimensionIdentityDigest: "dimension-1", SourceTime: sourceTime,
		},
		EventKind: kind, PrimaryLevelID: primary, Levels: levels,
	}
}
