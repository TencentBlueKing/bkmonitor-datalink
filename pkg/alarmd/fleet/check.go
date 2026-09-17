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
	CheckOwnershipSkewed     Check = "OWNERSHIP_SKEWED"
	CheckSlotsOverdue        Check = "SLOTS_OVERDUE"
	CheckNeverEvaluated      Check = "NEVER_EVALUATED"
	CheckRoundsStalled       Check = "ROUNDS_STALLED"
	CheckDetectionAbandoned  Check = "DETECTION_ABANDONED"
	CheckTimelinePruned      Check = "TIMELINE_PRUNED"
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
	CheckNoDataPersistent   Check = "NO_DATA_PERSISTENT"
	CheckSeriesChurning     Check = "SERIES_CHURNING"
	CheckSeriesDataMissing  Check = "SERIES_DATA_MISSING"
	CheckWindowUndecided    Check = "WINDOW_UNDECIDED"
	CheckPlanUnevaluable    Check = "PLAN_UNEVALUABLE"
	CheckConfigUnresolved   Check = "CONFIG_UNRESOLVED"
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
	CheckConfigRejected        Check = "CONFIG_REJECTED"
)

// sourceChecks maps a withheld disposition to the standing that carries it.
// STALE_CONFIG rides with CONFIG_REJECTED: the same refusal, on a strategy
// that still runs its last good Plan, and the group says which.
var sourceChecks = map[string]Check{
	dispositionSourceIncomplete:      CheckSourceIncomplete,
	dispositionCapabilityUnsupported: CheckCapabilityUnsupported,
	dispositionConfigRejected:        CheckConfigRejected,
	dispositionStaleConfig:           CheckConfigRejected,
}

// GroupBy is the key a check's objects are folded on. One backend not
// answering is one line with sixty objects under it, not sixty lines; which
// key makes the fold is a property of the check.
type GroupBy string

const (
	GroupByReplica    GroupBy = "replica"
	GroupByReasonCode GroupBy = "reason_code"
	GroupByDetail     GroupBy = "detail"
	GroupByStrategy   GroupBy = "strategy"
	GroupByGapKind    GroupBy = "gap_kind"
	// GroupByCause folds on what the window counts say happened: the reason
	// the detection could not use the record, or that the series are a mix of
	// new and old. It is the fold for the one check whose objects share a
	// symptom and not yet an owner.
	GroupByCause GroupBy = "cause"
	// GroupByDegradation folds on the kind of replica-level standing, the
	// closed DegradationKinds; the replicas in it are named on the group.
	GroupByDegradation GroupBy = "degradation"
)

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
	CheckCapabilityUnsupported: {OwnerAlarmd, GroupByReasonCode},
	CheckConfigRejected:        {OwnerStrategy, GroupByReasonCode},
	CheckCutoverFailing:        {OwnerAlarmd, GroupByReasonCode},
	CheckReplicaDegraded:       {OwnerAlarmd, GroupByDegradation},
	CheckOwnershipSkewed:       {OwnerAlarmd, GroupByReplica},
	CheckSlotsOverdue:          {OwnerAlarmd, GroupByReplica},
	CheckNeverEvaluated:        {OwnerAlarmd, GroupByReplica},
	CheckRoundsStalled:         {OwnerAlarmd, GroupByReplica},
	CheckDetectionAbandoned:    {OwnerAlarmd, GroupByLoss},
	CheckTimelinePruned:        {OwnerAlarmd, GroupByLoss},
	CheckDependencyDown:        {OwnerAlarmd, GroupByReasonCode},
	CheckDefect:                {OwnerAlarmd, GroupByReasonCode},
	CheckObservationGap:        {OwnerAlarmd, GroupByGapKind},

	CheckNoDataPersistent: {OwnerData, GroupByStrategy},

	CheckSeriesChurning:     {OwnerStrategy, GroupByStrategy},
	CheckPlanUnevaluable:    {OwnerStrategy, GroupByStrategy},
	CheckQueryTargetMissing: {OwnerStrategy, GroupByDetail},

	CheckQueryRefused:     {OwnerUndetermined, GroupByDetail},
	CheckWindowUndecided:  {OwnerUndetermined, GroupByCause},
	CheckConfigUnresolved: {OwnerUndetermined, GroupByStrategy},
	// A client-side timeout does not establish a fault on the data side: the
	// query's budget, the network and the backend's own latency all have to
	// be read first. And a window short of old-series points may be short
	// because this deployment did not fetch them. Both were handed to the
	// data owner as confirmed; a live review found neither confirmed.
	CheckBackendNotAnswering: {OwnerUndetermined, GroupByDetail},
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
	CheckCapabilityUnsupported,
	CheckCutoverFailing,
	CheckReplicaDegraded,
	CheckOwnershipSkewed,
	CheckSlotsOverdue,
	CheckNeverEvaluated,
	CheckRoundsStalled,
	CheckDetectionAbandoned,
	CheckTimelinePruned,
	CheckDependencyDown,
	CheckDefect,
	CheckObservationGap,
	CheckQueryRefused,
	CheckWindowUndecided,
	CheckConfigUnresolved,
	CheckBackendNotAnswering,
	CheckSeriesDataMissing,
	CheckNoDataPersistent,
	CheckSeriesChurning,
	CheckPlanUnevaluable,
	CheckQueryTargetMissing,
	CheckConfigRejected,
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
	return check == CheckSourceIncomplete || check == CheckCapabilityUnsupported || check == CheckConfigRejected
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
	failureCode := ""
	if anomaly.Failure != nil {
		failureCode = anomaly.Failure.Code
	}
	for _, code := range []string{anomaly.CauseReason, string(anomaly.Cause), failureCode, anomaly.ReasonCode} {
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
	case GroupByDetail:
		if anomaly.Failure != nil && anomaly.Failure.Detail != "" {
			return anomaly.Failure.Detail
		}
		if code := decidingCode(anomaly); code != "" {
			return code
		}
		return groupNoDetail
	case GroupByStrategy:
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
	case anomaly.Kind == KindNoData:
		return ResultNoData
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
	// Disposition and Samples are on a source standing's fold: which
	// disposition the control plane gave the strategies in it, and a bounded
	// sample of which strategies. Strategies above holds the count; there are
	// no objects to open, because none of these became one.
	Disposition string           `json:"disposition,omitempty"`
	Samples     []WithheldSample `json:"samples,omitempty"`
}

