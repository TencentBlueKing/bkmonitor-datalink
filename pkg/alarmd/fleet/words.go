// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"

// The product vocabulary: the words a reader of the page sees, decided here
// and sent with every response, so the page carries no table of its own.
//
// Two closed lists, fourteen words between them. A row's standing is one
// word from each: what state the strategy is in, and who does what about it.
// Every check the fleet can file a row under maps to exactly one pair (the
// table below, held closed by a test that walks checkOrder), and a handful
// of rules refine the pair from facts on the row -- which minutes a short
// window lacks and whose they are, whether a guard is still moving, whether
// the object has been heard from at all. Everything finer than these words
// -- the check, the reason code, the verdict -- rides beside them as the
// coordinate a developer reads, never in the sentence.
//
// A code word with no pair folds to DEFECT / SERVICE_FIX and nowhere else.
// The one direction a mis-fold must never take is towards "nothing to do":
// an error that reads as "no need to look" is never found.

// StateWord is what state the strategy is in.
type StateWord string

const (
	StateDetecting            StateWord = "DETECTING"
	StateResultUntrusted      StateWord = "RESULT_UNTRUSTED"
	StateNotDetecting         StateWord = "NOT_DETECTING"
	StateDataAbsent           StateWord = "DATA_ABSENT"
	StateStrategyInvalid      StateWord = "STRATEGY_INVALID"
	StateDependencyUnanswered StateWord = "DEPENDENCY_UNANSWERED"
	StateDefect               StateWord = "DEFECT"
	StateRecovered            StateWord = "RECOVERED"
)

// StateWords is the closed list, in the order the page lists them.
var StateWords = []StateWord{StateDetecting, StateResultUntrusted, StateNotDetecting, StateDataAbsent,
	StateStrategyInvalid, StateDependencyUnanswered, StateDefect, StateRecovered}

// ActionWord is who does what about it.
type ActionWord string

const (
	ActionServiceFix      ActionWord = "SERVICE_FIX"
	ActionStrategyEdit    ActionWord = "STRATEGY_EDIT"
	ActionDataCheck       ActionWord = "DATA_CHECK"
	ActionCacheWriterFill ActionWord = "CACHE_WRITER_FILL"
	ActionWatch           ActionWord = "WATCH"
	ActionNone            ActionWord = "NONE"
)

// ActionWords is the closed list, in the order the page's columns run:
// this service first, then the two owners a reader hands work to, then the
// cache writer, then what needs no hand.
var ActionWords = []ActionWord{ActionServiceFix, ActionStrategyEdit, ActionDataCheck, ActionCacheWriterFill, ActionWatch, ActionNone}

// WatchReason is why a WATCH standing is a wait and not a hand-off: each is
// decided by a rule on the row, never by the check alone. WINDOW_FILLING:
// the worst short window gained points this round or its holes are sliding
// out. GUARD_MOVING: a guard's count moved within the last StalledRounds
// rounds. NEXT_ROUND: the round that will say is the next one -- the
// configuration just changed, or the cause did not survive a restart -- for
// at most StalledRounds rounds, after which the row is this side's.
//
// A wait is an assertion about the future: one more round and this will
// clear. So every reason here is evidence that something is moving, and
// nothing here is the absence of evidence. "Nothing heard from the object
// within the window" is not a reason to wait -- an object nobody is hearing
// from is one nobody is evaluating, and the check table already files that
// as overdue or stalled, this side's to look at. Read as a wait it would be
// the state that is never looked at.
type WatchReason string

const (
	WatchWindowFilling WatchReason = "WINDOW_FILLING"
	WatchGuardMoving   WatchReason = "GUARD_MOVING"
	WatchNextRound     WatchReason = "NEXT_ROUND"
)

// WatchReasons is the closed list.
var WatchReasons = []WatchReason{WatchWindowFilling, WatchGuardMoving, WatchNextRound}

