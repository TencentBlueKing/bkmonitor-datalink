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
	"slices"
	"sort"
	"time"
)

// A Check is one line on the page's first screen.
//
// The page used to open on the objects: a few hundred rows, each with its own
// situation and three sentences, sorted and tabbed several ways. However it
// was arranged, a reader saw a few hundred rows. Ceph's health, Alertmanager's
// grouping and Nagios's service checks all put the same thing first instead: a
// short closed list of named conditions, each with a count and one sentence,
// and the objects behind a condition one click further in. This is that list.
//
// A check is a rule over the dimensions an object carries -- what it is doing
// now, how its last round ended, how long that has held, what its windows hold
// -- not a word per combination of them. The rules are in finding.go.
type Check string

const (
	// This deployment's own. The first two are deployment-wide standings
	// rather than rules over objects: the fleet executing content that is no
	// longer the current publication, and a replica past a bound the design
	// accepts. Neither has an object row under it, and both decided the
	// verdict without a line on the first screen until a running deployment
	// spent half a day executing a stale publication behind a DEGRADED badge
	// whose one sentence named something else.
	CheckCutoverFailing  Check = "CUTOVER_FAILING"
	CheckReplicaDegraded Check = "REPLICA_DEGRADED"
	// OwnershipSkewed is the third standing: the scheduler's own rebalance
	// round would move objects, so by its tolerance the ready replicas hold
	// uneven shares. It is the deployment's, not any object's: a replica
	// that left and came back holds nothing while the other holds all of
	// it, and every skip and timeout on the loaded one is this, not a
	// capacity question. The page printed 2370 against 0 in a table whose
	// heading said that case needs different handling, and no line said so.
	CheckOwnershipSkewed    Check = "OWNERSHIP_SKEWED"
	CheckSlotsOverdue       Check = "SLOTS_OVERDUE"
	CheckNeverEvaluated     Check = "NEVER_EVALUATED"
	CheckRoundsStalled      Check = "ROUNDS_STALLED"
	CheckDetectionAbandoned Check = "DETECTION_ABANDONED"
	CheckTimelinePruned     Check = "TIMELINE_PRUNED"
	// BookkeepingAbandoned is the span a query-free completion closed whose
	// every Slot an earlier attempt had already executed -- events sent,
	// state written -- and then failed to write the Progress for. Detection
	// happened; what was lost is the bookkeeping. It used to be filed under
	// DETECTION_ABANDONED as detection that never happened, and it is the
	// one visible face of a control-plane store failing writes, so it has a
	// line of its own: on the record side, never on the work list, with a
	// running count and the latest occurrence for the trend.
	CheckBookkeepingAbandoned Check = "BOOKKEEPING_ABANDONED"
	// NoDataMemoryRefused is a Plan whose absence memory the store will not
	// take: the round is fine -- it judged, its threshold results were sent
	// -- and what it learned about absence is not written down, so every
	// round after reads a memory one round old, a group that goes absent is
	// never recorded as first absent, and its no-data alert never fires. In
	// the family of detection that stopped, not of defects: the Plan runs,
	// and only its absence detection is silently gone.
	CheckNoDataMemoryRefused Check = "NO_DATA_MEMORY_REFUSED"
	// AwaitingView is a Query Group whose Worker's view has not carried it
	// for longer than a wait the design expects: every round refused only
	// because the view did not list it yet. Not a dependency that is down
	// -- the view is the deployment's own control plane -- and kept apart
	// from DEPENDENCY_DOWN, where every Worker start and every move used to
	// put a batch of objects for the seconds the view took to arrive.
	CheckAwaitingView        Check = "AWAITING_VIEW"
	CheckDependencyDown      Check = "DEPENDENCY_DOWN"
	CheckDefect              Check = "DEFECT"
	CheckObservationGap      Check = "OBSERVATION_GAP"
	CheckBackendNotAnswering Check = "BACKEND_NOT_ANSWERING"
	CheckQueryRefused        Check = "QUERY_REFUSED"
	// QueryTargetMissing is the refusal that names what is missing: the
	// backend read the query and answered that the table or field the
	// strategy references does not exist. That is the strategy's, and the
	// page's pool card already called these "策略本身不可用" while the line
	// under it said 待确认 -- one page, two verdicts on the same objects.
	CheckQueryTargetMissing Check = "QUERY_TARGET_MISSING"
	// RetainedShareApproaching is an object whose latest completed Slot held
	// at least RetainedShareApproachPercent of the retained pool's
	// one-object share. It is detecting; the line is the warning before
	// QG_BUDGET_SHARE_EXCEEDED, which refuses the object's every round and
	// stops the strategy whole, and which nothing announced before it came.
	CheckRetainedShareApproaching Check = "RETAINED_SHARE_APPROACHING"
	// ReadBeforeComplete is an object the late-data lookback found read
	// before its data was complete in two completed samples in a row: the
	// first read was empty and data came later, or a series it had came back
	// with other points or values. Its results are read from data that was
	// not all there. The strategy's time_delay moves the read; the row
	// carries the value that would have read those samples complete.
	CheckReadBeforeComplete Check = "READ_BEFORE_COMPLETE"
	// ReadHeld is an object alarmd holds the reads of, because its measured
	// arrival age is past what its time_delay waits: its rounds read the
	// data whole, later than the time_delay says. The strategy's owner sets
	// the time_delay that needs no hold; the row carries it.
	CheckReadHeld Check = "READ_HELD"
	// LatePastRound is the objects whose late series had crossed their
	// Slots in two supplemented windows in a row: the supplement recovered
	// none, the rounds were decided without them, and only a longer
	// time_delay reads them. The row carries the value that would.
	CheckLatePastRound Check = "LATE_PAST_ROUND"
	// LateSeriesMissed is the objects whose supplements recovered part of a
	// window and not the rest: the series later still than the supplement
	// reaches, counted, with the latest windows. A longer time_delay would
	// slow the whole object for them; the data is what to look at.
	CheckLateSeriesMissed Check = "LATE_SERIES_MISSED"
	CheckNoDataPersistent Check = "NO_DATA_PERSISTENT"
	// Every short window of the object is short only by minutes the query
	// answered whole without the series: the data was not there when it was
	// asked for, and nothing on this side is on record as missing it. The
	// data owner's, and not an item for this deployment to act on. A minute
	// the series was outside the strategy's target reads the same way - the
	// round answered whole and filtered the series out - so a series that
	// left the target and came back reads sparse for those minutes although
	// its data was there: the row is not proof that the host missed data.
	CheckSeriesSparse Check = "SERIES_SPARSE"
	// EmptyEveryRound is the strategy's half of no-data: an object this
	// process has never seen return records and whose every round for an
	// hour completed empty. Kept apart from NO_DATA_PERSISTENT -- data that
	// stopped, the data side's -- because the two owners do two different
	// things: the data side goes to see why the data stopped, the strategy's
	// owner checks whether there is anything to detect at this period at all.
	// Five strategies aggregating at fifteen seconds over a source that
	// reports every thirty were HEALTHY on the page for a day for want of
	// this line.
	CheckEmptyEveryRound Check = "EMPTY_EVERY_ROUND"
	// EmptyAfterTarget is the same run of empty rounds where the query did
	// return series and the monitoring target selected none of them. The
	// data is there, so the source is the wrong place to look: the strategy's
	// owner checks the target. A strategy whose nine reporting hosts sat in
	// one module while its target named another read EMPTY_EVERY_ROUND for a
	// day.
	CheckEmptyAfterTarget Check = "EMPTY_AFTER_TARGET"
	// Quiet is an event count of groups answered whole with no group: no
	// event in the range asked. Detecting, nothing to do: the row is there
	// so that "no data" is never read into an event stream at rest, and so
	// that a storage that failed silently - which answers the same way - is
	// one look away.
	CheckQuiet Check = "QUIET"
	// AnswerTruncated is an answer that may have been cut at the query
	// service's terms cap: thresholds evaluate the groups that came back,
	// and a Plan that detects no data judges nothing that round
	// (SKIPPED_ANSWER_TRUNCATED). The strategy groups by something wider than
	// the cap.
	CheckAnswerTruncated   Check = "ANSWER_TRUNCATED"
	CheckSeriesChurning    Check = "SERIES_CHURNING"
	CheckSeriesDataMissing Check = "SERIES_DATA_MISSING"
	CheckWindowUndecided   Check = "WINDOW_UNDECIDED"
	// CoverageReadingRefused is a window reason whose counts the observer
	// refused: the round's own window facts broke one of the observer's
	// rules, so nothing can say whether the window is filling. It is this
	// deployment's -- the counts are its own -- and it names the rule. Such
	// a row used to read as "the window cannot be decided", the line for a
	// window whose counts are known and say nothing yet, which sent a reader
	// to wait for a window whose counts would never arrive.
	CheckCoverageReadingRefused Check = "COVERAGE_READING_REFUSED"
	CheckPlanUnevaluable        Check = "PLAN_UNEVALUABLE"
	CheckConfigUnresolved       Check = "CONFIG_UNRESOLVED"
	// The three source standings: strategies the control leader's round
	// listed and did not accept, before any of them could be an object. They
	// fold the source's withheld groups rather than object rows, one line per
	// owner: a document the platform wrote without what the contract requires
	// (or did not write at all) is the platform's; a strategy this build
	// cannot run is this deployment's; a definition the compiler refused is
	// the strategy's. A deployment whose source withheld every strategy had
	// no line for it anywhere and read HEALTHY with nothing to do.
	CheckSourceIncomplete      Check = "SOURCE_INCOMPLETE"
	CheckCapabilityUnsupported Check = "CAPABILITY_UNSUPPORTED"
	// A strategy withheld as beyond this build's capability for a reason the
	// build has not declared as a capability (DeclaredCapabilities): a word
	// nobody listed, or a defect filed under the capability disposition. It
	// stays this deployment's until someone names it, rather than going to
	// the owner nobody acts for on the strength of the disposition alone.
	CheckCapabilityUnlisted Check = "CAPABILITY_UNLISTED"
	CheckConfigRejected     Check = "CONFIG_REJECTED"
	// A strategy the source accepted with a note on its configuration: a
	// part read as something other than what was written -- a time range
	// that does not parse, read as the whole day the way the platform's own
	// reader reads it -- or a part that runs as written and cannot do what
	// it appears to. The Plan runs; it is the strategy's to fix and it is
	// not a refusal. It has its own line because the disposition was
	// otherwise dropped on the floor (sourceChecks did not know it, so the
	// walk skipped it) while the page's hint counted it among the withheld.
	CheckConfigNoted Check = "CONFIG_NOTED"
	// The source's active set dropping strategies and listing them again:
	// the platform's list, read across rounds. One round's dispositions say
	// REMOVED, which reads as a strategy deleted; the account across rounds
	// said the list lost 99 entries for six minutes at the top of every hour,
	// 22 strategies removed and re-placed each time with four Slots of each
	// lost. Folded by the hour the strategies came back, over the last day,
	// so "every hour" is one line and not a word on one round.
	CheckSourceSetFlapping Check = "SOURCE_SET_FLAPPING"
)

