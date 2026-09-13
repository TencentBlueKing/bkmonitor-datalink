// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

// The anomaly column answers "what did this deployment fail to evaluate". That
// is not the same question as "is this deployment well", and the two were being
// read off one number.
//
// A live read had 99 objects in that column. Most were an algorithm waiting for
// history it does not have yet, a strategy edited underneath a round, or a
// backend that timed out. None of those get better with more replicas, more
// memory or a different design, and none of them are evidence against the
// deployment -- but every one of them made the verdict DEGRADED, so the verdict
// had been DEGRADED continuously for reasons alarmd cannot act on. A signal
// that is always on is not a signal.
//
// So the column is split by one question: would more capacity, or a different
// design, have prevented this? Only the objects where the answer is yes bear on
// whether the deployment is well; the rest are real work for someone else.
type Attribution string

const (
	// AttributionOurs is what capacity or design could have prevented: budgets
	// spent, queues that did not drain, rounds that stopped ending, state this
	// deployment could not read or write, objects it never got to.
	AttributionOurs Attribution = "OURS"
	// AttributionExternal is everything the deployment carried out correctly and
	// still could not produce a result for: the data is not there, the backend
	// did not answer, the strategy says something this build cannot evaluate.
	AttributionExternal Attribution = "EXTERNAL"
	// AttributionUnknown is an object with no evidence recorded either way.
	//
	// It exists because the first live read of this split had nineteen objects
	// against the deployment and seven of them were only there for want of a
	// record: restored from persisted state, which keeps the completion kind and
	// not the cause, four minutes after a rollout. Counting those as ours makes
	// the verdict DEGRADED after every deploy for as long as it takes each object
	// to finish one more round -- which is the failure this split was built to
	// remove, arriving by a different route.
	//
	// It is not the same as an unrecognised code. A code nobody has classified
	// is a failure mode this build is producing and counts against it; no code at
	// all, on an object this process never watched fail, is missing evidence. The
	// first is a verdict, the second is the absence of one.
	AttributionUnknown Attribution = "UNKNOWN"
)

// externalReasons are the reason codes that are not evidence against this
// deployment. Everything in the catalogue that is not listed here counts
// against it -- see attributionOf for why that is the safe direction.
//
// The question that decides a row is "would capacity or a different design
// have prevented this", never "what set it off". Those come apart constantly
// and only the first one is the split this table exists to make.
//
// A code describing something this deployment did wrong while reacting to an
// external event belongs on our side, however plainly external the trigger
// was: an upstream being unavailable is not ours, and writing a
// self-contradictory state in response to it is. The other reading -- that a
// visible external trigger makes the outcome external -- would let any defect
// out of this column as soon as somebody found the thing that provoked it, and
// almost every defect has one.
//
// Symmetrically, a code is not ours merely because our process emitted it.
// Every code here was emitted by this process; that is what makes "who
// emitted it" useless as the question and "who can fix it" the one that works.
//
// Grouped by who acts on it, because that is what the split is for.
var externalReasons = map[string]bool{
	// The data does not reach the window the algorithm needs. Nothing about
	// this deployment changes that.
	"HISTORY_GAPPED":  true,
	"HISTORY_WARMING": true,
	"GAP_SKIPPED":     true,
	"QUERY_NOT_READY": true,

	// The backend was asked correctly and did not answer, or answered that the
	// thing being asked for does not exist.
	"QUERY_TIMEOUT":        true,
	"QUERY_UNAVAILABLE":    true,
	"QUERY_PARTIAL":        true,
	"PROVIDER_UNAVAILABLE": true,
	"LATE_OUT_OF_WINDOW":   true,

	// The strategy's own configuration, or a change to it. CONFIG_DRIFT is the
	// strategy being edited between two reads of one round -- correct behaviour
	// on both sides, and it clears itself.
	"CONFIG_DRIFT":            true,
	"EFFECTIVE_TIME_INACTIVE": true,
	"EFFECTIVE_TIME_UNKNOWN":  true,

	// The definition handed to this deployment cannot be evaluated as written.
	// Someone has to change the strategy, or this build has to grow support --
	// neither is a capacity or design fault in the running deployment.
	"ALGORITHM_UNSUPPORTED":                 true,
	"MULTIPLE_EVALUATION_UNITS_UNSUPPORTED": true,
	"REQUIRED_FEATURE_UNSUPPORTED":          true,
	"SCHEMA_MAJOR_UNSUPPORTED":              true,
	"PLAN_INVALID":                          true,
	"PLAN_DUPLICATE_LEVEL_ID":               true,
	"PLAN_SET_CONFLICT":                     true,
	"LEVEL_INVALID":                         true,
	"SELECTOR_INVALID":                      true,
	"SELECTOR_ORDINAL_INVALID":              true,
	"REQUIRED_VALUE_MISSING":                true,
	"REQUIRED_VALUE_TYPE_MISMATCH":          true,
	"REQUIRED_VALUE_NORMALIZATION_FAILED":   true,
	"TIME_INVALID":                          true,
	"TENANT_INVALID":                        true,
	"MALFORMED_JSON":                        true,
	"PAYLOAD_DIGEST_MISMATCH":               true,
	"RECORD_INVALID":                        true,
	"RECORD_IDENTITY_CONFLICT":              true,
}

