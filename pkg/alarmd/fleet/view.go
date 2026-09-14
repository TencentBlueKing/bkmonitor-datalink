// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package fleet aggregates what each replica knows about the objects it owns
// into one answer about the whole deployment.
//
// The aggregation exists to make one failure impossible: a replica that stops
// publishing takes its anomalies with it, the remaining list gets shorter, and
// a naive reading calls that an improvement. Every gap therefore has to be
// counted as unknown, and unknown is never healthy.
package fleet

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Health is the answer to "does someone need to look at this".
type Health string

const (
	// HealthUnknown means the view is incomplete. It is not a degraded state
	// and it is not a healthy one; it means the question cannot be answered.
	HealthUnknown Health = "UNKNOWN"
	// HealthDegraded means the view is complete and contains anomalies.
	HealthDegraded Health = "DEGRADED"
	// HealthHealthy means the view is complete and contains no anomalies.
	HealthHealthy Health = "HEALTHY"
)

// GapKind classifies why part of the deployment is unaccounted for.
type GapKind string

const (
	// GapDenominatorUnavailable means the expected object set could not be
	// read. Without it there is no way to know what is missing.
	GapDenominatorUnavailable GapKind = "DENOMINATOR_UNAVAILABLE"
	// GapReplicaMissing means a replica that should have published a snapshot
	// did not.
	GapReplicaMissing GapKind = "REPLICA_MISSING"
	// GapSnapshotStale means a snapshot is older than the freshness budget, so
	// the objects it reports may no longer be the objects that exist.
	GapSnapshotStale GapKind = "SNAPSHOT_STALE"
	// GapListTruncated means a replica had more anomalies than it could
	// publish, so the list is bounded rather than complete.
	GapListTruncated GapKind = "LIST_TRUNCATED"
	// GapOwnershipShortfall means the covered objects do not add up to the
	// expected set even though every snapshot looked fresh.
	GapOwnershipShortfall GapKind = "OWNERSHIP_SHORTFALL"
	// GapCoverageInconsistent means more objects were reported as covered than
	// the expected set contains, so the two sides disagree and neither can be
	// trusted as the denominator.
	GapCoverageInconsistent GapKind = "COVERAGE_INCONSISTENT"
	// GapNoReplicas means nothing was expected to publish. A deployment with no
	// ready replica is not a healthy deployment with nothing to do.
	GapNoReplicas GapKind = "NO_REPLICAS"
	// GapUndetermined means a replica owns objects it cannot yet speak for, so
	// their absence from the anomaly list is not evidence that they are well.
	GapUndetermined GapKind = "UNDETERMINED"
)

// SinceSource records where an anomaly's start time came from, because the
// sources do not survive the same events and do not mean the same thing.
//
// A restored object is the reason this has to be carried per anomaly rather
// than stamped on the whole list: an object this process watched go wrong has a
// start time, and an object restored from a persisted cursor has a bound. Both
// render as a timestamp, and nothing else in the row tells them apart.
type SinceSource string

const (
	// SinceBusinessState is derived from persisted business state and
	// therefore survives a restart.
	SinceBusinessState SinceSource = "BUSINESS_STATE"
	// SinceSnapshotContinuity is only as old as the uninterrupted run of
	// snapshots that observed it, and resets when that run breaks.
	SinceSnapshotContinuity SinceSource = "SNAPSHOT_CONTINUITY"
	// SinceRestoredLastFull is the last round the object is known to have
	// completed in full, read back after a restart. It is not when the object
	// started going wrong: that moment is not persisted anywhere. It is the
	// latest moment the object is known to have been fine, so the duration
	// beside it is an upper bound on how long this has been going on.
	SinceRestoredLastFull SinceSource = "RESTORED_LAST_FULL"
	// SinceRestoredAtRestart is this process taking over an object whose
	// persisted state records no full completion to anchor against. The clock
	// starts at the handover, so the duration is a lower bound -- possibly a
	// far lower one -- rather than a measurement.
	SinceRestoredAtRestart SinceSource = "RESTORED_AT_RESTART"
	// SinceRefusedFuture marks a row whose start time was later than the moment
	// it was read. Nothing can have started after now, so the timestamp was
	// refused and the clock reset to the read.
	//
	// This should never appear. It exists because the previous way of getting it
	// wrong was silent: a future timestamp renders as a negative age, and the
	// list is ordered oldest-first, so it sorted to the end -- where a truncated
	// list drops it first. The rule meant to keep the worst objects visible
	// pushed the mis-stamped ones out of view instead. A row that says it was
	// refused is a bug report; a row that quietly sorts last is not.
	// SinceProcessStart is a run whose first observed round was this process's
	// own first round. The object was already in this state when the replica
	// started, so the duration is how long this process has been watching, not
	// how long the object has been wrong -- a lower bound, and often a very low
	// one. It reads identically to a measured duration, which is why it needs
	// its own name.
	SinceProcessStart  SinceSource = "PROCESS_START"
	SinceRefusedFuture SinceSource = "REFUSED_FUTURE"
)

// The columns the object list can be about. An object is in exactly one, which
// is what lets the healthy count be a subtraction instead of its own walk.
const (
	// ColumnAnomalies is what this deployment's own execution is failing at.
	ColumnAnomalies = "anomalies"
	// ColumnDemoted is what it has stopped asking for because the backend kept
	// answering unavailable. These are held out of the health verdict, which is
	// the whole reason they have to be listable: a mechanism that removes
	// objects from the denominator has to show which ones.
	ColumnDemoted = "demoted"
	// ColumnUndecidable is what completes every round without being able to
	// decide recovery, and where nothing else has gone wrong.
	//
	// Not a milder anomaly -- a different kind of statement. The others say
	// something is failing. This one says the detection window does not hold
	// the points the algorithm needs, so there is no basis on which to decide
	// that anything has gone back to normal. The data that is there is read
	// correctly, an anomalous result is still settled ahead of the
	// completeness gate and still fires, and no amount of capacity changes any
	// of it.
	//
	// It is listable for the same reason demotion is: it takes objects out of
	// the anomaly count, and anything that shrinks that count has to be able
	// to show exactly which objects it took.
	ColumnUndecidable = "undecidable"
)

