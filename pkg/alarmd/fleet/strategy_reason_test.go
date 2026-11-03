// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The expected sentences below are written out from what an operator was
// told the page should say, not from the templates: a strategy whose data
// arrives 66 s late and is held 56 s reads whole and its edit is optional; a
// strategy whose data arrives 2176 s late against a 600 s limit is read
// incomplete, and the sentence gives both ways out with the cost of the
// longer time_delay.
const (
	heldSentence = "数据最晚约在窗口结束后 66 秒到齐，alarmd 自动推后约 56 秒再读，读到的数据完整、结果可信；" +
		"如把策略 time_delay 从 0 秒（或未设置）改为 60 秒，alarmd 就不必再自动推后（可选）"
	atLimitSentence = "数据最晚约在窗口结束后 2176 秒（约 36 分钟）才到齐，超过 alarmd 最多推后的 600 秒（10 分钟），" +
		"每轮读到的都是不完整的数据，检测结果不能采信。两条路：数据负责人查数据为什么晚到这么久；或者接受延迟，" +
		"把策略 time_delay 从 60 秒改为 2160 秒（36 分钟），代价是告警要在数据时间之后约 36 分钟才发出"
)

// heldBy is the read-held column a replica publishes for one object, made
// the way production makes it: the tracker saw the strategy complete a round
// on the object, and the hold is in force.
func heldBy(t *testing.T, replica, queryGroup, strategy string, hold ReadHoldFacts) (map[string]ReadHoldFacts, []Anomaly) {
	t.Helper()
	tracker := NewTracker(nil, replica, (&clock{at: now}).Now)
	tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
		ExecuteOutcome: "COMPLETED", Result: observability.ResultSuccess,
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: strategy, BusinessID: "2", EvaluationTime: now.Unix()},
	})
	holds := map[string]ReadHoldFacts{queryGroup: hold}
	return holds, tracker.ReadHeld(holds)
}

// reasonRig is the deployment the reason tests read: 4101 held below its
// limit on pod-b's object, 4180 held at its limit on pod-a's other object,
// 4178 withheld for a retired source, 4115 and 4109 withheld by the source
// as incomplete (one reason worded, one not), 4103 detecting under no check,
// and 5001 a strategy the publication does not hold.
func reasonRig(t *testing.T) *diagnosisRig {
	t.Helper()
	facts := diagnosisFacts()
	pub := StrategyPublication{SnapshotRevision: "s1", Epoch: 7}
	facts["4180"] = StrategyLookupFacts{Available: true, Found: true, Publication: pub,
		Plans:        plans(StrategyPlanRef{Tenant: "default", Business: "2", QueryGroup: "qg-other", SnapshotRevision: "s1", QueryRevision: "q3", ScheduleRevision: "r1"}),
		Dispositions: []StrategyDisposition{{Scope: "PLAN", Disposition: "ACCEPTED"}}}
	facts["4178"] = StrategyLookupFacts{Available: true, Found: true, Publication: pub, Dispositions: []StrategyDisposition{
		{Scope: "PLAN", Disposition: "UNSUPPORTED_PHASE2_CAPABILITY", Reason: "QUERY_SOURCE_RETIRED", FieldPath: "items[0].query_configs[0]"}}}
	facts["4115"] = StrategyLookupFacts{Available: true, Found: true, Publication: pub, Dispositions: []StrategyDisposition{
		{Scope: "STRATEGY", Disposition: "SOURCE_INCOMPLETE", Reason: "SOURCE_OBJECT_INCOMPLETE"}}}
	snapshots := healthySnapshots()
	snapshots[0].Owned, snapshots[0].Determined = 2, 2
	snapshots[0].OwnedObjects = []string{"qg-4101-a", "qg-other"}
	snapshots[0].ReadHolds, snapshots[0].ReadHeld = heldBy(t, "pod-a", "qg-other", "4180", ReadHoldFacts{
		Millis: 600_000, LimitMillis: 600_000, ArrivalAgeMillis: 2_176_000, AtLimit: true, HeldSince: now.Add(-3 * 24 * time.Hour).Unix(),
		DelaySeconds: 60, SuggestedDelaySeconds: 2160})
	snapshots[1].Owned, snapshots[1].Determined = 1, 1
	snapshots[1].OwnedObjects = []string{"qg-4101-b"}
	snapshots[1].ReadHolds, snapshots[1].ReadHeld = heldBy(t, "pod-b", "qg-4101-b", "4101", ReadHoldFacts{
		Millis: 56_000, ArrivalAgeMillis: 66_000, HeldSince: now.Add(-time.Hour).Unix(), DelaySeconds: 0, SuggestedDelaySeconds: 60})
	rig := newDiagnosisRigFrom(t, facts, nil, snapshots)
	rig.universe = []string{"4101", "4180", "4178", "4115", "4109", "4103", "5001"}
	return rig
}

