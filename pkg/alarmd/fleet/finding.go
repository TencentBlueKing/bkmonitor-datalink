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

// A Finding is the one thing the page needs about an object, decided here.
//
// The page has four questions -- does anyone have to act, on which objects
// first, why, and where -- and until now it answered them by reading a reason
// code and a bag of counts and working the situation back out in JavaScript. A
// reason code is a stable vocabulary and therefore a coarse one: HISTORY_WARMING
// is six situations with five owners, and the page's job became inverting that
// loss with two hundred lines of conditions on the counts beside it.
//
// This moves the decision to one place, on the evidence, and hands the page
// four fields to render. The page does not decide anything after this. Whoever
// adds a situation answers the four questions here, in a table a test walks,
// rather than in a branch a reader has to find.

// Owner is who has to act. It is decided from the evidence an object carries,
// which is not the same as from its reason code: the same code lands on
// different owners depending on what the counts beside it say, and a code
// alone was how the page came to send strategy owners to fix data outages.
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
	// OwnerNobody: working as designed, and either resolving on its own or not
	// a problem at all.
	OwnerNobody Owner = "NOBODY"
	// OwnerUndetermined: the evidence does not decide between owners. Stays on
	// the to-do list rather than being handed to whichever owner is likeliest,
	// because a guess handed to the wrong person is work nobody will find the
	// cause of.
	OwnerUndetermined Owner = "UNDETERMINED"
)

// SelfHealing says whether waiting is an option.
type SelfHealing string

const (
	// HealsOnItsOwn: the next rounds resolve it without anyone acting.
	HealsOnItsOwn SelfHealing = "WILL"
	// WillNotHeal: no number of rounds changes it.
	WillNotHeal SelfHealing = "WONT"
	// HealingUnknown: the evidence does not say.
	HealingUnknown SelfHealing = "UNKNOWN"
)

// Where says which link the row carries. The page owns the URLs; this only
// says which kind.
type Where string

const (
	WhereStrategy Where = "strategy"
	WhereObject   Where = "object"
	WhereNowhere  Where = ""
)

// Situation is one specific state of affairs. One value means one situation:
// one owner, one answer about self-healing, one next step. That is the whole
// difference from a reason code, which is allowed to mean several.
type Situation string

const (
	// This deployment's own.
	SituationStalled            Situation = "STALLED"
	SituationNeverReached       Situation = "NEVER_REACHED"
	SituationBudgetExceeded     Situation = "BUDGET_EXCEEDED"
	SituationDetectionAbandoned Situation = "DETECTION_ABANDONED"
	SituationTimelinePruned     Situation = "TIMELINE_PRUNED"
	SituationRoundBlocked       Situation = "ROUND_BLOCKED"
	SituationDependencyDown     Situation = "DEPENDENCY_DOWN"
	SituationStateDefect        Situation = "STATE_DEFECT"
	SituationContractRefused    Situation = "CONTRACT_REFUSED"
	SituationUnclassified       Situation = "UNCLASSIFIED"

	// The data's.
	SituationBackendUnavailable Situation = "BACKEND_UNAVAILABLE"
	SituationBackendCooldown    Situation = "BACKEND_COOLDOWN"
	SituationSeriesDataMissing  Situation = "SERIES_DATA_MISSING"
	SituationDataIntermittent   Situation = "DATA_INTERMITTENT"

	// The strategy's.
	SituationSeriesChurning  Situation = "SERIES_CHURNING"
	SituationPlanUnevaluable Situation = "PLAN_UNEVALUABLE"
	SituationPlanTooLarge    Situation = "PLAN_TOO_LARGE"

	// Nobody's.
	SituationSeriesYoung    Situation = "SERIES_YOUNG"
	SituationSeriesRenewed  Situation = "SERIES_RENEWED"
	SituationVerdictHeld    Situation = "VERDICT_HELD"
	SituationDataJustGapped Situation = "DATA_JUST_GAPPED"
	SituationOffHours       Situation = "OFF_HOURS"

	// Undetermined.
	SituationQueryRejected        Situation = "QUERY_REJECTED"
	SituationWindowEmpty          Situation = "WINDOW_EMPTY"
	SituationSeriesMixed          Situation = "SERIES_MIXED"
	SituationConfigDrift          Situation = "CONFIG_DRIFT"
	SituationEffectiveTimeUnknown Situation = "EFFECTIVE_TIME_UNKNOWN"
	SituationRestoredWithoutCause Situation = "RESTORED_WITHOUT_CAUSE"
)

