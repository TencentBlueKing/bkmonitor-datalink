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
	"strings"

	// Aliased: a test helper in this package is named execution.
	routedetail "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Finding is what the page renders for one object, decided here.
//
// The page has four questions -- does anyone have to act, on which objects
// first, why, and where -- and it answers them by reading the finding and
// nothing else. The decision is made once, on the evidence, in one place:
// which line of the first screen the object is under (or none), which fold
// within it, who acts on that line, and the two dimensions of the object's own
// state that the row shows.
//
// There used to be a vocabulary between the evidence and the check -- one word
// per combination of "what is it doing now", "how did the last round end",
// "what do its windows hold" and "who owns that" -- and every question a reader
// asked added a word to it. The words are gone. What remains is the checks,
// which are rules over the dimensions, and the dimensions themselves.

// Owner is who has to act. It is a property of the check the object is under,
// not of the object: the line on the first screen and the row under it cannot
// name two owners.
type Owner string

const (
	// OwnerAlarmd: capacity or design of this deployment could have prevented
	// it. These decide the verdict.
	OwnerAlarmd Owner = "ALARMD"
	// OwnerData: the data is not arriving, or the backend is not answering.
	OwnerData Owner = "DATA"
	// OwnerStrategy: the strategy's own definition -- dimensions, window,
	// algorithm -- is what has to change.
	OwnerStrategy Owner = "STRATEGY"
	// OwnerNobody: under no line. Working as designed, and either resolving on
	// its own or not a problem at all.
	OwnerNobody Owner = "NOBODY"
	// OwnerUndetermined: the evidence does not decide between owners. Stays on
	// this deployment's side of the page rather than being handed to whichever
	// owner is likeliest, because a guess handed to the wrong person is work
	// nobody will find the cause of.
	OwnerUndetermined Owner = "UNDETERMINED"
	// OwnerPlatform: the platform that writes what this deployment reads --
	// the strategy cache, the host cache -- wrote something this deployment
	// cannot use, or nothing. Not this deployment's capacity or design, and
	// not the strategy's definition either: a document without the identity
	// fields the contract requires is the writer's to fix, and the strategy
	// it describes is not being detected until it is.
	OwnerPlatform Owner = "PLATFORM"
)

// Owners lists every owner, for the page's completeness check.
var Owners = []Owner{OwnerAlarmd, OwnerData, OwnerStrategy, OwnerNobody, OwnerUndetermined, OwnerPlatform}

// Finding is what the page renders for one object.
type Finding struct {
	// Check is the line of the first screen the object is under, empty when it
	// is under none, and Group the fold within it.
	Check Check  `json:"check,omitempty"`
	Group string `json:"group,omitempty"`
	// Owner is the check's owner, or NOBODY for an object under no check.
	Owner Owner `json:"owner"`
	// Schedule and Result are two of the four dimensions the row shows. The
	// other two -- how long, and the window counts -- are on the anomaly.
	Schedule Schedule `json:"schedule,omitempty"`
	Result   Result   `json:"result,omitempty"`
}