// The two ends of the list. Both are legitimate readings of the same
// population, and which one the first page shows decides what an operator sees
// during an incident.
const (
	// OrderOldest keeps the longest-running objects on the first page, which is
	// what a chronic backlog needs and what the list has always done.
	OrderOldest = "oldest"
	// OrderNewest puts what has just started there instead. On a deployment read
	// with 45 of 83 objects less than two hours old, none of them appeared
	// before page three under OrderOldest.
	OrderNewest = "newest"
)

// StrategyRef ties an object back to something an operator recognises.
type StrategyRef struct {
	StrategyID string `json:"strategy_id"`
	BusinessID string `json:"business_id"`
}

// FailureRef is why the object's last failing round failed, as the bounded
// classification the pipeline already emits.
//
// The completion kind says a round ended with something unavailable; it does not
// say what. Without this, a page can list a hundred objects sharing one reason
// code and still leave the reader with no idea what to look at.
type FailureRef struct {
	Stage    string `json:"stage"`
	Category string `json:"category"`
	Code     string `json:"code,omitempty"`
	// Detail is the one field that says what the backend actually did. The code
	// answers "the query did not come back"; this answers "it returned 503" or
	// "the connection was refused", and those are different people's problems.
	//
	// It was dropped on the way here, which only showed once a column existed
	// whose whole claim is "these are not our fault": the page could report
	// fifty-six objects sharing one code and still not name a symptom anyone
	// could act on, which restates the column's own name.
	//
	// Safe to carry because the emitter already bounds it: at most 96 bytes of
	// [a-z0-9_=.-], so URLs, messages and response bodies are refused upstream
	// rather than trimmed here.
	Detail string `json:"detail,omitempty"`
}

// HistoryCoverage is how far short of the required detection window this
// object's series were, and for how many consecutive rounds.
//
// It exists to separate two things that report HISTORY_WARMING identically on
// every round and need opposite responses:
//
//   - A window that is filling. A series a round or two old is short by a
//     point or two, and the next few rounds finish it. Nobody needs to do
//     anything.
//   - A window that will never fill. When a series lives for less time than
//     the window spans -- a network device that exists as long as one pod, a
//     container name that is never reused -- most of the window is missing on
//     every round, for ever. An alert on such an object can be raised, because
//     the anomalous branch is decided before the completeness gate; it can
//     never clear on its own, because recovery is decided behind it.
//
// The second is not a defect in this deployment and not a capacity problem.
// It is a strategy whose series identity contains something that churns, and
// what it needs is a change to the strategy, not to alarmd. But a reader
// cannot reach that conclusion from a label both cases share.
type HistoryCoverage struct {
	// Levels and Short are the counts from the last round: how many Level
	// windows were summarised, and how many held fewer points than required.
	Levels uint32 `json:"levels"`
	Short  uint32 `json:"short"`
	// WorstValid and WorstRequired are one window's pair -- the worst one --
	// never a minimum of one field beside a maximum of the other.
	WorstValid    uint32 `json:"worst_valid"`
	WorstRequired uint32 `json:"worst_required"`
	// ShortRounds is how many consecutive rounds have reported a short window,
	// counted by this process and therefore no older than it.
	//
	// This is the field that makes the distinction possible. One round cannot
	// tell a filling window from a permanently short one; both are short. Only
	// the sequence separates them, and a filling window converges.
	ShortRounds uint32 `json:"short_rounds"`
}

// Persistent reports a window that has stayed short for longer than filling it
// could possibly take.
//
// The threshold is the window's own requirement rather than a number chosen
// here. A window needing N points is full after N rounds of data; still short
// after more than N rounds means the data is not arriving, and no additional
// waiting changes that. Nothing to configure, and nothing that has to be
// retuned when a strategy changes its window.
//
// It assumes one round advances the window by at most one position. Where a
// round covers more, the window fills sooner and this waits longer than it
// needs to -- the error is towards calling a stuck object "still filling",
// which is the direction that does not make a false accusation.
func (coverage *HistoryCoverage) Persistent() bool {
	return coverage != nil && coverage.Short > 0 && coverage.WorstRequired > 0 &&
		coverage.ShortRounds > coverage.WorstRequired
}

// Anomaly is one object that is not making progress as expected.
type Anomaly struct {
	QueryCooldown *observability.QueryCooldownFacts `json:"query_cooldown,omitempty"`
	QueryGroup    string                            `json:"query_group"`
	Kind          string                            `json:"kind"`
	ReasonCode    string                            `json:"reason_code,omitempty"`
	// Cause separates the conditions that share one completion kind. Without it
	// a page can list hundreds of objects as degraded and give the reader no
	// way to tell which ones anyone can do something about, which is the same
	// as listing none.
	Cause string `json:"cause,omitempty"`
	// CauseReason is one level below Cause and is usually where the answer is.
	// A cause of LEVEL_OUTCOME_UNKNOWN carries a reason of either the coverage
	// class -- the data does not reach this window, nobody here did anything
	// wrong -- or the retryable class, which clears on its own. Neither is what
	// the column heading claims, and the cause alone cannot tell them apart.
	CauseReason string `json:"cause_reason,omitempty"`
	// Coverage is how short the detection windows were, when the reason was
	// about the window. It is here because HISTORY_WARMING describes two
	// situations that need opposite responses and reads identically in both:
	// a window a round or two from converging, and a window whose series do
	// not live long enough to ever fill it. Only the shortfall separates them,
	// and it used to be discarded in state/window.go.
	Coverage  *HistoryCoverage `json:"coverage,omitempty"`
	Since     time.Time        `json:"since"`
	SinceFrom SinceSource      `json:"since_from"`
	// Attribution says whether capacity or design could have prevented this.
	// Only the ones where it could decide the verdict; the rest are real work
	// for someone else. Filled in by Attribute rather than by the tracker, so
	// the page and the verdict read one field instead of each deriving it.
	Attribution Attribution `json:"attribution,omitempty"`
	// Unclassified says this object is counted against the deployment because
	// no rule matched, not because a rule said so. A release that adds a
	// vocabulary of failure codes puts every one of them here until somebody
	// classifies them, and that has to be visible rather than absorbed.
	Unclassified bool `json:"unclassified,omitempty"`
	// FailingSince is when the current unbroken sequence of rounds that
	// reached execution and did not finish began; zero while the last
	// conclusive round ended, however it ended. It is not Since: an object
	// can have been degraded for hours and failing to finish for a minute,
	// and Stalled is judged against this clock, not that one, precisely so a
	// long degraded history cannot turn one retrying round into a stall.
	//
	// A zero value is left off the wire by MarshalJSON below rather than by an
	// omitzero tag: the release pipeline builds with Go 1.23, whose encoder
	// does not know that option and would print a zero time on every object
	// whose rounds are ending normally.
	FailingSince time.Time   `json:"failing_since"`
	Replica      string      `json:"replica"`
	Failure      *FailureRef `json:"failure,omitempty"`
	// Stalled says the rounds have been failing to finish for longer than the
	// deployment's own budget for terminating an unfinishable Slot. The
	// distinction it draws is the one that decides whether anyone has to act: a
	// degraded round still ends and moves the cursor, while a round that never
	// ends leaves the object replaying the same evaluation forever. Both look
	// identical in a list that only shows how the last round went.
	//
	// Derived when the view is served rather than stored, so it is only as old as
	// the uninterrupted run of snapshots behind FailingSince: it under-reports
	// after a restart rather than over-reports.
	Stalled    bool          `json:"stalled,omitempty"`
	Strategies []StrategyRef `json:"strategies,omitempty"`
}