// Words is the vocabulary as sent: each word with its rendering. The page
// looks a word up here and nowhere else. Health is the deployment's own
// three-word answer, rendered here too so the page's first word is not an
// English constant.
type Words struct {
	State  map[StateWord]string   `json:"state"`
	Action map[ActionWord]string  `json:"action"`
	Watch  map[WatchReason]string `json:"watch"`
	Health map[Health]string      `json:"health"`
	// Hole and Verdict render a named window's holes and its verdict, the
	// two closed lists the strategy card shows beside the words.
	Hole    map[HoleCause]string     `json:"hole"`
	Verdict map[WindowVerdict]string `json:"verdict"`
	// SinceBasis renders the direction of a duration: measured, a lower
	// bound, an upper bound, or refused.
	SinceBasis map[SinceBasis]string `json:"since_basis"`
	// CoverageRejected renders the rule the server refused a window reading
	// under (observability.CoverageRejectionRules), for the line that
	// stands where the windows would have been.
	CoverageRejected map[string]string `json:"coverage_rejected"`
	// StateOrder and ActionOrder are the lists' own order, which a JSON
	// object cannot carry: the page lays its columns out in this order and
	// decides none of its own.
	StateOrder  []StateWord  `json:"state_order"`
	ActionOrder []ActionWord `json:"action_order"`
}

// ProductWords is the one rendering of the vocabulary.
func ProductWords() Words {
	return Words{
		Health: map[Health]string{HealthHealthy: "正常", HealthDegraded: "降级", HealthUnknown: "证据不全"},
		Hole: map[HoleCause]string{
			HoleAnsweredWithoutSeries: "查询正常返回，这条序列不在结果里", HoleAnsweredEmpty: "查询正常返回，整个对象没有数据",
			HoleInputIncomplete: "本侧那一轮没查全", HolePointUnusable: "记录到了，检测用不了",
			HolePrimaryUnrecorded: "那一轮查询答了什么没记下来", HoleNotInMemory: "超出本进程记忆",
		},
		Verdict: map[WindowVerdict]string{
			VerdictDataAbsentWhenQueried: "查询时数据不在", VerdictInputIncomplete: "本侧没查全",
			VerdictPointsUnusable: "记录检测用不了", VerdictUnknown: "说不出是谁的",
		},
		SinceBasis: map[SinceBasis]string{
			SinceExact: "起点确切", SinceAtLeast: "只会更久", SinceAtMost: "只会更短", SinceRefused: "时间异常，请上报",
		},
		CoverageRejected: map[string]string{
			string(observability.CoverageRejectLevelsZero):             "一个窗口都没计",
			string(observability.CoverageRejectShortOverLevels):        "短窗数多于窗口数",
			string(observability.CoverageRejectEmptyOverShort):         "空窗数多于短窗数",
			string(observability.CoverageRejectGuardedOverLevels):      "被守卫的窗多于窗口数",
			string(observability.CoverageRejectFreshOverLevels):        "新序列的窗多于窗口数",
			string(observability.CoverageRejectShortFreshOverShort):    "新序列的短窗多于短窗数",
			string(observability.CoverageRejectShortFreshOverFresh):    "新序列的短窗多于新序列的窗",
			string(observability.CoverageRejectUnusableOverLevels):     "用不了记录的级别多于窗口数",
			string(observability.CoverageRejectUnusableReasonUnpaired): "用不了记录的计数与原因不配对",
			string(observability.CoverageRejectEmptyWithValidPoints):   "有空窗却报了有效点",
			string(observability.CoverageRejectWindowsOverShort):       "点名的窗多于短窗数",
			string(observability.CoverageRejectWindowsOverBound):       "点名的窗超过上限",
			string(observability.CoverageRejectWindowUnnamed):          "点名的窗没有序列",
			string(observability.CoverageRejectWindowRequiredZero):     "点名的窗要求 0 个点",
			string(observability.CoverageRejectWindowNotShort):         "点名的窗其实是满的",
			string(observability.CoverageRejectWindowHoleArithmetic):   "缺的分钟数与窗口缺口对不上",
			string(observability.CoverageRejectWindowHoleListOverrun):  "缺的分钟列表比计数还长",
			string(observability.CoverageRejectWindowGuardReasonFree):  "写了守卫原因却没被守卫",
		},
		StateOrder: append([]StateWord(nil), StateWords...), ActionOrder: append([]ActionWord(nil), ActionWords...),
		State: map[StateWord]string{
			StateDetecting: "在检测", StateResultUntrusted: "检测结果不能采信", StateNotDetecting: "没在检测",
			StateDataAbsent: "数据没到", StateStrategyInvalid: "策略定义有问题", StateDependencyUnanswered: "依赖没应答",
			StateDefect: "程序缺陷", StateRecovered: "已恢复",
		},
		Action: map[ActionWord]string{
			ActionServiceFix: "本服务处理", ActionStrategyEdit: "策略负责人改", ActionDataCheck: "数据负责人查",
			ActionCacheWriterFill: "缓存写入方补", ActionWatch: "等着看", ActionNone: "不用处理",
		},
		Watch: map[WatchReason]string{
			WatchWindowFilling: "窗口在补", WatchGuardMoving: "保护在解除", WatchNextRound: "等下一轮",
		},
	}
}