// checkOf decides which check an object is under, from the evidence, or that
// it is under none. The third result says the object reached DEFECT for want
// of any rule matching, rather than because a rule said so: a failure mode
// nobody has classified counts against this deployment until somebody does,
// and that has to be visible rather than absorbed.
//
// Most specific evidence first. A stalled object is stalled whatever its last
// code said; an object whose turn has been missed is not being evaluated,
// whatever its last round said; the window counts say more about a window
// reason than the word does; and only then are the codes read.
func checkOf(anomaly Anomaly, schedule Schedule) (check Check, under bool, unclassified bool) {
	switch {
	case anomaly.Stalled:
		return CheckRoundsStalled, true, false
	case schedule == ScheduleOverdue, anomaly.Kind == KindOverdueWake:
		return CheckSlotsOverdue, true, false
	case anomaly.Kind == KindNoData:
		return CheckNoDataPersistent, true, false
	case anomaly.Kind == KindQueryCooldown:
		// Cooldown is what this deployment does about a backend that keeps not
		// answering; the line is the backend's, unless the backend answered and
		// refused.
		if queryRejected(anomaly.Failure) {
			return refusalCheck(anomaly.Failure), true, false
		}
		return CheckBackendNotAnswering, true, false
	}
	if anomaly.CauseReason == "HISTORY_WARMING" || anomaly.CauseReason == "HISTORY_GAPPED" {
		if check, under, decided := windowCheck(anomaly.CauseReason, anomaly.Coverage); decided {
			return check, under, false
		}
	}
	// A reason carried by a durable history guard is not this round's
	// finding. A Level judged WARMING or GAPPED under some trigger -- a
	// configuration change, a gap marker -- reports that trigger's reason on
	// every UNKNOWN outcome until the guard releases, and the counts beside
	// it stay live. Read through the code table, CONFIG_DRIFT under a guard
	// became "配置状态说不清" for six strategies whose configuration had not
	// changed and whose snapshot, query and schedule revisions were identical
	// before and after two of them recovered. The question such a row poses
	// is why the guard has not released, which is a window question: it goes
	// under the undecided window, folded on the trigger and on whether the
	// live window is still short or already full.
	if held, line := guardHeld(anomaly); held {
		if !line {
			// The round a guard converges on: full window, first round of
			// it. Not a line, and not a configuration question either.
			return "", false, false
		}
		return CheckWindowUndecided, true, false
	}
	failureCode := ""
	if anomaly.Failure != nil {
		failureCode = anomaly.Failure.Code
	}
	for _, code := range []string{anomaly.CauseReason, string(anomaly.Cause), failureCode, anomaly.ReasonCode} {
		if code == "" {
			continue
		}
		verdict, known := codeChecks[code]
		if !known {
			continue
		}
		if verdict.normal {
			return "", false, false
		}
		if verdict.check == CheckBackendNotAnswering && queryRejected(anomaly.Failure) {
			return refusalCheck(anomaly.Failure), true, false
		}
		return verdict.check, true, false
	}
	if restoredWithoutEvidence(anomaly) {
		return CheckObservationGap, true, false
	}
	return CheckDefect, true, true
}

// gapRestoredWithoutCause is the fold, within the observation gap, of objects
// rebuilt from a record that kept the completion kind and not the cause.
const gapRestoredWithoutCause = "RESTORED_WITHOUT_CAUSE"

// queryRejected reports a backend that answered and refused, as opposed to one
// that did not answer.
//
// The two share every code -- QUERY_UNAVAILABLE, the cooldown pool -- and are
// different people's problems. A timeout or a 5xx is the backend; a query the
// backend read and rejected is the query: either this deployment built or
// routed it wrongly for that source, or the strategy names a table or field
// that does not exist. A live deployment held 350 objects in the cooldown pool
// on exactly this, every one of them filed as the backend's, while the backend
// was answering every request with a status saying the field did not exist.
//
// Which of the two it is, is not decided here, because nothing on the anomaly
// decides it; what is decided is that it is not the backend being down, so it
// does not go to the data owner. The detail grammar is the emitter's: a
// response status the provider returned, or an HTTP 4xx.
// guardHeld says whether the object's completeness this round was held by a
// durable guard rather than computed, with a reason that is not a window word
// of its own; and whether that is a line. A guard over a live window that is
// still short is a line: the row is waiting on the window. A guard over a
// full window is a line from its second round: the guard converges on the
// first full record, so a second round in that state is a guard that should
// have released. The first such round is neither a line nor a configuration
// question.
//
// The window words themselves never reach here: checkOf reads them through
// windowCheck first, which decides every guarded shape. And a CONFIG_DRIFT
// on a round whose revisions actually moved is this round's own drift, not
// a carried one, however the window is guarded.
func guardHeld(anomaly Anomaly) (held bool, line bool) {
	coverage := anomaly.Coverage
	if coverage == nil || coverage.Levels == 0 || coverage.Guarded == 0 || anomaly.CauseReason == "" {
		return false, false
	}
	if anomaly.CauseReason == "CONFIG_DRIFT" && anomaly.ConfigChanged {
		return false, false
	}
	if coverage.Short == 0 {
		// Its own counter, not the reason clock: that clock runs on the
		// completion/reason pair and does not restart when the window
		// fills, so on the round a guard should converge it already reads
		// as many rounds as the window was short for -- and the first
		// version of this read that as a guard overdue to release.
		return true, coverage.HeldFullRounds >= 2
	}
	return true, true
}

// refusalCheck decides between the two refusals on what the backend said.
// A status that names something as not existing is the backend reading the
// strategy's table or field and answering that it is not there: that is the
// strategy's, confirmed by the backend itself, and the pool card already
// calls these strategies unusable. A status that names nothing -- a bare
// 4xx, or a status this build has no reading of -- stays undetermined.
//
// A substring rule over an open vocabulary, deliberately in the safe
// direction: a status it does not recognise stays on this deployment's side
// of the page rather than being handed to the strategy owner.
func refusalCheck(failure *FailureRef) Check {
	if refusalNamesMissingTarget(failure) {
		return CheckQueryTargetMissing
	}
	return CheckQueryRefused
}

