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
	"fmt"
	"strings"
	"time"
)

// Why a strategy stands where it does, and whether anyone has to act on it.
//
// The board row and the drawer said "检测结果不能采信 · 策略负责人改" for a
// strategy whose data arrived 36 minutes late against a hold capped at 10,
// while the diagnosis view's strategy line already held that sentence with
// its numbers: the page had the state and the owner, never the cause. And
// "策略负责人改" sat in the same place on a strategy whose detection was right
// and whose owner could only save alarmd an automatic hold, so a healthy
// strategy read as broken and sorted ahead of real problems. The sentence is
// written here, once, from the facts the deciding row already carries -- the
// page lays it out and decides nothing -- and how urgent acting is is a field
// of its own beside the owner.

// Urgency is whether acting on a strategy's standing is needed, optional or
// not asked. Closed: the page's facet and words are held to this list.
type Urgency string

const (
	// UrgencyActionNeeded: the results are wrong or missing until someone
	// acts -- read before complete, data absent, configuration rejected.
	UrgencyActionNeeded Urgency = "ACTION_NEEDED"
	// UrgencyOptional: detection is right as it stands; acting only removes
	// a cost alarmd absorbs for the strategy, such as the automatic hold.
	UrgencyOptional Urgency = "OPTIONAL"
	// UrgencyNone: nothing to do, or a wait with nobody's hand in it.
	UrgencyNone Urgency = "NONE"
	// UrgencyUnknown: the standing could not be read, so neither could how
	// urgent it is. Counted apart rather than folded into either side.
	UrgencyUnknown Urgency = "UNKNOWN"
)

// Urgencies is the closed list, in the page's order: what needs a hand
// first, what cannot be told next, then the optional and the quiet.
var Urgencies = []Urgency{UrgencyActionNeeded, UrgencyUnknown, UrgencyOptional, UrgencyNone}

// optionalChecks are the checks under which the strategy detects correctly
// and the action only spares alarmd a cost: READ_HELD reads the data whole,
// later than the time_delay says, and the owner's edit only ends the hold.
var optionalChecks = map[Check]bool{CheckReadHeld: true}

// urgencyOf is the decided strategy's urgency: unknown with its standing,
// optional under an optional check whatever its owner word, nothing to do
// under no action or a wait, and needed otherwise.
func urgencyOf(decided DiagnosisRow) Urgency {
	switch {
	case decided.Verdict == DiagnosisUnknown:
		return UrgencyUnknown
	case optionalChecks[decided.Check]:
		return UrgencyOptional
	case decided.Action == "" || decided.Action == ActionNone || decided.Action == ActionWatch:
		return UrgencyNone
	}
	return UrgencyActionNeeded
}

// StrategyReason is the strategy's cause in one sentence the page shows as it
// is, the code it was decided under, and the numbers it was written from,
// typed, so the page can format units and a test can hold the sentence to
// them.
type StrategyReason struct {
	// Code is the check the words were decided under, or for a standing
	// that could not be read, the reason it could not (UnknownLookupUnavailable
	// and its siblings). Both are closed lists.
	Code     string       `json:"code"`
	Sentence string       `json:"sentence"`
	Facts    *ReasonFacts `json:"facts,omitempty"`
}

// ReasonFacts is the numbers a sentence was written from, by kind; only the
// kind the sentence used is present.
type ReasonFacts struct {
	ReadHold   *ReadHoldReason   `json:"read_hold,omitempty"`
	ReadEarly  *ReadEarlyReason  `json:"read_early,omitempty"`
	DataAbsent *DataAbsentReason `json:"data_absent,omitempty"`
	Window     *WindowReason     `json:"window,omitempty"`
	Refusal    *RefusalReason    `json:"refusal,omitempty"`
	Withheld   *WithheldReason   `json:"withheld,omitempty"`
}