// sourceChecks maps a withheld disposition to the standing that carries it.
// STALE_CONFIG rides with CONFIG_REJECTED: the same refusal, on a strategy
// that still runs its last good Plan, and the group says which.
var sourceChecks = map[string]Check{
	dispositionSourceIncomplete:      CheckSourceIncomplete,
	dispositionCapabilityUnsupported: CheckCapabilityUnsupported,
	dispositionConfigRejected:        CheckConfigRejected,
	dispositionStaleConfig:           CheckConfigRejected,
	dispositionConfigNoted:           CheckConfigNoted,
}

// DeclaredCapabilities is the closed list of reasons this build declares as
// capabilities it does not have: a strategy withheld for one of them is the
// capability owner's, and waits for a build. Only these. The capability
// disposition carries other reasons too -- a deployment parameter, a writer
// ahead of the reader, a definition past a guardrail, and one defect
// (EVALUATION_STEP_INCONSISTENT) -- and any reason not named here, a word
// added tomorrow included, stays this deployment's (CheckCapabilityUnlisted)
// until it is named, because a line nobody acts on is the wrong default for
// a reason nobody has read.
var DeclaredCapabilities = []string{
	// Query sources and query features not yet carried over.
	"QUERY_SOURCE_NOT_MIGRATED", "QUERY_MIXED_PROMQL_NOT_MIGRATED", "QUERY_CMDB_LEVEL_BYPASSES_UQ",
	"QUERY_BK_DATA_LOCAL_TIME_NOT_MIGRATED", "QUERY_FUNCTION_NOT_MIGRATED", "EXPRESSION_FUNCTION_NOT_MIGRATED",
	// Detection algorithms (the AIOps ones among them) not carried over.
	"ALGORITHM_NOT_MIGRATED", "ALGORITHM_UNSUPPORTED",
	// The legacy target forms, and more than one item.
	"UNSUPPORTED_TARGET_SCOPE", "UNSUPPORTED_TARGET_SCOPE_UNRESOLVABLE", "UNSUPPORTED_TARGET_VALUE_SHAPE",
	"UNSUPPORTED_MULTI_ITEM_STRATEGY",
	// A global business strategy in a form this build cannot run as one.
	"GLOBAL_STRATEGY_UNSUPPORTED",
	// An effective-time snapshot schema newer than this build reads.
	"EFFECTIVE_TIME_SCHEMA_UNSUPPORTED",
}

// strategyOwnedCapabilityReasons are reasons filed under the capability
// disposition that no build will ever lift, where the strategy is the one
// thing that can change: FTA event sources are not supported, by ruling, and
// the strategy owner moves the strategy to another data source. Filed as a
// capability they would promise a build that is not coming; filed as this
// deployment's, a fix it will not make.
var strategyOwnedCapabilityReasons = []string{"QUERY_FTA_UNSUPPORTED"}

// sourceCheckOf is the line a withheld disposition and its reason are filed
// under: the disposition's own, except that the capability disposition goes
// to the capability owner only for a reason it declares, to the strategy's
// line for a reason only the strategy can answer, and otherwise stays this
// deployment's.
func sourceCheckOf(disposition, reason string) (Check, bool) {
	check, known := sourceChecks[disposition]
	if !known || check != CheckCapabilityUnsupported {
		return check, known
	}
	switch {
	case slices.Contains(DeclaredCapabilities, reason):
		return CheckCapabilityUnsupported, true
	case slices.Contains(strategyOwnedCapabilityReasons, reason):
		return CheckConfigRejected, true
	}
	return CheckCapabilityUnlisted, true
}

// GroupBy is the key a check's objects are folded on. One backend not
// answering is one line with sixty objects under it, not sixty lines; which
// key makes the fold is a property of the check.
type GroupBy string

const (
	GroupByReplica    GroupBy = "replica"
	GroupByReasonCode GroupBy = "reason_code"
	// GroupByHour folds by the clock hour an event fell in, UTC.
	GroupByHour     GroupBy = "hour"
	GroupByDetail   GroupBy = "detail"
	GroupByStrategy GroupBy = "strategy"
	GroupByGapKind  GroupBy = "gap_kind"
	// GroupByCause folds on what the window counts say happened: the reason
	// the detection could not use the record, or that the series are a mix of
	// new and old. It is the fold for the one check whose objects share a
	// symptom and not yet an owner.
	GroupByCause GroupBy = "cause"
	// GroupByDegradation folds on the kind of replica-level standing, the
	// closed DegradationKinds; the replicas in it are named on the group.
	GroupByDegradation GroupBy = "degradation"
	// GroupByBlocked folds on where the failure is stuck, what it was
	// talking to and what kind it was -- the row's Blocked reading -- with
	// the reason codes under that as a secondary count. It is the fold that
	// makes a Redis restart one problem ("commit, Redis, unavailable: 86
	// objects") instead of one line per code, and a query timeout one
	// problem whose dependency the fold does not pretend to know.
	GroupByBlocked GroupBy = "blocked"
)

// GroupBys is the closed list of folds, for the page's completeness test:
// a fold the page has no words for renders as its key on the line a reader
// opens a check with.
var GroupBys = []GroupBy{GroupByReplica, GroupByReasonCode, GroupByDetail, GroupByStrategy, GroupByGapKind, GroupByCause, GroupByDegradation, GroupByLoss, GroupByBlocked, GroupByHour}

// checkAnswers is the closed table: who acts on each check and what its
// objects fold on. Twenty rows, and a test holds the count there. A check
// whose owner is UNDETERMINED is one whose grouping key does not reach an
// external entity yet -- it stays on this deployment's side of the page until
// it does, rather than being handed to whichever owner is likeliest.
var checkAnswers = map[Check]struct {
	Owner   Owner
	GroupBy GroupBy
}{
	CheckSourceIncomplete:      {OwnerPlatform, GroupByReasonCode},
	CheckSourceSetFlapping:     {OwnerPlatform, GroupByHour},
	CheckCapabilityUnsupported: {OwnerCapability, GroupByReasonCode},
	CheckCapabilityUnlisted:    {OwnerAlarmd, GroupByReasonCode},
	CheckConfigRejected:        {OwnerStrategy, GroupByReasonCode},
	CheckConfigNoted:           {OwnerStrategy, GroupByReasonCode},
	CheckCutoverFailing:        {OwnerAlarmd, GroupByReasonCode},
	CheckReplicaDegraded:       {OwnerAlarmd, GroupByDegradation},
	CheckOwnershipSkewed:       {OwnerAlarmd, GroupByReplica},
	CheckSlotsOverdue:          {OwnerAlarmd, GroupByReplica},
	CheckNeverEvaluated:        {OwnerAlarmd, GroupByReplica},
	CheckRoundsStalled:         {OwnerAlarmd, GroupByReplica},
	CheckDetectionAbandoned:    {OwnerAlarmd, GroupByLoss},
	CheckTimelinePruned:        {OwnerAlarmd, GroupByLoss},
	CheckBookkeepingAbandoned:  {OwnerAlarmd, GroupByReplica},
	CheckNoDataMemoryRefused:   {OwnerAlarmd, GroupByReasonCode},
	// Folded by replica: one Worker waiting is its view stream, every Worker
	// waiting is the Leader not publishing.
	CheckAwaitingView:   {OwnerAlarmd, GroupByReplica},
	CheckDependencyDown: {OwnerAlarmd, GroupByBlocked},
	CheckDefect:         {OwnerAlarmd, GroupByBlocked},
	CheckObservationGap: {OwnerAlarmd, GroupByGapKind},
	// Folded on the rule the counts broke (the detail fold reads it).
	CheckCoverageReadingRefused: {OwnerAlarmd, GroupByDetail},

	CheckNoDataPersistent: {OwnerData, GroupByStrategy},
	CheckSeriesSparse:     {OwnerData, GroupByStrategy},

	CheckEmptyEveryRound:    {OwnerStrategy, GroupByStrategy},
	CheckEmptyAfterTarget:   {OwnerStrategy, GroupByStrategy},
	CheckQuiet:              {OwnerStrategy, GroupByStrategy},
	CheckAnswerTruncated:    {OwnerStrategy, GroupByStrategy},
	CheckSeriesChurning:     {OwnerStrategy, GroupByStrategy},
	CheckPlanUnevaluable:    {OwnerStrategy, GroupByStrategy},
	CheckQueryTargetMissing: {OwnerStrategy, GroupByDetail},
	// The strategy's, as the refusal it warns of is: what fits in a share is
	// the strategy's size, and the remedy is to shard or reshape it.
	CheckRetainedShareApproaching: {OwnerStrategy, GroupByStrategy},
	// The strategy's: its time_delay decides when its window is read.
	CheckReadBeforeComplete: {OwnerStrategy, GroupByStrategy},
	CheckReadHeld:           {OwnerStrategy, GroupByStrategy},
	CheckLatePastRound:      {OwnerStrategy, GroupByStrategy},
	// The data's: its late tail is past what a supplement reaches.
	CheckLateSeriesMissed: {OwnerData, GroupByStrategy},
	// The strategy's: the backend read the query and rejected it - a
	// condition value its storage refuses, an expression it cannot parse -
	// and the platform's own detector is refused the same way. The owner
	// rests on the cases checked on 2026-09-29: five refused queries, each
	// compared with the platform detector's request for the same strategy,
	// all five refused there too (four condition values opening with control
	// characters, one expression that did not parse). Folded by the stage
	// and class the refusal was blocked at.
	CheckQueryRefused: {OwnerStrategy, GroupByBlocked},

	CheckWindowUndecided:  {OwnerUndetermined, GroupByCause},
	CheckConfigUnresolved: {OwnerUndetermined, GroupByStrategy},
	// A client-side timeout does not establish a fault on the data side: the
	// query's budget, the network and the backend's own latency all have to
	// be read first. And a window short of old-series points may be short
	// because this deployment did not fetch them. Both were handed to the
	// data owner as confirmed; a live review found neither confirmed.
	CheckBackendNotAnswering: {OwnerUndetermined, GroupByBlocked},
	CheckSeriesDataMissing:   {OwnerUndetermined, GroupByStrategy},
}