// The board's row says the cause in one sentence with its numbers and how
// urgent acting is: the held strategy detects correctly and its edit is
// optional; the one held at its limit is read incomplete and needs a hand,
// with both ways out and the cost; the facts beside each sentence are the
// replica's own numbers, on the wire under the names the page reads.
func TestTheBoardRowSaysTheCauseWithItsNumbers(t *testing.T) {
	rig := reasonRig(t)
	code, board, raw := rig.board(t, "board=1")
	if code != http.StatusOK {
		t.Fatalf("board %d %v", code, raw)
	}
	rows := map[string]BoardRow{}
	for _, row := range board.Rows {
		rows[row.StrategyID] = row
	}
	held := rows["4101"]
	if held.Urgency != UrgencyOptional || held.UrgencyWord != "可选优化" || held.Reason == nil ||
		held.Reason.Code != string(CheckReadHeld) || held.Reason.Sentence != heldSentence {
		t.Fatalf("held row %+v reason %+v, want optional with %q", held, held.Reason, heldSentence)
	}
	if facts := held.Reason.Facts; facts == nil || facts.ReadHold == nil || *facts.ReadHold != (ReadHoldReason{
		ArrivalLatestSeconds: 66, HoldSeconds: 56, TimeDelaySeconds: 0, SuggestedTimeDelaySeconds: 60}) {
		t.Fatalf("held facts %+v", held.Reason.Facts)
	}
	limit := rows["4180"]
	if limit.Urgency != UrgencyActionNeeded || limit.Reason == nil || limit.Reason.Code != string(CheckReadBeforeComplete) ||
		limit.Reason.Sentence != atLimitSentence {
		t.Fatalf("at-limit row %+v reason %+v, want action needed with %q", limit, limit.Reason, atLimitSentence)
	}
	for _, wire := range raw["rows"].([]any) {
		row := wire.(map[string]any)
		if row["strategy_id"] != "4180" {
			continue
		}
		hold := row["reason"].(map[string]any)["facts"].(map[string]any)["read_hold"].(map[string]any)
		if hold["arrival_latest_seconds"] != 2176.0 || hold["hold_limit_seconds"] != 600.0 || hold["at_limit"] != true ||
			hold["time_delay_seconds"] != 60.0 || hold["suggested_time_delay_seconds"] != 2160.0 || row["urgency"] != "ACTION_NEEDED" {
			t.Fatalf("at-limit row on the wire %v", row)
		}
	}
	// The drawer's conclusion is the same row, read from the same computation.
	view := rig.view(t, "4180")
	if view.Row.Reason == nil || view.Row.Reason.Sentence != atLimitSentence || view.Row.Urgency != UrgencyActionNeeded {
		t.Fatalf("view row %+v reason %+v", view.Row, view.Row.Reason)
	}
	if composed := ComposeStrategyView(view, nil); composed.Conclusion.Reason == nil || composed.Conclusion.Reason.Sentence != atLimitSentence {
		t.Fatalf("composed conclusion %+v", composed.Conclusion)
	}
}