// ourReasons are the codes that do count against the deployment. Listed rather
// than left to the default so that a code added to the catalogue fails the
// completeness test instead of quietly picking a side.
var ourReasons = map[string]bool{
	// Budgets: this deployment ran out of something it allocates itself.
	"EXECUTION_BUDGET_EXHAUSTED": true,
	"SLOT_BUDGET_EXCEEDED":       true,
	"LEVEL_BUDGET_EXCEEDED":      true,
	"PLAN_BUDGET_EXCEEDED":       true,
	"STATE_BUDGET_EXCEEDED":      true,
	"VALIDATION_BUDGET_EXCEEDED": true,
	"MESSAGE_BUDGET_EXCEEDED":    true,
	"READINESS_BUDGET_INVALID":   true,
	"RECORD_TOO_LARGE":           true,
	"RESOURCE_HARD_STOP":         true,

	// Our own stores and infrastructure.
	"REDIS_UNAVAILABLE":        true,
	"KAFKA_UNAVAILABLE":        true,
	"STATE_CORRUPT":            true,
	"STATE_WRITE_RETRYABLE":    true,
	"STATE_SCHEMA_UNSUPPORTED": true,
	"PROJECTION_INVALID":       true,
	"PROGRESS_BEGIN_FAILED":    true,
	"PROGRESS_BEGIN_REJECTED":  true,
	"OUTPUT_ACK_UNKNOWN":       true,
	"AUDIT_DROP":               true,

	// Our control plane did not give the runner something to run. A live read
	// found twelve objects here whose strategies had been retired days
	// earlier: the disposition was known inside the system and never reached
	// the runner, so it retried a dead object every thirty seconds for forty
	// hours. That is a design gap in this deployment, not the strategy's doing.
	"BLOCKED_EXACT_SET_UNAVAILABLE": true,
	"SLOT_SOURCE_RETRY":             true,

	// The tracker's own outcome words, which is what actually reaches the page
	// in reason_code -- the contract codes above sit one layer further in and
	// never appear there. The twelve retired-strategy objects were being caught
	// by the fall-through for exactly this reason: the rule written for them
	// names BLOCKED_EXACT_SET_UNAVAILABLE and the field says "source_blocked".
	//
	// Found by the count of fall-throughs on its first live read, which is what
	// that count is for.
	//
	// The words themselves are folded in from BlockedOutcomes in init rather
	// than retyped here. A retyped copy is what caused this in the first place.
	"SNAPSHOT_UNAVAILABLE":   true,
	"SNAPSHOT_RETRY_PENDING": true,
	"ACTIVATION_READ_FAILED": true,
}

// uninformativeReasons are values that appear in these fields and say nothing
// about which side an object is on.
//
// Nothing reads this at runtime, and that is correct rather than an oversight:
// a word listed here is in neither classification map, so the search skips it
// either way. Checking it in the loop as well was dead code that read as a
// guard -- the invariant it appeared to enforce is enforced by the disjointness
// test instead, where it is real.
//
// What it is for is making "decided" three-valued for the completeness check:
// classified as ours, classified as external, or deliberately carrying no
// attribution information. Without the third value every completion kind would
// have to be filed on one side or the other, and both would be wrong.
//
// A completion kind says a round ended with something unavailable, and an
// execution outcome says a round failed; both are true of either side. They are
// listed rather than left to fall through because the two are different: a code
// that carries no attribution information should let the other fields answer,
// while a code nobody has classified should count against the deployment and be
// reported as unclassified. Folding them together would fill the fall-through
// count with words that will never be classifiable, and a count full of noise
// stops being read.
var uninformativeReasons = map[string]bool{
	// A completion kind that is not healthy. The healthy ones are added from
	// HealthyCompletions in init; this is the one the page sees most.
	"COMPLETED_WITH_UNAVAILABLE": true,
}

// The tracker's vocabularies are folded in here rather than retyped, because a
// retyped copy is what put the retired-strategy objects on the fall-through
// path in the first place: the rule named a contract code and the field carried
// an outcome word.
func init() {
	for _, outcome := range BlockedOutcomes {
		ourReasons[outcome] = true
	}
	for _, outcome := range FailedExecutions {
		uninformativeReasons[outcome] = true
	}
	for _, kind := range HealthyCompletions {
		uninformativeReasons[kind] = true
	}
}