// Finding is what the page renders for one object.
type Finding struct {
	Situation   Situation   `json:"situation"`
	Owner       Owner       `json:"owner"`
	SelfHealing SelfHealing `json:"self_healing"`
	Where       Where       `json:"where,omitempty"`
	// Rounds is the one number the page substitutes into the wording: for a
	// situation that heals, how many more rounds; for one that will not, how
	// many consecutive rounds it has held. Zero when the situation has no
	// meaningful count.
	Rounds uint32 `json:"rounds,omitempty"`
}

// situationAnswers is the table. Every Situation appears exactly once, and a
// test walks it against the constants above so that adding a situation without
// answering the three questions fails the build's tests rather than rendering
// as whatever the page does with an unknown value.
//
// Owner and SelfHealing are here rather than derived per call site so that two
// code paths cannot reach the same situation and disagree about who owns it.
var situationAnswers = map[Situation]struct {
	Owner       Owner
	SelfHealing SelfHealing
	Where       Where
}{
	SituationStalled:            {OwnerAlarmd, WillNotHeal, WhereObject},
	SituationNeverReached:       {OwnerAlarmd, HealingUnknown, WhereObject},
	SituationBudgetExceeded:     {OwnerAlarmd, WillNotHeal, WhereObject},
	SituationDetectionAbandoned: {OwnerAlarmd, WillNotHeal, WhereObject},
	SituationTimelinePruned:     {OwnerAlarmd, WillNotHeal, WhereObject},
	SituationRoundBlocked:       {OwnerAlarmd, HealingUnknown, WhereObject},
	SituationDependencyDown:     {OwnerAlarmd, HealingUnknown, WhereObject},
	SituationStateDefect:        {OwnerAlarmd, WillNotHeal, WhereObject},
	SituationContractRefused:    {OwnerAlarmd, WillNotHeal, WhereObject},
	SituationUnclassified:       {OwnerAlarmd, HealingUnknown, WhereObject},

	SituationBackendUnavailable: {OwnerData, HealingUnknown, WhereStrategy},
	SituationBackendCooldown:    {OwnerData, HealingUnknown, WhereStrategy},
	SituationSeriesDataMissing:  {OwnerData, WillNotHeal, WhereStrategy},
	SituationDataIntermittent:   {OwnerData, WillNotHeal, WhereStrategy},

	SituationSeriesChurning:  {OwnerStrategy, WillNotHeal, WhereStrategy},
	SituationPlanUnevaluable: {OwnerStrategy, WillNotHeal, WhereStrategy},
	SituationPlanTooLarge:    {OwnerStrategy, WillNotHeal, WhereStrategy},

	SituationSeriesYoung:    {OwnerNobody, HealsOnItsOwn, WhereNowhere},
	SituationSeriesRenewed:  {OwnerNobody, HealingUnknown, WhereNowhere},
	SituationVerdictHeld:    {OwnerNobody, HealsOnItsOwn, WhereNowhere},
	SituationDataJustGapped: {OwnerNobody, HealingUnknown, WhereNowhere},
	SituationOffHours:       {OwnerNobody, HealsOnItsOwn, WhereNowhere},

	SituationQueryRejected:        {OwnerUndetermined, WillNotHeal, WhereStrategy},
	SituationWindowEmpty:          {OwnerUndetermined, HealingUnknown, WhereStrategy},
	SituationSeriesMixed:          {OwnerUndetermined, HealingUnknown, WhereStrategy},
	SituationConfigDrift:          {OwnerUndetermined, HealingUnknown, WhereStrategy},
	SituationEffectiveTimeUnknown: {OwnerUndetermined, HealingUnknown, WhereStrategy},
	SituationRestoredWithoutCause: {OwnerUndetermined, HealsOnItsOwn, WhereObject},
}

// Situations lists every situation the table answers, for the tests that walk
// it and for the page's own completeness check.
func Situations() []Situation {
	list := make([]Situation, 0, len(situationAnswers))
	for situation := range situationAnswers {
		list = append(list, situation)
	}
	sortSituations(list)
	return list
}