// wordPair is one check's pair.
type wordPair struct {
	State  StateWord
	Action ActionWord
}

// checkWords is the closed table: every check the fleet files a row under,
// to one pair. Held to checkOrder by a test, so a check added without a pair
// is red before it ships rather than folded at runtime.
var checkWords = map[Check]wordPair{
	CheckSourceIncomplete:      {StateNotDetecting, ActionCacheWriterFill},
	CheckSourceSetFlapping:     {StateNotDetecting, ActionCacheWriterFill},
	CheckCapabilityUnsupported: {StateNotDetecting, ActionServiceFix},
	CheckConfigRejected:        {StateNotDetecting, ActionStrategyEdit},
	// Detecting, because the Plan runs. The action here is the line's when a
	// reason asks for an edit; a line whose reasons ask nothing is nobody's
	// (normalizedOwner), and each reason carries its own action.
	CheckConfigNormalized:       {StateDetecting, ActionStrategyEdit},
	CheckCutoverFailing:         {StateResultUntrusted, ActionServiceFix},
	CheckReplicaDegraded:        {StateResultUntrusted, ActionServiceFix},
	CheckOwnershipSkewed:        {StateDetecting, ActionNone},
	CheckSlotsOverdue:           {StateNotDetecting, ActionServiceFix},
	CheckNeverEvaluated:         {StateNotDetecting, ActionServiceFix},
	CheckRoundsStalled:          {StateNotDetecting, ActionServiceFix},
	CheckDetectionAbandoned:     {StateNotDetecting, ActionServiceFix},
	CheckTimelinePruned:         {StateNotDetecting, ActionServiceFix},
	CheckBookkeepingAbandoned:   {StateDetecting, ActionServiceFix},
	CheckNoDataMemoryRefused:    {StateResultUntrusted, ActionServiceFix},
	CheckDependencyDown:         {StateDependencyUnanswered, ActionServiceFix},
	CheckDefect:                 {StateDefect, ActionServiceFix},
	CheckObservationGap:         {StateResultUntrusted, ActionWatch},
	CheckQueryRefused:           {StateDependencyUnanswered, ActionServiceFix},
	CheckWindowUndecided:        {StateResultUntrusted, ActionServiceFix},
	CheckCoverageReadingRefused: {StateResultUntrusted, ActionServiceFix},
	CheckConfigUnresolved:       {StateResultUntrusted, ActionWatch},
	CheckBackendNotAnswering:    {StateDependencyUnanswered, ActionServiceFix},
	CheckSeriesDataMissing:      {StateResultUntrusted, ActionServiceFix},
	CheckNoDataPersistent:       {StateDataAbsent, ActionDataCheck},
	CheckEmptyEveryRound:        {StateDataAbsent, ActionStrategyEdit},
	CheckSeriesChurning:         {StateResultUntrusted, ActionStrategyEdit},
	CheckPlanUnevaluable:        {StateStrategyInvalid, ActionStrategyEdit},
	CheckQueryTargetMissing:     {StateStrategyInvalid, ActionStrategyEdit},
	// Detecting: every round completes. The strategy's to act on before the
	// share refuses it whole.
	CheckRetainedShareApproaching: {StateDetecting, ActionStrategyEdit},
}

// unpairedWords is where a code word the table does not know folds: this
// side's, to look at. Never towards nothing to do.
var unpairedWords = wordPair{StateDefect, ActionServiceFix}

// Standing is a row's two words, the coordinate they were decided from, and
// which rule decided them when it was not the check's own pair.
type Standing struct {
	State  StateWord   `json:"state"`
	Action ActionWord  `json:"action"`
	Watch  WatchReason `json:"watch,omitempty"`
	// Check is the coordinate: the check the row is under, empty for a row
	// under none.
	Check Check `json:"check,omitempty"`
	// RefinedBy names the rule that changed the check's own pair, from the
	// closed list StandingRules; empty when the pair is the check's.
	RefinedBy StandingRule `json:"refined_by,omitempty"`
	// About is the Plans this row's evidence is about, set on the words a
	// row gives a strategy when the row's object runs several Plans and the
	// check reads per-Plan evidence -- the guards, the series counts. The
	// words themselves are read from that strategy's own Plan; About lists
	// every Plan the row names so a card for a neighbour can say whose the
	// object's trouble is. Absent when the object runs one Plan, the check
	// is about the whole round, or the evidence names no Plan.
	About []StrategyRef `json:"about,omitempty"`
}

