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
	// A code the table files as this deployment's own defect is the line,
	// whatever else the row says: a gap guard in conflict with itself stops
	// the Slot, so the object also stalls, and filing it as stalled first
	// sent the reader to "restart the replica" for a defect that repeats
	// until fixed -- and flickered between the two lines every time the
	// object changed owner.
	if check, decided := codeVerdict(anomaly); decided && check == CheckDefect {
		return CheckDefect, true, false
	}
	// This deployment's own client refusing to write the round's events is
	// the same kind of line: a refusal the client decided, repeated every
	// round until the configuration or the converter changes. Read from the
	// failure's words, because the code it arrives under is the one the
	// broker not answering also arrives under.
	if _, kind, isOutput := outputFailureOf(anomaly); isOutput && kind == OutputFailureClientRejected && failureThisRound(anomaly) {
		return CheckDefect, true, false
	}
	switch {
	case anomaly.Stalled:
		return CheckRoundsStalled, true, false
	case schedule == ScheduleOverdue, anomaly.Kind == KindOverdueWake:
		return CheckSlotsOverdue, true, false
	case anomaly.Kind == KindNoData:
		return CheckNoDataPersistent, true, false
	case anomaly.Kind == KindEmptyEveryRound:
		return CheckEmptyEveryRound, true, false
	case anomaly.Kind == KindNoDataMemoryRefused:
		return CheckNoDataMemoryRefused, true, false
	case anomaly.Kind == KindRetainedShareApproaching:
		return CheckRetainedShareApproaching, true, false
	case anomaly.Kind == KindQueryCooldown, anomaly.HeldBy == heldByCooldown:
		// Cooldown is what this deployment does about a backend that keeps not
		// answering; the line is the backend's, unless the backend answered and
		// refused. A round the cooldown held until its Slot fell past the
		// replay range is the same line: the skip is the cooldown's
		// consequence and the cooldown is the failure's. Read by the round's
		// own outcome it was "detection abandoned", this deployment's, for
		// capacity -- and the same object moved back to the failure's line on
		// the next probe and out again on the next skip. The failure is read
		// whatever Slot it was seen on, because the holder says the skip is
		// its doing.
		if queryRejected(anomaly.Failure) {
			return refusalCheck(anomaly.Failure), true, false
		}
		return CheckBackendNotAnswering, true, false
	}
	// The window counts and the guard describe the last round that
	// completed. A round that failed since is read by its own facts, below:
	// an object whose last completion was warming and whose latest round
	// failed is a failed round, and the page follows the latest round even
	// when the object alternates -- one round failing, one completing
	// degraded -- because that is what the object is doing.
	if !failedExecution(anomaly.ReasonCode) {
		if anomaly.CauseReason == "HISTORY_WARMING" || anomaly.CauseReason == "HISTORY_GAPPED" {
			if check, under, decided := windowCheck(anomaly.CauseReason, anomaly.Coverage); decided {
				return check, under, false
			}
			// No counts because the observer refused them, not because the
			// round had none: the refusal is the finding, not the coarse
			// reading of a reason whose counts are missing.
			if anomaly.Coverage == nil && anomaly.CoverageRejected != nil {
				return CheckCoverageReadingRefused, true, false
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
	}
	if check, decided := codeVerdict(anomaly); decided {
		if check == "" {
			return "", false, false
		}
		return check, true, false
	}
	if restoredWithoutEvidence(anomaly) {
		return CheckObservationGap, true, false
	}
	return CheckDefect, true, true
}

// codeVerdict is what the code table says about the row's codes, read in
// the order they are trusted: the cause's reason, the cause, the query
// failure's code, the round's outcome. Decided with an empty check is a
// code the table calls normal. Not decided is a row no code reaches.
func codeVerdict(anomaly Anomaly) (check Check, decided bool) {
	for _, code := range decisionCodes(anomaly) {
		if code == "" {
			continue
		}
		verdict, known := codeChecks[code]
		if !known {
			continue
		}
		if verdict.normal {
			return "", true
		}
		if verdict.check == CheckBackendNotAnswering && queryRejected(anomaly.Failure) {
			return refusalCheck(anomaly.Failure), true
		}
		if verdict.check == CheckDetectionAbandoned && fullyExecuted(anomaly) {
			// The Slot was given up for bookkeeping, not for detection: an
			// earlier attempt had executed every Plan.
			return CheckBookkeepingAbandoned, true
		}
		return verdict.check, true
	}
	return "", false
}

// fullyExecuted reports whether the row's evidence says an earlier attempt
// executed every Plan of the Slot the row was decided on: the latest
// completion's evidence for an object row, the span's for a record.
func fullyExecuted(anomaly Anomaly) bool {
	if anomaly.Skip != nil {
		return anomaly.Skip.FullyApplied()
	}
	return anomaly.ExecutionEvidence != nil && anomaly.ExecutionEvidence.Reading == routedetail.EvidenceReadingFullyApplied
}

// decisionCodes is the row's codes in the order they are trusted, shared by
// the check and by the reading so the line and its stage cannot come from
// two different codes. For a row whose latest round completed: the cause's
// reason, the cause, the query failure's code, the round's outcome. For a
// row whose latest round failed, only this round's facts: the failure's
// code when the failure named this round, then the outcome. The cause
// describes the last round that completed and is not this round's -- read
// cause first, a row whose last completion was skipped past the replay
// window and whose rounds since were refused by a gap guard sat under
// "检测已停" while the refusal repeated every thirty seconds; and one whose
// rounds since failed with an error nobody named was still explained by the
// skip, with the error's words beside it. A failed round with no name of
// its own is unclassified, which is what it is. A failure kept from an
// earlier Slot is not this round's either and gets no say.
func decisionCodes(anomaly Anomaly) []string {
	failureCode := ""
	if anomaly.Failure != nil {
		failureCode = anomaly.Failure.Code
	}
	if !failureThisRound(anomaly) {
		// The comment above promised this for both branches and the code
		// kept it for one: a completed round read the failure's code
		// whatever Slot it was from, so one failed round's word decided
		// every degraded round after it until a healthy completion cleared
		// the failure -- a state-version conflict that failed one Slot kept
		// the object on DEFECT for as long as its series stayed short.
		failureCode = ""
	}
	if failedExecution(anomaly.ReasonCode) {
		return []string{failureCode, anomaly.ReasonCode}
	}
	return []string{anomaly.CauseReason, string(anomaly.Cause), failureCode, anomaly.ReasonCode}
}

// failureThisRound says the row's query failure belongs to its latest round:
// by Slot when both are known, by clock against the latest round's when the
// failure is stamped but a Slot is not. A failure with neither -- a row
// from a publisher before either field -- is read as it always was, as the
// round's: the fields exist to exclude a failure the evidence places in
// another round, not to exclude one the evidence says nothing about.
func failureThisRound(anomaly Anomaly) bool {
	if anomaly.Failure == nil {
		return false
	}
	if anomaly.RoundSlot != 0 && anomaly.Failure.Slot != 0 {
		return anomaly.Failure.Slot == anomaly.RoundSlot
	}
	if anomaly.Failure.At != nil {
		return !anomaly.Failure.At.Before(anomaly.ReasonLastAt)
	}
	return true
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
	// Under no line. The Plan was not in the active set while those Slots went
	// by, so there was nothing to run and nothing was lost: replaying them
	// would produce alerts for a strategy that did not exist at the time.
	// Putting it under a line would ask somebody to act on a stretch that is
	// already over and was correct while it lasted. It still carries its own
	// word rather than the pruned one, because a reader asking where the
	// rounds went is owed the active set and not retention.
	"PLAN_NOT_ACTIVE": isNormal,

	// This deployment's own stores and infrastructure did not answer. Retrying
	// may help, and nobody outside can help.
	"REDIS_UNAVAILABLE": lands(CheckDependencyDown),
	// Not DEPENDENCY_DOWN: this deployment asked for more than it left time to
	// receive, and the fix is the size of the read rather than the health of
	// the store. Landing it with the dependencies is what made 163 of these in
	// one day read as a Redis incident.
	"STATE_READ_TIMEOUT":  lands(CheckDefect),
	"STATE_READ_DEADLINE": lands(CheckDefect),
	// Not a defect and not a dependency: this strategy asks for more of one
	// replica than any single object may hold, and the answer is to shard it.
	"QG_BUDGET_SHARE_EXCEEDED": lands(CheckPlanUnevaluable),
	"KAFKA_UNAVAILABLE":        lands(CheckDependencyDown),
	"STATE_WRITE_RETRYABLE":    lands(CheckDependencyDown),
	"OUTPUT_ACK_UNKNOWN":       lands(CheckDependencyDown),
	"SNAPSHOT_UNAVAILABLE":     lands(CheckDependencyDown),
	"SNAPSHOT_RETRY_PENDING":   lands(CheckDependencyDown),
	"ACTIVATION_READ_FAILED":   lands(CheckDependencyDown),
	// The store answered and the activation record was not in it. That is the
	// infrastructure losing state rather than refusing a read, but it lands
	// here for the same reason the rest do: nobody outside this deployment can
	// help, and the question it sends the reader to is whether this
	// deployment's store is keeping what it is given.
	"ACTIVATION_MISSING": lands(CheckDependencyDown),

	// This deployment refused its own output before a broker was asked: the
	// converter would not write the decision, or the Kafka client would not
	// send the record on the protocol it is built with. Both are decided from
	// this process's own content and wiring, and a retry decides them the
	// same way; the Plan completes by the name each round until the strategy
	// or the deployment changes.
	"OUTPUT_CONVERSION_REJECTED": lands(CheckDefect),
	"OUTPUT_CLIENT_REJECTED":     lands(CheckDefect),
	// An output batch held back because the lease it would run under is
	// about to end: one round of it is the mechanism working at a handover
	// or a renewal hiccup; rounds of it mean the lease is not being renewed,
	// which is the store or the leader, not the content.
	"OUTPUT_LEASE_EXPIRING": lands(CheckDependencyDown),

	// What this deployment persisted cannot be read back as written. Retrying
	// reads the same bytes.
	"STATE_CORRUPT":            lands(CheckDefect),
	"STATE_SCHEMA_UNSUPPORTED": lands(CheckDefect),
	"AUDIT_DROP":               lands(CheckDefect),
	// The Level contract on the record this deployment stored is not the one
	// its compiled Plan asks for. Both sides are this system's own -- it wrote
	// the record and it compiled the Plan -- and retrying compares the same two
	// again, so the Query Group does not complete until one of them changes.
	// Nothing about the data or the strategy is wrong.
	"STATE_LEVEL_CONTRACT_MISMATCH": lands(CheckDefect),
	// The trigger evaluator refused its own state before deciding. Which
	// invariant is on the line as a field; that it failed at all is this
	// deployment's.
	"TRIGGER_INVARIANT": lands(CheckDefect),
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
	// The state this deployment persisted for a series moved under the Slot
	// that was writing it: the version the Slot read is no longer the version
	// in the store (STATE_VERSION_CONFLICT, the compare-and-set refused), or
	// the Slot's own version is behind the one already persisted
	// (STATE_STALE_VERSION). Both sides of the comparison are this system's
	// writes -- a concurrent Slot on the same series, a batch applied in part,
	// a previous owner's tail -- and retrying reads the same two versions
	// again. Named at the terminal by the scheduler; before it was, the
	// rounds reached here as internal_unknown and the row carried no code.
	"STATE_VERSION_CONFLICT": lands(CheckDefect),
	"STATE_STALE_VERSION":    lands(CheckDefect),
	// The Plan gap marker moved between this Slot's read and its write. Same
	// reading as the two above and for the same reason: both sides of the
	// comparison are this system's own writes, so the question the row should
	// send a reader to is which two Slots were writing the same Plan-level
	// marker. A defect rather than a dependency - the store answered, it
	// answered no.
	//
	// These are what a same-Slot retry then clears, which is why they need a
	// name more than most: the round completes, the object looks recovered,
	// and the only trace that a second writer exists is this code.
	// A reading, not a refusal: the Slot completed, and what this says is that
	// its evaluation produced a shape the contract forbids. It puts the object
	// under no line, deliberately -- until the shape is understood, counting
	// it against the deployment would put objects on the page for something
	// nobody has decided is their problem.
	"GAP_GUARD_DUPLICATED_ACROSS_BATCHES": isNormal,
	// A refusal, unlike the reading above, and deterministic: the Slot did not
	// complete, and re-running it from the same markers reaches the same
	// disagreement. The object is not detecting, so it belongs on the page.
	"GAP_GUARD_DISAGREE":      lands(CheckDefect),
	"GAP_APPLY_CONFLICT":      lands(CheckDefect),
	"GAP_APPLY_STALE_VERSION": lands(CheckDefect),
	// The write did not land at all. That is the store not answering, which is
	// the dependency's line, beside STATE_WRITE_RETRYABLE above.
	"GAP_WRITE_RETRYABLE": lands(CheckDependencyDown),
	// The ownership store refused this deployment's own worker: its fence
	// went stale, the assignment names another worker, another owner holds
	// the lease, or the content scope moved under a fenced write. Each is a
	// mechanism of this system's -- a handover, a lease expiring, a scope
	// change taking effect -- and one round of any of them is that mechanism
	// working. An object does not reach a line on one round: it takes
	// DefaultDegradedRounds in a row, and a worker that keeps being refused
	// for an object it keeps trying is this deployment's ownership loop
	// disagreeing with its own store. Nobody outside can help with that, so
	// it is ours, whichever of the four words it wears.
	//
	// One of the four has a lawful run: a lease held by an owner that went
	// away without releasing stays BUSY to the next desired worker until the
	// lease's TTL runs out, so a clean pod kill is up to LeaseTTL of BUSY in a
	// row, and on a ten-second object three rounds is exactly that TTL. Today
	// no producer puts any of the four on a Slot's terminal reason -- they are
	// on the admission, lease and state lines and the refusals counter -- so
	// this row is not reachable; the first producer to make it reachable
	// (the content-scope work) has to bring the lease TTL onto the snapshot
	// and gate this line on the reason having held longer than TTL plus one
	// period, not on a round count. The mapping is kept so the code is
	// decided rather than absorbed by the default.
	"OWNERSHIP_STALE_FENCE": lands(CheckDefect),
	"OWNERSHIP_NOT_DESIRED": lands(CheckDefect),
	"OWNERSHIP_LEASE_BUSY":  lands(CheckDefect),
	"CONTENT_SCOPE_MOVED":   lands(CheckDefect),
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
	"VIEW_NOT_EXECUTABLE":           lands(CheckDependencyDown),
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
	// The per-Slot capacity budgets, as the query failure names the one that
	// rejected: observability.CapacityBudgetFailureCode, one word per budget
	// and OTHER for a budget it does not know. The round's own reason for
	// the same rejection is RESOURCE_HARD_STOP above; the failure's word is
	// read first and used to fall through the table -- the row landed on
	// the fault line as "EVALUATE/UNLOCATED/UNLOCATED" and marked the
	// deployment degraded, for a rejection this deployment decided.
	"BUDGET_SERIES":          lands(CheckDetectionAbandoned),
	"BUDGET_RETAINED_BYTES":  lands(CheckDetectionAbandoned),
	"BUDGET_STATE_MUTATIONS": lands(CheckDetectionAbandoned),
	"BUDGET_EVENTS":          lands(CheckDetectionAbandoned),
	"BUDGET_GAP_MUTATIONS":   lands(CheckDetectionAbandoned),
	"BUDGET_OTHER":           lands(CheckDetectionAbandoned),

	// Budgets the strategy compiler applies to a definition. The compiler is
	// their only producer, emitting them when a definition does not fit within
	// the compile limits -- too many levels, algorithms, conditions, AST nodes,
	// or a trigger window beyond the cap -- and the control plane's catalog
	// files them beside ALGORITHM_UNSUPPORTED as one disposition. They are the
	// strategy's: a plan over budget needs to be made smaller.
	"PLAN_BUDGET_EXCEEDED":  lands(CheckPlanUnevaluable),
	"LEVEL_BUDGET_EXCEEDED": lands(CheckPlanUnevaluable),

	// The backend was asked and did not answer usefully.
	"QUERY_TIMEOUT":     lands(CheckBackendNotAnswering),
	"QUERY_UNAVAILABLE": lands(CheckBackendNotAnswering),
	"QUERY_PARTIAL":     lands(CheckBackendNotAnswering),
	// The backend answered and the dependency holds no rows: the data the
	// algorithm compares against is missing, the same reading a series with
	// no history point gets.
	"QUERY_EMPTY":          lands(CheckSeriesDataMissing),
	"PROVIDER_UNAVAILABLE": lands(CheckBackendNotAnswering),
	"QUERY_NOT_READY":      lands(CheckBackendNotAnswering),
	"LATE_OUT_OF_WINDOW":   lands(CheckBackendNotAnswering),

	// Coarse readings for a window reason without counts.
	"HISTORY_GAPPED":  lands(CheckSeriesDataMissing),
	"HISTORY_WARMING": lands(CheckWindowUndecided),

	// The strategy's own configuration, or a change to it. Outside its own
	// effective time is the configuration doing what it was written to do.
	"CONFIG_DRIFT": lands(CheckConfigUnresolved),
	// The same selection coming back after a PLAN_NOT_ACTIVE stretch: nothing
	// changed for anyone to resolve, and the next Slot runs as before.
	"PLAN_REACTIVATED":        isNormal,
	"EFFECTIVE_TIME_INACTIVE": isNormal,
	"EFFECTIVE_TIME_UNKNOWN":  lands(CheckConfigUnresolved),

	// The definition cannot be evaluated as written.
	"ALGORITHM_UNSUPPORTED":                 lands(CheckPlanUnevaluable),
	"MULTIPLE_EVALUATION_UNITS_UNSUPPORTED": lands(CheckPlanUnevaluable),
	"REQUIRED_FEATURE_UNSUPPORTED":          lands(CheckPlanUnevaluable),
	"SCHEMA_MAJOR_UNSUPPORTED":              lands(CheckPlanUnevaluable),
	"PLAN_INVALID":                          lands(CheckPlanUnevaluable),
	"PLAN_DUPLICATE_LEVEL_ID":               lands(CheckPlanUnevaluable),
	// The strategy turned no-data detection on and this build cannot compile
	// the settings, or cannot derive the expected set the target implies.
	//
	// Under no line. It used to land here because the whole Plan was withheld,
	// and the comment argued that a half-wired Plan answers "is this strategy
	// covered" with neither yes nor no. The ruling went the other way and the
	// argument with it: the strategy is evaluated, its thresholds detect, and
	// only its absence detection is suspended. Putting it under "the Plan
	// cannot be evaluated" would file a working strategy as a broken one, and
	// the half that is off is not invisible -- it is counted in
	// catalog_no_data_plans under its own suspended source and listed by
	// strategy, which is the coverage question this belongs to rather than a
	// first-screen line.
	"NO_DATA_CONFIG_INVALID":     isNormal,
	"NO_DATA_ROSTER_UNSUPPORTED": isNormal,
	// The shape the config layer cannot see: a no-data setting that passes
	// validation and then runs its trigger window past a compile limit refuses
	// the whole definition. A reader met by this has a strategy that detects
	// nothing, which is the line above rather than the one beside it. A reading
	// keyed by code alone cannot tell two states apart under one code, which is
	// why this shape has its own.
	"NO_DATA_PLAN_UNCOMPILABLE": lands(CheckPlanUnevaluable),
	// The definition's input projection, not this deployment's state
	// projection: the compiler emits it for a plan whose input_projection is
	// invalid, and the catalog files it as CONFIG_REJECTED beside PLAN_INVALID.
	// The Plan's effective time. The definition names a window or a calendar
	// the compiler cannot turn into a rule, so the strategy detects nothing
	// until it is fixed - the same line as any other definition that cannot be
	// evaluated. The two that are not the definition's fault are below.
	"EFFECTIVE_TIME_INVALID":                   lands(CheckPlanUnevaluable),
	"EFFECTIVE_TIME_SNAPSHOT_INVALID":          lands(CheckPlanUnevaluable),
	"EFFECTIVE_TIME_SNAPSHOT_STATUS_INVALID":   lands(CheckPlanUnevaluable),
	"EFFECTIVE_TIME_CALENDAR_IDENTITY_INVALID": lands(CheckPlanUnevaluable),
	"EFFECTIVE_TIME_CALENDAR_DUPLICATE":        lands(CheckPlanUnevaluable),
	"EFFECTIVE_TIME_CALENDAR_ITEMS_MISSING":    lands(CheckPlanUnevaluable),
	// This build cannot read the snapshot's schema: a newer writer, and
	// nothing in the definition or the deployment to change.
	"EFFECTIVE_TIME_SCHEMA_UNSUPPORTED": lands(CheckPlanUnevaluable),
	// The snapshot did not arrive, or arrived without the calendar the
	// strategy names. The definition is not wrong and this build is not
	// lacking anything; a piece of the source is missing, and the strategy
	// detects nothing until it comes.
	"EFFECTIVE_TIME_SNAPSHOT_UNAVAILABLE": lands(CheckPlanUnevaluable),
	"EFFECTIVE_TIME_CALENDARS_MISSING":    lands(CheckPlanUnevaluable),
	"EFFECTIVE_TIME_CALENDAR_NOT_PRESENT": lands(CheckPlanUnevaluable),
	"EFFECTIVE_TIME_CALENDAR_MISSING":     lands(CheckPlanUnevaluable),
	// A terminal this build's table has no entry for. It is one strategy's
	// refusal like any other, and the compiler's own code travels in the
	// disposition's detail so the next reader is not guessing.
	"COMPILER_TERMINAL_UNCLASSIFIED":      lands(CheckPlanUnevaluable),
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