// ReadHoldReason is a held object's hold, in seconds: how late the data has
// been seen to arrive -- the latest seen, an upper bound and not a typical
// value -- what alarmd holds and the most it will, the time_delay the query
// runs under and the one that would need no hold, net of the settling wait.
type ReadHoldReason struct {
	ArrivalLatestSeconds      int64 `json:"arrival_latest_seconds"`
	HoldSeconds               int64 `json:"hold_seconds"`
	HoldLimitSeconds          int64 `json:"hold_limit_seconds,omitempty"`
	AtLimit                   bool  `json:"at_limit,omitempty"`
	TimeDelaySeconds          int64 `json:"time_delay_seconds"`
	SuggestedTimeDelaySeconds int64 `json:"suggested_time_delay_seconds,omitempty"`
	SettlingWaitSeconds       int64 `json:"settling_wait_seconds,omitempty"`
}

// ReadEarlyReason is the late-data lookback's finding: in how many samples
// data changed after the read, and the time_delay that would have read them
// whole.
type ReadEarlyReason struct {
	Samples                   int   `json:"samples"`
	TimeDelaySeconds          int64 `json:"time_delay_seconds"`
	SuggestedTimeDelaySeconds int64 `json:"suggested_time_delay_seconds,omitempty"`
}

// DataAbsentReason is how long the rounds have answered without data, from
// when, and whether that start is only a lower bound.
type DataAbsentReason struct {
	Since             *time.Time `json:"since,omitempty"`
	AbsentSeconds     int64      `json:"absent_seconds,omitempty"`
	SinceIsLowerBound bool       `json:"since_is_lower_bound,omitempty"`
	Rounds            int        `json:"rounds,omitempty"`
	NeverSawData      bool       `json:"never_saw_data,omitempty"`
}

// WindowReason is the worst short window's pair: the valid positions it held
// against those its algorithm requires.
type WindowReason struct {
	WorstValid    uint32 `json:"worst_valid"`
	WorstRequired uint32 `json:"worst_required"`
}

// RefusalReason is the code the query service refused the query under.
type RefusalReason struct {
	Code string `json:"code"`
}

// WithheldReason is the source's disposition of a strategy it did not run
// as written, its reason word, and who that reason is for.
type WithheldReason struct {
	Disposition string       `json:"disposition"`
	Reason      string       `json:"reason,omitempty"`
	Kind        WithheldKind `json:"kind"`
}

// strategyReasonOf is the decided strategy's reason at the board's instant,
// nil exactly when there is nothing to say: a strategy that detects under no
// check.
func strategyReasonOf(decided DiagnosisRow, at time.Time) *StrategyReason {
	if decided.Verdict == DiagnosisUnknown {
		return &StrategyReason{Code: decided.Reason, Sentence: unknownSentence(decided.Reason)}
	}
	if decided.Check == "" {
		return nil
	}
	reason := &StrategyReason{Code: string(decided.Check)}
	if disposition, filed := dispositionUnder(decided); filed {
		reason.Sentence = withheldSentence(decided.Check, disposition.Disposition, disposition.Reason)
		reason.Facts = &ReasonFacts{Withheld: &WithheldReason{Disposition: disposition.Disposition, Reason: disposition.Reason,
			Kind: withheldKindOf(disposition.Reason)}}
		return reason
	}
	if row := decided.deciding; row != nil {
		if sentence, facts, given := factSentence(decided.Check, *row, at); given {
			reason.Sentence, reason.Facts = sentence, facts
			return reason
		}
	}
	reason.Sentence = checkSentence(decided.Check)
	if factChecks[decided.Check] {
		// The check speaks in numbers and the deciding row carried none --
		// a summary cut, a row from an older build: say so rather than pass
		// the numberless sentence off as the whole answer.
		reason.Sentence += "（这次没有读到具体数字）"
	}
	return reason
}

// dispositionUnder is the first of the strategy's dispositions filed under
// the check its words were decided under: the withheld item that decided a
// withheld strategy, or the note on a detecting one.
func dispositionUnder(decided DiagnosisRow) (DiagnosisDisposition, bool) {
	for _, disposition := range decided.Dispositions {
		if check, known := sourceCheckOf(disposition.Disposition, disposition.Reason); known && check == decided.Check {
			return disposition, true
		}
	}
	return DiagnosisDisposition{}, false
}