// checkOrder is the order the first screen lists the checks in, and the order
// a reader acts in: this deployment's own first, worst first -- work not being
// done at all, then work lost, then infrastructure, then defects, then what
// cannot be spoken for -- then what nobody can hand to anyone yet, then what
// is confirmed as somebody else's. It is the table's severity, stated as an
// order rather than as a fifth field beside each row. A test holds it to the
// same keys as checkAnswers.
var checkOrder = []Check{
	// The source standings first: a strategy held at the configuration step
	// never reaches anything below, and a reader who starts at the bottom
	// would find nothing there to explain an empty deployment.
	CheckSourceIncomplete,
	CheckSourceSetFlapping,
	CheckCapabilityUnsupported,
	CheckCapabilityUnlisted,
	CheckCutoverFailing,
	CheckReplicaDegraded,
	CheckOwnershipSkewed,
	CheckSlotsOverdue,
	CheckNeverEvaluated,
	CheckRoundsStalled,
	CheckDetectionAbandoned,
	CheckTimelinePruned,
	CheckBookkeepingAbandoned,
	CheckNoDataMemoryRefused,
	CheckAwaitingView,
	CheckDependencyDown,
	CheckDefect,
	CheckObservationGap,
	CheckCoverageReadingRefused,
	CheckWindowUndecided,
	CheckConfigUnresolved,
	CheckBackendNotAnswering,
	CheckSeriesDataMissing,
	CheckNoDataPersistent,
	CheckSeriesSparse,
	CheckEmptyEveryRound,
	CheckEmptyAfterTarget,
	CheckSeriesChurning,
	CheckPlanUnevaluable,
	CheckQueryTargetMissing,
	CheckQueryRefused,
	CheckConfigRejected,
	// Below the lines that stop detection, above the one that only warns:
	// this one detects, from data read before it was all there.
	CheckReadBeforeComplete,
	CheckLatePastRound,
	CheckLateSeriesMissed,
	// Below every line that stops detection: this one only says a line that
	// would is near.
	CheckRetainedShareApproaching,
	// The last line over objects: the strategy runs and reads its data
	// whole, only later than its time_delay says. Any other row of the
	// strategy decides it.
	CheckReadHeld,
	// The event counts at rest: detecting, with nothing to judge. Any other
	// row of the strategy decides it.
	CheckQuiet,
	// An answer that may have been cut: thresholds evaluate what came back,
	// and no data is judged that round.
	CheckAnswerTruncated,
	// Last: the strategy runs. A reader who starts at the top meets every
	// line that stops detection before the one that only widens it.
	CheckConfigNoted,
}

// Standing is whether a check is a fact about the whole deployment rather
// than a rule over objects: no object row under it, and its count is the
// replicas it names. The three are decided in one place so the line count,
// the first screen's arithmetic and the page's layout cannot disagree on
// which checks those are.
func (check Check) Standing() bool {
	return check == CheckCutoverFailing || check == CheckReplicaDegraded || check == CheckOwnershipSkewed ||
		check.SourceStanding()
}

// SourceStanding reports whether the check folds the source's withheld
// strategies rather than object rows. Its groups carry strategies and
// samples; nothing under it can be listed as an object, because none of
// these ever became one.
func (check Check) SourceStanding() bool {
	return check == CheckSourceIncomplete || check == CheckCapabilityUnsupported || check == CheckCapabilityUnlisted ||
		check == CheckConfigRejected ||
		check == CheckSourceSetFlapping || check == CheckConfigNoted
}

// timeOrNil is a time for a JSON field that is absent when unset.
func timeOrNil(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	return &at
}

// Checks lists every check the table answers, in the order the page lists
// them, for the tests that walk it and for the page's completeness check.
func Checks() []Check {
	list := make([]Check, len(checkOrder))
	copy(list, checkOrder)
	return list
}

func checkRank(check Check) int {
	for index, candidate := range checkOrder {
		if candidate == check {
			return index
		}
	}
	return len(checkOrder)
}

// ChecksWithoutAProducer names the checks nothing decides yet. They are in the
// table so the page has words for them the day they arrive, and named here so
// that a check with no writer cannot read as a mechanism that is wired: the
// due index will produce NEVER_EVALUATED once it knows when each object was
// taken over, and a test holds this list to exactly that one.
var ChecksWithoutAProducer = []Check{CheckNeverEvaluated}

// decidingCode is the code the check was decided on, in the order checkOf
// reads them. It is the grouping key for the checks that fold on a code.
func decidingCode(anomaly Anomaly) string {
	for _, code := range decisionCodes(anomaly) {
		if code != "" {
			return code
		}
	}
	return ""
}

// The key an object falls under within its check. Objects with no key of the
// kind the check folds on share one group named for the absence, so they are
// counted rather than dropped.
const (
	groupNoStrategy = "(未观察到策略)"
	groupNoDetail   = "(没有症状记录)"
)

func groupKeyOf(anomaly Anomaly, check Check) string {
	switch checkAnswers[check].GroupBy {
	case GroupByReplica:
		return anomaly.Replica
	case GroupByReasonCode:
		return decidingCode(anomaly)
	case GroupByBlocked:
		return blockedKey(anomaly.Blocked)
	case GroupByDetail:
		if anomaly.Failure != nil && anomaly.Failure.Detail != "" {
			return anomaly.Failure.Detail
		}
		if anomaly.Coverage == nil && anomaly.CoverageRejected != nil {
			return anomaly.CoverageRejected.Rule
		}
		if code := decidingCode(anomaly); code != "" {
			return code
		}
		return groupNoDetail
	case GroupByStrategy:
		// The strategy the row's evidence is about when it names one;
		// otherwise the smallest id, so the same row always folds under the
		// same key. On an object running six Plans the fold had named the
		// smallest id while every guard on the row belonged to another.
		if key := strategyGroupKey(anomaly, check); key != "" {
			return key
		}
		key := ""
		for _, strategy := range anomaly.Strategies {
			if key == "" || strategy.StrategyID < key {
				key = strategy.StrategyID
			}
		}
		if key == "" {
			return groupNoStrategy
		}
		return key
	case GroupByGapKind:
		// The only object-borne member of the observation gap is the object
		// restored without its cause; the view's own gaps are added by kind
		// in ReportChecks.
		return gapRestoredWithoutCause
	case GroupByCause:
		return windowCause(anomaly.Coverage, anomaly.CauseReason)
	case GroupByLoss:
		// An object a column put on the line folds on the code that put it
		// there, which for these lines is a budget rejection -- the fold a
		// reader may call capacity. A record row folds on its kind, set
		// where the row is made (skippedRows); no record row comes through
		// here, so no branch for it -- a branch nothing reaches would read
		// as a rule.
		return decidingCode(anomaly)
	}
	return ""
}

// The folds of a window that will not fill and whose owner the counts do not
// decide. A starved window is a record that arrived and could not be used --
// the reason the detection gave is the fold, because REQUIRED_VALUE_MISSING
// and an algorithm's refusal are different conversations -- and a window
// short over a mix of new and old series is its own.
const (
	causeSeriesMixed    = "新老序列混合"
	causeUnusableNoWord = "检测用不了记录（原因没带上）"
	causeNoCounts       = "没有窗口计数（副本没报）"
	// A window held by a durable guard: the reason on the row is the one
	// the guard was established with, carried onto every round until the
	// guard releases, not what happened this round. Folded on that trigger,
	// with whether the live window is still short or already full -- the
	// second is a guard that should have released and has not.
	causeGuardHeld           = "保护未解除"
	causeGuardHeldWindowFull = "保护未解除且窗口已满"
)

func windowCause(coverage *HistoryCoverage, reason string) string {
	switch {
	case coverage == nil || coverage.Levels == 0:
		return causeNoCounts
	case coverage.Guarded > 0 && reason != "" && reason != "HISTORY_WARMING" && reason != "HISTORY_GAPPED":
		if coverage.Short == 0 {
			return causeGuardHeldWindowFull + "（最初触发 " + reason + "）"
		}
		return causeGuardHeld + "（最初触发 " + reason + "）"
	case coverage.Starved():
		if coverage.UnusableReason != "" {
			return coverage.UnusableReason
		}
		return causeUnusableNoWord
	default:
		return causeSeriesMixed
	}
}

// Result is how the last round ended. COMPLETED is a round that ran to its
// end and produced its outcome -- the reason code beside it says what that
// outcome was, and the window numbers say whether it could decide recovery.
// ERROR is a round that failed. REFUSED is a backend that read the query and
// would not run it, which is neither the backend being down nor the data
// being absent. NO_DATA -- a query that answered with no points -- arrives
// when the bit that separates it from a detection error crosses from the
// evaluator; until then those rounds read as COMPLETED with their reason.
type Result string

const (
	ResultCompleted Result = "COMPLETED"
	ResultError     Result = "ERROR"
	ResultRefused   Result = "REFUSED"
	ResultNoData    Result = "NO_DATA"
)

// Results lists every result value, for the page's completeness check.
var Results = []Result{ResultCompleted, ResultError, ResultRefused, ResultNoData}

// resultOf reads the last round off the anomaly. Empty when there was no
// round to speak of: an object whose wake time passed has no last result.
func resultOf(anomaly Anomaly) Result {
	switch {
	case anomaly.Kind == KindOverdueWake, anomaly.Kind == KindSkippedSpan:
		return ""
	case anomaly.Kind == KindNoData, anomaly.Kind == KindEmptyEveryRound:
		return ResultNoData
	case anomaly.Kind == KindNoDataMemoryRefused, anomaly.Kind == KindRetainedShareApproaching, anomaly.Kind == KindReadBeforeComplete,
		anomaly.Kind == KindReadHeld, anomaly.Kind == KindLatePastRound, anomaly.Kind == KindLateSeriesMissed:
		// The round completed; what was refused was the memory beside it,
		// or nothing yet.
		return ResultCompleted
	case queryRejected(anomaly.Failure):
		return ResultRefused
	case anomaly.Failure != nil, anomaly.Kind == KindBlockedRun, failedExecution(anomaly.ReasonCode):
		return ResultError
	}
	return ResultCompleted
}