// A withheld strategy's reason is its own reason's meaning and next step,
// not the state's: the retired source says what replaced it and what to
// rewrite into, never "nothing to do"; the incomplete source document names
// the cache and who writes it, not the developer's word for that writer; a
// reason no table words is named as such, beside the check's own sentence.
func TestAWithheldStrategySaysItsOwnReason(t *testing.T) {
	rig := reasonRig(t)
	_, board, _ := rig.board(t, "board=1")
	rows := map[string]BoardRow{}
	for _, row := range board.Rows {
		rows[row.StrategyID] = row
	}
	retired := rows["4178"]
	if retired.Urgency != UrgencyActionNeeded || retired.Reason == nil || retired.Reason.Code != string(CheckConfigRejected) ||
		!strings.Contains(retired.Reason.Sentence, "GSE 系统事件") || !strings.Contains(retired.Reason.Sentence, "改写为自定义事件策略") ||
		strings.Contains(retired.Reason.Sentence, "无事可做") {
		t.Fatalf("retired row %+v reason %+v", retired, retired.Reason)
	}
	if withheld := retired.Reason.Facts.Withheld; withheld == nil || withheld.Reason != "QUERY_SOURCE_RETIRED" ||
		withheld.Disposition != "UNSUPPORTED_PHASE2_CAPABILITY" || withheld.Kind != WithheldStrategyDefinition {
		t.Fatalf("retired facts %+v", retired.Reason.Facts)
	}
	incomplete := rows["4115"]
	if incomplete.Reason == nil || incomplete.Reason.Code != string(CheckSourceIncomplete) ||
		!strings.Contains(incomplete.Reason.Sentence, "策略缓存") || !strings.Contains(incomplete.Reason.Sentence, "补写这条策略") ||
		strings.Contains(incomplete.Reason.Sentence, "缓存写入方") {
		t.Fatalf("incomplete row reason %+v", incomplete.Reason)
	}
	unworded := rows["4109"]
	if unworded.Reason == nil || !strings.HasSuffix(unworded.Reason.Sentence, "（原因 EFFECTIVE_TIME_SNAPSHOT_UNAVAILABLE）") ||
		unworded.Reason.Facts.Withheld.Kind != WithheldUnknownReason {
		t.Fatalf("unworded row reason %+v", unworded.Reason)
	}
}

// Urgency is a facet over the whole universe and the board's first order:
// what needs a hand first, then what cannot be told, then the optional, then
// the quiet; a strategy detecting under no check carries no reason, and one
// the publication does not hold says why its standing is unknown. The facet
// filters, and a word outside the list is refused rather than selecting
// nothing.
func TestUrgencyIsAFacetAndTheBoardsFirstOrder(t *testing.T) {
	rig := reasonRig(t)
	_, board, _ := rig.board(t, "board=1")
	want := map[Urgency]int{UrgencyActionNeeded: 4, UrgencyUnknown: 1, UrgencyOptional: 1, UrgencyNone: 1}
	for urgency, count := range want {
		if board.Facets.Urgency[urgency] != count {
			t.Errorf("facet %s = %d, want %d (%v)", urgency, board.Facets.Urgency[urgency], count, board.Facets.Urgency)
		}
	}
	var order []string
	for _, row := range board.Rows {
		order = append(order, string(row.Urgency))
	}
	if got := strings.Join(order, ","); got != "ACTION_NEEDED,ACTION_NEEDED,ACTION_NEEDED,ACTION_NEEDED,UNKNOWN,OPTIONAL,NONE" {
		t.Fatalf("rows in urgency order %s", got)
	}
	for _, row := range board.Rows {
		switch row.StrategyID {
		case "4103":
			if row.Reason != nil || row.Urgency != UrgencyNone {
				t.Errorf("detecting row %+v carries reason %+v", row, row.Reason)
			}
		case "5001":
			if row.Reason == nil || row.Reason.Code != UnknownNotYetPublished || !strings.Contains(row.Reason.Sentence, "还没有它") {
				t.Errorf("unpublished row reason %+v", row.Reason)
			}
		}
	}
	_, optional, _ := rig.board(t, "board=1&urgency=OPTIONAL")
	if optional.Matched != 1 || optional.Rows[0].StrategyID != "4101" || optional.Facets.Urgency[UrgencyActionNeeded] != 4 {
		t.Fatalf("optional page %+v, want 4101 alone with the facet over the universe", optional)
	}
	if code, _, raw := rig.board(t, "board=1&urgency=SOON"); code != http.StatusBadRequest || raw["facet"] != "urgency" {
		t.Fatalf("unknown urgency answered %d %v", code, raw)
	}
}