// sourceReasonWords are the reasons the source files under SOURCE_INCOMPLETE,
// which withheldReasonWords (the capability disposition's) does not hold: the
// writer they name is the platform job that writes the strategy cache.
var sourceReasonWords = map[string]WithheldReasonWords{
	"SOURCE_OBJECT_INCOMPLETE": {Kind: WithheldWriterAhead,
		What: "监控平台写的策略缓存里，这条策略的文档是空的或读不出来，alarmd 拿不到它的配置，没有在检测",
		Next: "由写策略缓存的一方（监控平台的策略缓存刷新）补写这条策略；补上后下一轮自动开始检测，alarmd 不用改"},
	"SOURCE_IDENTITY_UNAVAILABLE": {Kind: WithheldWriterAhead,
		What: "监控平台写的这条策略文档缺租户或空间标识（bk_tenant_id、space_uid），alarmd 判断不了它属于谁，没有在检测",
		Next: "由写策略缓存的一方补上租户与空间标识；补上后下一轮自动开始检测，alarmd 不用改"},
}

// withheldWordsKnown is a reason's words from either table, and whether one
// of them knows it.
func withheldWordsKnown(reason string) (WithheldReasonWords, bool) {
	if words, known := sourceReasonWords[reason]; known {
		return words, true
	}
	words, known := withheldReasonWords[reason]
	return words, known
}

func withheldKindOf(reason string) WithheldKind {
	if words, known := withheldWordsKnown(reason); known {
		return words.Kind
	}
	return WithheldUnknownReason
}

// withheldSentence is a withheld or noted strategy's sentence: its reason's
// meaning and next step where a table words it, and otherwise the check's own
// sentence with the reason named, never another reason's remedy.
func withheldSentence(check Check, disposition, reason string) string {
	if words, known := withheldWordsKnown(reason); known {
		return words.What + "。" + words.Next
	}
	code := reason
	if code == "" {
		code = disposition
	}
	return checkSentence(check) + "（原因 " + code + "）"
}

// factSentence is the sentence the deciding row's own facts make for the
// check, false when the row carries none the check reads.
func factSentence(check Check, row Anomaly, at time.Time) (string, *ReasonFacts, bool) {
	switch {
	case row.Kind == KindReadHeld && row.ReadHold != nil:
		return readHoldSentence(row.ReadHold)
	case row.Kind == KindReadBeforeComplete && row.ReadEarly != nil:
		return readEarlySentence(row.ReadEarly)
	case check == CheckNoDataPersistent && absentSeconds(at, row.Since) > 0:
		absent := &DataAbsentReason{Since: utcPointer(row.Since), AbsentSeconds: absentSeconds(at, row.Since),
			SinceIsLowerBound: sinceBasisOf(row.SinceFrom) == SinceAtLeast}
		longer := ""
		if absent.SinceIsLowerBound {
			longer = "（只会更久）"
		}
		return fmt.Sprintf("查询正常返回，但已经%s没有数据%s；数据负责人查数据为什么停了（采集、上报或入库）",
			aboutWords(absent.AbsentSeconds), longer), &ReasonFacts{DataAbsent: absent}, true
	case (check == CheckEmptyEveryRound || check == CheckEmptyAfterTarget) && row.EmptyEveryRound != nil:
		return emptyRoundsSentence(check, row.EmptyEveryRound, at)
	case (check == CheckQueryRefused || check == CheckQueryTargetMissing) && row.Failure != nil && row.Failure.Code != "":
		return checkSentence(check) + "（查询服务答复 " + row.Failure.Code + "）", &ReasonFacts{Refusal: &RefusalReason{Code: row.Failure.Code}}, true
	case windowChecks[check] && row.Coverage != nil && row.Coverage.Short > 0:
		window := &WindowReason{WorstValid: row.Coverage.WorstValid, WorstRequired: row.Coverage.WorstRequired}
		return checkSentence(check) + fmt.Sprintf("（最差的窗口只有 %d 个有效点，要 %d 个）", window.WorstValid, window.WorstRequired),
			&ReasonFacts{Window: window}, true
	}
	return "", nil, false
}