func refusalNamesMissingTarget(failure *FailureRef) bool {
	if failure == nil {
		return false
	}
	prefix := routedetail.RouteDetailKindResponse + "=" + routedetail.ResponseFailureStatusPrefix
	if !strings.HasPrefix(failure.Detail, prefix) {
		return false
	}
	status := strings.TrimPrefix(failure.Detail, prefix)
	return strings.Contains(status, "not_exist") || strings.Contains(status, "not_found")
}

func queryRejected(failure *FailureRef) bool {
	if failure == nil {
		return false
	}
	detail := failure.Detail
	return strings.HasPrefix(detail, routedetail.RouteDetailKindResponse+"="+routedetail.ResponseFailureStatusPrefix) ||
		strings.HasPrefix(detail, routedetail.RouteDetailKindHTTPStatus+"=4")
}

// windowCheck reads a window reason on its counts. decided is false when the
// object carries the reason but no counts at all -- an older replica, or a
// round that summarised nothing -- and the code table then answers with the
// coarse reading. under is false for a window that is a normal value: still
// filling, just re-keyed, a held verdict, a hole that just appeared.
func windowCheck(reason string, coverage *HistoryCoverage) (check Check, under bool, decided bool) {
	if coverage == nil || coverage.Levels == 0 {
		return "", false, false
	}
	// A complete window under a reason that says it is not: the reason is held
	// over from an earlier round and the next round or two releases it.
	if coverage.Short == 0 {
		if coverage.Guarded > 0 {
			return "", false, true
		}
		// Complete and not guarded, under a reason about the window. Nothing
		// here can explain it, and saying "nobody's" about an unexplained row
		// is the wrong direction.
		return "", false, false
	}
	// Windows with nothing in them at all: every round's record arrived and
	// the detection could not use it. The reason it gave is the fold; whose it
	// is -- the strategy naming a field the records do not carry, or the data
	// having lost it -- the counts do not say.
	if coverage.Starved() {
		return CheckWindowUndecided, true, true
	}
	if reason == "HISTORY_GAPPED" {
		if coverage.ShortRounds > coverage.WorstRequired {
			return CheckSeriesDataMissing, true, true
		}
		return "", false, true
	}
	// HISTORY_WARMING.
	if !coverage.Persistent() {
		return "", false, true
	}
	if coverage.Churning() {
		return CheckSeriesChurning, true, true
	}
	switch {
	case coverage.ShortFresh == 0:
		return CheckSeriesDataMissing, true, true
	case coverage.ShortFresh == coverage.Short:
		// Every short window fresh, but not yet for a full window's worth of
		// rounds. The round after a strategy edit looks exactly like this --
		// every series re-keyed at once -- and so does the start of churn. The
		// next rounds decide it and nobody has to act until they do.
		return "", false, true
	default:
		return CheckWindowUndecided, true, true
	}
}

// verdict is what a code decides on its own: the check an object carrying it
// is under, or that the code is a normal value and the object is under none.
type verdict struct {
	check  Check
	normal bool
}

func lands(check Check) verdict { return verdict{check: check} }

var isNormal = verdict{normal: true}