// attributionOf decides which side one anomaly falls on.
//
// The order is deliberate: the strongest evidence about this deployment is
// checked first, because an object that has stopped progressing is ours
// whatever its last reason code said. A stalled object's last recorded reason
// is often the external thing that happened before it got stuck.
// attributedByRule reports whether a rule decided this, rather than the
// fall-through.
//
// The two are different confidence levels and the difference is what goes
// stale. attributionOf reads four sources and only one of them -- the reason
// catalogue -- is a closed list this package can check itself against; a
// failure code is open by construction, and a release can add a whole
// vocabulary that reaches here without touching the catalogue at all.
//
// Checked against a batch of 47 rejection codes due in the next release: the
// completeness test stays green while every one of them falls through, because
// they arrive in a different vocabulary than the one it iterates. A guard
// anchored to one closed list cannot cover an open input, so the fall-through
// is counted and shown instead of being silently absorbed.
func attributedByRule(anomaly Anomaly) bool {
	failureCode := ""
	if anomaly.Failure != nil {
		failureCode = anomaly.Failure.Code
	}
	for _, code := range []string{
		anomaly.CauseReason, string(anomaly.Cause), failureCode, anomaly.ReasonCode,
	} {
		if code != "" && (externalReasons[code] || ourReasons[code]) {
			return true
		}
	}
	// The two rules that do not read a code at all still decide by a rule.
	return anomaly.Stalled || anomaly.Kind == KindOverdueWake
}

func attributionOf(anomaly Anomaly) Attribution {
	// Rounds have stopped ending. Nothing outside this deployment can produce
	// that, and nothing outside it will end them.
	if anomaly.Stalled {
		return AttributionOurs
	}
	// Never reached at all. Whatever the object would have done, not getting to
	// it is this deployment's.
	if anomaly.Kind == KindOverdueWake {
		return AttributionOurs
	}
	// Most specific first. The failure record classifies what went wrong and
	// sits ahead of the run outcome, which used to be the other way round: an
	// object whose execution failed on a backend timeout reports reason_code
	// "error" and failure code QUERY_TIMEOUT, and checking the outcome word
	// first would have decided it before the specific code was ever read.
	failureCode := ""
	if anomaly.Failure != nil {
		failureCode = anomaly.Failure.Code
	}
	for _, code := range []string{
		anomaly.CauseReason, string(anomaly.Cause), failureCode, anomaly.ReasonCode,
	} {
		if code == "" {
			continue
		}
		if externalReasons[code] {
			return AttributionExternal
		}
		if ourReasons[code] {
			return AttributionOurs
		}
	}
	// Nothing said which side this is. Two very different cases reach here.
	//
	// An object rebuilt from persisted state has no cause because the cause was
	// never written down, not because there is nothing to say -- this process
	// has not seen it fail yet. Reporting that as a fault of the deployment
	// would make every rollout look like a regression for a few minutes.
	// Only the restored case. An object this process watched fail and recorded
	// nothing about is a different thing: the evidence was not missing, it was
	// never produced, and that is this deployment's own observability failing.
	// Merging the two would let a real hole in what we record hide behind the
	// same word as a known persistence gap.
	if restoredWithoutEvidence(anomaly) {
		return AttributionUnknown
	}
	// Otherwise something was reported and nobody has classified it. That counts
	// against the deployment, and that is the whole point of choosing a default
	// rather than leaving one.
	//
	// The other direction is the dangerous one: a failure mode nobody has
	// classified yet is exactly the kind this deployment has just started
	// producing, and defaulting it to "not our problem" would let a new fault
	// arrive as a HEALTHY verdict. Defaulting it to ours costs a look at
	// something that turns out to be external; the reverse costs the signal.
	return AttributionOurs
}

// restoredWithoutEvidence reports an object rebuilt from a record that does not
// carry why it was failing.
//
// The completion kind is persisted and the cause below it is not, so a restored
// object arrives with a kind and nothing else. That is missing evidence about a
// real anomaly, not evidence of a healthy one and not evidence against the
// deployment.
func restoredWithoutEvidence(anomaly Anomaly) bool {
	if anomaly.Cause != "" || anomaly.CauseReason != "" {
		return false
	}
	if anomaly.Failure != nil && anomaly.Failure.Code != "" {
		return false
	}
	for _, restored := range RestoredSinceSources {
		if anomaly.SinceFrom == restored {
			return true
		}
	}
	return false
}

// UnattributedCount returns how many carry no evidence either way.
func UnattributedCount(anomalies []Anomaly) int {
	unknown := 0
	for _, anomaly := range anomalies {
		if anomaly.Attribution == AttributionUnknown {
			unknown++
		}
	}
	return unknown
}

// Attribute fills in the attribution on every anomaly in the list.
//
// It runs over the rows rather than being computed in the tracker so that the
// rule lives in one place and the page cannot disagree with the verdict: both
// read this field, neither re-derives it.
func Attribute(anomalies []Anomaly) {
	for index := range anomalies {
		anomalies[index].Attribution = attributionOf(anomalies[index])
		// Recorded per object rather than derived twice, so the page and the
		// counts cannot disagree about which of these was actually decided.
		anomalies[index].Unclassified = anomalies[index].Attribution == AttributionOurs &&
			!attributedByRule(anomalies[index])
	}
}

// OursCount returns how many of these count against the deployment.
func OursCount(anomalies []Anomaly) int {
	ours := 0
	for _, anomaly := range anomalies {
		if anomaly.Attribution == AttributionOurs {
			ours++
		}
	}
	return ours
}