// readHoldSentence is a held object's sentence. Below the limit the data is
// read whole and the edit is optional; at the limit each round is read before
// the data is all there, and the sentence gives both ways out with the cost
// of the second, since a time_delay that long delays every alert by as much.
func readHoldSentence(hold *ReadHoldFacts) (string, *ReasonFacts, bool) {
	facts := &ReadHoldReason{ArrivalLatestSeconds: heldSeconds(hold.ArrivalAgeMillis), HoldSeconds: heldSeconds(hold.Millis),
		HoldLimitSeconds: heldSeconds(hold.LimitMillis), AtLimit: hold.AtLimit, TimeDelaySeconds: hold.DelaySeconds,
		SuggestedTimeDelaySeconds: hold.SuggestedDelaySeconds, SettlingWaitSeconds: hold.SettlingWaitSeconds}
	settling := ""
	if facts.SettlingWaitSeconds > 0 {
		settling = fmt.Sprintf("（alarmd 另有约 %d 秒就绪等待）", facts.SettlingWaitSeconds)
	}
	if facts.AtLimit {
		sentence := fmt.Sprintf("数据最晚约在窗口结束后 %s才到齐，超过 alarmd 最多推后的 %s，每轮读到的都是不完整的数据，检测结果不能采信。"+
			"两条路：数据负责人查数据为什么晚到这么久；或者接受延迟",
			secondsWords(facts.ArrivalLatestSeconds), secondsWords(facts.HoldLimitSeconds))
		if facts.SuggestedTimeDelaySeconds > 0 {
			sentence += fmt.Sprintf("，把策略 time_delay 从 %s改为 %s%s，代价是告警要在数据时间之后%s才发出",
				currentDelayWords(facts.TimeDelaySeconds), secondsWords(facts.SuggestedTimeDelaySeconds), settling, aboutWords(facts.SuggestedTimeDelaySeconds))
		} else {
			sentence += "，调大策略的 time_delay，代价是告警跟着晚发"
		}
		return sentence, &ReasonFacts{ReadHold: facts}, true
	}
	sentence := fmt.Sprintf("数据最晚约在窗口结束后 %s到齐，alarmd 自动推后约 %s再读，读到的数据完整、结果可信",
		secondsWords(facts.ArrivalLatestSeconds), secondsWords(facts.HoldSeconds))
	if facts.SuggestedTimeDelaySeconds > 0 {
		sentence += fmt.Sprintf("；如把策略 time_delay 从 %s改为 %s%s，alarmd 就不必再自动推后（可选）",
			currentDelayWords(facts.TimeDelaySeconds), secondsWords(facts.SuggestedTimeDelaySeconds), settling)
	}
	return sentence, &ReasonFacts{ReadHold: facts}, true
}

// currentDelayWords is the strategy's time_delay as the sentence says it. The
// control plane reads an unset time_delay as 0, so 0 says it may be either
// rather than claim the owner set it.
func currentDelayWords(seconds int64) string {
	if seconds == 0 {
		return "0 秒（或未设置）"
	}
	return fmt.Sprintf("%d 秒", seconds)
}

// readEarlySentence is the lookback's sentence: the samples that changed
// after they were read, and the time_delay that would have read them whole.
func readEarlySentence(early *ReadEarlyFacts) (string, *ReasonFacts, bool) {
	facts := &ReadEarlyReason{Samples: len(early.Samples), TimeDelaySeconds: early.CurrentDelaySeconds,
		SuggestedTimeDelaySeconds: early.SuggestedDelaySeconds}
	sentence := fmt.Sprintf("最近 %d 次抽样里，数据在 alarmd 读取之后又有新增或改动，每轮读到的数据还不全，检测结果不能采信", facts.Samples)
	if facts.SuggestedTimeDelaySeconds > 0 {
		sentence += fmt.Sprintf("；策略负责人把 time_delay 从 %s改为 %s即可读全，代价是告警要在数据时间之后%s才发出",
			currentDelayWords(facts.TimeDelaySeconds), secondsWords(facts.SuggestedTimeDelaySeconds), aboutWords(facts.SuggestedTimeDelaySeconds))
	} else {
		sentence += "；策略负责人调大 time_delay"
	}
	return sentence, &ReasonFacts{ReadEarly: facts}, true
}