// finding builds the Finding for a situation from the table. It is the only
// constructor, so a situation cannot be reported with an owner the table did
// not give it.
func finding(situation Situation, rounds uint32) Finding {
	answers, known := situationAnswers[situation]
	if !known {
		// Reaching here is a programming error the completeness test exists
		// to catch before it ships. If it ships anyway, the safe reading is
		// ours: an unanswered situation is a failure mode nobody has looked
		// at, and the other direction hides it.
		return Finding{Situation: SituationUnclassified, Owner: OwnerAlarmd, SelfHealing: HealingUnknown,
			Where: WhereObject}
	}
	return Finding{Situation: situation, Owner: answers.Owner, SelfHealing: answers.SelfHealing,
		Where: answers.Where, Rounds: rounds}
}

// ActionRequired reports whether the object belongs on the to-do list: this
// deployment has to act, or nobody can yet say who does.
//
// Undetermined is on the list on purpose. The alternative -- handing it to the
// likeliest owner -- is how a data outage came to be filed as a strategy's
// dimensions and a strategy's algorithm as a collection problem. What cannot be
// confirmed stays where someone will confirm it.
func (finding Finding) ActionRequired() bool {
	return finding.Owner == OwnerAlarmd || finding.Owner == OwnerUndetermined
}

// findingOf decides the situation from the evidence. Most specific evidence
// first: a stalled object is stalled whatever its last code said, and a window
// count says more about a HISTORY_WARMING than the word does.
func findingOf(anomaly Anomaly) Finding {
	if anomaly.Stalled {
		return finding(SituationStalled, 0)
	}
	if anomaly.Kind == KindOverdueWake {
		return finding(SituationNeverReached, 0)
	}
	if anomaly.Kind == KindQueryCooldown {
		if queryRejected(anomaly.Failure) {
			return finding(SituationQueryRejected, 0)
		}
		return finding(SituationBackendCooldown, 0)
	}
	// The coverage-bearing reasons are decided on the coverage, because the
	// code alone cannot say which of six things happened.
	if anomaly.CauseReason == "HISTORY_WARMING" || anomaly.CauseReason == "HISTORY_GAPPED" {
		if situation, decided := windowSituation(anomaly.CauseReason, anomaly.Coverage); decided {
			return situation
		}
	}
	failureCode := ""
	if anomaly.Failure != nil {
		failureCode = anomaly.Failure.Code
	}
	for _, code := range []string{anomaly.CauseReason, string(anomaly.Cause), failureCode, anomaly.ReasonCode} {
		if code == "" {
			continue
		}
		if situation, known := codeSituations[code]; known {
			if situation == SituationBackendUnavailable && queryRejected(anomaly.Failure) {
				return finding(SituationQueryRejected, 0)
			}
			return finding(situation, 0)
		}
	}
	if restoredWithoutEvidence(anomaly) {
		return finding(SituationRestoredWithoutCause, 0)
	}
	return finding(SituationUnclassified, 0)
}

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
func queryRejected(failure *FailureRef) bool {
	if failure == nil {
		return false
	}
	detail := failure.Detail
	return strings.HasPrefix(detail, routedetail.RouteDetailKindResponse+"="+routedetail.ResponseFailureStatusPrefix) ||
		strings.HasPrefix(detail, routedetail.RouteDetailKindHTTPStatus+"=4")
}

