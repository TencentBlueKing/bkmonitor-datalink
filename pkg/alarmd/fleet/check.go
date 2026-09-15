// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "sort"

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
// -- not a word per combination of them. The situations in finding.go are the
// combinations; they are being folded into these rules and will go.
type Check string

const (
	// This deployment's own.
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
	CheckNoDataPersistent    Check = "NO_DATA_PERSISTENT"
	CheckSeriesChurning      Check = "SERIES_CHURNING"
	CheckSeriesDataMissing   Check = "SERIES_DATA_MISSING"
	CheckWindowUndecided     Check = "WINDOW_UNDECIDED"
	CheckPlanUnevaluable     Check = "PLAN_UNEVALUABLE"
	CheckConfigUnresolved    Check = "CONFIG_UNRESOLVED"
)

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
)

// checkAnswers is the closed table: who acts on each check and what its
// objects fold on. Sixteen rows, and a test holds the count there. A check
// whose owner is UNDETERMINED is one whose grouping key does not reach an
// external entity yet -- it stays on this deployment's side of the page until
// it does, rather than being handed to whichever owner is likeliest.
var checkAnswers = map[Check]struct {
	Owner   Owner
	GroupBy GroupBy
}{
	CheckSlotsOverdue:       {OwnerAlarmd, GroupByReplica},
	CheckNeverEvaluated:     {OwnerAlarmd, GroupByReplica},
	CheckRoundsStalled:      {OwnerAlarmd, GroupByReplica},
	CheckDetectionAbandoned: {OwnerAlarmd, GroupByReplica},
	CheckTimelinePruned:     {OwnerAlarmd, GroupByReplica},
	CheckDependencyDown:     {OwnerAlarmd, GroupByReasonCode},
	CheckDefect:             {OwnerAlarmd, GroupByReasonCode},
	CheckObservationGap:     {OwnerAlarmd, GroupByGapKind},

	CheckBackendNotAnswering: {OwnerData, GroupByDetail},
	CheckNoDataPersistent:    {OwnerData, GroupByStrategy},
	CheckSeriesDataMissing:   {OwnerData, GroupByStrategy},

	CheckSeriesChurning:  {OwnerStrategy, GroupByStrategy},
	CheckPlanUnevaluable: {OwnerStrategy, GroupByStrategy},

	CheckQueryRefused:     {OwnerUndetermined, GroupByDetail},
	CheckWindowUndecided:  {OwnerUndetermined, GroupByStrategy},
	CheckConfigUnresolved: {OwnerUndetermined, GroupByStrategy},
}

// checkOrder is the order the first screen lists the checks in, and the order
// a reader acts in: this deployment's own first, worst first -- work not being
// done at all, then work lost, then infrastructure, then defects, then what
// cannot be spoken for -- then what nobody can hand to anyone yet, then what
// is confirmed as somebody else's. It is the table's severity, stated as an
// order rather than as a fifth field beside each row. A test holds it to the
// same keys as checkAnswers.
var checkOrder = []Check{
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
	CheckNoDataPersistent,
	CheckSeriesDataMissing,
	CheckSeriesChurning,
	CheckPlanUnevaluable,
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
// due index will produce NEVER_EVALUATED, the empty-window split will produce
// NO_DATA_PERSISTENT, and a test holds this list to exactly those two.
var ChecksWithoutAProducer = []Check{CheckNeverEvaluated, CheckNoDataPersistent}

// checkOf decides which check an object is under, or none: an object whose
// situation is a normal value of some dimension -- a series still young, a
// strategy outside its hours -- is not on any line of the first screen.
//
// Decided from the situation while situations exist, with one look past it:
// ROUND_BLOCKED covers both a source this deployment could not read and a
// round that panicked, and those are a dependency and a defect respectively.
func checkOf(anomaly Anomaly) (Check, bool) {
	switch anomaly.Finding.Situation {
	case SituationStalled:
		return CheckRoundsStalled, true
	case SituationNeverReached:
		// The overdue wake says the wake time passed and nothing came back;
		// it does not yet say whether the object was ever evaluated. Until the
		// due index says, this is the coarse reading.
		return CheckSlotsOverdue, true
	case SituationBudgetExceeded, SituationDetectionAbandoned:
		// Both are this deployment giving up on work because of its own
		// limits, and the next step is the same: capacity.
		return CheckDetectionAbandoned, true
	case SituationTimelinePruned:
		return CheckTimelinePruned, true
	case SituationRoundBlocked:
		if code := decidingCode(anomaly); code == "panic" || code == "other_error" {
			return CheckDefect, true
		}
		return CheckDependencyDown, true
	case SituationDependencyDown:
		return CheckDependencyDown, true
	case SituationStateDefect, SituationContractRefused, SituationUnclassified:
		return CheckDefect, true
	case SituationRestoredWithoutCause:
		return CheckObservationGap, true
	case SituationBackendUnavailable, SituationBackendCooldown:
		// Cooldown is what this deployment does about a backend that keeps not
		// answering; the line on the page is the backend, and the cooldown is a
		// mark on the object's row.
		return CheckBackendNotAnswering, true
	case SituationQueryRejected:
		return CheckQueryRefused, true
	case SituationSeriesDataMissing, SituationDataIntermittent:
		return CheckSeriesDataMissing, true
	case SituationSeriesChurning:
		return CheckSeriesChurning, true
	case SituationWindowEmpty, SituationSeriesMixed:
		return CheckWindowUndecided, true
	case SituationPlanUnevaluable, SituationPlanTooLarge:
		return CheckPlanUnevaluable, true
	case SituationConfigDrift, SituationEffectiveTimeUnknown:
		return CheckConfigUnresolved, true
	}
	return "", false
}

// decidingCode is the code the finding was decided on, in the order findingOf
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
		return string(anomaly.Finding.Situation)
	}
	return ""
}