// emptyRoundsSentence is the run of empty rounds: how many, for how long, and
// absentSeconds is how long since a start, in whole seconds, and 0 when there
// is no start or it is not before at: a replica whose clock runs ahead of this
// process's puts its start in the future, and a sentence then says no span
// rather than a negative one.
func absentSeconds(at, since time.Time) int64 {
	if since.IsZero() || !since.Before(at) {
		return 0
	}
	return int64(at.Sub(since) / time.Second)
}

// whose to look at -- the strategy's period, or its target when the query did
// return series and the target selected none.
func emptyRoundsSentence(check Check, empty *EmptyEveryRoundFacts, at time.Time) (string, *ReasonFacts, bool) {
	absent := &DataAbsentReason{Rounds: empty.Rounds, SinceIsLowerBound: empty.SinceIsLowerBound, NeverSawData: empty.NeverSawData}
	span := ""
	if !empty.Since.IsZero() {
		absent.Since = utcPointer(empty.Since)
	}
	if seconds := absentSeconds(at, empty.Since); seconds > 0 {
		absent.AbsentSeconds = seconds
		span = "（" + aboutWords(absent.AbsentSeconds)
		if absent.SinceIsLowerBound {
			span += "，只会更久"
		}
		span += "）"
	}
	if check == CheckEmptyAfterTarget {
		return fmt.Sprintf("查询返回了数据，但监控目标一条也没选中，已连续 %d 轮%s；策略负责人检查监控目标是否选对了主机或模块", empty.Rounds, span),
			&ReasonFacts{DataAbsent: absent}, true
	}
	never := ""
	if empty.NeverSawData {
		never = "，从没查到过数据"
	}
	return fmt.Sprintf("每轮查询都正常返回但没有数据，已连续 %d 轮%s%s；策略负责人确认这个周期下本来就有数据可查（例如聚合周期比数据上报间隔短，或指标、过滤条件写错）",
		empty.Rounds, span, never), &ReasonFacts{DataAbsent: absent}, true
}

// factChecks are the checks whose sentence is written from numbers; one
// whose deciding row carried none says so.
var factChecks = map[Check]bool{
	CheckReadHeld: true, CheckReadBeforeComplete: true, CheckNoDataPersistent: true, CheckEmptyEveryRound: true,
	CheckEmptyAfterTarget: true, CheckQueryRefused: true, CheckQueryTargetMissing: true,
}

// windowChecks are the checks decided on a short window, whose sentence adds
// the worst window's pair.
var windowChecks = map[Check]bool{
	CheckWindowUndecided: true, CheckSeriesDataMissing: true, CheckSeriesSparse: true, CheckSeriesChurning: true,
	CheckCoverageReadingRefused: true, CheckSeriesSparserThanWindow: true,
}