// MarshalJSON leaves failing_since off the wire while it is zero. The outer
// field shadows the embedded one of the same name, so a nil pointer is
// omitted and a set one carries the time; nothing else about the encoding
// changes, and decoding needs no counterpart because a missing field decodes
// to the zero time.
func (anomaly Anomaly) MarshalJSON() ([]byte, error) {
	type wire Anomaly
	encoded := struct {
		wire
		FailingSince *time.Time `json:"failing_since,omitempty"`
	}{wire: wire(anomaly)}
	if !anomaly.FailingSince.IsZero() {
		encoded.FailingSince = &anomaly.FailingSince
	}
	return json.Marshal(encoded)
}

// Snapshot is one replica's contribution. Owned is the number of objects the
// replica holds, which is what makes the coverage arithmetic possible: the
// anomaly list alone cannot distinguish "nothing wrong" from "nothing seen".
//
// Determined completes that arithmetic. Owning an object is not the same as
// being able to speak for it: a replica that just restarted owns everything and
// knows nothing, and an empty anomaly list from it is indistinguishable from a
// healthy one unless the two counts are reported separately.
type Snapshot struct {
	Replica string    `json:"replica"`
	TakenAt time.Time `json:"taken_at"`
	Owned   int       `json:"owned"`
	// OwnedObjects is which objects, not how many. Owned stays authoritative
	// for the count: the list is bounded like the anomaly list, so a replica
	// holding more than the budget publishes a short list beside a full count
	// rather than a smaller count.
	OwnedObjects []string `json:"owned_objects,omitempty"`
	// StartedAt is when this replica's process started.
	//
	// It is the ceiling on every duration this replica reports. A run this
	// process watched begin cannot predate the process, so after a rollout every
	// "wrong for N minutes" on the page is really "this process has been
	// watching for N minutes" -- and the page had no way to say so, because
	// nothing published the one number that bounds them all.
	//
	// Read three times in one investigation before anyone noticed: 55 pooled
	// objects all reporting the same duration, 83 anomalies all reporting the
	// same duration, and a scheduled comparison that turned out to be measuring
	// a rollout. The per-row provenance says which durations are bounds; this
	// says what they are bounded by.
	StartedAt      time.Time `json:"started_at,omitempty"`
	Determined     int       `json:"determined"`
	Anomalies      []Anomaly `json:"anomalies"`
	TotalAnomalies int       `json:"total_anomalies"`
	// Demoted are the objects held back because their backend kept answering
	// unavailable. They are published apart from Anomalies, not folded into
	// them, because they answer a different question: Anomalies is what this
	// deployment is failing at, Demoted is what it has stopped asking for.
	Demoted      []Anomaly `json:"demoted"`
	TotalDemoted int       `json:"total_demoted"`
	// Undecidable are the objects whose rounds end without a basis to decide
	// recovery, with nothing else wrong. Beside the anomalies rather than in
	// them, because the statement is different in kind: not "this is failing"
	// but "there is nothing here to decide recovery on".
	//
	// A replica running an older build sends neither field. Its objects of
	// this kind stay in Anomalies, which is what they did before this column
	// existed -- the aggregate keeps adding up and the deployment reads
	// slightly worse than it is until every replica is on the new build.
	Undecidable      []Anomaly `json:"undecidable,omitempty"`
	TotalUndecidable int       `json:"total_undecidable,omitempty"`
	// DemotionEntries and DemotionExits are cumulative since this replica
	// started. The pair is the check on the pool: demotion removes objects from
	// the health denominator, so a pool with entries and no exits is a mechanism
	// that only hides, and its occupancy alone cannot show that.
	DemotionEntries    int `json:"demotion_entries"`
	DemotionExtensions int `json:"demotion_extensions"`
	DemotionExits      int `json:"demotion_exits"`
	// LastDemotionExit is when this replica last saw an object leave the pool.
	// Zero means it has not seen one, which is not the same as "none left
	// recently" and must not be rendered as a duration.
	LastDemotionExit time.Time `json:"last_demotion_exit,omitempty"`
	// Capacity is how close this replica is to its own limits. Absent on a
	// replica that does not report it, which is why the aggregate counts the
	// replicas it actually heard from rather than assuming every one answered.
	Capacity *Capacity `json:"capacity,omitempty"`
	// Overdue counts the objects this replica should have picked up and has not.
	// Absent means the replica has nothing holding wake times, which is a
	// different answer from "nothing is overdue" and must not be shown as one.
	Overdue *OverdueFacts `json:"overdue,omitempty"`
	// Suppression is present only on a build whose dispatcher actually holds
	// objects back. Absent, the overdue count above is structurally zero and
	// must not be read as "nothing is overdue".
	//
	// Pointer and omitempty are both load-bearing, and the guard for that is
	// TestSnapshotOmitsDispatchOnAReplicaThatDoesNotSuppress rather than a note
	// here: it asserts on the encoded form, which is where the page reads this.
	Dispatch *DispatchSuppression `json:"dispatch,omitempty"`
	// AppliedActivationRecordRevision is the Activation this replica executes
	// by, as its record revision. Zero means the replica did not report it,
	// which the aggregate keeps apart from any lag.
	AppliedActivationRecordRevision uint64 `json:"applied_activation_record_revision,omitempty"`
	// OpenAlertSet is the state of this replica's copy of the consumer's open
	// alert set, which gates RECOVERY envelopes. Absent on a build without the
	// gate, which is a different answer from a copy that is fine.
	OpenAlertSet *OpenAlertSetFacts `json:"open_alert_set,omitempty"`
}