// Schedule is what the object is doing now: the dimension the first sentence
// on the page is built from. The values this build can tell apart are these;
// the due index will split RUNNING into on time and late, and add an object
// that has never run.
type Schedule string

const (
	ScheduleRunning Schedule = "RUNNING"
	ScheduleStalled Schedule = "STALLED"
	ScheduleCooling Schedule = "COOLING"
	ScheduleOverdue Schedule = "OVERDUE"
	SchedulePaused  Schedule = "PAUSED"
)

// Schedules lists every schedule value, for the page's completeness check.
var Schedules = []Schedule{ScheduleRunning, ScheduleStalled, ScheduleCooling, ScheduleOverdue, SchedulePaused}

func scheduleOf(anomaly Anomaly) Schedule {
	switch {
	case anomaly.Stalled:
		return ScheduleStalled
	case anomaly.Kind == KindOverdueWake:
		return ScheduleOverdue
	case anomaly.Kind == KindQueryCooldown:
		return ScheduleCooling
	case anomaly.CauseReason == "EFFECTIVE_TIME_INACTIVE":
		return SchedulePaused
	}
	return ScheduleRunning
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
	case anomaly.Kind == KindOverdueWake:
		return ""
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
	// Partial says at least one column this check draws from was truncated by
	// its replica, so the counts here are a sample of that column.
	Partial bool         `json:"partial,omitempty"`
	Groups  []CheckGroup `json:"groups"`
}

// CheckGroup is one fold of a check's objects: the objects sharing one key.
type CheckGroup struct {
	Key        string `json:"key"`
	Objects    int    `json:"objects"`
	Strategies int    `json:"strategies"`
	Businesses int    `json:"businesses"`
}

// ReportChecks folds every object in every column into the checks it is
// under, and adds the observation gaps -- which are not objects but are the
// same line: things this deployment cannot currently speak for.
//
// Ordered as checkOrder is, so the page renders the list in the order the
// reader acts and does not sort by a rule of its own.
func ReportChecks(columns [][]Anomaly, truncated map[string]bool, view *View) []CheckReport {
	type tally struct {
		objects    int
		strategies map[string]struct{}
		businesses map[string]struct{}
		groups     map[string]*CheckGroup
		groupSets  map[string][2]map[string]struct{}
		partial    bool
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
	}
	reports := make([]CheckReport, 0, len(tallies))
	for check, entry := range tallies {
		report := CheckReport{Code: check, Owner: checkAnswers[check].Owner, GroupBy: checkAnswers[check].GroupBy,
			Objects: entry.objects, Strategies: len(entry.strategies), Businesses: len(entry.businesses),
			Partial: entry.partial}
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

// columnNames is the order the handler passes the columns in, so a truncated
// flag can be looked up by position.
var columnNames = []string{ColumnAnomalies, ColumnDemoted, ColumnUndecidable, ColumnByDesign}

// ActionRequired reports whether the check is this reader's to act on: this
// deployment's own, or one nobody can yet hand to anyone.
func (report CheckReport) ActionRequired() bool {
	return report.Owner == OwnerAlarmd || report.Owner == OwnerUndetermined
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
// within one of its groups when group is not empty. This is the list a line on
// the first screen opens; its total is how many it holds.
func UnderCheck(check Check, group string, columns ...[]Anomaly) []Anomaly {
	list := []Anomaly{}
	for _, column := range columns {
		for _, anomaly := range column {
			if anomaly.Finding.Check != check {
				continue
			}
			if group != "" && anomaly.Finding.Group != group {
				continue
			}
			list = append(list, anomaly)
		}
	}
	sortByUrgency(list)
	return list
}