// checkSentences is every check's sentence where its row carries no numbers:
// what it means for the strategy and who does what. Held to checkOrder by a
// test, so a check added without one fails before it ships.
var checkSentences = map[Check]string{
	CheckSourceIncomplete:         "监控平台写的策略缓存里这条策略的文档不完整，alarmd 拿不到可用的配置，没有在检测；由写策略缓存的一方补齐",
	CheckSourceSetFlapping:        "监控平台的活动策略列表反复把这条策略拿掉又放回，拿掉的那几分钟没有检测；由写策略列表的一方查列表为什么会丢",
	CheckCapabilityUnsupported:    "这条策略用到的能力本构建还不支持，没有在检测；策略不用改，等支持该能力的构建",
	CheckCapabilityUnlisted:       "这条策略因为一个本构建没有登记的原因被扣住，没有在检测；由本服务确认原因",
	CheckCutoverFailing:           "alarmd 还在执行旧版本的策略发布，新的发布切不过去，检测结果不能采信；由本服务处理，策略不用改",
	CheckReplicaDegraded:          "alarmd 有实例超出了设计承受的范围，检测结果不能采信；由本服务处理，策略不用改",
	CheckOwnershipSkewed:          "alarmd 各实例分到的检测对象不均，检测照常；不用处理，调度会自己再平衡",
	CheckSlotsOverdue:             "这条策略的检测轮次超期没有执行，没有按时检测；由本服务处理",
	CheckNeverEvaluated:           "这条策略的检测对象接手后一轮也没有跑过；由本服务处理",
	CheckRoundsStalled:            "这条策略的检测轮次停住了，多轮没有推进；由本服务处理",
	CheckDetectionAbandoned:       "有一段检测轮次被放弃，那段时间没有检测；由本服务处理",
	CheckTimelinePruned:           "有一段待检测的轮次被裁掉，那段时间没有检测；由本服务处理",
	CheckBookkeepingAbandoned:     "检测照常做了、告警也发了，只是进度没有记下来（控制面存储写入失败）；由本服务处理",
	CheckNoDataMemoryRefused:      "阈值检测照常，但存储不收无数据检测的记录，这条策略的无数据告警发不出来；由本服务处理",
	CheckAwaitingView:             "alarmd 的执行视图迟迟没有带上这条策略的检测对象，暂时没在检测；由本服务处理",
	CheckDependencyDown:           "alarmd 依赖的服务没应答，这条策略暂时检测不了；由本服务处理",
	CheckDefect:                   "alarmd 处理这条策略时遇到程序缺陷，每轮都会重复；由本服务处理",
	CheckObservationGap:           "alarmd 对这条策略的观察有缺口，结果暂时不能确认；等后续轮次补上",
	CheckCoverageReadingRefused:   "这轮检测窗口的计数不自洽，说不清窗口是不是在补，检测结果不能采信；由本服务处理",
	CheckWindowUndecided:          "检测窗口里缺点，alarmd 判不出是谁的问题（不是已知的本服务故障），检测结果不能采信；由 alarmd 维护方查明是谁的",
	CheckConfigUnresolved:         "这条策略的配置还没解析完，结果暂时不能确认；等后续轮次",
	CheckBackendNotAnswering:      "查询服务没应答这条策略的查询，暂时检测不了；是查询服务还是连接的问题还判不出（不是已知的本服务故障），由 alarmd 维护方查明是谁的",
	CheckQueryRefused:             "查询服务拒绝了这条策略的查询，每轮都检测不了；可能是策略的输入，也可能是查询服务的故障，还判不出（不是已知的本服务故障），由 alarmd 维护方查明是谁的",
	CheckQueryTargetMissing:       "查询服务答复这条策略引用的表或字段不存在；策略负责人检查指标和数据源是否还在",
	CheckPlanUnevaluable:          "这条策略的检测计划算不了，策略定义有问题；策略负责人检查算法与触发条件",
	CheckRetainedShareApproaching: "这条策略单轮占用的内存已接近单个对象的上限，再涨就会整条停检；策略负责人收窄分组或缩短窗口",
	CheckReadBeforeComplete:       "alarmd 在数据到齐之前就读取判定，检测结果不能采信；策略负责人把 time_delay 调到数据到齐所需",
	CheckReadHeld:                 "数据比 time_delay 晚到，alarmd 自动推后读取，读到的数据完整、结果可信；把策略 time_delay 调到数据到齐所需，就不必再推后（可选）",
	CheckLatePastRound:            "部分序列晚到，连续两轮补读都没补回，这些轮次是在缺数据时判定的；策略负责人调大 time_delay",
	CheckLateSeriesMissed:         "部分序列晚到，补读只补回了一部分，其余比补读还晚；数据负责人查这些序列为什么晚到",
	CheckNoDataPersistent:         "查询正常返回，但数据停了；数据负责人查采集、上报或入库",
	CheckSeriesSparse:             "检测窗口里缺的点都是查询正常返回、序列不在结果里，数据当时没到；数据负责人查（序列离开过监控目标也会这样）",
	CheckEmptyEveryRound:          "每轮查询都正常返回但没有数据，从没查到过；策略负责人确认这个周期下本来就有数据可查（例如聚合周期比数据上报间隔短，或指标、过滤条件写错）",
	CheckEmptyAfterTarget:         "查询返回了数据，但监控目标一条也没选中；策略负责人检查监控目标是否选对了主机或模块",
	CheckIdentityUnmatched:        "查询返回了数据，但序列都对不上监控目标的身份字段（缺维度或模型条件不成立）；策略负责人或采集方对齐身份标签",
	CheckQuiet:                    "事件计数正常返回，这段时间没有事件；在检测，不用处理",
	CheckLogCountEmpty:            "日志计数正常返回但是空的，分不清是没有日志还是日志没到；在检测，需要时数据负责人确认日志在不在",
	CheckAnswerTruncated:          "查询结果可能被查询服务按分组数上限截断，只检测了返回的分组；策略负责人收窄分组维度",
	CheckSeriesChurning:           "序列的身份不断变化（按 Pod 名这类短命维度分组），窗口永远攒不满，检测结果不能采信；策略负责人去掉短命的分组维度",
	CheckSeriesSparserThanWindow:  "序列上报比检测窗口稀，告警照常、按平台规则恢复，只是不会出现完整窗口；不用处理，要完整窗口时策略负责人调整周期",
	CheckSeriesDataMissing:        "检测窗口里缺点，可能是 alarmd 没查全，也可能是数据当时没到，还判不出（不是已知的本服务故障）；检测结果不能采信，由 alarmd 维护方查明是谁的",
	CheckConfigRejected:           "这条策略的配置被拒，没有在检测；策略负责人按原因修改配置",
	CheckConfigNoted:              "这条策略在检测，但配置里有一处按平台的读法读成了别的意思；看原因决定是否修改",
}