// windowSituation is the six-way split of HISTORY_WARMING and the three-way
// split of HISTORY_GAPPED, on the counts that were computed for exactly this
// and dropped for a year.
//
// decided is false when the object carries the reason but no coverage at all
// -- an older replica, or a round that summarised nothing -- and the code
// table below then answers with the coarse reading.
func windowSituation(reason string, coverage *HistoryCoverage) (Finding, bool) {
	if coverage == nil || coverage.Levels == 0 {
		return Finding{}, false
	}
	// A complete window under a reason that says it is not: the reason is held
	// over from an earlier round and the next round or two releases it.
	if coverage.Short == 0 {
		if coverage.Guarded > 0 {
			return finding(SituationVerdictHeld, 0), true
		}
		// Complete and not guarded, under a reason about the window. Nothing
		// here can explain it, and saying "nobody's" about an unexplained row
		// is the wrong direction.
		return Finding{}, false
	}
	// Windows with nothing in them at all. The point that is always there is
	// the record being evaluated, so this is either no record arriving or a
	// record whose detection produced nothing usable -- and those have
	// different owners. Until the bit that separates them crosses, this stays
	// undetermined rather than being filed as a data outage.
	if coverage.Starved() {
		return finding(SituationWindowEmpty, coverage.EmptyRounds), true
	}
	if reason == "HISTORY_GAPPED" {
		if coverage.ShortRounds > coverage.WorstRequired {
			return finding(SituationDataIntermittent, coverage.ShortRounds), true
		}
		return finding(SituationDataJustGapped, coverage.ShortRounds), true
	}
	// HISTORY_WARMING.
	if !coverage.Persistent() {
		remaining := uint32(0)
		if coverage.WorstRequired > coverage.WorstValid {
			remaining = coverage.WorstRequired - coverage.WorstValid
		}
		return finding(SituationSeriesYoung, remaining), true
	}
	if coverage.Churning() {
		return finding(SituationSeriesChurning, coverage.FreshRounds), true
	}
	switch {
	case coverage.ShortFresh == 0:
		return finding(SituationSeriesDataMissing, coverage.ShortRounds), true
	case coverage.ShortFresh == coverage.Short:
		// Every short window fresh, but not yet for a full window's worth of
		// rounds. The round after a strategy edit looks exactly like this --
		// every series re-keyed at once -- and so does the start of churn. The
		// next rounds decide it and nobody has to act until they do.
		remaining := uint32(0)
		if coverage.WorstRequired > coverage.FreshRounds {
			remaining = coverage.WorstRequired - coverage.FreshRounds
		}
		return finding(SituationSeriesRenewed, remaining), true
	default:
		return finding(SituationSeriesMixed, coverage.ShortRounds), true
	}
}