// Urgency by the decided words, case by case: a check that only spares
// alarmd a cost is optional whatever its owner word; no action and a wait
// ask nothing; a standing nobody could read is unknown, not needed and not
// quiet; every other owner word asks a hand.
func TestUrgencyFollowsWhetherTheResultsAreRight(t *testing.T) {
	cases := []struct {
		row  DiagnosisRow
		want Urgency
	}{
		{DiagnosisRow{Verdict: StateDetecting, Action: ActionStrategyEdit, Check: CheckReadHeld}, UrgencyOptional},
		{DiagnosisRow{Verdict: StateResultUntrusted, Action: ActionStrategyEdit, Check: CheckReadBeforeComplete}, UrgencyActionNeeded},
		{DiagnosisRow{Verdict: StateDataAbsent, Action: ActionDataCheck, Check: CheckNoDataPersistent}, UrgencyActionNeeded},
		{DiagnosisRow{Verdict: StateNotDetecting, Action: ActionCacheWriterFill, Check: CheckSourceIncomplete}, UrgencyActionNeeded},
		{DiagnosisRow{Verdict: StateResultUntrusted, Action: ActionWatch, Check: CheckObservationGap}, UrgencyNone},
		{DiagnosisRow{Verdict: StateDetecting, Action: ActionNone, Check: CheckQuiet}, UrgencyNone},
		{DiagnosisRow{Verdict: StateDetecting, Action: ActionNone}, UrgencyNone},
		{DiagnosisRow{Verdict: DiagnosisUnknown, Reason: UnknownSnapshotStale}, UrgencyUnknown},
	}
	for _, c := range cases {
		if got := urgencyOf(c.row); got != c.want {
			t.Errorf("%s/%s/%s: urgency %s, want %s", c.row.Verdict, c.row.Action, c.row.Check, got, c.want)
		}
	}
}

// Every check the fleet files a row under has a sentence, so a reason is
// never blank; none of them names a developer coordinate, which the page
// does not show; a check that speaks in numbers says when it had none, and
// one that never does says nothing of numbers.
func TestEveryCheckHasASentenceAndNoneNamesACoordinate(t *testing.T) {
	forbidden := []string{"查询组", "Slot", "副本", "修订", "query_group", "replica"}
	sentences := []string{}
	for _, check := range checkOrder {
		sentence, known := checkSentences[check]
		if !known || sentence == "" {
			t.Errorf("check %s has no sentence", check)
		}
		sentences = append(sentences, sentence)
	}
	for _, words := range sourceReasonWords {
		sentences = append(sentences, words.What, words.Next)
	}
	for _, reason := range []string{UnknownLookupUnavailable, UnknownNotYetPublished, UnknownObjectNotObserved, UnknownReplicaUnreadable, UnknownSnapshotStale} {
		sentence, known := unknownSentences[reason]
		if !known {
			t.Errorf("unknown reason %s has no sentence", reason)
		}
		sentences = append(sentences, sentence)
	}
	for _, sentence := range sentences {
		for _, word := range forbidden {
			if strings.Contains(sentence, word) {
				t.Errorf("sentence %q names %q", sentence, word)
			}
		}
	}
	bare := strategyReasonOf(DiagnosisRow{Verdict: StateDetecting, Action: ActionStrategyEdit, Check: CheckReadHeld}, now)
	if !strings.HasSuffix(bare.Sentence, "（这次没有读到具体数字）") || bare.Facts != nil {
		t.Errorf("held reason without its row %+v, want the numbers named as missing", bare)
	}
	quiet := strategyReasonOf(DiagnosisRow{Verdict: StateDetecting, Action: ActionNone, Check: CheckQuiet}, now)
	if strings.Contains(quiet.Sentence, "数字") {
		t.Errorf("quiet reason %q speaks of numbers", quiet.Sentence)
	}
}