// checkSentence is a check's sentence, and for a code word no table knows,
// the code named as unknown rather than any other check's remedy.
func checkSentence(check Check) string {
	if sentence, known := checkSentences[check]; known {
		return sentence
	}
	return "检查码 " + string(check) + " 本构建还没有说明；由本服务确认"
}

// unknownSentences say why a strategy's standing could not be read.
var unknownSentences = map[string]string{
	UnknownLookupUnavailable: "策略目录暂时读不到，说不清这条策略的状态；稍后再看，一直这样时由本服务处理",
	UnknownNotYetPublished:   "这条策略在监控平台的活动列表里，但 alarmd 当前的发布里还没有它；等下一次发布",
	UnknownObjectNotObserved: "这条策略的检测对象还没有被任何 alarmd 实例观察到，说不清在不在检测；稍后再看",
	UnknownReplicaUnreadable: "负责这条策略的 alarmd 实例读不到，说不清它的状态；由本服务处理",
	UnknownSnapshotStale:     "负责这条策略的 alarmd 实例上报过期了，说不清它的状态；由本服务处理",
}

func unknownSentence(reason string) string {
	if sentence, known := unknownSentences[reason]; known {
		return sentence
	}
	return "说不清这条策略的状态（原因 " + reason + "）；由本服务确认"
}

// secondsWords is a duration in seconds as the sentence writes it: whole
// seconds, and beside them the minutes or hours from two minutes up.
func secondsWords(seconds int64) string {
	if seconds < 120 {
		return fmt.Sprintf("%d 秒", seconds)
	}
	return fmt.Sprintf("%d 秒（%s）", seconds, spanWords(seconds))
}

// spanWords is a duration in the largest unit that reads naturally: exact
// minutes as they are, otherwise rounded and marked as about.
func spanWords(seconds int64) string {
	switch {
	case seconds < 120:
		return fmt.Sprintf("约 %d 秒", seconds)
	case seconds%60 == 0 && seconds < 2*3600:
		return fmt.Sprintf("%d 分钟", seconds/60)
	case seconds < 2*3600:
		return fmt.Sprintf("约 %d 分钟", (seconds+30)/60)
	case seconds < 48*3600:
		return fmt.Sprintf("约 %d 小时", (seconds+1800)/3600)
	}
	return fmt.Sprintf("约 %d 天", (seconds+43200)/86400)
}

// aboutWords is spanWords always read as approximate: what a cost or an
// elapsed time is said as, since neither is exact to the minute.
func aboutWords(seconds int64) string {
	words := spanWords(seconds)
	if strings.HasPrefix(words, "约") {
		return words
	}
	return "约 " + words
}

func utcPointer(at time.Time) *time.Time {
	at = at.UTC()
	return &at
}
