// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
)

// fixedJudge answers every verdict with verdict and records what it was
// asked.
type fixedJudge struct {
	verdict sourceliveness.Verdict
	judged  []sourceliveness.Subject
	ahead   []int64
	// aheadFrom is each probe ahead's last real point.
	aheadFrom []int64
}

func (judge *fixedJudge) Judge(subject sourceliveness.Subject, _ execution.QueryPlanFacts, _ int64) sourceliveness.Verdict {
	judge.judged = append(judge.judged, subject)
	return judge.verdict
}

func (judge *fixedJudge) Ahead(_ execution.QueryPlanFacts, lastRealPoint, judged, _ int64) {
	judge.ahead = append(judge.ahead, judged)
	judge.aheadFrom = append(judge.aheadFrom, lastRealPoint)
}

// An absent series whose no-data end is due asks its source first: not
// shown alive, nothing is ended or removed - the entry stays, counted - and
// alive or unsupported, it ends as configured. A series due on the next
// round has its source probed ahead; one due later asks nothing.
func TestAnAbsentSeriesEndsOnNoDataOnlyOnALiveSource(t *testing.T) {
	plan := internalCompiledPlanRecovering(t, "301", 1, 60, json.RawMessage(`{"enabled":true,"consecutive_windows":2,"missing_data":"evaluate_as_zero"}`))
	due := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "301"}, CompiledPlan: plan}
	const now = int64(1_788_000_000)
	dueNow, dueNext, later := noDataEndEntryFor("a", "fa", now-240), noDataEndEntryFor("b", "fb", now-180), noDataEndEntryFor("c", "fc", now-60)
	held := fakeOpenAlertJudge{held: map[string]bool{"fa": true, "fb": true, "fc": true}, judged: true}
	for _, test := range []struct {
		verdict sourceliveness.Verdict
		ends    bool
	}{
		{sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonEmptyWithoutStatus}, false},
		{sourceliveness.Verdict{Result: sourceliveness.ResultUnknown, Reason: sourceliveness.ReasonStatus}, false},
		{sourceliveness.Verdict{Result: sourceliveness.ResultAlive}, true},
		{sourceliveness.Verdict{Result: sourceliveness.ResultUnsupported, Reason: sourceliveness.ReasonPromQL}, true},
	} {
		judge := &fixedJudge{verdict: test.verdict}
		round := noDataEndPlan{loaded: execution.NoDataEndLoaded{Entries: []execution.NoDataEndEntry{dueNow, dueNext, later}}, complete: true,
			present: map[execution.SeriesIdentityDigest]struct{}{},
			source:  &noDataEndSource{judge: judge, found: true, plan: due.Identity, judged: now}}
		decided := decideNoDataEnds(due, round, nil, held, now, 10)
		if ended := decided.ends[dueNow.Series] != nil; ended != test.ends || len(decided.ends) > 1 || len(decided.write.Deletes) != 0 {
			t.Errorf("%+v: decided %+v, want ended %t and nothing removed", test.verdict, decided, test.ends)
		}
		if held := decided.sourceUnknown == 1; held == test.ends {
			t.Errorf("%+v: held %d, want one exactly when not ended", test.verdict, decided.sourceUnknown)
		}
		if len(judge.judged) != 1 || judge.judged[0].Series != dueNow.Series || judge.judged[0].NoDataSpanSeconds != 240 ||
			judge.judged[0].LastRealPoint != dueNow.LastRealPoint {
			t.Errorf("%+v: asked %+v, want the due series alone, with its 4-minute span", test.verdict, judge.judged)
		}
		if len(judge.ahead) != 1 || judge.ahead[0] != now || judge.aheadFrom[0] != dueNext.LastRealPoint {
			t.Errorf("%+v: ahead %v, want one probe for the series due next round", test.verdict, judge.ahead)
		}
	}
	// No judge: ends as configured.
	round := noDataEndPlan{loaded: execution.NoDataEndLoaded{Entries: []execution.NoDataEndEntry{dueNow}}, complete: true,
		present: map[execution.SeriesIdentityDigest]struct{}{}}
	if decided := decideNoDataEnds(due, round, nil, held, now, 10); decided.ends[dueNow.Series] == nil {
		t.Fatalf("without a judge: %+v, want ended", decided)
	}
}