// OpenAlertSetFacts is what a replica says about its copy of the consumer's
// open alert set. Mode is one of never_loaded, authoritative and
// self_maintained. StaleBeyondBound is the one fact the verdict reads: the
// copy had the consumer's publication and has been without it for longer
// than the staleness bound, so the gate has been working from the replica's
// own knowledge past the exposure it was designed for. A copy that never
// loaded is not stale -- the publisher may not be deployed -- and the mode
// says so without degrading anything.
type OpenAlertSetFacts struct {
	Mode             string `json:"mode"`
	StaleBeyondBound bool   `json:"stale_beyond_bound"`
	// AuthoritativeAgeSeconds is how long ago the last publication was read.
	// Absent until there has been one; a zero here would read as "just now".
	AuthoritativeAgeSeconds *float64 `json:"authoritative_age_seconds,omitempty"`
}

// DegradationKind names a replica-level condition that degrades the verdict
// without being an anomaly on any one object. Closed set.
type DegradationKind string

const (
	// DegradationOpenAlertSetStale: the replica's copy of the consumer's open
	// alert set has been without the consumer's publication for longer than
	// the staleness bound. Every recovery it holds meanwhile on its own
	// knowledge is an alert that stays open past its due, and nothing on the
	// object list shows that.
	DegradationOpenAlertSetStale DegradationKind = "OPEN_ALERT_SET_STALE"
)

// Degradation is one replica-level reason the deployment is degraded.
type Degradation struct {
	Kind    DegradationKind `json:"kind"`
	Replica string          `json:"replica"`
}

// Truncated reports whether the replica had more anomalies than it published.
func (s Snapshot) Truncated() bool {
	return s.TotalAnomalies > len(s.Anomalies)
}

// Gap is one reason the view is incomplete.
type Gap struct {
	Kind    GapKind `json:"kind"`
	Replica string  `json:"replica,omitempty"`
	Detail  string  `json:"detail,omitempty"`
}

// Expectation is the denominator. Known is false when the control plane object
// could not be read, which is different from an empty deployment.
type Expectation struct {
	QueryGroups int
	Known       bool
	// IDs is the catalogue's own object set, not just its size. Carrying it
	// costs one already-loaded slice and is what turns "the two sides disagree
	// by twelve" into "these twelve objects, and here is which kind of
	// disagreement they are". Without it the gap can only name its own two
	// hypotheses, which it did on a running deployment for over a day.
	//
	// Empty is allowed and means the caller could count the set but not carry
	// it; the arithmetic below then falls back to comparing sizes, which is
	// what it did before.
	IDs []string
	// ActivationRecordRevision is the record revision of the Activation the
	// control plane has published, the version every replica is expected to
	// have applied. Zero means it could not be read, and no replica is then
	// called lagging.
	ActivationRecordRevision uint64
}

// Disagreement names which kind of coverage disagreement a deployment has,
// computed from the sets rather than from their sizes.
//
// The two kinds need opposite responses -- one is ownership to clean up, the
// other is this view double counting -- and a difference of counts cannot tell
// them apart. Saying so was honest and useless: it left a reader with "do not
// trust this" and no way to find out.
// The three lists are samples, not the whole set. Each is one object identity
// per entry and the sets are sized by the installation, so on a deployment with
// tens of thousands of objects a rendezvous going wrong would put every one of
// them in a response that is polled every few seconds. The counts beside them
// are the full figures, which is what a reader acts on; the identities are
// there so the reader has somewhere to start looking.
type Disagreement struct {
	// HeldBySeveral are objects more than one replica claims. Covered is a sum,
	// so each of these inflates it by one without any object being left over.
	HeldBySeveral []string `json:"held_by_several,omitempty"`
	// HeldNotExpected are objects some replica holds that the catalogue does
	// not list: ownership that outlived the object.
	HeldNotExpected []string `json:"held_not_expected,omitempty"`
	// ExpectedNotHeld are catalogue objects no replica claims.
	ExpectedNotHeld []string `json:"expected_not_held,omitempty"`
	// The full sizes, which the lists above may not reach. A reader deciding
	// what to do needs the count; the identities only say where to start.
	HeldBySeveralTotal   int `json:"held_by_several_total"`
	HeldNotExpectedTotal int `json:"held_not_expected_total"`
	ExpectedNotHeldTotal int `json:"expected_not_held_total"`
	// Comparable is false when some replica could not publish its object set,
	// so the three lists above are not the whole story. A zero from an
	// incomparable read means "not established", not "none".
	Comparable bool `json:"comparable"`
}

// ReplicaView is one replica's own numbers, kept beside the deployment totals
// rather than only summed into them.
//
// The totals answer "is this deployment well". They cannot answer "is one
// replica carrying the problem", and that is usually the next question: a
// deployment reporting seventy anomalies looks the same whether they are spread
// evenly or all on one replica, and those need different actions. Summing first
// and offering no way back down is what made every investigation start by
// reading the object list one row at a time.
type ReplicaView struct {
	Replica string `json:"replica"`
	Owned   int    `json:"owned"`
	// Determined, Anomalies, Demoted and Undecidable partition Owned the same
	// way the deployment columns partition Expected, so a replica row adds up
	// on its own and a reader can see which replica breaks the sum.
	Determined  int `json:"determined"`
	Anomalies   int `json:"anomalies"`
	Demoted     int `json:"demoted"`
	Undecidable int `json:"undecidable"`
	Healthy     int `json:"healthy"`
	Unknown     int `json:"unknown"`
	// AgeSeconds is how old this replica's snapshot was at the moment of the
	// read. A replica publishing late is reporting about a past it has not
	// updated, and that is invisible in any of the numbers above.
	AgeSeconds float64 `json:"age_seconds"`
	// UptimeSeconds is how long this replica's process has been running, and it
	// is what every duration this replica reports is bounded by. Zero means the
	// replica did not publish it, which is not the same as "just started".
	UptimeSeconds float64 `json:"uptime_seconds,omitempty"`
	// The same three-way split the verdict is decided on, per replica.
	//
	// Anomalies alone cannot answer "which replica is unwell". A live read had
	// 33 against 53, which reads as one replica being sixty percent worse, while
	// the split that actually decides the verdict was 5 against 8 -- most of the
	// difference was external work that is not either replica's doing. The
	// deployment total cannot show that and neither can the anomaly count.
	Ours         int `json:"ours"`
	External     int `json:"external"`
	Unattributed int `json:"unattributed"`
	// Truncated says this replica published a shorter list than it had, so its
	// own counts are floors.
	Truncated bool `json:"truncated"`
	// Capacity is this replica's own occupancy. Present only where the replica
	// reports it; absent is different from zero and stays absent.
	Capacity *Capacity `json:"capacity,omitempty"`
	// AckedVersion is the Activation record revision this replica reported
	// executing by, absent when it reported none. Lag is the difference
	// between it and the deployment's PublishedVersion, two persisted
	// versions; it is never derived from when the report was made.
	AckedVersion *uint64 `json:"acked_version,omitempty"`
}