// codeChecks maps the reason codes that decide a check by themselves. The
// window reasons are here too, for objects that carry the reason without the
// counts; with the counts, windowCheck decides first.
var codeChecks = map[string]verdict{
	// This deployment gave up on a span of time. Two causes share the code
	// GAP_SKIPPED at the completion; SCHEDULE_PRUNED is the one that names
	// itself, and it is the retention's doing rather than the capacity's.
	"GAP_SKIPPED":     lands(CheckDetectionAbandoned),
	"SCHEDULE_PRUNED": lands(CheckTimelinePruned),

	// This deployment's own stores and infrastructure did not answer. Retrying
	// may help, and nobody outside can help.
	"REDIS_UNAVAILABLE":      lands(CheckDependencyDown),
	"KAFKA_UNAVAILABLE":      lands(CheckDependencyDown),
	"STATE_WRITE_RETRYABLE":  lands(CheckDependencyDown),
	"OUTPUT_ACK_UNKNOWN":     lands(CheckDependencyDown),
	"SNAPSHOT_UNAVAILABLE":   lands(CheckDependencyDown),
	"SNAPSHOT_RETRY_PENDING": lands(CheckDependencyDown),
	"ACTIVATION_READ_FAILED": lands(CheckDependencyDown),
	// The store answered and the activation record was not in it. That is the
	// infrastructure losing state rather than refusing a read, but it lands
	// here for the same reason the rest do: nobody outside this deployment can
	// help, and the question it sends the reader to is whether this
	// deployment's store is keeping what it is given.
	"ACTIVATION_MISSING": lands(CheckDependencyDown),

	// What this deployment persisted cannot be read back as written. Retrying
	// reads the same bytes.
	"STATE_CORRUPT":            lands(CheckDefect),
	"STATE_SCHEMA_UNSUPPORTED": lands(CheckDefect),
	"AUDIT_DROP":               lands(CheckDefect),
	// The store this deployment routed to cannot do what the write needs. It is
	// wiring rather than weather: retrying reaches the same backend and gets the
	// same answer. It used to arrive as REDIS_UNAVAILABLE, which sent the reader
	// to look at a Redis that was fine and let the work retry forever.
	"BACKEND_CAPABILITY_MISSING": lands(CheckDefect),
	// The gap marker this deployment persisted for a Slot and the one the Slot
	// proposes neither match nor subsume each other. Both are this system's own
	// writes, and retrying compares the same two facts again -- so the Slot
	// does not advance on its own, and the Query Group behind it stops. The
	// refusal carries both values, which is what makes it actionable rather
	// than something to watch.
	"GAP_GUARD_CONFLICT": lands(CheckDefect),
	// A Plan this deployment cannot serve: it asks for a Snapshot kept longer
	// than the retention, or leaves its own queries no time to run. Neither is
	// weather and neither clears itself -- somebody changes the strategy or
	// the deployment -- and the strategy is not being evaluated meanwhile.
	"SNAPSHOT_RETENTION_INSUFFICIENT": lands(CheckDetectionAbandoned),
	"COMPLETION_OFFSET_BELOW_RESERVE": lands(CheckDetectionAbandoned),

	// The control plane did not give the runner something to run. A live read
	// found twelve objects whose strategies had been retired days earlier and
	// were still being retried every thirty seconds: the disposition was known
	// inside the system and never reached the runner.
	"BLOCKED_EXACT_SET_UNAVAILABLE": lands(CheckDependencyDown),
	"SLOT_SOURCE_RETRY":             lands(CheckDependencyDown),
	"PROGRESS_BEGIN_FAILED":         lands(CheckDependencyDown),
	"PROGRESS_BEGIN_REJECTED":       lands(CheckDependencyDown),

	// Budgets this deployment allocates itself, at run time: work it gave up on
	// because of its own limits. The next step is capacity, the same as for a
	// span skipped for falling behind.
	"EXECUTION_BUDGET_EXHAUSTED": lands(CheckDetectionAbandoned),
	"SLOT_BUDGET_EXCEEDED":       lands(CheckDetectionAbandoned),
	"STATE_BUDGET_EXCEEDED":      lands(CheckDetectionAbandoned),
	"VALIDATION_BUDGET_EXCEEDED": lands(CheckDetectionAbandoned),
	"MESSAGE_BUDGET_EXCEEDED":    lands(CheckDetectionAbandoned),
	"READINESS_BUDGET_INVALID":   lands(CheckDetectionAbandoned),
	"RECORD_TOO_LARGE":           lands(CheckDetectionAbandoned),
	"RESOURCE_HARD_STOP":         lands(CheckDetectionAbandoned),

	// Budgets the strategy compiler applies to a definition. The compiler is
	// their only producer, emitting them when a definition does not fit within
	// the compile limits -- too many levels, algorithms, conditions, AST nodes,
	// or a trigger window beyond the cap -- and the control plane's catalog
	// files them beside ALGORITHM_UNSUPPORTED as one disposition. They are the
	// strategy's: a plan over budget needs to be made smaller.
	"PLAN_BUDGET_EXCEEDED":  lands(CheckPlanUnevaluable),
	"LEVEL_BUDGET_EXCEEDED": lands(CheckPlanUnevaluable),

	// The backend was asked and did not answer usefully.
	"QUERY_TIMEOUT":        lands(CheckBackendNotAnswering),
	"QUERY_UNAVAILABLE":    lands(CheckBackendNotAnswering),
	"QUERY_PARTIAL":        lands(CheckBackendNotAnswering),
	"PROVIDER_UNAVAILABLE": lands(CheckBackendNotAnswering),
	"QUERY_NOT_READY":      lands(CheckBackendNotAnswering),
	"LATE_OUT_OF_WINDOW":   lands(CheckBackendNotAnswering),

	// Coarse readings for a window reason without counts.
	"HISTORY_GAPPED":  lands(CheckSeriesDataMissing),
	"HISTORY_WARMING": lands(CheckWindowUndecided),

	// The strategy's own configuration, or a change to it. Outside its own
	// effective time is the configuration doing what it was written to do.
	"CONFIG_DRIFT":            lands(CheckConfigUnresolved),
	"EFFECTIVE_TIME_INACTIVE": isNormal,
	"EFFECTIVE_TIME_UNKNOWN":  lands(CheckConfigUnresolved),

	// The definition cannot be evaluated as written.
	"ALGORITHM_UNSUPPORTED":                 lands(CheckPlanUnevaluable),
	"MULTIPLE_EVALUATION_UNITS_UNSUPPORTED": lands(CheckPlanUnevaluable),
	"REQUIRED_FEATURE_UNSUPPORTED":          lands(CheckPlanUnevaluable),
	"SCHEMA_MAJOR_UNSUPPORTED":              lands(CheckPlanUnevaluable),
	"PLAN_INVALID":                          lands(CheckPlanUnevaluable),
	"PLAN_DUPLICATE_LEVEL_ID":               lands(CheckPlanUnevaluable),
	// The strategy turned no-data detection on and its settings produce no
	// decision. It is the strategy's, like the rest of this group: nothing about
	// this deployment changes the answer, and the whole Plan is withheld rather
	// than run with its thresholds and no absence detection - a Plan half-wired
	// that way would answer "is this strategy covered" with neither yes nor no.
	"NO_DATA_CONFIG_INVALID": lands(CheckPlanUnevaluable),
	// The definition's input projection, not this deployment's state
	// projection: the compiler emits it for a plan whose input_projection is
	// invalid, and the catalog files it as CONFIG_REJECTED beside PLAN_INVALID.
	"PROJECTION_INVALID":                  lands(CheckPlanUnevaluable),
	"PLAN_SET_CONFLICT":                   lands(CheckPlanUnevaluable),
	"LEVEL_INVALID":                       lands(CheckPlanUnevaluable),
	"SELECTOR_INVALID":                    lands(CheckPlanUnevaluable),
	"SELECTOR_ORDINAL_INVALID":            lands(CheckPlanUnevaluable),
	"REQUIRED_VALUE_MISSING":              lands(CheckPlanUnevaluable),
	"REQUIRED_VALUE_TYPE_MISMATCH":        lands(CheckPlanUnevaluable),
	"REQUIRED_VALUE_NORMALIZATION_FAILED": lands(CheckPlanUnevaluable),
	"TIME_INVALID":                        lands(CheckPlanUnevaluable),
	"TENANT_INVALID":                      lands(CheckPlanUnevaluable),
	"MALFORMED_JSON":                      lands(CheckPlanUnevaluable),
	"PAYLOAD_DIGEST_MISMATCH":             lands(CheckPlanUnevaluable),
	"RECORD_INVALID":                      lands(CheckPlanUnevaluable),
	"RECORD_IDENTITY_CONFLICT":            lands(CheckPlanUnevaluable),
}