// codeSituations maps the reason codes that decide a situation by themselves.
// The window reasons are here too, for objects that carry the reason without
// the counts; with the counts, windowSituation decides first.
var codeSituations = map[string]Situation{
	// This deployment gave up on a span of time. Two causes share the code
	// GAP_SKIPPED at the completion; SCHEDULE_PRUNED is the one that names
	// itself, and it is the retention's doing rather than the capacity's.
	"GAP_SKIPPED":     SituationDetectionAbandoned,
	"SCHEDULE_PRUNED": SituationTimelinePruned,

	// This deployment's own stores and infrastructure did not answer. Retrying
	// may help, and nobody outside can help.
	"REDIS_UNAVAILABLE":      SituationDependencyDown,
	"KAFKA_UNAVAILABLE":      SituationDependencyDown,
	"STATE_WRITE_RETRYABLE":  SituationDependencyDown,
	"OUTPUT_ACK_UNKNOWN":     SituationDependencyDown,
	"SNAPSHOT_UNAVAILABLE":   SituationDependencyDown,
	"SNAPSHOT_RETRY_PENDING": SituationDependencyDown,
	"ACTIVATION_READ_FAILED": SituationDependencyDown,

	// What this deployment persisted cannot be read back as written. Retrying
	// reads the same bytes.
	"STATE_CORRUPT":            SituationStateDefect,
	"STATE_SCHEMA_UNSUPPORTED": SituationStateDefect,
	"AUDIT_DROP":               SituationStateDefect,

	// The control plane did not give the runner something to run. A live read
	// found twelve objects whose strategies had been retired days earlier and
	// were still being retried every thirty seconds: the disposition was known
	// inside the system and never reached the runner.
	"BLOCKED_EXACT_SET_UNAVAILABLE": SituationRoundBlocked,
	"SLOT_SOURCE_RETRY":             SituationRoundBlocked,
	"PROGRESS_BEGIN_FAILED":         SituationRoundBlocked,
	"PROGRESS_BEGIN_REJECTED":       SituationRoundBlocked,

	// Budgets this deployment allocates itself, at run time.
	"EXECUTION_BUDGET_EXHAUSTED": SituationBudgetExceeded,
	"SLOT_BUDGET_EXCEEDED":       SituationBudgetExceeded,
	"STATE_BUDGET_EXCEEDED":      SituationBudgetExceeded,

	// Budgets the strategy compiler applies to a definition. These two codes
	// sat with the run-time budgets above, which sent whoever read them to
	// look for an alarmd budget to raise. On the runtime this page observes
	// there is none: the fleet tracker is fed by phase two only, and in phase
	// two these codes have exactly one producer, strategy/compiler.go, which
	// emits them when a definition does not fit within the compile limits --
	// too many levels, algorithms, conditions, AST nodes, or a trigger window
	// beyond the cap. The control plane's catalog already files them beside
	// ALGORITHM_UNSUPPORTED as one disposition (controlplane/
	// runtime_executable_catalog.go, terminalDisposition), and this table
	// had split that one disposition across two owners.
	//
	// They are not merged into PLAN_UNEVALUABLE because the next step differs:
	// an unsupported algorithm needs a different algorithm or a build that
	// supports it; a plan over budget needs to be made smaller.
	//
	// Phase one has a run-time producer for PLAN_BUDGET_EXCEEDED as well
	// (detect/admitPlans), and on that runtime this reading would be wrong.
	// Nothing on the anomaly says which runtime produced the code, so this
	// table does not try to serve both; it serves the one that feeds it.
	"PLAN_BUDGET_EXCEEDED":       SituationPlanTooLarge,
	"LEVEL_BUDGET_EXCEEDED":      SituationPlanTooLarge,
	"VALIDATION_BUDGET_EXCEEDED": SituationBudgetExceeded,
	"MESSAGE_BUDGET_EXCEEDED":    SituationBudgetExceeded,
	"READINESS_BUDGET_INVALID":   SituationBudgetExceeded,
	"RECORD_TOO_LARGE":           SituationBudgetExceeded,
	"RESOURCE_HARD_STOP":         SituationBudgetExceeded,

	// The backend was asked and did not answer usefully.
	"QUERY_TIMEOUT":        SituationBackendUnavailable,
	"QUERY_UNAVAILABLE":    SituationBackendUnavailable,
	"QUERY_PARTIAL":        SituationBackendUnavailable,
	"PROVIDER_UNAVAILABLE": SituationBackendUnavailable,
	"QUERY_NOT_READY":      SituationBackendUnavailable,
	"LATE_OUT_OF_WINDOW":   SituationBackendUnavailable,

	// Coarse readings for a window reason without counts.
	"HISTORY_GAPPED":  SituationDataIntermittent,
	"HISTORY_WARMING": SituationWindowEmpty,

	// The strategy's own configuration, or a change to it.
	"CONFIG_DRIFT":            SituationConfigDrift,
	"EFFECTIVE_TIME_INACTIVE": SituationOffHours,
	"EFFECTIVE_TIME_UNKNOWN":  SituationEffectiveTimeUnknown,

	// The definition cannot be evaluated as written.
	"ALGORITHM_UNSUPPORTED":                 SituationPlanUnevaluable,
	"MULTIPLE_EVALUATION_UNITS_UNSUPPORTED": SituationPlanUnevaluable,
	"REQUIRED_FEATURE_UNSUPPORTED":          SituationPlanUnevaluable,
	"SCHEMA_MAJOR_UNSUPPORTED":              SituationPlanUnevaluable,
	"PLAN_INVALID":                          SituationPlanUnevaluable,
	"PLAN_DUPLICATE_LEVEL_ID":               SituationPlanUnevaluable,
	// The definition's input projection, not this deployment's state
	// projection. It sat with the state defects, inherited from the table
	// before this one, and the whole-catalogue check against the control
	// plane's grouping is what found it: the compiler emits it for a plan
	// whose input_projection is invalid, and the catalog files it as
	// CONFIG_REJECTED beside PLAN_INVALID.
	"PROJECTION_INVALID":                  SituationPlanUnevaluable,
	"PLAN_SET_CONFLICT":                   SituationPlanUnevaluable,
	"LEVEL_INVALID":                       SituationPlanUnevaluable,
	"SELECTOR_INVALID":                    SituationPlanUnevaluable,
	"SELECTOR_ORDINAL_INVALID":            SituationPlanUnevaluable,
	"REQUIRED_VALUE_MISSING":              SituationPlanUnevaluable,
	"REQUIRED_VALUE_TYPE_MISMATCH":        SituationPlanUnevaluable,
	"REQUIRED_VALUE_NORMALIZATION_FAILED": SituationPlanUnevaluable,
	"TIME_INVALID":                        SituationPlanUnevaluable,
	"TENANT_INVALID":                      SituationPlanUnevaluable,
	"MALFORMED_JSON":                      SituationPlanUnevaluable,
	"PAYLOAD_DIGEST_MISMATCH":             SituationPlanUnevaluable,
	"RECORD_INVALID":                      SituationPlanUnevaluable,
	"RECORD_IDENTITY_CONFLICT":            SituationPlanUnevaluable,
}