// WorkerAcknowledgement partitions the replicas the view counted by whether
// they have applied the Activation the control plane published: Ready is the
// counted replicas and equals Acked + Lagging + Unknown. Unknown is a replica
// that reported no version, or a deployment whose published version could
// not be read; it is never folded into either side.
type WorkerAcknowledgement struct {
	Ready   int `json:"ready"`
	Acked   int `json:"acked"`
	Lagging int `json:"lagging"`
	Unknown int `json:"unknown"`
}

// View is the aggregated answer returned to callers.
type View struct {
	Health   Health `json:"health"`
	Expected *int   `json:"expected"`
	Covered  int    `json:"covered"`
	// Determined is the subset of Covered whose owning replica has actually
	// observed a conclusive round. Covered minus Determined is counted into
	// Unknown, not into health.
	Determined int   `json:"determined"`
	Unknown    int   `json:"unknown"`
	Gaps       []Gap `json:"gaps,omitempty"`
	// Degradations are replica-level conditions that make the verdict
	// DEGRADED on their own, beside the anomalies: what is wrong is a replica's
	// standing, not any object it evaluates.
	Degradations []Degradation `json:"degradations,omitempty"`
	// AnomaliesTotal is how many anomalies the replicas actually had, which is
	// larger than the returned list whenever a snapshot was truncated. Reporting
	// the returned length as the total would understate an incident by exactly
	// the amount that made it worth reporting.
	AnomaliesTotal int `json:"anomalies_total"`
	// Capacity answers "how close is this deployment to its limits" from the
	// same read that produced the verdict, so the two cannot disagree and the
	// answer does not wait on collection.
	Capacity *CapacityView `json:"capacity,omitempty"`
	// Overdue counts objects nothing came back for, across the deployment. The
	// objects themselves are in the anomaly list; this survives that list being
	// paged or truncated, because "how many objects are not being evaluated" is
	// the one number that must not depend on how much of the list fitted.
	Overdue *OverdueFacts `json:"overdue,omitempty"`
	// Suppression says whether anything in this deployment can be parked at
	// all. See DispatchSuppression: its absence, not its value, is the answer.
	Dispatch  *DispatchSuppression `json:"dispatch,omitempty"`
	Anomalies []Anomaly            `json:"anomalies"`
	// Healthy is Determined minus the listed columns. It is computed here
	// rather than left to the page because it is the column a reader trusts
	// most, and a page that derives it by subtraction can be made to show a
	// healthy count that nothing produced.
	Healthy int `json:"healthy"`
	// Undecidable are the objects that complete every round without a basis to
	// decide recovery, and where nothing else is wrong. They are a normal
	// condition, not a milder fault, and they are out of the anomaly count for
	// that reason -- listed here because anything that shrinks that count has
	// to show which objects it took out of it.
	Undecidable      []Anomaly `json:"undecidable"`
	UndecidableTotal int       `json:"undecidable_total"`
	// Demoted are objects whose backend kept answering unavailable. They are
	// out of the health verdict on purpose -- that is what demotion is for --
	// which is exactly why the flow numbers below travel with them.
	Demoted      []Anomaly `json:"demoted"`
	DemotedTotal int       `json:"demoted_total"`
	// DemotionEntries, DemotionExits and LastDemotionExit are summed over the
	// counted replicas. Exits is the one that matters: demotion takes objects
	// out of the denominator, so a pool that fills and never drains is a
	// mechanism for making a deployment look well, and only this number says so.
	DemotionEntries    int       `json:"demotion_entries"`
	DemotionExtensions int       `json:"demotion_extensions"`
	DemotionExits      int       `json:"demotion_exits"`
	LastDemotionExit   time.Time `json:"last_demotion_exit,omitempty"`
	// DemotedDue counts pooled objects whose own cooldown window has already
	// elapsed at the moment of this read: they are due to be tried again and are
	// still in the pool.
	//
	// It is derived from each object's own deadline rather than from a threshold
	// chosen here, which is why it can be reported before anyone has decided
	// what number is too many. A pool where this keeps climbing is one whose way
	// out has stopped working, and that is the failure demotion can cause and
	// occupancy cannot show.
	//
	// No alert threshold is set on any of this, and that is a decision rather
	// than an omission. Drawing the line needs a deployment's own numbers, and
	// the four published here are what it needs: entries and exits give the
	// balance, extensions separate a pool being retried from one nobody is
	// touching, and this one says how many are already overdue for that retry.
	// Anyone can draw the line from two reads of these; nobody can draw a
	// defensible one today, and a number invented now would be obeyed later as
	// though it had been measured.
	DemotedDue int `json:"demoted_due"`
	// Coverage says which kind of disagreement the counts have, when the sets
	// were available to compare. Absent when no replica published its set.
	Coverage *Disagreement `json:"coverage,omitempty"`
	Replicas []string      `json:"replicas"`
	// PerReplica is the same deployment broken back down. It is not derived by
	// the page: the page can only divide totals, which cannot recover which
	// replica an anomaly came from.
	PerReplica []ReplicaView `json:"per_replica"`
	// PublishedVersion is the Activation record revision the control plane
	// published, the version the replicas' acked_version columns are read
	// against; absent when it could not be read.
	PublishedVersion uint64                `json:"published_version,omitempty"`
	Workers          WorkerAcknowledgement `json:"workers"`
}