// The tracker's own vocabularies fold in the same way: an outcome word the
// tracker writes into reason_code has to reach a check, or it falls through
// to DEFECT as unclassified and counts against the deployment for want of
// anyone having said otherwise.
func init() {
	// Rounds that produced nothing. A source this deployment could not read
	// is a dependency; a round that panicked is a defect.
	for _, outcome := range BlockedOutcomes {
		if outcome == "panic" || outcome == "other_error" {
			codeChecks[outcome] = lands(CheckDefect)
			continue
		}
		codeChecks[outcome] = lands(CheckDependencyDown)
	}
	// The result contract refused what this deployment produced. An invariant
	// this deployment violated is its own, and it repeats until fixed.
	for _, refusal := range ResultContractRefusals {
		codeChecks[refusal] = lands(CheckDefect)
	}
}

// sortOldestFirst orders a list oldest first, then by name so the order is
// total. It is here rather than on the page so that page two continues page
// one, and an object that has been in its state for a day -- costing whatever
// it costs for a day -- is on page one.
func sortOldestFirst(list []Anomaly) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && before(list[j], list[j-1]); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func before(left, right Anomaly) bool {
	if !left.Since.Equal(right.Since) {
		return left.Since.Before(right.Since)
	}
	return left.QueryGroup < right.QueryGroup
}