// The tracker's own vocabularies fold in the same way attribution's do: an
// outcome word the tracker writes into reason_code has to reach a situation,
// or it falls through to unclassified and counts against the deployment for
// want of anyone having said otherwise.
func init() {
	// Rounds that produced nothing: the source could not be read, or the round
	// panicked. Nothing outside this deployment does that.
	for _, outcome := range BlockedOutcomes {
		codeSituations[outcome] = SituationRoundBlocked
	}
	// The result contract refused what this deployment produced. An invariant
	// this deployment violated is its own, and it repeats until fixed.
	for _, refusal := range ResultContractRefusals {
		codeSituations[refusal] = SituationContractRefused
	}
}

// attributionFromFinding is the verdict's reading of a finding. It is derived
// here rather than decided beside it so that the verdict and the page cannot
// classify one object two ways.
//
// Only ALARMD decides the verdict. UNDETERMINED stays on the to-do list but
// does not degrade the deployment -- with one exception, the object restored
// without a cause, which is missing evidence and keeps the verdict UNKNOWN
// exactly as it did before findings existed.
func attributionFromFinding(finding Finding) Attribution {
	switch {
	case finding.Owner == OwnerAlarmd:
		return AttributionOurs
	case finding.Situation == SituationRestoredWithoutCause:
		return AttributionUnknown
	default:
		return AttributionExternal
	}
}

// Owners lists every owner, for the filter's error message and the page's
// completeness check.
var Owners = []Owner{OwnerAlarmd, OwnerData, OwnerStrategy, OwnerNobody, OwnerUndetermined}

func knownOwner(name string) bool {
	for _, owner := range Owners {
		if string(owner) == name {
			return true
		}
	}
	return false
}

func ownerNames() []string {
	names := make([]string, len(Owners))
	for index, owner := range Owners {
		names[index] = string(owner)
	}
	return names
}

func filterByOwner(anomalies []Anomaly, owner Owner) []Anomaly {
	kept := make([]Anomaly, 0, len(anomalies))
	for _, anomaly := range anomalies {
		if anomaly.Finding.Owner == owner {
			kept = append(kept, anomaly)
		}
	}
	return kept
}

// ActionRequired is the to-do list drawn from every column: what this
// deployment has to act on, and what nobody can yet say who owns.
//
// Ordered by what decides whether to act first. This deployment's own before
// undetermined; within each, what will not heal before what might before what
// will; and within that, the oldest first, because an object that has been in
// this state for a day has been costing whatever it costs for a day. The
// ordering is here rather than on the page so that page two continues page
// one, and so the page cannot sort by a rule the count was not taken under.
func ActionRequired(columns ...[]Anomaly) []Anomaly {
	list := []Anomaly{}
	for _, column := range columns {
		for _, anomaly := range column {
			if anomaly.Finding.ActionRequired() {
				list = append(list, anomaly)
			}
		}
	}
	sortByUrgency(list)
	return list
}

// Everything is every object from every column, one list, in the columns'
// own order. The caller orders it.
func Everything(columns ...[]Anomaly) []Anomaly {
	list := []Anomaly{}
	for _, column := range columns {
		list = append(list, column...)
	}
	return list
}

func sortByUrgency(list []Anomaly) {
	rankOwner := func(owner Owner) int {
		if owner == OwnerAlarmd {
			return 0
		}
		return 1
	}
	rankHealing := func(healing SelfHealing) int {
		switch healing {
		case WillNotHeal:
			return 0
		case HealingUnknown:
			return 1
		default:
			return 2
		}
	}
	before := func(left, right Anomaly) bool {
		if l, r := rankOwner(left.Finding.Owner), rankOwner(right.Finding.Owner); l != r {
			return l < r
		}
		if l, r := rankHealing(left.Finding.SelfHealing), rankHealing(right.Finding.SelfHealing); l != r {
			return l < r
		}
		if !left.Since.Equal(right.Since) {
			return left.Since.Before(right.Since)
		}
		return left.QueryGroup < right.QueryGroup
	}
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && before(list[j], list[j-1]); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func sortSituations(list []Situation) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