// Aggregate folds the published snapshots into one view.
//
// expectation is read from the control plane rather than derived from any
// replica's own state: on a deployment with several replicas only the control
// leader knows the whole active set, and the others see an empty one. Deriving
// the denominator locally would make three replicas out of four report full
// coverage of nothing.
func Aggregate(expectation Expectation, snapshots []Snapshot, expectedReplicas []string, now time.Time, freshness time.Duration) View {
	view := View{Health: HealthHealthy, Anomalies: []Anomaly{}, Demoted: []Anomaly{},
		Undecidable: []Anomaly{},
		Replicas:    []string{}, PerReplica: []ReplicaView{}}
	ownedByReplica := make([]string, 0, len(expectedReplicas))
	// The snapshots this view is willing to speak for. Every other number below
	// is built from these and not from the argument, because the argument
	// contains snapshots that were rejected: one too old to describe the
	// deployment now, and one from a replica that is no longer part of it and
	// whose key has simply not expired yet.
	counted := make([]Snapshot, 0, len(expectedReplicas))
	ownedSets := make([][]string, 0, len(expectedReplicas))
	setsComplete := true

	byReplica := make(map[string]Snapshot, len(snapshots))
	for _, snapshot := range snapshots {
		byReplica[snapshot.Replica] = snapshot
	}
	view.PublishedVersion = expectation.ActivationRecordRevision

	// No replica means no evidence. Reporting healthy here would turn the whole
	// deployment being gone into the quietest possible answer.
	if len(expectedReplicas) == 0 {
		view.Gaps = append(view.Gaps, Gap{Kind: GapNoReplicas})
	}

	for _, replica := range expectedReplicas {
		snapshot, published := byReplica[replica]
		if !published {
			view.Gaps = append(view.Gaps, Gap{Kind: GapReplicaMissing, Replica: replica})
			continue
		}
		if freshness > 0 && now.Sub(snapshot.TakenAt) > freshness {
			view.Gaps = append(view.Gaps, Gap{Kind: GapSnapshotStale, Replica: replica})
			continue
		}
		view.Replicas = append(view.Replicas, replica)
		counted = append(counted, snapshot)
		ownedByReplica = append(ownedByReplica, fmt.Sprintf("%s %d", shortReplicaName(replica), snapshot.Owned))
		view.Covered += snapshot.Owned
		ownedSets = append(ownedSets, snapshot.OwnedObjects)
		if len(snapshot.OwnedObjects) < snapshot.Owned {
			// A replica that published a short set cannot be compared, and a
			// zero from an incomparable read is not "none".
			setsComplete = false
		}
		view.Determined += snapshot.Determined
		view.AnomaliesTotal += snapshot.TotalAnomalies
		view.Anomalies = append(view.Anomalies, snapshot.Anomalies...)
		view.DemotedTotal += snapshot.TotalDemoted
		view.Demoted = append(view.Demoted, snapshot.Demoted...)
		view.UndecidableTotal += snapshot.TotalUndecidable
		view.Undecidable = append(view.Undecidable, snapshot.Undecidable...)
		view.DemotionEntries += snapshot.DemotionEntries
		view.DemotionExtensions += snapshot.DemotionExtensions
		view.DemotionExits += snapshot.DemotionExits
		if snapshot.LastDemotionExit.After(view.LastDemotionExit) {
			view.LastDemotionExit = snapshot.LastDemotionExit
		}
		if snapshot.Truncated() {
			view.Gaps = append(view.Gaps, Gap{Kind: GapListTruncated, Replica: replica})
		}
		if snapshot.OpenAlertSet != nil && snapshot.OpenAlertSet.StaleBeyondBound {
			view.Degradations = append(view.Degradations, Degradation{Kind: DegradationOpenAlertSetStale, Replica: replica})
		}
		perReplica := ReplicaView{
			Replica: replica, Owned: snapshot.Owned, Determined: snapshot.Determined,
			Anomalies: snapshot.TotalAnomalies, Demoted: snapshot.TotalDemoted,
			Undecidable: snapshot.TotalUndecidable,
			AgeSeconds:  now.Sub(snapshot.TakenAt).Seconds(),
			// Left at zero when the replica did not publish a start time, which
			// an older build will not. Zero has to read as "not reported" rather
			// than "started just now", so the page checks before using it.
			UptimeSeconds: uptimeSeconds(snapshot.StartedAt, now),
			Truncated:     snapshot.Truncated(), Capacity: snapshot.Capacity,
		}
		view.Workers.Ready++
		switch {
		case snapshot.AppliedActivationRecordRevision == 0 || expectation.ActivationRecordRevision == 0:
			view.Workers.Unknown++
		case snapshot.AppliedActivationRecordRevision >= expectation.ActivationRecordRevision:
			// Ahead of the published version can only be a read of the two
			// facts straddling an activation; the replica is not behind.
			view.Workers.Acked++
		default:
			view.Workers.Lagging++
		}
		if snapshot.AppliedActivationRecordRevision != 0 {
			acked := snapshot.AppliedActivationRecordRevision
			perReplica.AckedVersion = &acked
		}
		// Same subtraction as the deployment view, for the same reason: a
		// healthy count built by its own walk drifts from the lists beside it.
		perReplica.Unknown = snapshot.Owned - snapshot.Determined
		if perReplica.Unknown < 0 {
			perReplica.Unknown = 0
		}
		perReplica.Healthy = snapshot.Determined - snapshot.TotalAnomalies -
			snapshot.TotalDemoted - snapshot.TotalUndecidable
		if perReplica.Healthy < 0 {
			perReplica.Healthy = 0
		}
		view.PerReplica = append(view.PerReplica, perReplica)
	}
	// Determined is what the replicas can speak for, and every object they can
	// speak for is in exactly one of the four. Subtracting rather than counting
	// healthy objects directly is deliberate: a healthy count built by its own
	// walk can drift from the lists beside it, and the drift shows up as a
	// column that adds up to slightly more than the deployment has.
	view.Healthy = view.Determined - view.AnomaliesTotal - view.DemotedTotal - view.UndecidableTotal
	if view.Healthy < 0 {
		// The three columns claim more objects than the replicas said they can
		// speak for. Something is being counted twice, so no column can be
		// trusted -- including the healthy one.
		//
		// The difference goes to UNKNOWN and nowhere else, and this looks like
		// needless caution until you ask where else it could go. Letting the
		// healthy column absorb it just makes that number smaller, and a smaller
		// healthy count is the one reading nobody investigates: it looks like
		// the deployment having a bad day, which is exactly what a page full of
		// anomalies has already told them. A double count that lands there is
		// never found. Every other column is looked at by someone who wants it
		// explained, so an error that lands in one of those surfaces on its own.
		//
		// So this is not "be conservative when unsure". It is: the arithmetic
		// broke, and the only honest place to put a broken number is the column
		// that means "do not trust this view".
		view.Gaps = append(view.Gaps, Gap{Kind: GapCoverageInconsistent,
			Detail: fmt.Sprintf("%d anomalies and %d demoted against %d determined; the columns claim more "+
				"objects than the replicas can speak for, so none of them adds up",
				view.AnomaliesTotal, view.DemotedTotal, view.Determined)})
		view.Healthy = 0
	}

	// Built from the same snapshots as the verdict, for the same reason the
	// verdict refuses them: a stale snapshot's occupancy is what the process was
	// doing when it last published, and the page presents capacity as "right
	// now". Reading the whole argument here meant a deployment whose replicas had
	// all gone quiet showed UNKNOWN with no live replica beside memory and permit
	// figures from before it went quiet -- the one moment those numbers are read
	// hardest, and the one moment they are not about the present.
	aggregateCapacity(&view, counted)
	// Same snapshots, same reason: a stale replica's idea of what it has not
	// picked up describes a moment that has passed.
	aggregateOverdue(&view, counted)
	aggregateDispatchSuppression(&view, counted)

	// Owning an object is not knowing about it. A replica that has just restarted
	// owns everything and has observed nothing, so its empty anomaly list is not
	// evidence of health -- and neither is the list of an object whose every
	// round is inconclusive, nor of one the tracker dropped at its bound.
	switch {
	case view.Determined > view.Covered:
		view.Gaps = append(view.Gaps, Gap{Kind: GapCoverageInconsistent,
			Detail: "more objects reported as determined than owned"})
	case view.Covered > view.Determined:
		view.Unknown += view.Covered - view.Determined
		view.Gaps = append(view.Gaps, Gap{Kind: GapUndetermined})
	}

	view.Coverage = compareCoverage(ownedSets, expectation, setsComplete)
	if !expectation.Known {
		view.Gaps = append(view.Gaps, Gap{Kind: GapDenominatorUnavailable})
	} else {
		expected := expectation.QueryGroups
		view.Expected = &expected
		switch {
		case view.Covered > expected:
			// Counting is not set arithmetic: more covered than expected means
			// the two sides disagree about which objects exist, and a shortfall
			// could still be hiding inside that difference.
			//
			// It says both numbers because this gap alone holds the verdict at
			// UNKNOWN. Naming only the condition leaves a reader with "do not
			// trust this" and nothing to act on -- they cannot tell twelve
			// objects left over from a catalogue that shrank from a deployment
			// owning hundreds it should not, and those call for opposite
			// responses. The sibling branch above already explains itself; this
			// one did not, which is the whole difference.
			// The per-replica split travels with the totals because Covered is
			// a sum, and a sum cannot tell two replicas owning distinct shares
			// from two replicas both holding the same objects through a
			// rendezvous change. Those are different problems -- one is stale
			// ownership to clean up, the other is this view double counting --
			// and stating only the difference asserts the first.
			view.Gaps = append(view.Gaps, Gap{Kind: GapCoverageInconsistent,
				Detail: coverageDetail(view.Coverage, view.Covered, expected, ownedByReplica)})
		case expected > view.Covered:
			// Added rather than assigned: objects nobody owns and objects owned
			// by a replica that cannot speak for them are both unknown, and they
			// are different objects.
			view.Unknown += expected - view.Covered
			view.Gaps = append(view.Gaps, Gap{Kind: GapOwnershipShortfall})
		}
	}

	sortAnomalies(view.Anomalies)
	sortAnomalies(view.Demoted)
	for _, demoted := range view.Demoted {
		if demoted.QueryCooldown != nil && !demoted.QueryCooldown.Until.IsZero() &&
			demoted.QueryCooldown.Until.Before(now) {
			view.DemotedDue++
		}
	}

	Attribute(view.Anomalies)
	Settle(&view)
	return view
}