// The rest of the facts a sentence is written from, each from the deciding
// row's own: how long the data has been absent and whether that is a lower
// bound; the run of empty rounds and whose to look at; the refusal's code;
// the worst window's pair; the lookback's samples and suggestion.
func TestTheOtherChecksWriteTheirSentenceFromTheirRowsFacts(t *testing.T) {
	at := now
	reason := func(check Check, row Anomaly) *StrategyReason {
		words := checkWords[check]
		return strategyReasonOf(DiagnosisRow{Verdict: words.State, Action: words.Action, Check: check, deciding: &row}, at)
	}
	absent := reason(CheckNoDataPersistent, Anomaly{Kind: KindNoData, Since: at.Add(-3 * 24 * time.Hour), SinceFrom: SinceProcessStart})
	if absent.Sentence != "查询正常返回，但已经约 3 天没有数据（只会更久）；数据负责人查数据为什么停了（采集、上报或入库）" ||
		absent.Facts.DataAbsent.AbsentSeconds != 3*24*3600 || !absent.Facts.DataAbsent.SinceIsLowerBound {
		t.Errorf("absent %+v %+v", absent, absent.Facts.DataAbsent)
	}
	exact := reason(CheckNoDataPersistent, Anomaly{Kind: KindNoData, Since: at.Add(-90 * time.Minute), SinceFrom: SinceBusinessState})
	if !strings.Contains(exact.Sentence, "已经约 90 分钟没有数据；") || exact.Facts.DataAbsent.SinceIsLowerBound {
		t.Errorf("exact absent %+v", exact)
	}
	empty := reason(CheckEmptyEveryRound, Anomaly{Kind: KindEmptyEveryRound,
		EmptyEveryRound: &EmptyEveryRoundFacts{Rounds: 240, Since: at.Add(-4 * time.Hour), NeverSawData: true}})
	if !strings.HasPrefix(empty.Sentence, "每轮查询都正常返回但没有数据，已连续 240 轮（约 4 小时），从没查到过数据；策略负责人") ||
		empty.Facts.DataAbsent.Rounds != 240 {
		t.Errorf("empty %+v", empty)
	}
	target := reason(CheckEmptyAfterTarget, Anomaly{Kind: KindEmptyEveryRound,
		EmptyEveryRound: &EmptyEveryRoundFacts{Rounds: 60, Since: at.Add(-time.Hour), SinceIsLowerBound: true, Cause: EmptyEveryRoundCauseOutsideTarget}})
	if !strings.HasPrefix(target.Sentence, "查询返回了数据，但监控目标一条也没选中，已连续 60 轮（约 60 分钟，只会更久）；") {
		t.Errorf("outside target %+v", target)
	}
	refused := reason(CheckQueryRefused, Anomaly{Kind: KindBlockedRun, Failure: &FailureRef{Code: "QUERY_TOO_LARGE", Text: "internal detail"}})
	if !strings.HasSuffix(refused.Sentence, "（查询服务答复 QUERY_TOO_LARGE）") || refused.Facts.Refusal.Code != "QUERY_TOO_LARGE" ||
		strings.Contains(refused.Sentence, "internal detail") {
		t.Errorf("refused %+v", refused)
	}
	window := reason(CheckWindowUndecided, Anomaly{Kind: KindDegradedRun, Coverage: &HistoryCoverage{Levels: 4, Short: 2, WorstValid: 3, WorstRequired: 5}})
	if !strings.HasSuffix(window.Sentence, "（最差的窗口只有 3 个有效点，要 5 个）") || *window.Facts.Window != (WindowReason{WorstValid: 3, WorstRequired: 5}) {
		t.Errorf("window %+v", window)
	}
	early := reason(CheckReadBeforeComplete, Anomaly{Kind: KindReadBeforeComplete, ReadEarly: &ReadEarlyFacts{CurrentDelaySeconds: 60,
		SuggestedDelaySeconds: 180, Samples: []ReadEarlySample{{EvaluationTime: 1}, {EvaluationTime: 2}}}})
	if early.Sentence != "最近 2 次抽样里，数据在 alarmd 读取之后又有新增或改动，每轮读到的数据还不全，检测结果不能采信；"+
		"策略负责人把 time_delay 从 60 秒改为 180 秒（3 分钟）即可读全，代价是告警要在数据时间之后约 3 分钟才发出" {
		t.Errorf("read early %q", early.Sentence)
	}
}

