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
	"time"
)

// Blocked is the one shape every failure on a row is read in: which step of
// the work it stopped at, which dependency and operation it was talking to,
// what kind of failure it was, and what it did to the detection. It exists
// so that a Redis restart, a query backend timing out and a strategy that
// cannot be compiled all answer the same four questions -- where is it
// stuck, what is affected, is it recovering, what to do -- instead of each
// arriving with its own vocabulary. Two Redis restarts in one morning showed
// three lines with three kinds of number and no sentence saying "the state
// store was unreachable for 35 seconds and detection kept failing for 90".
//
// It is decided from the row's evidence in one place, beside the finding,
// and not by the emitters: the reason codes are theirs, the reading is ours,
// and a code nobody has read is UNLOCATED on every axis rather than guessed.
type Blocked struct {
	Stage Stage `json:"stage"`
	// Dependency is named only on evidence: a code that names it, or the
	// error's own text carrying the dependency's signature. A timeout does
	// not name the backend -- the query's budget, the network and the
	// backend's own latency all look the same from here -- so it stays
	// UNLOCATED, and DependencyEvidence says which of the two named it when
	// one did.
	Dependency         Dependency `json:"dependency"`
	DependencyEvidence string     `json:"dependency_evidence,omitempty"`
	Operation          string     `json:"operation,omitempty"`
	Class              Class      `json:"class"`
	// Code is the reason code the reading was taken from, the same one the
	// check was decided on.
	Code string `json:"code,omitempty"`
	Text string `json:"text,omitempty"`
	// At is when the failure was last seen; LastSuccessAt when this process
	// last saw the object complete healthily, absent when it never has.
	At            *time.Time `json:"at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	// Retrying says a retry is scheduled or the next round will try again;
	// Effect is what the failure did to the detection.
	Retrying bool   `json:"retrying"`
	Effect   Effect `json:"effect"`
}

// Stage is the step of the work a failure stopped at.
type Stage string

const (
	// StageConfig: fetching what to run -- the compiled snapshot, the
	// activation, the strategy's definition.
	StageConfig Stage = "CONFIG"
	// StageSchedule: taking the object over and deciding which Slot to run.
	StageSchedule Stage = "SCHEDULE"
	// StageQuery: asking the data backend, including being admitted to ask.
	StageQuery Stage = "QUERY"
	// StageEvaluate: computing the detection over what came back.
	StageEvaluate Stage = "EVALUATE"
	// StageCommit: persisting the state and the progress, and emitting.
	StageCommit Stage = "COMMIT"
	// StageUnlocated: the evidence does not say.
	StageUnlocated Stage = "UNLOCATED"
)

// Dependency is what the failing step was talking to.
type Dependency string

const (
	// DependencyRedis: the control plane and state store.
	DependencyRedis Dependency = "REDIS"
	// DependencyQueryBackend: the data backend the queries go to.
	DependencyQueryBackend Dependency = "QUERY_BACKEND"
	// DependencyControlSource: the strategy source the catalogue is
	// refreshed from.
	DependencyControlSource Dependency = "CONTROL_SOURCE"
	// DependencyKafka: the output.
	DependencyKafka Dependency = "KAFKA"
	// DependencyNone: this deployment's own decision -- a budget, a deadline
	// it set for itself, a definition it cannot run.
	DependencyNone Dependency = "NONE"
	// DependencyUnlocated: the evidence does not say.
	DependencyUnlocated Dependency = "UNLOCATED"
)

// Class is the stable kind of a failure, across dependencies.
type Class string

const (
	ClassTimeout     Class = "TIMEOUT"
	ClassUnavailable Class = "UNAVAILABLE"
	ClassRefused     Class = "REFUSED"
	ClassCapacity    Class = "CAPACITY"
	// ClassContract: this deployment's own state or contract in conflict
	// with itself.
	ClassContract Class = "CONTRACT"
	// ClassConfig: the definition cannot be run as written.
	ClassConfig Class = "CONFIG"
	// ClassRetention: the deployment keeps less than the definition needs.
	ClassRetention Class = "RETENTION"
	ClassUnlocated Class = "UNLOCATED"
)

// Effect is what a failure did to the detection.
type Effect string

const (
	// EffectDelayed: the round is late; nothing was lost yet.
	EffectDelayed Effect = "DELAYED"
	// EffectRetrying: the round did not complete and will be tried again.
	EffectRetrying Effect = "RETRYING"
	// EffectSkipped: Slots this deployment recorded as skipped, with the
	// record persisted -- a confirmed gap. A skip only reaches a row through
	// a completion the emitter reports after the commit, which is what makes
	// it confirmed rather than assumed.
	EffectSkipped Effect = "SKIPPED"
	// EffectUnconfirmed: the round ended and its result cannot be relied on
	// -- a window short of points, an outcome nobody could decide, a state
	// restored without its cause.
	EffectUnconfirmed Effect = "UNCONFIRMED"
	// EffectMemoryLost: the round ended and its result stands; what was lost
	// is the round's upkeep of a Plan's absence memory -- the write the store
	// refused, or the renewal it would not do -- and the store is asked again
	// next round. None of the four above says that: the memory row read as
	// UNCONFIRMED, which told a reader the round's result could not be relied
	// on when it could, and read the fold as recovering while the store was
	// refusing every round.
	EffectMemoryLost Effect = "MEMORY_LOST"
)

// Closed lists, for the page's completeness tests.
var (
	Stages       = []Stage{StageConfig, StageSchedule, StageQuery, StageEvaluate, StageCommit, StageUnlocated}
	Dependencies = []Dependency{DependencyRedis, DependencyQueryBackend, DependencyControlSource, DependencyKafka, DependencyNone, DependencyUnlocated}
	Classes      = []Class{ClassTimeout, ClassUnavailable, ClassRefused, ClassCapacity, ClassContract, ClassConfig, ClassRetention, ClassUnlocated}
	Effects      = []Effect{EffectDelayed, EffectRetrying, EffectSkipped, EffectUnconfirmed, EffectMemoryLost}
)

// DependencyEvidence values: how the dependency was named.
const (
	dependencyByCode = "code"
	dependencyByText = "text"
)

type facets struct {
	stage Stage
	class Class
	// dependency is set only for codes that name it themselves; the rest
	// leave it to the error's text or to UNLOCATED.
	dependency Dependency
}

// failureFacets reads every reason code the check table knows, plus the
// codes the tracker writes itself. A test holds it to the same keys as
// codeChecks: a code that reaches a line without a reading here would be a
// row whose "where is it stuck" is silently blank.
//
// Dependency is written only where the code names it. STATE_WRITE_RETRYABLE
// and SNAPSHOT_UNAVAILABLE go through the same store as REDIS_UNAVAILABLE
// but do not say so, and an absent snapshot is also what a snapshot nobody
// published looks like; the error text decides those, or nothing does.
var failureFacets = map[string]facets{
	// Skipped and pruned spans: the scheduler's decision about time.
	"GAP_SKIPPED":     {StageSchedule, ClassCapacity, DependencyNone},
	"SCHEDULE_PRUNED": {StageSchedule, ClassRetention, DependencyNone},

	// The stores and infrastructure this deployment depends on.
	"REDIS_UNAVAILABLE":      {StageCommit, ClassUnavailable, DependencyRedis},
	"KAFKA_UNAVAILABLE":      {StageCommit, ClassUnavailable, DependencyKafka},
	"STATE_WRITE_RETRYABLE":  {StageCommit, ClassUnavailable, ""},
	"OUTPUT_ACK_UNKNOWN":     {StageCommit, ClassUnavailable, DependencyKafka},
	"SNAPSHOT_UNAVAILABLE":   {StageConfig, ClassUnavailable, ""},
	"SNAPSHOT_RETRY_PENDING": {StageConfig, ClassUnavailable, ""},
	"ACTIVATION_READ_FAILED": {StageConfig, ClassUnavailable, ""},
	// The store answered and the activation record was not in it: the
	// configuration step with nothing to load. The code names the record
	// and the store it lives in, so the dependency is named by it; whether
	// the store lost the record or nobody wrote it is the next question, and
	// the leader answers it by rebuilding from the catalogue or degrading
	// by name.
	"ACTIVATION_MISSING": {StageConfig, ClassUnavailable, DependencyRedis},

	// This deployment refusing its own output before any broker saw it: the
	// converter would not represent the decision (its content), or the client
	// would not send the record (its wiring, such as a protocol too old for
	// the record's headers). Neither is Kafka's doing; both used to land on
	// OUTPUT_ACK_UNKNOWN and send the reader to a Kafka that was up.
	"OUTPUT_CONVERSION_REJECTED": {StageCommit, ClassContract, DependencyNone},
	"OUTPUT_CLIENT_REJECTED":     {StageCommit, ClassConfig, DependencyNone},
	// The Slot's lease had less life left than one output batch needs, so
	// the batch was not started (decision-016 per-batch admission). The
	// step is the commit; whether the lease is short because renewals are
	// failing or because the Query Group is moving is the next question,
	// and the ownership refusals answer it.
	"OUTPUT_LEASE_EXPIRING": {StageCommit, ClassUnavailable, ""},

	// This deployment in conflict with what it persisted.
	"STATE_CORRUPT":              {StageCommit, ClassContract, DependencyNone},
	"STATE_SCHEMA_UNSUPPORTED":   {StageCommit, ClassContract, DependencyNone},
	"AUDIT_DROP":                 {StageCommit, ClassContract, DependencyNone},
	"BACKEND_CAPABILITY_MISSING": {StageCommit, ClassContract, DependencyNone},
	"GAP_GUARD_CONFLICT":         {StageEvaluate, ClassContract, DependencyNone},
	"GAP_SCOPE_REASON_CONFLICT":  {StageEvaluate, ClassContract, DependencyNone},
	"EVALUATION_FAILED":          {StageEvaluate, ClassContract, DependencyNone},
	// The series state moved under the Slot writing it. The step is the
	// state write -- the apply and its preflight are the commit of the
	// round's result -- so a reader is sent to what was committing against
	// what, not to the evaluation, which had finished.
	"STATE_VERSION_CONFLICT": {StageCommit, ClassContract, DependencyNone},
	"STATE_STALE_VERSION":    {StageCommit, ClassContract, DependencyNone},
	// The ownership store refused this worker: at the commit step, since the
	// fence is checked on the way to the writes (the admission before them,
	// the fenced write itself), and REFUSED because the store answered and
	// said no rather than not answering. The store is this deployment's
	// Redis, but the refusal is about the lease and not about Redis, so no
	// dependency is named -- pointing the reader at a Redis that is fine is
	// what REDIS_UNAVAILABLE used to do for a capability that was missing.
	"OWNERSHIP_STALE_FENCE": {StageCommit, ClassRefused, DependencyNone},
	"OWNERSHIP_NOT_DESIRED": {StageCommit, ClassRefused, DependencyNone},
	"OWNERSHIP_LEASE_BUSY":  {StageCommit, ClassRefused, DependencyNone},
	"CONTENT_SCOPE_MOVED":   {StageCommit, ClassRefused, DependencyNone},

	// A Plan this deployment keeps too little for.
	"SNAPSHOT_RETENTION_INSUFFICIENT": {StageConfig, ClassRetention, DependencyNone},
	"COMPLETION_OFFSET_BELOW_RESERVE": {StageConfig, ClassRetention, DependencyNone},

	// The control plane did not hand the runner something to run.
	"BLOCKED_EXACT_SET_UNAVAILABLE": {StageConfig, ClassUnavailable, ""},
	"SLOT_SOURCE_RETRY":             {StageConfig, ClassUnavailable, ""},
	"PROGRESS_BEGIN_FAILED":         {StageSchedule, ClassUnavailable, ""},
	"PROGRESS_BEGIN_REJECTED":       {StageSchedule, ClassRefused, DependencyNone},

	// Budgets this deployment allocates itself.
	"EXECUTION_BUDGET_EXHAUSTED": {StageEvaluate, ClassCapacity, DependencyNone},
	"SLOT_BUDGET_EXCEEDED":       {StageEvaluate, ClassCapacity, DependencyNone},
	"STATE_BUDGET_EXCEEDED":      {StageEvaluate, ClassCapacity, DependencyNone},
	"VALIDATION_BUDGET_EXCEEDED": {StageEvaluate, ClassCapacity, DependencyNone},
	"MESSAGE_BUDGET_EXCEEDED":    {StageCommit, ClassCapacity, DependencyNone},
	"READINESS_BUDGET_INVALID":   {StageSchedule, ClassCapacity, DependencyNone},
	"RECORD_TOO_LARGE":           {StageEvaluate, ClassCapacity, DependencyNone},
	"RESOURCE_HARD_STOP":         {StageEvaluate, ClassCapacity, DependencyNone},
	"QUERY_PERMIT_DEADLINE":      {StageQuery, ClassTimeout, DependencyNone},

	// Budgets the compiler applies to a definition.
	"PLAN_BUDGET_EXCEEDED":  {StageConfig, ClassConfig, DependencyNone},
	"LEVEL_BUDGET_EXCEEDED": {StageConfig, ClassConfig, DependencyNone},

	// The backend was asked. Only PROVIDER_UNAVAILABLE names it; a timeout
	// or a partial answer does not say where the time went.
	"QUERY_TIMEOUT":     {StageQuery, ClassTimeout, ""},
	"QUERY_UNAVAILABLE": {StageQuery, ClassUnavailable, ""},
	"QUERY_PARTIAL":     {StageQuery, ClassUnavailable, ""},
	// The backend answered, completely, with nothing: the dependency data is
	// not there. Not unavailable -- the query succeeded -- and where the data
	// went is not this deployment's to say.
	"QUERY_EMPTY":          {StageQuery, ClassUnlocated, ""},
	"PROVIDER_UNAVAILABLE": {StageQuery, ClassUnavailable, DependencyQueryBackend},
	"QUERY_NOT_READY":      {StageQuery, ClassUnavailable, ""},
	"LATE_OUT_OF_WINDOW":   {StageQuery, ClassTimeout, ""},

	// The window: the detection ran and could not decide.
	"HISTORY_GAPPED":  {StageEvaluate, ClassUnlocated, ""},
	"HISTORY_WARMING": {StageEvaluate, ClassUnlocated, ""},

	// The strategy's own configuration.
	"CONFIG_DRIFT":            {StageConfig, ClassConfig, DependencyNone},
	"EFFECTIVE_TIME_INACTIVE": {StageConfig, ClassConfig, DependencyNone},
	"EFFECTIVE_TIME_UNKNOWN":  {StageConfig, ClassConfig, DependencyNone},

	// The definition cannot be evaluated as written.
	"ALGORITHM_UNSUPPORTED":                 {StageConfig, ClassConfig, DependencyNone},
	"MULTIPLE_EVALUATION_UNITS_UNSUPPORTED": {StageConfig, ClassConfig, DependencyNone},
	"REQUIRED_FEATURE_UNSUPPORTED":          {StageConfig, ClassConfig, DependencyNone},
	"SCHEMA_MAJOR_UNSUPPORTED":              {StageConfig, ClassConfig, DependencyNone},
	"PLAN_INVALID":                          {StageConfig, ClassConfig, DependencyNone},
	"PLAN_DUPLICATE_LEVEL_ID":               {StageConfig, ClassConfig, DependencyNone},
	// Both no-data suspensions: the strategy's own settings, and nothing about
	// this deployment changes the answer. They are here because the code has
	// to be classified, not because either blocks the strategy -- its
	// thresholds are detected either way.
	"NO_DATA_CONFIG_INVALID":              {StageConfig, ClassConfig, DependencyNone},
	"NO_DATA_ROSTER_UNSUPPORTED":          {StageConfig, ClassConfig, DependencyNone},
	"PROJECTION_INVALID":                  {StageConfig, ClassConfig, DependencyNone},
	"PLAN_SET_CONFLICT":                   {StageConfig, ClassConfig, DependencyNone},
	"LEVEL_INVALID":                       {StageConfig, ClassConfig, DependencyNone},
	"SELECTOR_INVALID":                    {StageConfig, ClassConfig, DependencyNone},
	"SELECTOR_ORDINAL_INVALID":            {StageConfig, ClassConfig, DependencyNone},
	"REQUIRED_VALUE_MISSING":              {StageConfig, ClassConfig, DependencyNone},
	"REQUIRED_VALUE_TYPE_MISMATCH":        {StageConfig, ClassConfig, DependencyNone},
	"REQUIRED_VALUE_NORMALIZATION_FAILED": {StageConfig, ClassConfig, DependencyNone},
	"TIME_INVALID":                        {StageConfig, ClassConfig, DependencyNone},
	"TENANT_INVALID":                      {StageConfig, ClassConfig, DependencyNone},
	"MALFORMED_JSON":                      {StageConfig, ClassConfig, DependencyNone},
	"PAYLOAD_DIGEST_MISMATCH":             {StageConfig, ClassConfig, DependencyNone},
	"RECORD_INVALID":                      {StageConfig, ClassConfig, DependencyNone},
	"RECORD_IDENTITY_CONFLICT":            {StageConfig, ClassConfig, DependencyNone},
}

// failureRefCodes are the codes that reach a row through the query failure
// reference or the skip record rather than through the check table: an
// evaluation that errored, a gap marker in conflict with itself, a permit
// wait that ended at the query deadline. They have a reading here and no
// row in codeChecks, and the test that holds the two tables together knows
// them by name.
var failureRefCodes = []string{"EVALUATION_FAILED", "GAP_SCOPE_REASON_CONFLICT", "QUERY_PERMIT_DEADLINE"}

// categoryStages reads the step from a query failure's category when no
// code named it: the categories are the pipeline's own words for where the
// failure was raised, and they are on every failure the pipeline reports.
var categoryStages = map[string]Stage{
	"source_backend":      StageQuery,
	"provider_transport":  StageQuery,
	"admission":           StageQuery,
	"series_identity":     StageEvaluate,
	"named_input":         StageEvaluate,
	"completion_contract": StageEvaluate,
	"evaluation":          StageEvaluate,
	"budget":              StageEvaluate,
}

// dependencySignatures are the fragments an error's own text carries when
// it came from a dependency's client. They are the client library's words,
// not ours, which is what makes them evidence: "redis:" is go-redis's error
// prefix and "LOADING Redis is loading" the server's own reply.
var dependencySignatures = []struct {
	fragment   string
	dependency Dependency
}{
	{"redis:", DependencyRedis},
	{"LOADING Redis", DependencyRedis},
	{"sentinel:", DependencyRedis},
	{":6379", DependencyRedis},
	{"kafka", DependencyKafka},
}

// blockedOf reads the row into the shape above. Nil for a row that records
// no failure: a normal object, or one whose data stopped, which is not
// this deployment stuck anywhere.
func blockedOf(anomaly Anomaly, schedule Schedule) *Blocked {
	if anomaly.Kind == KindNoData || anomaly.Kind == KindEmptyEveryRound {
		return nil
	}
	// A span every Slot of which an earlier attempt executed, or an object
	// whose latest completion found its Slot executed whole, is not detection
	// stuck anywhere: the reading it would get -- SCHEDULE, capacity, a
	// confirmed skip or an unconfirmed result -- is the one the row exists to
	// contradict.
	if fullyExecuted(anomaly) {
		return nil
	}
	blocked := &Blocked{Stage: StageUnlocated, Dependency: DependencyUnlocated, Class: ClassUnlocated}
	// The code the check was decided on, in the same order checkOf reads
	// them, so the reading and the line cannot come from two different codes.
	for _, code := range decisionCodes(anomaly) {
		if code == "" {
			continue
		}
		if reading, known := failureFacets[code]; known {
			blocked.Code = code
			blocked.Stage, blocked.Class = reading.stage, reading.class
			if reading.dependency != "" {
				blocked.Dependency, blocked.DependencyEvidence = reading.dependency, dependencyByCode
			}
			break
		}
		if blocked.Code == "" {
			// Kept so the row says which code nobody has read, even though
			// every axis stays UNLOCATED for it.
			blocked.Code = code
		}
	}
	// A failure the pipeline classified but no code read: the category says
	// which step raised it, and only that -- when the failure is this
	// round's. A failure kept from an earlier Slot names no step for this one.
	if blocked.Stage == StageUnlocated && failureThisRound(anomaly) {
		if stage, known := categoryStages[anomaly.Failure.Category]; known {
			blocked.Stage = stage
			if blocked.Code == "" {
				blocked.Code = anomaly.Failure.Code
			}
		}
	}
	// A backend that answered and refused named itself by answering: the
	// refusal is the query stage's, and the dependency is the one that
	// spoke. Whose fault the refusal is stays with the owner, not here.
	if failureThisRound(anomaly) && queryRejected(anomaly.Failure) {
		blocked.Stage, blocked.Class = StageQuery, ClassRefused
		blocked.Dependency, blocked.DependencyEvidence = DependencyQueryBackend, dependencyByCode
		if blocked.Code == "" {
			blocked.Code = anomaly.Failure.Code
		}
	}
	// A failure writing the round's events is read by its words, not by its
	// code: the code says the ACK did not come and nothing about why. The
	// broker not answering is the dependency's, unavailable; the client
	// refusing to send is this deployment's, a contract, and no dependency
	// is named for it -- pointing the reader at the broker for a refusal the
	// client decided before any byte left is the reading that lost an
	// afternoon. Words nobody has a signature for stay unlocated, on this
	// deployment's side of the page.
	if failureThisRound(anomaly) {
		if failure, kind, isOutput := outputFailureOf(anomaly); isOutput {
			blocked.DependencyEvidence = kind
			if blocked.Code == "" {
				blocked.Code = failure.Code
			}
			// A code the sink named itself already has its reading in the
			// table -- the two refusal words say commit, no dependency, and
			// which class -- and the words only add which kind. A failure
			// under the shared code is read here, by its words.
			if !outputRejectionCodes[failure.Code] {
				blocked.Stage = StageCommit
				switch kind {
				case OutputFailureClientRejected:
					blocked.Dependency, blocked.Class = DependencyNone, ClassContract
				case OutputFailureBrokerError:
					blocked.Dependency, blocked.Class = DependencyKafka, ClassUnavailable
				default:
					blocked.Dependency, blocked.Class = DependencyUnlocated, ClassUnlocated
				}
			}
		}
	}
	// The current round is the latest thing the row records: a skip record's
	// time, the failing round's time, the latest round that said the reason,
	// or when the reason began. Everything below is read from that round
	// and nothing older. The error's words and the query failure's detail
	// are kept until a healthy completion, so on a row whose latest round
	// ended some other way they describe an earlier round -- and read as
	// current they lent a new reason an old Redis error as its dependency
	// and called a round that had just ended silence.
	//
	// Whether they are this round's is decided by Slot when both sides know
	// theirs, and by clock only when one does not. A failure is observed on
	// its way to the round's end, so its stamp is a moment before the
	// round's: judged by clock alone, a real failure filed one millisecond
	// before its own Slot completed was dropped, and the row said the round
	// was stuck at commit with nothing to show for it. Two Slots that are
	// known and differ are two rounds, whatever the clocks say -- a failure
	// stamped after the latest round ended is the next round's, still in
	// flight, and becomes this round's when that round ends.
	latest := time.Time{}
	for _, candidate := range []time.Time{anomaly.ReasonLastAt, anomaly.ReasonSince} {
		if candidate.After(latest) {
			latest = candidate
		}
	}
	if anomaly.LastError != nil && anomaly.LastError.At.After(latest) {
		latest = anomaly.LastError.At
	}
	if anomaly.Skip != nil && anomaly.Skip.At.After(latest) {
		latest = anomaly.Skip.At
	}
	thisRound := func(slot int64, at *time.Time) bool {
		if anomaly.RoundSlot != 0 && slot != 0 {
			return slot == anomaly.RoundSlot
		}
		return at != nil && !at.Before(latest)
	}
	errorCurrent := anomaly.LastError != nil && thisRound(anomaly.LastError.EvaluationTime, &anomaly.LastError.At)
	failureCurrent := anomaly.Failure != nil && thisRound(anomaly.Failure.Slot, anomaly.Failure.At)
	// A code that does not name the dependency defers to the error's text,
	// which the dependency's client wrote -- this round's text only.
	if blocked.Dependency == DependencyUnlocated {
		texts := []string{}
		if errorCurrent {
			texts = append(texts, anomaly.LastError.Text)
		}
		if failureCurrent {
			texts = append(texts, anomaly.Failure.Detail)
		}
		for _, text := range texts {
			if dependency, named := dependencyFromText(text); named {
				blocked.Dependency, blocked.DependencyEvidence = dependency, dependencyByText
				break
			}
		}
	}
	switch {
	case errorCurrent:
		blocked.Text, blocked.Operation = anomaly.LastError.Text, anomaly.LastError.Operation
	case failureCurrent && anomaly.Failure.Detail != "":
		blocked.Text = anomaly.Failure.Detail
	case failureCurrent && anomaly.Failure.Text != "":
		blocked.Text = anomaly.Failure.Text
	}
	if !latest.IsZero() {
		at := latest
		blocked.At = &at
	}
	if !anomaly.LastHealthyAt.IsZero() {
		at := anomaly.LastHealthyAt
		blocked.LastSuccessAt = &at
	}
	blocked.Effect = effectOf(anomaly, schedule, failedExecution(anomaly.ReasonCode))
	blocked.Retrying = blocked.Effect == EffectRetrying
	// An overdue wake with no failure behind it is not stuck at any step:
	// it is late, and the reading says only that.
	if anomaly.Kind == KindOverdueWake && blocked.Code == "" {
		blocked.Stage = StageSchedule
	}
	return blocked
}

// effectOf is what the failure did to the detection, read from the row's
// evidence in the order that separates them: a persisted skip is a loss
// whatever else the row says; a retry scheduled or a round that did not
// finish will be tried again; a round that ended without a usable result
// is unconfirmed; a late round with nothing else wrong is only late.
//
// roundFailed says the row's latest round ended by failing to finish, which
// is the round being tried again: read from how that round ended, not from
// whether an error is kept, because an error is kept until a healthy
// completion and a round that ended degraded after its own error is over.
func effectOf(anomaly Anomaly, schedule Schedule, roundFailed bool) Effect {
	switch {
	case anomaly.Skip != nil, anomaly.Kind == KindSkippedSpan:
		return EffectSkipped
	case anomaly.Kind == KindNoDataMemoryRefused:
		// The round is over and fine; the memory's upkeep is what the store
		// refused, and it is asked again next round.
		return EffectMemoryLost
	case anomaly.QueryCooldown != nil, roundFailed, anomaly.Kind == KindBlockedRun, anomaly.Stalled:
		return EffectRetrying
	case anomaly.Kind == KindDegradedRun, anomaly.Coverage != nil, restoredWithoutEvidence(anomaly):
		return EffectUnconfirmed
	case schedule == ScheduleOverdue, schedule == ScheduleLate, anomaly.Kind == KindOverdueWake:
		return EffectDelayed
	}
	return EffectUnconfirmed
}

func dependencyFromText(text string) (Dependency, bool) {
	if text == "" {
		return "", false
	}
	for _, signature := range dependencySignatures {
		if strings.Contains(text, signature.fragment) {
			return signature.dependency, true
		}
	}
	return "", false
}

// The tracker's own vocabularies are read the same way the code table folds
// them: a source this deployment could not read is the configuration step
// waiting on a dependency it does not name; a panic is a contract broken
// in evaluation; a result the contract refused is the same at commit.
func init() {
	for _, outcome := range BlockedOutcomes {
		if outcome == "panic" || outcome == "other_error" {
			failureFacets[outcome] = facets{StageEvaluate, ClassContract, DependencyNone}
			continue
		}
		failureFacets[outcome] = facets{StageConfig, ClassUnavailable, ""}
	}
	for _, refusal := range ResultContractRefusals {
		failureFacets[refusal] = facets{StageCommit, ClassContract, DependencyNone}
	}
}