// Settle sets the verdict, and the per-replica breakdown of what it is about,
// from the anomalies as currently attributed.
//
// It is a function rather than inline code because it runs twice: once when the
// view is built, and again once the caller has marked which objects are stalled
// -- and a stalled object is ours whatever its last reason code said. Two
// copies of this rule would be two verdicts that agree until they do not.
//
// Only the objects this deployment could have prevented decide the verdict. The
// rest stay in the list, counted and visible, because they are real work; they
// are just not this deployment's work, and a verdict that cannot come back
// while they exist tells nobody anything.
func Settle(view *View) {
	// The per-replica split is refreshed in the same pass that decides the
	// verdict, because they are two readings of one classification: computed
	// separately they can disagree, and the disagreement would be invisible --
	// a deployment reported DEGRADED with every replica showing zero of the
	// thing that made it so.
	byReplica := map[string]*ReplicaView{}
	for index := range view.PerReplica {
		replica := &view.PerReplica[index]
		replica.Ours, replica.External, replica.Unattributed = 0, 0, 0
		byReplica[replica.Replica] = replica
	}
	for _, anomaly := range view.Anomalies {
		replica, known := byReplica[anomaly.Replica]
		if !known {
			continue
		}
		switch anomaly.Attribution {
		case AttributionExternal:
			replica.External++
		case AttributionUnknown:
			replica.Unattributed++
		default:
			replica.Ours++
		}
	}

	// Order matters: an incomplete view cannot be called healthy, and it cannot
	// be called degraded either, because the anomalies it does show are not the
	// whole story.
	switch {
	case len(view.Gaps) > 0:
		view.Health = HealthUnknown
	// A replica's standing degrades the verdict the way its objects do, and
	// before the object list is consulted: a copy of the open alert set that
	// has been on its own past its bound is a fault the object list cannot
	// show, because every alert it keeps open looks like one still due.
	case len(view.Degradations) > 0:
		view.Health = HealthDegraded
	case OursCount(view.Anomalies) > 0:
		view.Health = HealthDegraded
	// An object whose cause was never recorded is missing evidence about a real
	// anomaly. This package already refuses to call a view with missing evidence
	// either healthy or degraded, and that rule does not stop applying because
	// the evidence is missing per object rather than per replica.
	//
	// It clears itself: each of these has a cause again as soon as it completes
	// one more round, and one that never completes another is marked stalled,
	// which is ours. So a rollout reads UNKNOWN for a minute or two instead of
	// reading DEGRADED, and neither reads as well.
	case UnattributedCount(view.Anomalies) > 0:
		view.Health = HealthUnknown
	default:
		view.Health = HealthHealthy
	}
}