// ReportChecks folds every object in every column into the checks it is
// under, and adds the observation gaps -- which are not objects but are the
// same line: things this deployment cannot currently speak for.
//
// Ordered as checkOrder is, so the page renders the list in the order the
// reader acts and does not sort by a rule of its own.
func ReportChecks(columns [][]Anomaly, truncated map[string]bool, view *View, now time.Time) []CheckReport {
	type tally struct {
		objects    int
		strategies map[string]struct{}
		businesses map[string]struct{}
		groups     map[string]*CheckGroup
		groupSets  map[string][2]map[string]struct{}
		partial    bool
		demoted    int
		current    int
		retained   int
		lastHour   int
		newest     time.Time
		activation *ActivationFacts
		replica    string
		rebalance  *RebalanceFacts
		skipped    *Consequence
		reasons    map[string]int
		// sourceStrategies is a source standing's count: withheld records,
		// summed over its groups, where the object lines count distinct
		// strategies behind objects.
		sourceStrategies int
	}
	tallies := map[Check]*tally{}
	ensure := func(check Check) *tally {
		entry := tallies[check]
		if entry == nil {
			entry = &tally{strategies: map[string]struct{}{}, businesses: map[string]struct{}{},
				groups: map[string]*CheckGroup{}, groupSets: map[string][2]map[string]struct{}{}}
			tallies[check] = entry
		}
		return entry
	}
	add := func(entry *tally, key string, anomaly *Anomaly) {
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
	listed := map[string]struct{}{}
	for columnIndex, column := range columns {
		columnPartial := false
		if truncated != nil && columnIndex < len(columnNames) {
			columnPartial = truncated[columnNames[columnIndex]]
		}
		for index := range column {
			anomaly := &column[index]
			check := anomaly.Finding.Check
			if check == "" {
				continue
			}
			entry := ensure(check)
			entry.partial = entry.partial || columnPartial
			add(entry, anomaly.Finding.Group, anomaly)
			entry.current++
			if columnIndex < len(columnNames) && columnNames[columnIndex] == ColumnDemoted {
				entry.demoted++
			}
			listed[underKey(check, anomaly.QueryGroup)] = struct{}{}
			// A failure of this deployment's own making under a line the
			// column decided is a second fact, and it gets its second line:
			// the pool object filed as HTTP 400 that also hit an aggregation
			// conflict every round was invisible under the refusal.
			if anomaly.Internal != nil && check != CheckDefect {
				defect := ensure(CheckDefect)
				defect.partial = defect.partial || columnPartial
				add(defect, anomaly.Internal.Code, anomaly)
				defect.current++
				listed[underKey(CheckDefect, anomaly.QueryGroup)] = struct{}{}
			}
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
			if row.Loss == LossOngoing || row.Loss == LossAfterRestart {
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
		for index := range view.NoData {
			row := &view.NoData[index]
			if row.Finding.Check == "" {
				continue
			}
			add(ensure(row.Finding.Check), row.Finding.Group, row)
			ensure(row.Finding.Check).current++
		}
	}
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
		// The source standings: every withheld group of the leader's last
		// round, folded on its reason under the line its disposition owns.
		// The line's strategy count is the sum of its groups; a strategy
		// withheld at two levels is two records in the source and counts
		// twice here, as it does in the control plane's own gauge.
		if view.Source != nil {
			for _, withheld := range view.Source.Withheld {
				check, known := sourceChecks[withheld.Disposition]
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
				entry.groups[key] = &CheckGroup{Key: key, Strategies: withheld.Count, Replicas: []string{view.SourceReplica},
					Disposition: withheld.Disposition, Samples: withheld.Samples}
			}
		}
	}
	reports := make([]CheckReport, 0, len(tallies))
	for check, entry := range tallies {
		report := CheckReport{Code: check, Owner: checkAnswers[check].Owner, GroupBy: checkAnswers[check].GroupBy,
			Objects: entry.objects, Strategies: len(entry.strategies), Businesses: len(entry.businesses),
			Partial: entry.partial, Demoted: entry.demoted, Activation: entry.activation, Replica: entry.replica,
			Current: entry.current, Retained: entry.retained, RetainedLastHour: entry.lastHour,
			Consequence: entry.skipped, SkipReasons: entry.reasons, Rebalance: entry.rebalance}
		if check.SourceStanding() {
			report.Strategies = entry.sourceStrategies
		}
		if !entry.newest.IsZero() {
			newest := entry.newest
			report.RetainedNewest = &newest
		}
		for key, group := range entry.groups {
			if sets, known := entry.groupSets[key]; known {
				group.Strategies, group.Businesses = len(sets[0]), len(sets[1])
			}
			report.Groups = append(report.Groups, *group)
		}
		sort.Slice(report.Groups, func(i, j int) bool {
			if report.Groups[i].Objects != report.Groups[j].Objects {
				return report.Groups[i].Objects > report.Groups[j].Objects
			}
			return report.Groups[i].Key < report.Groups[j].Key
		})
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
}

// SummarizeTodo counts the first screen. The reports say which lines exist;
// the columns say which objects are under them, so the distinct count is
// taken from the objects and not from the lines.
func SummarizeTodo(reports []CheckReport, columns [][]Anomaly, view *View, now time.Time) Todo {
	todo := Todo{RecentWindowSeconds: int(RecentSkipWindow / time.Second), RestartGraceSeconds: int(RestartCatchUpGrace / time.Second)}
	ours := map[string]struct{}{}
	undetermined := map[string]struct{}{}
	theirs := map[string]struct{}{}
	count := func(list []Anomaly) {
		for _, anomaly := range list {
			if anomaly.Finding.Check == "" {
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
	}
	if view != nil {
		// One walk over the records, the same one the lines make. A loss in
		// progress is this deployment's and current, so its object counts
		// with ours; a demoted object's record is its line's consequence; a
		// stopped loss is the record.
		var ongoingNewest, retainedNewest time.Time
		whileDemoted := map[string]struct{}{}
		lossRecords(view, now, func(queryGroup string, _, _ Check, _ string, skip SkippedSpan, loss Loss) {
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
			todo.Governance++
		case report.Owner == OwnerUndetermined && up:
			todo.Undetermined++
		case up:
			todo.Checks++
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
	listed := map[string]struct{}{}
	demoted := demotedObjects(view)
	for _, column := range [][]Anomaly{view.Anomalies, view.Demoted, view.Undecidable, view.ByDesign, view.NoData} {
		for _, anomaly := range column {
			// Under DEFECT a row is also the one whose column filed it
			// elsewhere but which carries a failure of this deployment's own
			// making: listed by that failure's code, the second fact.
			if check == CheckDefect && anomaly.Finding.Check != check && anomaly.Internal != nil {
				listed[underKey(check, anomaly.QueryGroup)] = struct{}{}
				if group == "" || anomaly.Internal.Code == group {
					list = append(list, anomaly)
				}
				continue
			}
			if anomaly.Finding.Check != check {
				continue
			}
			// Marked as listed before the group narrows, so an object in
			// another group is not re-listed from its retained skip.
			listed[underKey(check, anomaly.QueryGroup)] = struct{}{}
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
			list = append(list, anomaly)
		}
	}
	rows, _ := skippedRows(view, listed, now)
	for _, row := range rows {
		if row.Finding.Check != check || (group != "" && row.Finding.Group != group) {
			continue
		}
		list = append(list, row)
	}
	// Oldest first: on one line every owner is the same, so age is the only
	// order left, and the oldest is the one to look at. The two lines that
	// keep records of past loss are the exception: the record grows, and
	// what a reader can act on is the newest entry -- who was just lost and
	// which span -- not the oldest.
	if check == CheckDetectionAbandoned || check == CheckTimelinePruned {
		SortAnomaliesNewestFirst(list)
	} else {
		sortOldestFirst(list)
	}
	return list
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
	lossRecords(view, now, func(queryGroup string, check, line Check, reason string, skip SkippedSpan, loss Loss) {
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
		item.Finding = Finding{Check: check, Group: string(loss), Owner: checkAnswers[check].Owner}
		item.Attribution = attributionOf(item)
		// The record in the one shape every failure is read in: a persisted
		// skip, which is the confirmed loss.
		item.Blocked = blockedOf(item, item.Finding.Schedule)
		rows = append(rows, item)
	})
	sort.Slice(rows, func(i, j int) bool { return rows[i].QueryGroup < rows[j].QueryGroup })
	return rows, consequences
}