// CheckReport is one line of the first screen: the check, who acts on it, how
// much it covers, and the groups its objects fold into.
type CheckReport struct {
	Code  Check `json:"code"`
	Owner Owner `json:"owner"`
	// GroupBy names what the groups below are folded on, so the page can say
	// "by strategy" without knowing the table.
	GroupBy GroupBy `json:"group_by"`
	// Objects, Strategies and Businesses are over every object under this
	// check, across every column. Strategies and Businesses are distinct
	// counts, not sums over groups: a strategy in two groups is one strategy.
	Objects    int `json:"objects"`
	Strategies int `json:"strategies"`
	Businesses int `json:"businesses"`
	// Demoted is how many of Objects sit in the demoted pool: this deployment
	// has already stopped re-querying them. The pool card says so of the
	// pool; the line has to say so of its own objects, or the two read as
	// different verdicts on the same strategies.
	Demoted int `json:"demoted,omitempty"`
	// MemoryGap is how many of Current carry a MemoryGap - short at
	// positions before this replica's memory of them, after a restart or a
	// handover - and MemoryGapUntil the latest any of them settles by.
	MemoryGap      int        `json:"memory_gap,omitempty"`
	MemoryGapUntil *time.Time `json:"memory_gap_until,omitempty"`
	// Current and Retained split Objects into what is wrong now and what was
	// lost in the past and is kept on record. A line that added the two read
	// as 393 objects to act on when 10 were anomalous and 383 were records of
	// Slots skipped hours ago; the reader could not tell which without
	// opening every group. On the two record lines a record younger than
	// RecentSkipWindow is a loss in progress and counts as Current; only the
	// records that stopped are Retained. RetainedLastHour is how many of
	// those stopped within the last hour, RetainedNewest the latest -- not
	// "new" records: a record keeps one skip per object, the latest, so how
	// many objects were added cannot be known from it.
	Current          int        `json:"current"`
	Retained         int        `json:"retained,omitempty"`
	RetainedLastHour int        `json:"retained_last_hour,omitempty"`
	RetainedNewest   *time.Time `json:"retained_newest,omitempty"`
	// Consequence is on the lines whose objects are in the demoted pool: how
	// many of them also skipped detection while there. The skip is what the
	// cooldown did to the backlog, so it is this line's, and no capacity
	// changes it.
	Consequence *Consequence `json:"consequence,omitempty"`
	// SkipReasons, on the record lines, counts the current records by the
	// last step before the skip: a permit deadline missed, a budget
	// rejection, or nothing tried. It is what replaced inferring the
	// mechanism from the object's period.
	SkipReasons map[string]int `json:"skip_reasons,omitempty"`
	// Onsets, on the two no-data lines, is the objects by the minute their
	// run began, largest minutes first and at most MaxOnsetFold of them with
	// the rest summed under Other and the rows with no start under
	// WithoutOnset, so the fold adds up to the line: a reader who subtracts
	// the minutes from the count must not read the remainder as a sample
	// dropped. The strategy fold cannot show that many
	// runs began together; this is the fold that separates one event from
	// many quiet sources. A minute that is the minute a release began
	// recording the runs is a lower bound, not an event, and the rows say
	// so (EmptyEveryRoundFacts.SinceIsLowerBound).
	Onsets *OnsetFold `json:"onsets,omitempty"`
	// Recovered is the objects that recovered from this line within
	// RecoveredRetention, over its folds; RecoveredLast the latest of them.
	// Not in Objects or Current: they are not under the line now. A line with
	// nothing current and something recovered is a problem that ended, and
	// the page lists it with the history rather than with the work.
	Recovered     int        `json:"recovered,omitempty"`
	RecoveredLast *time.Time `json:"recovered_last,omitempty"`
	// Partial says at least one column this check draws from was truncated by
	// its replica, so the counts here are a sample of that column.
	Partial bool         `json:"partial,omitempty"`
	Groups  []CheckGroup `json:"groups"`
	// Activation and Replica are on CUTOVER_FAILING only: the leader's
	// standing, whole, and which replica it is. The line's sentence is built
	// from them -- which publication is running, which one is not, since
	// when -- and an object count cannot say any of that.
	Activation *ActivationFacts `json:"activation,omitempty"`
	Replica    string           `json:"replica,omitempty"`
	// Rebalance is on OWNERSHIP_SKEWED only: the leader's planning round,
	// whole, and Replica which leader. The sentence is built from it -- who
	// holds how much, what the even share is, what the round would move and
	// whether anything moves it -- and the two replicas in the group cannot
	// say that.
	Rebalance *RebalanceFacts `json:"rebalance,omitempty"`
	// Bookkeeping is on BOOKKEEPING_ABANDONED only: the replicas' running
	// count of Slots executed and then closed without their Progress, the
	// objects, and the latest -- the trend the records cannot carry.
	Bookkeeping *BookkeepingFacts `json:"bookkeeping,omitempty"`
	// Line is the line's sentence, composed here for the lines whose
	// sentence depends on what is under them rather than on the line alone:
	// the capability line names how many strategies fall under each kind of
	// cause, so the page cannot state one cause for every reason.
	Line string `json:"line,omitempty"`
}

// OnsetFold is the objects of a line by the minute their run began.
type OnsetFold struct {
	Minutes []OnsetMinute `json:"minutes"`
	// Other is the objects in minutes past the bound, summed.
	Other int `json:"other,omitempty"`
	// WithoutOnset is the line's objects whose row carries no start. With
	// Minutes and Other it adds up to the line's objects.
	WithoutOnset int `json:"without_onset,omitempty"`
	// Distinct is how many different minutes there were, bound or not: one
	// is one event, hundreds are hundreds of quiet sources.
	Distinct int `json:"distinct"`
}

// OnsetMinute is one minute of an onset fold.
type OnsetMinute struct {
	Minute  time.Time `json:"minute"`
	Objects int       `json:"objects"`
}

// MaxOnsetFold bounds the minutes an onset fold lists.
const MaxOnsetFold = 8

// onsetFold orders the minutes by objects, then by time, and cuts to the
// bound; nil when the line has no rows of the kinds that carry a start.
func onsetFold(onsets map[time.Time]int, withoutOnset int) *OnsetFold {
	if len(onsets) == 0 && withoutOnset == 0 {
		return nil
	}
	fold := &OnsetFold{Distinct: len(onsets), WithoutOnset: withoutOnset}
	for minute, objects := range onsets {
		fold.Minutes = append(fold.Minutes, OnsetMinute{Minute: minute, Objects: objects})
	}
	sort.Slice(fold.Minutes, func(i, j int) bool {
		if fold.Minutes[i].Objects != fold.Minutes[j].Objects {
			return fold.Minutes[i].Objects > fold.Minutes[j].Objects
		}
		return fold.Minutes[i].Minute.Before(fold.Minutes[j].Minute)
	})
	if len(fold.Minutes) > MaxOnsetFold {
		for _, minute := range fold.Minutes[MaxOnsetFold:] {
			fold.Other += minute.Objects
		}
		fold.Minutes = fold.Minutes[:MaxOnsetFold]
	}
	return fold
}

// CheckGroup is one fold of a check's objects: the objects sharing one key.
// Replicas is on the groups of the standing checks, whose folds have no
// objects and name the replicas instead.
type CheckGroup struct {
	Key        string   `json:"key"`
	Objects    int      `json:"objects"`
	Strategies int      `json:"strategies"`
	Businesses int      `json:"businesses"`
	Replicas   []string `json:"replicas,omitempty"`
	// Stage and Text are on a standing's fold where the replica's facts name
	// the failure behind it: the line then says what failed, not only which
	// bound was passed.
	Stage string `json:"stage,omitempty"`
	Text  string `json:"text,omitempty"`
	// Degradations is the replica-level standings folded under this group,
	// whole, on REPLICA_DEGRADED: Replicas above names them, this carries
	// each one's own facts -- how long it has held, how many attempts, what
	// the last one said -- so the line can say "副本 X 输出未就绪 5 分钟" of
	// each replica rather than one kind name over a list of names.
	Degradations []Degradation `json:"degradations,omitempty"`
	// Disposition and Samples are on a source standing's fold: which
	// disposition the control plane gave the strategies in it, and a bounded
	// sample of which strategies. Strategies above holds the count; there are
	// no objects to open, because none of these became one. Words is what
	// the fold's reason means and what to do about it, decided per reason;
	// on the line of strategies this deployment cannot run, where one
	// sentence for every reason once sent an operator to a parameter no
	// reason under it was about.
	Disposition string               `json:"disposition,omitempty"`
	Samples     []WithheldSample     `json:"samples,omitempty"`
	Words       *WithheldReasonWords `json:"words,omitempty"`
	// Codes counts the reason codes under a fold on the Blocked reading: the
	// secondary key, so "commit, Redis, unavailable" can still say it was
	// REDIS_UNAVAILABLE 60 and STATE_WRITE_RETRYABLE 26.
	Codes map[string]int `json:"codes,omitempty"`
	// The problem's own facts, on every fold with object rows: how many of
	// its objects are overdue now and how many are retrying, when it began
	// (the earliest onset), when it was last seen failing, when any of its
	// objects last completed healthily -- and Recovery, read from those.
	Overdue      int        `json:"overdue,omitempty"`
	Retrying     int        `json:"retrying,omitempty"`
	FirstFailure *time.Time `json:"first_failure,omitempty"`
	LastFailure  *time.Time `json:"last_failure,omitempty"`
	LastSuccess  *time.Time `json:"last_success,omitempty"`
	Recovery     Recovery   `json:"recovery,omitempty"`
	// Behind the recovery reading: objects seen failing within the recent
	// window, objects whose rounds ended within it without a usable result,
	// objects only late, and objects nothing was heard from within it.
	FailingNow    int `json:"failing_now,omitempty"`
	CompletingNow int `json:"completing_now,omitempty"`
	Delayed       int `json:"delayed,omitempty"`
	Silent        int `json:"silent,omitempty"`
	// Stalled is the objects among CompletingNow whose way out has not moved
	// for StalledRounds rounds: a held guard's count or a short window's
	// valid count. It is what turns the fold's reading from recovering to
	// stalled, and it is on the wire so the page can say how many.
	Stalled int `json:"stalled,omitempty"`
	// Recovered is the objects that completed healthily after being listed
	// under this fold, within RecoveredRetention: the positive evidence. On a
	// fold with objects still under it, it is how far the problem has come
	// back without lifting the state -- one object still failing keeps the
	// group blocked. On a fold with none, the fold is the record of a problem
	// that recovered, and Objects is zero. RecoveredFirst and RecoveredLast
	// bound when they recovered.
	Recovered      int        `json:"recovered,omitempty"`
	RecoveredFirst *time.Time `json:"recovered_first,omitempty"`
	RecoveredLast  *time.Time `json:"recovered_last,omitempty"`
}

// Recovery is where a problem is between failing and fixed, read from its
// objects' rows and never from the dependency's own health: a store that
// answers again is not detection that has caught up.
type Recovery string

const (
	// RecoveryBlocked: objects were seen failing within the recent window.
	RecoveryBlocked Recovery = "BLOCKED"
	// RecoveryRecovering: no failure within the window; rounds are ending
	// again (with results nobody can use yet) or are only late.
	RecoveryRecovering Recovery = "RECOVERING"
	// RecoveryStalled: no failure within the window and rounds are ending,
	// but what the fold is waiting on -- a held guard's count, a short
	// window's valid count -- has not moved for StalledRounds rounds on at
	// least one object. Rounds ending is not progress: a guard at 0 of 9 for
	// twenty rounds read as "recovering" for twenty rounds, and the reader
	// who took that at its word waited for something no round was bringing.
	RecoveryStalled Recovery = "STALLED"
	// RecoveryUnconfirmed: no failure within the window, and nothing heard
	// from any object within it either -- a cooldown, a round not yet due.
	// Not recovered: recovery needs a success, and none was seen.
	RecoveryUnconfirmed Recovery = "UNCONFIRMED"
	// RecoveryRecovered: every object that was under the fold completed
	// healthily after being listed, and none is under it now. Read from the
	// recoveries the trackers recorded at the completion, never from the
	// fold's absence -- a restart or a change of owner empties a fold too.
	RecoveryRecovered Recovery = "RECOVERED"
	// RecoveryHistorical: the objects run normally now; what remains is a
	// record of detection that did not happen and cannot be made to.
	RecoveryHistorical Recovery = "HISTORICAL"
)

// RecoveryStates is the closed list, for the page's completeness test.
var RecoveryStates = []Recovery{RecoveryBlocked, RecoveryRecovering, RecoveryStalled, RecoveryUnconfirmed, RecoveryRecovered, RecoveryHistorical}

// StalledRounds is how many consecutive rounds a guard's count or a short
// window's valid count must have stayed put before a completing object is
// read as stalled rather than recovering: one is a round, two a coincidence,
// three a run.
const StalledRounds = 3