// StandingRule names each rule that can refine a check's pair.
type StandingRule string

const (
	// RuleUnpaired: the check has no pair; folded to this side's.
	RuleUnpaired StandingRule = "UNPAIRED"
	// RuleHistoricalLoss: a retained record older than the window -- the
	// object runs, only the record is left.
	RuleHistoricalLoss StandingRule = "HISTORICAL_LOSS"
	// RuleWindowVerdict: the short windows' holes decided whose the window
	// is (coverage.windows[].verdict, every short window named).
	RuleWindowVerdict StandingRule = "WINDOW_VERDICT"
	// RuleStalled: nothing has moved for StalledRounds rounds; whose to act
	// is read from whether the Plans bound any series.
	RuleStalled StandingRule = "STALLED"
	// RuleWatch: the row is moving or unheard, and the wait has a reason.
	RuleWatch StandingRule = "WATCH"
	// RulePlanEvidence: the words were read from the strategy's own Plan on
	// an object that runs several -- a Plan bound to no series for longer
	// than the stall bound is the data's, whatever the object's other Plans
	// are doing.
	RulePlanEvidence StandingRule = "PLAN_EVIDENCE"
)

// StandingRules is the closed list.
var StandingRules = []StandingRule{RuleUnpaired, RuleHistoricalLoss, RuleWindowVerdict, RuleStalled, RuleWatch, RulePlanEvidence}

// standingOf decides a row's words: the check's pair, then the rules in
// order, each one a closed predicate on the row. A row under no check is
// detecting with nothing to do.
func standingOf(row Anomaly) Standing {
	if row.Finding.Check == "" {
		return Standing{State: StateDetecting, Action: ActionNone}
	}
	standing := Standing{Check: row.Finding.Check}
	pair, paired := checkWords[row.Finding.Check]
	if !paired {
		pair, standing.RefinedBy = unpairedWords, RuleUnpaired
	}
	standing.State, standing.Action = pair.State, pair.Action
	// A retained record older than the window: what is left of a loss the
	// object has run past. Its object runs; the record is history.
	if row.Loss == LossHistorical {
		standing.State, standing.Action, standing.RefinedBy = StateRecovered, ActionNone, RuleHistoricalLoss
		return standing
	}
	// The undecided windows: decided by their holes when every short window
	// is named, and by whether anything is moving otherwise. Stalled is read
	// before the wait on purpose: a wait asserts that the next round will
	// move things, and a guard or window flat for StalledRounds rounds is
	// the direct evidence that it will not. Read the other way round, a
	// stuck object would read as "give it one more round" for ever -- the
	// exact state STALLED was added to name.
	if planScopedCheck(row.Finding.Check) {
		// The Plans this row's evidence names, when the object runs several:
		// carried on the object's own words so a card can say whose the
		// object's trouble is, and copied onto each strategy's words.
		standing.About = implicatedStrategies(row, row.Finding.Check)
		if stalled(&row) {
			standing.RefinedBy = RuleStalled
			if verdict, decided := windowVerdictWords(row); decided {
				standing.State, standing.Action, standing.RefinedBy = verdict.State, verdict.Action, RuleWindowVerdict
			} else if planBoundNoSeries(row) {
				standing.State, standing.Action = StateDataAbsent, ActionDataCheck
			} else {
				standing.State, standing.Action = StateResultUntrusted, ActionServiceFix
			}
			return standing
		}
		if reason, waiting := watchReasonOf(row); waiting {
			standing.Action, standing.Watch, standing.RefinedBy = ActionWatch, reason, RuleWatch
			return standing
		}
		if verdict, decided := windowVerdictWords(row); decided {
			standing.State, standing.Action, standing.RefinedBy = verdict.State, verdict.Action, RuleWindowVerdict
		}
		return standing
	}
	// The checks whose own pair is a wait carry the reason for it -- and a
	// wait that has outlived its bound is not a wait: a row still saying
	// the same thing after StalledRounds rounds is this side's to look at,
	// or the column would be where things go to not be seen.
	if standing.Action == ActionWatch {
		if reason, waiting := watchReasonOf(row); waiting {
			standing.Watch = reason
		} else {
			standing.Action, standing.RefinedBy = ActionServiceFix, RuleStalled
		}
	}
	return standing
}