// compareCoverage works out which kind of disagreement the counts have.
//
// Nil when nothing can be compared -- no replica published a set, or the
// catalogue did not carry one. Nil is the honest answer there: the previous
// behaviour was to state both hypotheses and let the reader pick, which on a
// running deployment meant a gap that said the same ambiguous thing for over a
// day while nobody could act on it.
func compareCoverage(ownedSets [][]string, expectation Expectation, setsComplete bool) *Disagreement {
	held := make(map[string]int)
	for _, set := range ownedSets {
		// Counted per replica, not per row, so one replica listing an object
		// twice cannot look like two replicas holding it.
		seen := make(map[string]struct{}, len(set))
		for _, object := range set {
			if _, repeat := seen[object]; repeat {
				continue
			}
			seen[object] = struct{}{}
			held[object]++
		}
	}
	if len(held) == 0 {
		return nil
	}
	result := &Disagreement{Comparable: setsComplete && expectation.Known && len(expectation.IDs) > 0}
	for object, holders := range held {
		if holders > 1 {
			result.HeldBySeveral = append(result.HeldBySeveral, object)
		}
	}
	if len(expectation.IDs) > 0 {
		expected := make(map[string]struct{}, len(expectation.IDs))
		for _, object := range expectation.IDs {
			expected[object] = struct{}{}
		}
		for object := range held {
			if _, listed := expected[object]; !listed {
				result.HeldNotExpected = append(result.HeldNotExpected, object)
			}
		}
		for object := range expected {
			if _, owned := held[object]; !owned {
				result.ExpectedNotHeld = append(result.ExpectedNotHeld, object)
			}
		}
	}
	sort.Strings(result.HeldBySeveral)
	sort.Strings(result.HeldNotExpected)
	sort.Strings(result.ExpectedNotHeld)
	// Counted before the cut, so the numbers are the real ones however many
	// identities travel. Sorted first, so the sample is the same objects on
	// every read rather than whichever the map happened to yield.
	result.HeldBySeveralTotal = len(result.HeldBySeveral)
	result.HeldNotExpectedTotal = len(result.HeldNotExpected)
	result.ExpectedNotHeldTotal = len(result.ExpectedNotHeld)
	result.HeldBySeveral = firstN(result.HeldBySeveral, MaxCoverageSample)
	result.HeldNotExpected = firstN(result.HeldNotExpected, MaxCoverageSample)
	result.ExpectedNotHeld = firstN(result.ExpectedNotHeld, MaxCoverageSample)
	return result
}

// MaxCoverageSample bounds how many object identities a coverage disagreement
// carries. The page shows six; the rest is headroom for someone reading the
// JSON, not a second copy of the catalogue.
const MaxCoverageSample = 32

func firstN(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

// coverageDetail states what the disagreement is, falling back to naming the
// two possibilities only when the sets were not available to tell them apart.
func coverageDetail(coverage *Disagreement, covered, expected int, ownedByReplica []string) string {
	head := fmt.Sprintf("%d owned (%s) against %d expected, %d more",
		covered, strings.Join(ownedByReplica, ", "), expected, covered-expected)
	if coverage == nil || !coverage.Comparable {
		return head + "; Covered is a sum, so replicas holding the same object during a rendezvous " +
			"change look the same as objects left over, and this read could not compare the sets to say which"
	}
	// The totals, not the lengths of the lists beside them. Those lists are a
	// bounded sample now, so reading their length would report the size of the
	// sample as the size of the problem -- and it would do it only once the
	// problem grew past the bound, which is when the number matters most.
	return fmt.Sprintf("%s: %d held by more than one replica (Covered double counts these), "+
		"%d held but no longer in the catalogue (ownership to release), %d in the catalogue that nobody holds",
		head, coverage.HeldBySeveralTotal, coverage.HeldNotExpectedTotal, coverage.ExpectedNotHeldTotal)
}

// uptimeSeconds turns a published start time into an age, refusing the two
// values that are not one.
//
// A zero start time is a replica that did not publish one; a start time in the
// future is a clock disagreement. Both would render as a very small uptime,
// which is the reading that matters most here -- a small uptime is what tells
// the page every duration below it is capped. Getting that wrong invents a
// restart that did not happen.
func uptimeSeconds(startedAt time.Time, now time.Time) float64 {
	if startedAt.IsZero() || startedAt.After(now) {
		return 0
	}
	return now.Sub(startedAt).Seconds()
}

// sortAnomalies orders a column oldest-first. Both columns use it, because a
// reader comparing them is comparing two lists and an ordering that differs
// between them turns "which has been wrong longer" into a question about the
// page.
func sortAnomalies(anomalies []Anomaly) {
	sort.Slice(anomalies, func(left, right int) bool {
		if anomalies[left].Since.Equal(anomalies[right].Since) {
			return anomalies[left].QueryGroup < anomalies[right].QueryGroup
		}
		return anomalies[left].Since.Before(anomalies[right].Since)
	})
}

// SortAnomaliesNewestFirst is the other end of the same list.
//
// Oldest-first exists so an object that has been wrong for days cannot be
// pushed off the end by a burst of new ones. It has the symmetric failure: a
// burst of new ones lands past the end instead. Neither ordering is wrong and
// neither answers both questions, so the reader picks -- rather than the page
// picking and then asserting in its own wording that its choice is the batch
// worth reading.
//
// The tiebreak is reversed too. Reversing only the timestamp would leave
// same-instant objects in the same relative order in both directions, which
// reads as an ordering that did not fully apply.
func SortAnomaliesNewestFirst(anomalies []Anomaly) {
	sort.Slice(anomalies, func(left, right int) bool {
		if anomalies[left].Since.Equal(anomalies[right].Since) {
			return anomalies[left].QueryGroup > anomalies[right].QueryGroup
		}
		return anomalies[left].Since.After(anomalies[right].Since)
	})
}

// shortReplicaName keeps the part of a Pod name that differs between replicas.
// The full name repeats the deployment on every entry, which pushes the numbers
// this detail exists for off the end of whatever is reading it.
func shortReplicaName(replica string) string {
	if index := strings.LastIndex(replica, "-"); index >= 0 && index+1 < len(replica) {
		return replica[index+1:]
	}
	return replica
}