// stalled reports an object whose rounds end but whose way out has not moved:
// a held guard whose observed count has been the same for StalledRounds
// rounds, or a short window whose worst valid count has. A guard that has
// already reached its requirement is not stalled -- it is a release that has
// not happened yet, which HeldFullRounds reads. Read from the row's own
// counters, never from how long the object has been listed: an object listed
// for an hour whose count rose last round is recovering, slowly.
func stalled(anomaly *Anomaly) bool {
	for _, guard := range anomaly.Guards {
		if guard.Required > 0 && guard.Observed < guard.Required && guard.UnchangedRounds >= StalledRounds {
			return true
		}
	}
	if coverage := anomaly.Coverage; coverage != nil && coverage.Short > 0 &&
		coverage.WorstValid < coverage.WorstRequired && coverage.UnchangedRounds >= StalledRounds {
		return true
	}
	return false
}

// RecoveryStatesWithoutAProducer names the states nothing decides yet. Empty
// since the trackers began recording recoveries: every state has a producer,
// and the test that checks so has nothing to excuse. Kept so a state added
// before its producer has somewhere to be declared as waiting.
var RecoveryStatesWithoutAProducer = []Recovery{}

// blockedKey is the fold key on the Blocked reading: stage, dependency and
// class, joined so the page can split them back out. A row with no reading
// -- one that records no failure -- folds under the unlocated triple.
func blockedKey(blocked *Blocked) string {
	if blocked == nil {
		return string(StageUnlocated) + "/" + string(DependencyUnlocated) + "/" + string(ClassUnlocated)
	}
	return string(blocked.Stage) + "/" + string(blocked.Dependency) + "/" + string(blocked.Class)
}

// noteProblem adds one object row to its group's problem facts, counted
// under code when the fold names one and under the row's own code otherwise.
func noteProblem(group *CheckGroup, anomaly *Anomaly, code string, now time.Time) {
	blocked := anomaly.Blocked
	if group.Codes == nil {
		group.Codes = map[string]int{}
	}
	if code == "" {
		code = decidingCode(*anomaly)
		if blocked != nil && blocked.Code != "" {
			code = blocked.Code
		}
	}
	if code == "" {
		code = skipReasonNone
	}
	group.Codes[code]++
	if anomaly.Finding.Schedule == ScheduleOverdue {
		group.Overdue++
	}
	if !anomaly.Since.IsZero() && (group.FirstFailure == nil || anomaly.Since.Before(*group.FirstFailure)) {
		since := anomaly.Since
		group.FirstFailure = &since
	}
	if blocked == nil {
		group.Silent++
		return
	}
	if blocked.Retrying {
		group.Retrying++
	}
	if blocked.At != nil && (group.LastFailure == nil || blocked.At.After(*group.LastFailure)) {
		at := *blocked.At
		group.LastFailure = &at
	}
	if blocked.LastSuccessAt != nil && (group.LastSuccess == nil || blocked.LastSuccessAt.After(*group.LastSuccess)) {
		at := *blocked.LastSuccessAt
		group.LastSuccess = &at
	}
	recent := blocked.At != nil && now.Sub(*blocked.At) <= RecentSkipWindow
	switch {
	case !recent:
		group.Silent++
	case blocked.Effect == EffectRetrying, blocked.Effect == EffectSkipped, blocked.Effect == EffectMemoryLost:
		// A refusal within the window is the store still refusing: the
		// memory line is blocked, not recovering, while that lasts.
		group.FailingNow++
	case blocked.Effect == EffectDelayed:
		group.Delayed++
	default:
		group.CompletingNow++
		if stalled(anomaly) {
			group.Stalled++
		}
	}
}

// recoveryOf reads the state from the counts noteProblem kept. A retained
// record fold is historical by construction: its objects run normally and
// the record is what is left.
func recoveryOf(group *CheckGroup, historical bool) Recovery {
	switch {
	case historical:
		return RecoveryHistorical
	case group.FailingNow > 0:
		return RecoveryBlocked
	case group.Stalled > 0:
		return RecoveryStalled
	case group.CompletingNow > 0 || group.Delayed > 0:
		return RecoveryRecovering
	default:
		return RecoveryUnconfirmed
	}
}

// ReportChecks folds every object in every column into the checks it is
// under, and adds the observation gaps -- which are not objects but are the
// same line: things this deployment cannot currently speak for.
//
// Ordered as checkOrder is, so the page renders the list in the order the
// reader acts and does not sort by a rule of its own.
func ReportChecks(columns [][]Anomaly, truncated map[string]bool, view *View, now time.Time) []CheckReport {
	return reportChecksFrom(checkRowsOf(columns, view, now), truncatedColumns(truncated), view, now)
}

// checkTally is one check's fold before it is a report.
type checkTally struct {
	objects    int
	strategies map[string]struct{}
	businesses map[string]struct{}
	groups     map[string]*CheckGroup
	groupSets  map[string][2]map[string]struct{}
	// columns is the columns its rows came from, a bit per position in
	// columnNames: the line is a sample when any of them was published cut,
	// on any replica, whichever replica its own rows came from.
	columns uint8
	demoted int
	// memoryGap counts the current objects carrying a MemoryGap, and
	// memoryGapUntil is the latest any of them settles by.
	memoryGap      int
	memoryGapUntil time.Time
	current        int
	retained       int
	lastHour       int
	newest         time.Time
	activation     *ActivationFacts
	replica        string
	rebalance      *RebalanceFacts
	skipped        *Consequence
	reasons        map[string]int
	// recovered is the objects that recovered from this line within the
	// retention, recoveredLast the latest of them.
	recovered     int
	recoveredLast time.Time
	// sourceStrategies is a source standing's count: withheld records,
	// summed over its groups, where the object lines count distinct
	// strategies behind objects.
	sourceStrategies int
	// onsets is the no-data lines' objects by the minute their run
	// began, for the fold that tells one event from many; withoutOnset
	// the rows of those lines that carry no start, so the fold's sum
	// and the line's count can be read against each other.
	onsets       map[time.Time]int
	withoutOnset int
}

// checkTallies is every check's fold, by check.
type checkTallies map[Check]*checkTally

func (tallies checkTallies) ensure(check Check) *checkTally {
	entry := tallies[check]
	if entry == nil {
		entry = &checkTally{strategies: map[string]struct{}{}, businesses: map[string]struct{}{},
			groups: map[string]*CheckGroup{}, groupSets: map[string][2]map[string]struct{}{}}
		tallies[check] = entry
	}
	return entry
}

// code is what the fold counts the row under, when the fold is not on
// the row's own deciding code: the second fact under DEFECT counts the
// internal code, not the refusal the row is listed for.
func (entry *checkTally) addAs(key string, anomaly *Anomaly, code string, now time.Time) {
	entry.objects++
	group := entry.groups[key]
	if group == nil {
		group = &CheckGroup{Key: key}
		entry.groups[key] = group
		entry.groupSets[key] = [2]map[string]struct{}{{}, {}}
	}
	group.Objects++
	if anomaly == nil {
		return
	}
	noteProblem(group, anomaly, code, now)
	sets := entry.groupSets[key]
	for _, strategy := range anomaly.Strategies {
		entry.strategies[strategy.StrategyID] = struct{}{}
		sets[0][strategy.StrategyID] = struct{}{}
		if strategy.BusinessID != "" {
			entry.businesses[strategy.BusinessID] = struct{}{}
			sets[1][strategy.BusinessID] = struct{}{}
		}
	}
}

// checkRowsOf is ReportChecks' rows half: every row of the columns, the
// skip records and the lists no column holds, each folded into the check it
// is under. It is what one replica's rows add (ReplicaPart.CheckRows) and
// what replicas' add up to (mergeCheckTallies). The view supplies the
// records and the lists, and may be nil.
func checkRowsOf(columns [][]Anomaly, view *View, now time.Time) checkTallies {
	tallies := checkTallies{}
	ensure := tallies.ensure
	addAs := func(entry *checkTally, key string, anomaly *Anomaly, code string) {
		entry.addAs(key, anomaly, code, now)
	}
	add := func(entry *checkTally, key string, anomaly *Anomaly) { entry.addAs(key, anomaly, "", now) }
	listed := map[string]struct{}{}
	for columnIndex, column := range columns {
		columnBit := uint8(0)
		if columnIndex < len(columnNames) {
			columnBit = 1 << columnIndex
		}
		for index := range column {
			anomaly := &column[index]
			check := anomaly.Finding.Check
			// A failure of this deployment's own making under a line the
			// column decided is a second fact, and it gets its second line:
			// the pool object filed as HTTP 400 that also hit an aggregation
			// conflict every round was invisible under the refusal. Read
			// before the row's own line is, because "under no line" is a
			// line the column decided too: a warming object -- nobody's,
			// heals on its own -- whose every round also ended in a state
			// version conflict was skipped here as nothing to report, while
			// opening the DEFECT line listed it. The line said two and its
			// rows were four.
			if anomaly.Internal != nil && check != CheckDefect {
				defect := ensure(CheckDefect)
				defect.columns |= columnBit
				addAs(defect, anomaly.Internal.Code, anomaly, anomaly.Internal.Code)
				defect.current++
				listed[underKey(CheckDefect, anomaly.QueryGroup)] = struct{}{}
			}
			if check == "" {
				continue
			}
			entry := ensure(check)
			entry.columns |= columnBit
			add(entry, anomaly.Finding.Group, anomaly)
			if check == CheckBookkeepingAbandoned {
				// Interrupted bookkeeping is a record, never work: the object
				// detected and alerted. Counted with the records, newest kept.
				entry.retained++
				if !anomaly.ReasonLastAt.IsZero() && anomaly.ReasonLastAt.After(entry.newest) {
					entry.newest = anomaly.ReasonLastAt
				}
				continue
			}
			entry.current++
			if columnIndex < len(columnNames) && columnNames[columnIndex] == ColumnDemoted {
				entry.demoted++
			}
			if gap := anomaly.MemoryGap; gap != nil {
				entry.memoryGap++
				if gap.Until != nil && gap.Until.After(entry.memoryGapUntil) {
					entry.memoryGapUntil = *gap.Until
				}
			}
			listed[underKey(check, anomaly.QueryGroup)] = struct{}{}
		}
	}
	// What this deployment gave up on and never evaluated, retained past the
	// rounds that followed. A record made within the window is a loss in
	// progress and is current; one that stopped is retained: an object that
	// skipped Slots an hour ago and has run normally since is under no
	// column, and it stays on this line until a restart forgets it, because
	// the loss is permanent and the row is the only record. A demoted
	// object's record is neither: it is the consequence of the line the
	// object is under, and is counted there.
	// And the objects whose data stopped: under no column either, their rounds
	// complete, and on the data side's line.
	if view != nil {
		rows, consequences := skippedRows(view, listed, now)
		for _, row := range rows {
			entry := ensure(row.Finding.Check)
			add(entry, row.Finding.Group, &row)
			if (row.Loss == LossOngoing || row.Loss == LossAfterRestart || row.Loss == LossAfterCooldown) && row.Finding.Check != CheckBookkeepingAbandoned {
				entry.current++
				if entry.reasons == nil {
					entry.reasons = map[string]int{}
				}
				reason := skipReasonNone
				if row.Skip != nil && row.Skip.Reason != "" {
					reason = row.Skip.Reason
				}
				entry.reasons[reason]++
				continue
			}
			entry.retained++
			if row.Skip != nil {
				if now.Sub(row.Skip.At) <= time.Hour {
					entry.lastHour++
				}
				if row.Skip.At.After(entry.newest) {
					entry.newest = row.Skip.At
				}
			}
		}
		for check, consequence := range consequences {
			ensure(check).skipped = consequence
		}
		for _, list := range [][]Anomaly{view.NoData, view.NoDataMemory, view.RetainedShare, view.ReadEarly, view.ReadHeld, view.LateSeries} {
			for index := range list {
				row := &list[index]
				if row.Finding.Check == "" {
					continue
				}
				entry := ensure(row.Finding.Check)
				add(entry, row.Finding.Group, row)
				entry.current++
				// These lines fold by strategy, one object to a group, and
				// three hundred groups of one cannot show that three
				// hundred runs began in the same two minutes. The onset
				// fold can: a platform event is one minute with hundreds,
				// a population of quiet sources is hundreds of minutes with
				// one. On a live deployment 327 of 366 empty runs began in
				// two minutes -- the minutes a release began recording
				// them, a lower bound and not an event, and the fold is
				// what shows either.
				if row.Kind == KindEmptyEveryRound || row.Kind == KindNoData {
					if row.Since.IsZero() {
						entry.withoutOnset++
					} else {
						if entry.onsets == nil {
							entry.onsets = map[time.Time]int{}
						}
						entry.onsets[row.Since.UTC().Truncate(time.Minute)]++
					}
				}
			}
		}
	}
	return tallies
}