// windowVerdictWords reads the named windows into a pair, when they decide:
// every short window named, and their verdicts agree on an owner. One
// window this side did not see whole keeps the row this side's; unusable
// records without that are the strategy's; every hole the data's when
// asked for is the data's. Unknown holes decide nothing.
func windowVerdictWords(row Anomaly) (wordPair, bool) {
	coverage := row.Coverage
	if coverage == nil || len(coverage.Windows) == 0 || uint32(len(coverage.Windows)) != coverage.Short {
		return wordPair{}, false
	}
	incomplete, unusable, unknown := 0, 0, 0
	for _, window := range coverage.Windows {
		switch window.Verdict {
		case VerdictInputIncomplete:
			incomplete++
		case VerdictPointsUnusable:
			unusable++
		case VerdictDataAbsentWhenQueried:
		default:
			unknown++
		}
	}
	switch {
	case incomplete > 0:
		return wordPair{StateResultUntrusted, ActionServiceFix}, true
	case unusable > 0:
		return wordPair{StateStrategyInvalid, ActionStrategyEdit}, true
	case unknown > 0:
		return wordPair{}, false
	default:
		return wordPair{StateDataAbsent, ActionDataCheck}, true
	}
}

// planBoundNoSeries reports whether any of the row's Plans was bound to no
// series on its latest round: a guard on such a Plan sits at zero for as
// long as the Plan has no input, and that is the data's.
func planBoundNoSeries(row Anomaly) bool {
	for _, plan := range row.PlanSeries {
		if plan.Matched == 0 {
			return true
		}
	}
	return false
}

// watchReasonOf reads whether the row is a wait, and why, from the facts
// the wait is about: a window gaining points or sliding its holes out, a
// guard whose count moved within StalledRounds, or a cause that the next
// round decides. Every reason is something moving; silence is not one.
func watchReasonOf(row Anomaly) (WatchReason, bool) {
	if coverage := row.Coverage; coverage != nil && coverage.Short > 0 {
		if (coverage.PreviousKnown && coverage.WorstValid > coverage.PreviousWorstValid) ||
			(row.WindowFill != nil && row.WindowFill.Sliding) {
			return WatchWindowFilling, true
		}
	}
	for _, guard := range row.Guards {
		if guard.Required > 0 && guard.Observed > 0 && guard.Observed < guard.Required && guard.UnchangedRounds < StalledRounds {
			return WatchGuardMoving, true
		}
	}
	// The next round decides -- for at most StalledRounds rounds. A row that
	// has said the same thing for longer than that is not waiting on a
	// round, whatever its check says; Consecutive is the row's own count of
	// rounds under its current result and reason.
	if (row.ConfigChanged || row.Finding.Check == CheckObservationGap || row.Finding.Check == CheckConfigUnresolved) &&
		row.Consecutive <= StalledRounds {
		return WatchNextRound, true
	}
	return "", false
}

// SinceBasis is the direction of a duration: what the since source says
// about the clock beside it, in one of four words, so a reader who does
// not know the sources knows which way the number can be wrong.
type SinceBasis string

const (
	// SinceExact: this process, or persisted business state, saw it start.
	SinceExact SinceBasis = "EXACT"
	// SinceAtLeast: the clock started when this process began watching, or
	// at the earliest moment a record could still prove; the real start is
	// no later, and may be far earlier.
	SinceAtLeast SinceBasis = "AT_LEAST"
	// SinceAtMost: the clock is the last moment the object is known to have
	// been fine; it went wrong some time after.
	SinceAtMost SinceBasis = "AT_MOST"
	// SinceRefused: the start was later than the read and was refused.
	SinceRefused SinceBasis = "REFUSED"
)

// SinceBases is the closed list.
var SinceBases = []SinceBasis{SinceExact, SinceAtLeast, SinceAtMost, SinceRefused}

// sinceBasisOf reads a since source into its direction. A source the table
// does not know reads as a lower bound: the one direction that never
// overstates how long something has been wrong.
func sinceBasisOf(source SinceSource) SinceBasis {
	switch source {
	case SinceBusinessState, SinceSnapshotContinuity:
		return SinceExact
	case SinceRestoredLastFull:
		return SinceAtMost
	case SinceRefusedFuture:
		return SinceRefused
	default:
		return SinceAtLeast
	}
}