// A strategy that runs, decided by one of its objects' rows, says that row's
// cause even when a disposition rides beside it -- a level the source
// rejected while the rest of the strategy runs: the disposition decides the
// sentence only when the words were decided under its own check.
func TestARunningStrategySaysItsRowsCauseNotADispositionBesideIt(t *testing.T) {
	hold := ReadHoldFacts{Millis: 56_000, ArrivalAgeMillis: 66_000, HeldSince: 1, DelaySeconds: 0, SuggestedDelaySeconds: 60}
	decided := DiagnosisRow{Verdict: StateDetecting, Action: ActionStrategyEdit, Check: CheckReadHeld,
		Dispositions: []DiagnosisDisposition{{StrategyDisposition: StrategyDisposition{Scope: "LEVEL", LevelID: 2,
			Disposition: "CONFIG_REJECTED", Reason: "LEVEL_INVALID"}}},
		deciding: &Anomaly{Kind: KindReadHeld, ReadHold: &hold}}
	reason := strategyReasonOf(decided, now)
	if reason.Sentence != heldSentence || reason.Facts.Withheld != nil || reason.Facts.ReadHold == nil {
		t.Fatalf("reason %+v, want the held row's sentence %q", reason, heldSentence)
	}
	decided.Check = CheckConfigRejected
	if reason := strategyReasonOf(decided, now); reason.Facts.Withheld == nil || !strings.HasSuffix(reason.Sentence, "（原因 LEVEL_INVALID）") {
		t.Fatalf("reason under the disposition's own check %+v, want the disposition's", reason)
	}
}

// A check whose owner the evidence does not decide (check.go files it
// UNDETERMINED) stays on this deployment's side of the page, but its sentence
// must not read as a verdict that alarmd is at fault: it says alarmd will find
// out whose it is. Before, WINDOW_UNDECIDED said "由本服务处理" for windows
// whose holes nobody could attribute.
func TestAnUndeterminedOwnersSentenceIsNotAVerdictOnThisService(t *testing.T) {
	undetermined := 0
	for check, answer := range checkAnswers {
		if answer.Owner != OwnerUndetermined {
			continue
		}
		undetermined++
		sentence := checkSentences[check]
		if strings.Contains(sentence, "本服务处理") {
			t.Errorf("%s says %q, a verdict on this service", check, sentence)
		}
		if strings.Count(sentence, "本服务故障") != strings.Count(sentence, "不是已知的本服务故障") {
			t.Errorf("%s names a fault of this service as known: %q", check, sentence)
		}
		if check != CheckConfigUnresolved && !strings.Contains(sentence, "由 alarmd 维护方查明是谁的") {
			t.Errorf("%s does not say who finds out: %q", check, sentence)
		}
	}
	if undetermined == 0 {
		t.Fatal("no check is filed UNDETERMINED; the table this test reads has moved")
	}
}

// A replica whose clock runs ahead puts a data-absent start after the moment
// the board is read. The sentence then gives no span rather than a negative
// one, and the facts carry no absent seconds.
func TestADataAbsentStartAheadOfTheClockGivesNoSpan(t *testing.T) {
	at := time.Unix(1_800_000_000, 0).UTC()
	reason := func(check Check, row Anomaly) *StrategyReason {
		words := checkWords[check]
		return strategyReasonOf(DiagnosisRow{Verdict: words.State, Action: words.Action, Check: check, deciding: &row}, at)
	}
	ahead := at.Add(3 * time.Second)
	for name, got := range map[string]*StrategyReason{
		"no data":     reason(CheckNoDataPersistent, Anomaly{Kind: KindNoData, Since: ahead, SinceFrom: SinceBusinessState}),
		"empty every": reason(CheckEmptyEveryRound, Anomaly{Kind: KindEmptyEveryRound, EmptyEveryRound: &EmptyEveryRoundFacts{Rounds: 3, Since: ahead}}),
	} {
		if got == nil {
			continue
		}
		if strings.Contains(got.Sentence, "-") || strings.Contains(got.Sentence, "约 0 秒") {
			t.Errorf("%s: sentence %q gives a span for a start ahead of the clock", name, got.Sentence)
		}
		if got.Facts != nil && got.Facts.DataAbsent != nil && got.Facts.DataAbsent.AbsentSeconds != 0 {
			t.Errorf("%s: absent seconds %d, want none", name, got.Facts.DataAbsent.AbsentSeconds)
		}
	}
	// One second behind is still a span: the clamp is at the start, not a margin.
	behind := reason(CheckNoDataPersistent, Anomaly{Kind: KindNoData, Since: at.Add(-90 * time.Second), SinceFrom: SinceBusinessState})
	if behind.Facts == nil || behind.Facts.DataAbsent == nil || behind.Facts.DataAbsent.AbsentSeconds != 90 {
		t.Fatalf("a start 90 s back = %+v, want 90 absent seconds", behind)
	}
}