// reportChecksFrom adds what is not a row -- what the view cannot speak
// for, the standings, the problems that recovered, the source's -- to the
// rows' folds, and makes the reports. The folds are finished in place.
func reportChecksFrom(tallies checkTallies, truncated uint8, view *View, now time.Time) []CheckReport {
	ensure := tallies.ensure
	add := func(entry *checkTally, key string, anomaly *Anomaly) { entry.addAs(key, anomaly, "", now) }
	// What the view cannot speak for. Unknown is the objects a replica holds
	// and has said nothing conclusive about; the gaps are the reasons the rest
	// of the answer may be incomplete. Neither has objects the list can show,
	// so they are groups with a count and no rows behind them.
	if view != nil {
		if view.Unknown > 0 {
			entry := ensure(CheckObservationGap)
			add(entry, string(GapUndetermined), nil)
			entry.objects += view.Unknown - 1
			entry.current += view.Unknown
			entry.groups[string(GapUndetermined)].Objects += view.Unknown - 1
		}
		for _, gap := range view.Gaps {
			if gap.Kind == GapUndetermined {
				// Counted above, by object, rather than once per replica here.
				continue
			}
			entry := ensure(CheckObservationGap)
			if entry.groups[string(gap.Kind)] == nil {
				entry.groups[string(gap.Kind)] = &CheckGroup{Key: string(gap.Kind)}
			}
		}
		// The standings. Not objects either: the fleet executing a stale
		// publication is one fact about the whole deployment, folded on why
		// the activation fails; a replica past a bound is one fact per
		// replica, folded on which bound.
		if view.Activation != nil && view.Activation.BehindBeyondBound {
			entry := ensure(CheckCutoverFailing)
			entry.activation, entry.replica = view.Activation, view.ActivationReplica
			key := view.Activation.Reason()
			entry.groups[key] = &CheckGroup{Key: key, Replicas: []string{view.ActivationReplica}}
		}
		for _, degradation := range view.Degradations {
			if degradation.Kind == DegradationActivationBehind || degradation.Kind == DegradationSourceBlocked {
				// Their own lines: the cutover line above, the source
				// standings below.
				continue
			}
			entry := ensure(CheckReplicaDegraded)
			group := entry.groups[string(degradation.Kind)]
			if group == nil {
				group = &CheckGroup{Key: string(degradation.Kind)}
				entry.groups[string(degradation.Kind)] = group
			}
			group.Replicas = append(group.Replicas, degradation.Replica)
			group.Degradations = append(group.Degradations, degradation)
			if group.Text == "" && degradation.Text != "" {
				group.Stage, group.Text = degradation.Stage, degradation.Text
			}
		}
		// The third standing: the scheduler would move objects between the
		// two replicas it names. Folded on the loaded one, the pair on the
		// group, the round whole on the line. The judgement is the
		// scheduler's tolerance, not a share the page decides is too much.
		if view.Rebalance.Skewed() {
			entry := ensure(CheckOwnershipSkewed)
			entry.rebalance, entry.replica = view.Rebalance, view.RebalanceReplica
			key := view.Rebalance.MostOwnedBy
			entry.groups[key] = &CheckGroup{Key: key, Replicas: []string{view.Rebalance.MostOwnedBy, view.Rebalance.LeastOwnedBy}}
		}
		// The problems whose objects recovered. Onto the fold they were under:
		// beside the objects still there as how far it has come back, or as
		// the whole fold when none is left -- which is the RECOVERED reading's
		// only producer. The clocks the fold shows for a recovered problem are
		// the record's, since no row is left to read them from.
		for _, problem := range view.Recovered {
			if problem.Objects == 0 || problem.Check == "" {
				continue
			}
			entry := ensure(problem.Check)
			group := entry.groups[problem.Key]
			if group == nil {
				group = &CheckGroup{Key: problem.Key}
				entry.groups[problem.Key] = group
			}
			group.Recovered += problem.Objects
			entry.recovered += problem.Objects
			if !problem.FirstRecovery.IsZero() && (group.RecoveredFirst == nil || problem.FirstRecovery.Before(*group.RecoveredFirst)) {
				at := problem.FirstRecovery
				group.RecoveredFirst = &at
			}
			if !problem.LastRecovery.IsZero() {
				at := problem.LastRecovery
				if group.RecoveredLast == nil || at.After(*group.RecoveredLast) {
					group.RecoveredLast = &at
				}
				if at.After(entry.recoveredLast) {
					entry.recoveredLast = at
				}
				// A healthy completion after the failure is the success the
				// recovery reading asks for, on this fold.
				if group.LastSuccess == nil || at.After(*group.LastSuccess) {
					group.LastSuccess = &at
				}
			}
			if group.Codes == nil {
				// No row left: the record's clocks are the fold's.
				if !problem.FirstFailure.IsZero() && (group.FirstFailure == nil || problem.FirstFailure.Before(*group.FirstFailure)) {
					at := problem.FirstFailure
					group.FirstFailure = &at
				}
				if !problem.LastFailure.IsZero() && (group.LastFailure == nil || problem.LastFailure.After(*group.LastFailure)) {
					at := problem.LastFailure
					group.LastFailure = &at
				}
			}
		}
		// The source standings: every withheld group of the leader's last
		// round, folded on its reason under the line its disposition owns.
		// The line's strategy count is the sum of its groups; a strategy
		// withheld at two levels is two records in the source and counts
		// twice here, as it does in the control plane's own gauge.
		if view.Source != nil {
			for _, withheld := range view.Source.Withheld {
				check, known := sourceCheckOf(withheld.Disposition, withheld.Reason)
				if !known || withheld.Count == 0 {
					continue
				}
				entry := ensure(check)
				entry.replica = view.SourceReplica
				entry.sourceStrategies += withheld.Count
				key := withheld.Reason
				if withheld.Disposition == dispositionStaleConfig {
					// Same reason, different consequence: the strategy still
					// runs its last good Plan. Folded apart so the count of
					// strategies not detecting is not inflated by ones that are.
					key = withheld.Disposition + "/" + withheld.Reason
				}
				group := &CheckGroup{Key: key, Strategies: withheld.Count, Replicas: []string{view.SourceReplica},
					Disposition: withheld.Disposition, Samples: withheld.Samples}
				if check == CheckCapabilityUnsupported || check == CheckCapabilityUnlisted || check == CheckConfigNoted {
					words := WithheldWordsOf(withheld.Reason)
					group.Words = &words
				} else if words, known := withheldReasonWords[withheld.Reason]; known {
					// A reason moved onto another line keeps the words the
					// table has for it.
					group.Words = &words
				}
				entry.groups[key] = group
			}
		}
	}
	// The running count rides on the bookkeeping line, and makes the line
	// when the records alone would not: a record keeps one span per object.
	if view != nil && view.BookkeepingAbandoned != nil && view.BookkeepingAbandoned.Slots > 0 {
		ensure(CheckBookkeepingAbandoned)
	}
	// The set flapping: the hours of the last day in which the source listed
	// strategies again after dropping them, one fold per hour with the
	// strategies that came back. A standing over the leader's account, not
	// over object rows; the strategies under grace right now are on the
	// source facts for the first screen, not a fold here -- a fold is a
	// thing that happened, and grace is a thing that is happening.
	if view != nil && view.Source != nil && view.Source.Set != nil {
		for _, hour := range view.Source.Set.Hours {
			if hour.Reactivated == 0 || now.Sub(hour.Hour) > 24*time.Hour {
				continue
			}
			entry := ensure(CheckSourceSetFlapping)
			entry.replica = view.SourceReplica
			entry.sourceStrategies += hour.Reactivated
			key := hour.Hour.UTC().Format("2006-01-02T15Z")
			group := &CheckGroup{Key: key, Strategies: hour.Reactivated, Replicas: []string{view.SourceReplica},
				Text: sourceSetHourText(hour)}
			for _, strategyID := range hour.Samples {
				group.Samples = append(group.Samples, WithheldSample{StrategyID: strategyID, Scope: "STRATEGY"})
			}
			entry.groups[key] = group
		}
	}
	reports := make([]CheckReport, 0, len(tallies))
	for check, entry := range tallies {
		report := CheckReport{Code: check, Owner: checkAnswers[check].Owner, GroupBy: checkAnswers[check].GroupBy,
			Objects: entry.objects, Strategies: len(entry.strategies), Businesses: len(entry.businesses),
			Partial: entry.columns&truncated != 0, Demoted: entry.demoted, MemoryGap: entry.memoryGap, MemoryGapUntil: timeOrNil(entry.memoryGapUntil),
			Activation: entry.activation, Replica: entry.replica,
			Current: entry.current, Retained: entry.retained, RetainedLastHour: entry.lastHour,
			Consequence: entry.skipped, SkipReasons: entry.reasons, Rebalance: entry.rebalance,
			Recovered: entry.recovered, Onsets: onsetFold(entry.onsets, entry.withoutOnset)}
		if check.SourceStanding() {
			report.Strategies = entry.sourceStrategies
		}
		if check == CheckBookkeepingAbandoned && view != nil && view.BookkeepingAbandoned != nil {
			facts := *view.BookkeepingAbandoned
			report.Bookkeeping = &facts
		}
		if !entry.newest.IsZero() {
			newest := entry.newest
			report.RetainedNewest = &newest
		}
		if !entry.recoveredLast.IsZero() {
			last := entry.recoveredLast
			report.RecoveredLast = &last
		}
		for key, group := range entry.groups {
			if sets, known := entry.groupSets[key]; known {
				group.Strategies, group.Businesses = len(sets[0]), len(sets[1])
			}
			// A fold with object rows behind it gets its recovery reading; a
			// standing's fold and a gap's have no rows and no reading. Record
			// folds of stopped loss are historical; the folds of loss in
			// progress are read like any other. A fold with no rows and
			// recoveries behind it is a problem that recovered.
			switch {
			case group.Codes != nil:
				group.Recovery = recoveryOf(group, key == string(LossHistorical) || key == string(LossWhileDemoted) ||
					check == CheckBookkeepingAbandoned)
			case group.Recovered > 0:
				group.Recovery = RecoveryRecovered
			}
			report.Groups = append(report.Groups, *group)
		}
		sort.Slice(report.Groups, func(i, j int) bool {
			if report.Groups[i].Objects != report.Groups[j].Objects {
				return report.Groups[i].Objects > report.Groups[j].Objects
			}
			return report.Groups[i].Key < report.Groups[j].Key
		})
		if check == CheckCapabilityUnsupported || check == CheckCapabilityUnlisted {
			report.Line = capabilityLine(report.Strategies, report.Groups)
		}
		if check == CheckConfigNoted {
			report.Owner = notedOwner(report.Groups)
		}
		if check == CheckSourceSetFlapping && view != nil && view.Source != nil && view.Source.Set != nil {
			// Newest hour first: the question is "is it still happening".
			sort.Slice(report.Groups, func(i, j int) bool { return report.Groups[i].Key > report.Groups[j].Key })
			report.Line = sourceSetLine(report.Strategies, len(report.Groups), view.Source.Set)
		}
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool {
		return checkRank(reports[i].Code) < checkRank(reports[j].Code)
	})
	return reports
}

// Todo is the first screen's arithmetic, done once here rather than by the
// page adding lines up. The page summed every check's object count and said
// "需要处理 768 个对象": records of past losses counted in, and an object under
// two lines counted twice. What a reader needs is how many lines are theirs,
// how many distinct objects those lines cover now, and -- apart from that --
// how much was lost in the past and how much of it just now.
//
// Three parts, because "not confirmed as this deployment's" and "confirmed
// as somebody else's" are different statements and the page made them one:
// it said the rest were the strategy's or the data's people while the lines
// under it still read 待确认. Checks and Objects are what is confirmed as
// this deployment's; Undetermined what nobody can yet hand to anyone;
// Governance what is confirmed as the strategy's or the data's.
type Todo struct {
	// Checks is the lines confirmed as this deployment's that have something
	// on them now: an object, or a standing of the deployment itself.
	Checks int `json:"checks"`
	// Objects is the distinct objects under those lines, now. An object
	// under two lines is one object.
	Objects int `json:"objects"`
	// Undetermined is the lines whose owner the evidence does not decide,
	// and the distinct objects under them. Not this deployment's to fix and
	// not yet anybody else's: the part a reader must not hand over.
	Undetermined        int `json:"undetermined"`
	UndeterminedObjects int `json:"undetermined_objects"`
	// Ongoing is the distinct objects, not in the demoted pool, whose latest
	// skip record was made within RecentWindowSeconds and not in their
	// replica's restart grace: detection being lost now, by a mechanism
	// that is not the restart. OngoingNewest is the latest of them. They
	// are on the current lines and in Objects; they are named here because
	// they are the answer to "is it still happening", which the record
	// count is not. AfterRestart is the objects whose record falls in the
	// grace after their replica started: the restart's catch-up, current
	// and on the lines and in Objects too, named apart because it is
	// expected to stop on its own and asks no capacity question.
	Ongoing             int        `json:"ongoing"`
	OngoingNewest       *time.Time `json:"ongoing_newest,omitempty"`
	AfterRestart        int        `json:"after_restart"`
	RestartGraceSeconds int        `json:"restart_grace_seconds"`
	// AfterCooldown is the recent records whose Slot a query cooldown held
	// on an object that has since left the pool: the cooldown's consequence
	// with no line to fold onto, named apart from Ongoing because it asks
	// no capacity question.
	AfterCooldown int `json:"after_cooldown"`
	// RestartGraceUnknown is the recent records judged against no anchor --
	// a publisher that sent neither its process start nor when it first saw
	// the object -- and so read by age alone. Counted rather than silent:
	// such a publisher would otherwise make every recent skip read ONGOING.
	RestartGraceUnknown int `json:"restart_grace_unknown"`
	// WhileDemoted is the distinct objects in the demoted pool that also
	// skipped detection there, and how many within the window: the
	// cooldown's consequence, counted on the lines the objects are under.
	WhileDemoted       int `json:"while_demoted"`
	WhileDemotedRecent int `json:"while_demoted_recent"`
	// Retained is the distinct objects with a record of a loss that stopped:
	// older than the window, and not demoted. RetainedLastHour is how many of
	// those stopped within the last hour, RetainedNewest the latest. The
	// record keeps one skip per object, so neither says how many objects
	// were added.
	Retained         int        `json:"retained"`
	RetainedLastHour int        `json:"retained_last_hour"`
	RetainedNewest   *time.Time `json:"retained_newest,omitempty"`
	// RecentWindowSeconds is the window the counts above are decided on,
	// so the page prints the bound it was measured with.
	RecentWindowSeconds int `json:"recent_window_seconds"`
	// Governance is the lines already confirmed as somebody else's, and the
	// distinct objects under them.
	Governance        int `json:"governance"`
	GovernanceObjects int `json:"governance_objects"`
	// PlatformChecks is how many of Checks are the platform's lines -- a
	// strategy the platform wrote unusably, which the operator of this
	// deployment goes and says so about -- and PlatformStrategies the
	// strategies under them. They are in Checks, and named apart: a reader
	// told "alarmd 已确认 10 类" who finds one owned by the platform's cache
	// writer has been told the wrong thing, and the count of strategies is
	// not a count of objects. The page derived this by walking the lines;
	// a reader of the JSON had nothing to read.
	PlatformChecks     int `json:"platform_checks"`
	PlatformStrategies int `json:"platform_strategies"`
}

// SummarizeTodo counts the first screen. The reports say which lines exist;
// the columns say which objects are under them, so the distinct count is
// taken from the objects and not from the lines.
func SummarizeTodo(reports []CheckReport, columns [][]Anomaly, view *View, now time.Time) Todo {
	return todoFrom(todoRowsOf(columns, view, now), reports, view)
}

// todoRowsOf is SummarizeTodo's rows half: the distinct objects each owner
// has among the rows and the records, and the records' counts. Replicas
// hold separate objects, so one replica's half adds to another's
// (mergeTodoRows).
func todoRowsOf(columns [][]Anomaly, view *View, now time.Time) Todo {
	todo := Todo{RecentWindowSeconds: int(RecentSkipWindow / time.Second), RestartGraceSeconds: int(RestartCatchUpGrace / time.Second)}
	ours := map[string]struct{}{}
	undetermined := map[string]struct{}{}
	theirs := map[string]struct{}{}
	count := func(list []Anomaly) {
		for _, anomaly := range list {
			// Interrupted bookkeeping is a record and nobody's work: its
			// object detected and alerted, and its line carries its own count.
			if anomaly.Finding.Check == "" || anomaly.Finding.Check == CheckBookkeepingAbandoned {
				continue
			}
			switch checkAnswers[anomaly.Finding.Check].Owner {
			case OwnerAlarmd:
				ours[anomaly.QueryGroup] = struct{}{}
			case OwnerUndetermined:
				undetermined[anomaly.QueryGroup] = struct{}{}
			default:
				theirs[anomaly.QueryGroup] = struct{}{}
			}
		}
	}
	for _, column := range columns {
		count(column)
	}
	if view != nil {
		count(view.NoData)
		count(view.NoDataMemory)
		count(view.RetainedShare)
		count(view.ReadEarly)
		count(view.ReadHeld)
		count(view.LateSeries)
	}
	if view != nil {
		// One walk over the records, the same one the lines make. A loss in
		// progress is this deployment's and current, so its object counts
		// with ours; a demoted object's record is its line's consequence; a
		// stopped loss is the record.
		var ongoingNewest, retainedNewest time.Time
		whileDemoted := map[string]struct{}{}
		lossRecords(view, now, func(queryGroup string, check, _ Check, _ string, skip SkippedSpan, loss Loss, graceUnknown bool) {
			if check == CheckBookkeepingAbandoned {
				// Not a loss of detection in progress or on record: the
				// bookkeeping line counts these itself.
				return
			}
			if graceUnknown && now.Sub(skip.At) <= RecentSkipWindow {
				// Judged without an anchor: counted, so a publisher that
				// sends none cannot make every recent skip read ONGOING in
				// silence.
				todo.RestartGraceUnknown++
			}
			switch loss {
			case LossOngoing:
				ours[queryGroup] = struct{}{}
				todo.Ongoing++
				if skip.At.After(ongoingNewest) {
					ongoingNewest = skip.At
				}
			case LossAfterRestart:
				ours[queryGroup] = struct{}{}
				todo.AfterRestart++
			case LossAfterCooldown:
				// The cooldown's, like WHILE_DEMOTED, with no line left to
				// fold onto: on the record line, named apart, not this
				// deployment's work.
				todo.AfterCooldown++
			case LossWhileDemoted:
				whileDemoted[queryGroup] = struct{}{}
				if now.Sub(skip.At) <= RecentSkipWindow {
					todo.WhileDemotedRecent++
				}
			default:
				todo.Retained++
				if now.Sub(skip.At) <= time.Hour {
					todo.RetainedLastHour++
				}
				if skip.At.After(retainedNewest) {
					retainedNewest = skip.At
				}
			}
		})
		todo.WhileDemoted = len(whileDemoted)
		if !ongoingNewest.IsZero() {
			todo.OngoingNewest = &ongoingNewest
		}
		if !retainedNewest.IsZero() {
			todo.RetainedNewest = &retainedNewest
		}
	}
	todo.Objects, todo.UndeterminedObjects, todo.GovernanceObjects = len(ours), len(undetermined), len(theirs)
	return todo
}

// todoFrom adds the objects nobody can speak for and the lines to the rows'
// half.
func todoFrom(todo Todo, reports []CheckReport, view *View) Todo {
	if view != nil {
		// The objects a replica holds and has said nothing conclusive about
		// are under OBSERVATION_GAP and have no row to be distinct by; they
		// are in no column, so adding the count cannot double-count.
		todo.Objects += view.Unknown
	}
	for _, report := range reports {
		up := report.Current > 0 || report.Code.Standing()
		switch {
		case !report.ActionRequired():
			// A governance line with nothing under it now -- there only for
			// the problems that recovered from it -- is not a line in
			// governance; the page lists it with the history.
			if report.Objects > 0 || report.Recovered == 0 {
				todo.Governance++
			}
		case report.Owner == OwnerUndetermined && up:
			todo.Undetermined++
		case up:
			todo.Checks++
			if report.Owner == OwnerPlatform {
				todo.PlatformChecks++
				todo.PlatformStrategies += report.Strategies
			}
		}
	}
	return todo
}

func (owner Owner) actionRequired() bool {
	// The platform's lines are on the first screen with this deployment's:
	// a strategy the platform wrote unusably is not detecting, and the
	// operator of this deployment is the one who can go and say so.
	return owner == OwnerAlarmd || owner == OwnerUndetermined || owner == OwnerPlatform
}

// columnNames is the order the handler passes the columns in, so a truncated
// flag can be looked up by position.
var columnNames = []string{ColumnAnomalies, ColumnDemoted, ColumnUndecidable, ColumnByDesign}

// ActionRequired reports whether the check is this reader's to act on: this
// deployment's own, or one nobody can yet hand to anyone.
func (report CheckReport) ActionRequired() bool {
	return report.Owner.actionRequired()
}

func knownCheck(name string) bool {
	_, known := checkAnswers[Check(name)]
	return known
}

func checkNames() []string {
	checks := Checks()
	names := make([]string, len(checks))
	for index, check := range checks {
		names[index] = string(check)
	}
	return names
}

// UnderCheck is every object in every column that is under one check, and
// within one of its groups when group is not empty, plus the retained skip
// records under it. This is the list a line on the first screen opens; its
// total is how many it holds.
func UnderCheck(check Check, group string, view *View, now time.Time) []Anomaly {
	list := []Anomaly{}
	walkObjectRows(check, group, "", view, now, func(row Anomaly) { list = append(list, row) })
	// Loss records are read newest first; current problems oldest first.
	if check == CheckDetectionAbandoned || check == CheckTimelinePruned || check == CheckBookkeepingAbandoned {
		SortAnomaliesNewestFirst(list)
	} else {
		sortOldestFirst(list)
	}
	return list
}

// walkObjectRows is the shared list/detail resolver. Empty check keeps all
// facts, in column order followed by retained records, for legacy detail URLs.
// A selected check never falls back to another fact about the same object.
func walkObjectRows(check Check, group, queryGroup string, view *View, now time.Time, visit func(Anomaly)) {
	listed := map[string]struct{}{}
	// Detail only needs this object's demotion and retained records. Narrow
	// those maps before resolving losses, rather than materializing every
	// retained row just to return one object.
	selected := *view
	if queryGroup != "" {
		selected.Demoted = nil
		for _, row := range view.Demoted {
			if row.QueryGroup == queryGroup {
				selected.Demoted = append(selected.Demoted, row)
			}
		}
		selected.GapSkips = map[string]SkippedSpan{}
		if skip, ok := view.GapSkips[queryGroup]; ok {
			selected.GapSkips[queryGroup] = skip
		}
		selected.PrunedSkips = map[string]PrunedSkip{}
		if skip, ok := view.PrunedSkips[queryGroup]; ok {
			selected.PrunedSkips[queryGroup] = skip
		}
	}
	demoted := demotedObjects(&selected)
	for _, column := range [][]Anomaly{view.Anomalies, view.Demoted, view.Undecidable, view.ByDesign, view.NoData, view.NoDataMemory, view.RetainedShare, view.ReadEarly, view.ReadHeld, view.LateSeries} {
		for _, anomaly := range column {
			if queryGroup != "" && anomaly.QueryGroup != queryGroup {
				continue
			}
			// Under DEFECT a row is also the one whose column filed it
			// elsewhere but which carries a failure of this deployment's own
			// making: listed by that failure's code, the second fact.
			if check == CheckDefect && anomaly.Finding.Check != check && anomaly.Internal != nil {
				listed[underKey(check, anomaly.QueryGroup)] = struct{}{}
				if group == "" || anomaly.Internal.Code == group {
					standing := standingOf(anomaly)
					anomaly.Standing = &standing
					visit(anomaly)
				}
				continue
			}
			if check != "" && anomaly.Finding.Check != check {
				continue
			}
			// Marked as listed before the group narrows, so an object in
			// another group is not re-listed from its retained skip.
			listed[underKey(anomaly.Finding.Check, anomaly.QueryGroup)] = struct{}{}
			if group != "" && anomaly.Finding.Group != group {
				continue
			}
			// A demoted object's record rides on its own row: what it lost
			// while under this line, said on the row rather than on a line
			// that would file it as this deployment's capacity.
			if object, isDemoted := demoted[anomaly.QueryGroup]; isDemoted && object.line != "" && anomaly.Skip == nil {
				if skip, recorded := view.GapSkips[anomaly.QueryGroup]; recorded && !skip.At.Before(object.since) {
					record := skip
					anomaly.Skip, anomaly.Loss = &record, LossWhileDemoted
				}
			}
			standing := standingOf(anomaly)
			anomaly.Standing = &standing
			visit(anomaly)
		}
	}
	rows, _ := skippedRows(&selected, listed, now)
	for _, row := range rows {
		if (check != "" && row.Finding.Check != check) || (group != "" && row.Finding.Group != group) {
			continue
		}
		standing := standingOf(row)
		row.Standing = &standing
		visit(row)
	}
}

// skipReasonNone is the fold of a skip that followed no failure of its
// Slot's: the Slot fell past the replay bound with nothing tried on it.
const skipReasonNone = "NOTHING_TRIED"

// underKey names one object under one check, so a retained skip does not add
// a second row for an object a column already lists under that check while an
// object listed under some other check still gets its skip row: those are two
// facts, and Ceph lists an OSD under every check it fails.
func underKey(check Check, queryGroup string) string {
	return string(check) + "|" + queryGroup
}

// skippedRows turns the view's retained skip records into rows for the two
// record lines, one per object not already listed under the same check: a
// pruned span is TIMELINE_PRUNED and a replay-window skip is
// DETECTION_ABANDONED, both this deployment's, folded on what the loss is --
// in progress, or stopped. A demoted object's record is not a row here: it
// is handed back as the consequence of the line the object is under, keyed
// by that line, because the cooldown that line reports is what skipped the
// rounds. A demoted object under no line -- which the tracker does not
// produce -- is treated like any other.
func skippedRows(view *View, listed map[string]struct{}, now time.Time) ([]Anomaly, map[Check]*Consequence) {
	rows := []Anomaly{}
	consequences := map[Check]*Consequence{}
	lossRecords(view, now, func(queryGroup string, check, line Check, reason string, skip SkippedSpan, loss Loss, _ bool) {
		if loss == LossWhileDemoted {
			if consequences[line] == nil {
				consequences[line] = &Consequence{}
			}
			consequences[line].note(skip.At, now)
			return
		}
		if _, already := listed[underKey(check, queryGroup)]; already {
			return
		}
		listed[underKey(check, queryGroup)] = struct{}{}
		record := skip
		item := Anomaly{QueryGroup: queryGroup, Kind: KindSkippedSpan, ReasonCode: reason,
			Since: skip.At, SinceFrom: SinceSnapshotContinuity, Replica: skip.Replica, Skip: &record,
			Strategies: skip.Strategies, Loss: loss}
		group := string(loss)
		if check == CheckBookkeepingAbandoned {
			// Folded on the replica whose Progress write was lost: which store
			// client is failing is the question, not what kind of loss -- there
			// is no detection loss.
			group = skip.Replica
		}
		item.Finding = Finding{Check: check, Group: group, Owner: checkAnswers[check].Owner}
		item.Attribution = attributionOf(item)
		// The record in the one shape every failure is read in: a persisted
		// skip, which is the confirmed loss.
		item.Blocked = blockedOf(item, item.Finding.Schedule)
		rows = append(rows, item)
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].QueryGroup < rows[j].QueryGroup })
	return rows, consequences
}

// sourceSetHourText is one hour's fold as a sentence fragment: how many
// came back and how long the longest was gone.
func sourceSetHourText(hour SourceSetHour) string {
	text := fmt.Sprintf("%d 条策略回到活动集", hour.Reactivated)
	if hour.LongestAbsentSeconds > 0 {
		text += fmt.Sprintf("，最长缺席 %.0f 分钟", hour.LongestAbsentSeconds/60)
	}
	// Removed counts the Plans that left the Catalog in the hour; the ones
	// that came back after it are their own count.
	if hour.ReturnedAfterRemoval > 0 {
		text += fmt.Sprintf("，其中 %d 条是已被移除后重新放置", hour.ReturnedAfterRemoval)
	}
	return text
}

// sourceSetLine is the line's sentence: how often in the last day the
// source's list dropped strategies and listed them again, and what is under
// grace right now.
func sourceSetLine(strategies, hours int, set *SourceSetFacts) string {
	line := fmt.Sprintf("上游活动集近 24 小时有 %d 个小时掉过策略又列回来，共 %d 条次", hours, strategies)
	if set.PendingRemoval > 0 {
		line += fmt.Sprintf("；此刻 %d 条在宽限中", set.PendingRemoval)
	}
	if set.ReactivatedThisHour > 0 {
		line += fmt.Sprintf("；本小时已回来 %d 条", set.ReactivatedThisHour)
	}
	return line + fmt.Sprintf("（账自 %s 起）", set.Since.UTC().Format("01-02 15:04Z"))
}

// notedOwner is whose the CONFIG_NOTED line is: the strategy's when
// any of its reasons asks the strategy to change what it wrote, nobody's when
// none does. The strategies under it are all detecting, and a line that asks
// for an edit nobody should make teaches its reader to skip the line, the
// reasons that do need one included. A reason with no action of its own is
// taken as asking for the edit, which is what the line asked before reasons
// carried one.
func notedOwner(groups []CheckGroup) Owner {
	for _, group := range groups {
		if group.Words == nil || group.Words.Action != ActionNone {
			return OwnerStrategy
		}
	}
	return OwnerNobody
}

// mergeTodoRows adds one replica's rows' half of the to-do into another's.
func mergeTodoRows(into *Todo, from Todo) {
	into.RecentWindowSeconds, into.RestartGraceSeconds = from.RecentWindowSeconds, from.RestartGraceSeconds
	into.Objects += from.Objects
	into.UndeterminedObjects += from.UndeterminedObjects
	into.GovernanceObjects += from.GovernanceObjects
	into.Ongoing += from.Ongoing
	into.AfterRestart += from.AfterRestart
	into.AfterCooldown += from.AfterCooldown
	into.RestartGraceUnknown += from.RestartGraceUnknown
	into.WhileDemoted += from.WhileDemoted
	into.WhileDemotedRecent += from.WhileDemotedRecent
	into.Retained += from.Retained
	into.RetainedLastHour += from.RetainedLastHour
	into.OngoingNewest = latest(into.OngoingNewest, from.OngoingNewest)
	into.RetainedNewest = latest(into.RetainedNewest, from.RetainedNewest)
}

// truncatedColumns is which columns were published cut, a bit per position
// in columnNames.
func truncatedColumns(truncated map[string]bool) uint8 {
	mask := uint8(0)
	for index, name := range columnNames {
		if truncated[name] {
			mask |= 1 << index
		}
	}
	return mask
}
