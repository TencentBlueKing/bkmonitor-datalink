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
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Paging is mandatory rather than optional. An endpoint that returns everything
// by default is fine until the day it is needed most, when the list is longest.
const (
	// DefaultPageSize is what a caller gets without asking.
	DefaultPageSize = 50
	// MaxPageSize bounds what a caller can ask for.
	MaxPageSize = 500
)

// Page describes the slice of anomalies in a response. Total is how many the
// response could page over, which is not how many exist: when a replica
// truncated its list, the real count is the view's anomalies_total and the gap
// that says so.
type Page struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
	Total  int `json:"total"`
}

// ListResponse is the object list, and it is exported for the same reason
// DetailResponse is: the page's field names are checked against the Go type
// that produces them, and a type the check cannot see is a response whose
// fields can be renamed out from under the page with everything green.
//
// This one went uncovered longer than the others because the page read it into
// a variable named d, shared with three unrelated responses -- so the check
// could not be pointed at it without failing on every other response's fields.
// The variable is named objects now.
type ListResponse struct {
	View
	// Replica echoes the filter, so a caller cannot mistake a filtered response
	// for a deployment-wide one. The coverage numbers stay deployment-wide.
	Replica  string `json:"replica,omitempty"`
	Strategy string `json:"strategy,omitempty"`
	Business string `json:"business,omitempty"`
	// Interval echoes the period filter, when one was asked for: the rows are
	// the objects a per-period number was counted over. A pointer because 0
	// is a real answer (objects whose period is not known).
	Interval *int64 `json:"interval,omitempty"`
	// Applied says a filter narrowed this response. An empty table means
	// something different when it was filtered, and the caller cannot tell the
	// two apart from the rows alone.
	Applied bool `json:"filtered"`
	// Column says which of the two lists the rows and the summary are about.
	// Always sent, never inferred from the request: a response that does not say
	// looks identical either way, and the two lists mean opposite things about
	// whose fault the objects are.
	Column string `json:"column"`
	// Order says which end of the population the first page is, for the same
	// reason Column is echoed: the two orderings return disjoint first pages
	// from one list, and a response that does not say looks the same either way.
	Order   string  `json:"order"`
	Summary Summary `json:"summary"`
	// StallAfterSeconds is the budget an object's rounds have to finish in before
	// the list calls it stalled. It is reported rather than assumed by the reader
	// so the flag can be checked against the deployment that produced it instead
	// of against a number someone remembers.
	StallAfterSeconds int `json:"stall_after_seconds,omitempty"`
	// StalledTotal counts stalled objects across the whole deployment, whatever
	// the filter and whichever column is being served. The filtered count
	// belongs in Summary with everything else, but this one number must survive
	// both: it is the objects that will not recover on their own, and anything
	// that hides them -- a filter, or a reader having switched columns -- reads
	// as "nothing to do here".
	StalledTotal int `json:"stalled_total"`
	// Checks is the first screen: every check with objects under it, across
	// every column, before any filter. It is on this response rather than the
	// verdict's so that the counts and the rows a check opens come from one
	// read of the view.
	Checks []CheckReport `json:"checks"`
	// Todo is the first screen's arithmetic: lines to act on, distinct
	// objects under them now, and the record of past loss apart from both.
	Todo Todo `json:"todo"`
	// Cohorts and Cooling are the same as the verdict route's, on this
	// response too because the first screen is drawn from this read: the
	// join from a per-period number to its rows, and the cooldown line.
	Cohorts []CohortView `json:"cohorts"`
	Cooling CoolingFacts `json:"cooling"`
	// Check and Group echo which line and which fold the rows are, when the
	// request asked for one. Echoed rather than inferred from the request, like
	// Column: the rows of one check under another's heading read as that
	// check's.
	Check Check  `json:"check,omitempty"`
	Group string `json:"group,omitempty"`
	Page  Page   `json:"page"`
}

// HealthResponse is what the verdict route answers with.
//
// It is a type rather than the map it used to be, and exported for the same
// reason DetailResponse is: the page's own test reflects over it and fails on
// any field the page reads that this does not send. As a map there was nothing
// to reflect over, so the verdict route was the one response the check could not
// cover -- and that is where it went wrong. Four columns were added to the View
// and to the page in one change; the map in between was not touched, so the page
// asked for fields nobody sent. A missing field is not an error in JavaScript,
// and the four new cells rendered "undefined" on a live deployment.
//
// The lists themselves stay out. This route answers "how is the deployment",
// which has to stay small enough to poll; the objects route carries the rows.
type HealthResponse struct {
	Health     Health `json:"health"`
	Expected   *int   `json:"expected"`
	Covered    int    `json:"covered"`
	Determined int    `json:"determined"`
	Unknown    int    `json:"unknown"`
	// Healthy, AnomaliesTotal, DemotedTotal, UndecidableTotal and Unknown
	// partition the expected set. The page prints their sum against Expected,
	// so all of them have to come from this one read: computed from two reads
	// they could disagree for reasons that have nothing to do with the
	// deployment.
	Healthy        int `json:"healthy"`
	AnomaliesTotal int `json:"anomalies_total"`
	DemotedTotal   int `json:"demoted_total"`
	// UndecidableTotal is the objects completing every round with no basis to
	// decide recovery, and nothing else wrong. A normal condition, held out of
	// the anomaly count -- and therefore reported here, because a column that
	// makes the anomaly count smaller has to be visible next to it.
	UndecidableTotal int `json:"undecidable_total"`
	// ByDesignTotal is the rounds interrupted by a change already being
	// made on purpose. Reported for the same reason as the column above: it
	// makes the anomaly count smaller, so it has to be visible beside it.
	ByDesignTotal int `json:"by_design_total"`
	// EmptyEveryRoundTotal is the objects this deployment has never seen
	// return records and whose every round for an hour completed empty. Not
	// part of the partition above -- those objects are in Healthy, which
	// they are as far as this deployment goes -- and reported beside it
	// because a strategy with nothing to detect at its period reads as a
	// healthy one everywhere else on this response.
	EmptyEveryRoundTotal int `json:"empty_every_round_total"`
	// Ours and Unattributed are the two numbers the verdict is actually
	// decided on, and they were not on this response at all.
	//
	// A live deployment read UNKNOWN with every number on the verdict panel at
	// zero -- no coverage gaps, unknown-state zero, all five columns adding up
	// to the expected total -- and nothing on the page could say why. The
	// verdict was UNKNOWN because some anomalies carried no cause, which is a
	// different "unknown" from the state column beside it and had no field of
	// its own. A reader could reach no conclusion except that the page was
	// wrong.
	//
	// They come from the same settled view as Health, so the number and the
	// verdict cannot be from different reads.
	Ours         int `json:"ours"`
	Unattributed int `json:"unattributed"`
	// Impact is the same population in the unit the work is done in.
	//
	// Every other number on this response counts objects, which are this
	// deployment's own identities -- an operator cannot look one up, cannot
	// mention one to whoever configured the strategy, and cannot tell from a
	// count of them whether this is one misconfiguration or fifty. A reader
	// could see HEALTHY beside 58 demoted objects and still not know which
	// alerts are not being raised or how many businesses that touches.
	Impact Impact `json:"impact"`
	// StrategyLinkBase turns the strategy references in the object list into
	// links to the strategy itself.
	//
	// The page's drill-down stopped at its own table: clicking a strategy
	// filtered the list by it, which is the one thing a reader who has already
	// found it does not need. Handing the id to the next person meant copying a
	// number into a search box, and "找策略和数据源的人" is not a handover.
	//
	// Sent as the origin only. The path is the product's own route and is built
	// in the page, so an environment configures one value and nothing about how
	// the page is put together. Empty when nobody configured one, and the
	// references then render as the plain labels they always were.
	StrategyLinkBase string `json:"strategy_link_base,omitempty"`
	// DemotedDue and the three flow counts are the check on demotion, which is
	// the one mechanism here that makes a deployment look better by removing
	// objects from the denominator.
	DemotedDue int `json:"demoted_due"`
	// DemotedDueOldestSeconds is how long the most overdue object has waited
	// past its own retry deadline. The count alone reads the same whether the
	// retry path is working or stopped; this is what separates them.
	DemotedDueOldestSeconds int `json:"demoted_due_oldest_seconds,omitempty"`
	DemotionEntries         int `json:"demotion_entries"`
	DemotionExtensions      int `json:"demotion_extensions"`
	DemotionExits           int `json:"demotion_exits"`
	// DemotionRestored, DemotionHandovers and DemotionReentries: see View.
	// Entries plus restored equal exits plus handovers plus the pool now.
	DemotionRestored  int `json:"demotion_restored"`
	DemotionHandovers int `json:"demotion_handovers"`
	DemotionReentries int `json:"demotion_reentries"`
	// A pointer because omitempty does nothing for a struct: a zero time.Time
	// still serialises, as "0001-01-01T00:00:00Z", and that string is truthy in
	// the page. The page guards this field by truthiness, so a zero would render
	// 最近一次出池 as a date in the year 1 -- the guard written to catch "no exit
	// recorded" passing the one value it exists to catch.
	//
	// The tracker writes the count and the timestamp on adjacent lines, so the
	// deployment cannot reach that state today. The guard should not depend on
	// an invariant kept in a different file, and a fixture should not be able to
	// express a deployment that cannot exist.
	LastDemotionExit *time.Time `json:"last_demotion_exit,omitempty"`
	// PrunedSkips are the objects that lost a span of Slots to a pruned
	// timeline. They are in no column and in no total on this response, because
	// they belong to none: the objects are running now.
	//
	// A list rather than a count. The count alone cannot be acted on -- what a
	// reader needs is which objects and how long a span each lost, and the
	// spans differ by orders of magnitude between a cursor that fell a minute
	// behind and one that fell a day behind.
	PrunedSkips []PrunedSkipRef `json:"pruned_skips,omitempty"`
	// PrunedSkipsTotal, RetainedShareTotal and ReadEarlyTotal are how many
	// objects each of the three lists has; each list carries the first
	// FirstScreenListBound of them in its order, and the objects route lists
	// them all under their checks. Uncapped, a list grew with the deployment
	// and every page refresh carried all of it.
	PrunedSkipsTotal int `json:"pruned_skips_total,omitempty"`
	// RetainedShare is the objects whose latest completed Slot held at least
	// RetainedShareApproachPercent of the one-object share of the retained
	// pool, fullest first. In no column and no total: they are detecting. A
	// list, because what a reader acts on is which strategy and how close,
	// and the refusal it warns of stops that strategy whole.
	RetainedShare      []RetainedShareRef `json:"retained_share,omitempty"`
	RetainedShareTotal int                `json:"retained_share_total,omitempty"`
	// ReadEarly is the objects the late-data lookback found read before
	// their data was complete in two samples in a row, with the time_delay
	// that would have read them complete. In no column and no total: they
	// are detecting, from data that was not all there.
	ReadEarly      []ReadEarlyRef `json:"read_early,omitempty"`
	ReadEarlyTotal int            `json:"read_early_total,omitempty"`
	// PublishedVersion and Workers are the acknowledgement view: which
	// Activation the control plane published and how many counted replicas
	// have applied it. Per-replica versions are on PerReplica.
	PublishedVersion uint64                `json:"published_version,omitempty"`
	Workers          WorkerAcknowledgement `json:"workers"`
	// Builds is which build each counted replica runs, grouped. The first
	// thing to establish about any reading is what produced it; before this
	// field that meant a PromQL query against build_info for each pod.
	Builds []BuildGroup `json:"builds"`
	// OutputProtocols is which output protocol choice each counted replica
	// runs with, grouped the same way. Before this field the answer was in a
	// values file on a machine the reader could not reach. Per strategy, the
	// frozen format is on the directory route's effective_output.
	OutputProtocols []OutputProtocolGroup `json:"output_protocols"`
	// Retentions is which retention lengths each counted replica runs with,
	// and their inputs, grouped the same way: what runtime.get answers on
	// the CLI channel, for a deployment read only through its pages.
	Retentions []RetentionGroup `json:"retentions"`
	// OutputPath is whether any event can leave as the standard raw event
	// under those choices, from the leader's Plan counts: the first-screen
	// sentence a two-line, one-Kafka-read investigation reduced to.
	OutputPath OutputPathFacts `json:"output_path"`
	// Cohorts is every evaluation period with its population and what the
	// rows say about it, and Cooling the objects waiting in a query cooldown
	// by whose line they are under: the join from a number read per period
	// to the objects it was counted over (the list route takes interval=).
	Cohorts []CohortView `json:"cohorts"`
	Cooling CoolingFacts `json:"cooling"`
	// Degradations are the replica-level standings the verdict was decided
	// on, and Activation the control leader's standing on the publication
	// the fleet executes. Both decided the verdict before they were on this
	// response: a DEGRADED badge whose only sentence named the object list,
	// over a deployment that had executed a stale publication for half a
	// day. Present and empty when there are none, so a reader can tell "no
	// standing degrades this deployment" from "this build has no such field".
	Degradations []Degradation    `json:"degradations"`
	Activation   *ActivationFacts `json:"activation"`
	// NoDataHorizon is the platform no-data tracking horizon every Plan that
	// states none has frozen, and which layer set it (DEFAULT, VALUES or
	// DYNAMIC). A Plan with its own says STRATEGY on its tracking row.
	NoDataHorizon *NoDataHorizonFacts `json:"no_data_horizon,omitempty"`
	// PlatformSettingFields is every platform setting's effective value and
	// the layer it came from (DYNAMIC, VALUES, DEFAULT), as a named replica
	// resolves them, and the first replica that resolved other values.
	PlatformSettingFields *PlatformSettingFieldsFacts `json:"platform_setting_fields,omitempty"`
	// Load is the operating judgment the capacity panel opens with: on
	// time, backlog, loss, bottleneck, with the numbers each was read from
	// and the limits it holds under. Decided here, once, from the same view
	// the numbers under it come from.
	Load              Load   `json:"load"`
	ActivationReplica string `json:"activation_replica,omitempty"`
	// Rebalance is the leader's latest rebalance planning round, whole, and
	// RebalanceReplica which leader. On the verdict route because the
	// replica table under it shows the counts the round judged, and the
	// judgement has to be beside them.
	Rebalance        *RebalanceFacts `json:"rebalance,omitempty"`
	RebalanceReplica string          `json:"rebalance_replica,omitempty"`
	// AssignmentScope is the leader's last round's census of the content
	// scope on the Assignment records, and AssignmentScopeReplica which
	// leader: how far the content contract has reached the records, from
	// the read the round makes anyway.
	AssignmentScope        *AssignmentScopeFacts `json:"assignment_scope,omitempty"`
	AssignmentScopeReplica string                `json:"assignment_scope_replica,omitempty"`
	// AssignmentSweep is the leader's last sweep of the Assignment records
	// for retired Query Groups, beside the census that cannot see them.
	AssignmentSweep        *AssignmentSweepFacts `json:"assignment_sweep,omitempty"`
	AssignmentSweepReplica string                `json:"assignment_sweep_replica,omitempty"`
	// LeaderRound is the leader's last reconcile round, stage by stage, and
	// LeaderRoundReplica which leader.
	LeaderRound        *LeaderRoundFacts `json:"leader_round,omitempty"`
	LeaderRoundReplica string            `json:"leader_round_replica,omitempty"`
	// ViewStream is the Leader's account of the view stream, with its one
	// sentence for the first screen, and ViewStreamReplica which replica.
	ViewStream        *ViewStreamFacts `json:"view_stream,omitempty"`
	ViewStreamReplica string           `json:"view_stream_replica,omitempty"`
	// Source is what the leader's last round found at the strategy source,
	// and SourceReplica which leader. On the verdict route because Expected
	// is decided by it: an expected of 0 next to a source listing 81 is a
	// deployment holding everything at the configuration step, and a reader
	// with only the 0 concludes there is nothing to detect.
	Source        *SourceFacts `json:"source"`
	SourceReplica string       `json:"source_replica,omitempty"`
	// SourceStanding is Source read against what the deployment executes:
	// whether the cache can update the run, and the two sentences the first
	// screen shows for the run and for the cache. Absent without a round.
	SourceStanding *SourceStanding `json:"source_standing,omitempty"`
	// NoDataTracking is the fleet's one line on the no-data tracking horizon:
	// how many Plans decide against which kind of horizon and what their last
	// deciding rounds counted, summed over the counted replicas. Absent when
	// no replica reports a deciding round.
	NoDataTracking *NoDataTrackingSummary `json:"no_data_tracking,omitempty"`
	// Dependencies is where this deployment's external systems are and what
	// one replica has seen of them, DependenciesReplica which replica, and
	// DependenciesReplicas how many replicas published a list -- the one here
	// is one of that many, and each replica's own is on its per_replica row.
	Dependencies         []Endpoint `json:"dependencies"`
	DependenciesReplica  string     `json:"dependencies_replica,omitempty"`
	DependenciesReplicas int        `json:"dependencies_replicas"`
	// LinkdConsole is the alert link's Console read against whether the
	// deployment needs it; see LinkdConsoleStanding.
	LinkdConsole *LinkdConsoleStanding `json:"linkd_console,omitempty"`
	// ReplicasNotReady is how many counted replicas answer their own
	// readiness probe with no; which bit, on each per_replica row.
	ReplicasNotReady int `json:"replicas_not_ready"`
	// Overdue rides here rather than only in the list because the list can be
	// paged or truncated, and "how many objects are not being evaluated" must
	// not depend on how much of the list fitted.
	// Coverage says which kind of coverage disagreement this deployment has,
	// when the sets were available to compare. It is the difference between a
	// verdict a reader can act on and one that only says "do not trust this".
	Coverage *Disagreement `json:"coverage"`
	// Handover is the note beside the numbers read from the replicas' parts
	// while an object is held by more than one replica: absent when none is.
	Handover *HandoverMarker `json:"handover,omitempty"`
	// PerReplica breaks the deployment back down. Present on the verdict route
	// because that is where a reader lands first, and "which replica" is the
	// question the totals raise and cannot answer.
	PerReplica []ReplicaView `json:"per_replica"`
	Overdue    *OverdueFacts `json:"overdue"`
	// Dispatch says whether anything can be parked at all. Without it the zero
	// above is unreadable: a build that suppresses nothing reports the same zero
	// as one where every object is being reached on time.
	Dispatch *DispatchSuppression `json:"dispatch"`
	// Schedule is the census the first sentence of the page is built from:
	// waiting, late, overdue, never yet evaluated, and the on-time rates over
	// two windows. Absent when no replica has a due index, and the page then
	// says it cannot tell rather than saying "on time".
	Schedule *ScheduleCensus `json:"schedule"`
	Gaps     []Gap           `json:"gaps"`
	// Capacity rides on the verdict rather than getting an endpoint of its own:
	// the two are answers from one read, and splitting them would let a page
	// show a verdict from one moment beside occupancy from another.
	Capacity *CapacityView `json:"capacity"`
	// VerdictHistory is the last changes of the verdict this replica
	// decided, oldest first, each with the degradations, gaps and counts
	// that decided it; VerdictHistorySince is when the record starts, which
	// is this process's first verdict. See RecordVerdict.
	VerdictHistory      []VerdictChange `json:"verdict_history,omitempty"`
	VerdictHistorySince *time.Time      `json:"verdict_history_since,omitempty"`
	// VerdictHistoryReplica is the replica whose record VerdictHistory is.
	VerdictHistoryReplica string `json:"verdict_history_replica,omitempty"`
}

// Count is one value and how many anomalies carry it.
type Count struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Summary groups the anomalies this request is about.
//
// It exists because the list alone cannot answer the question a long list
// immediately raises: is this one problem repeated, or many separate ones. The
// counts are exact over the whole list rather than over the returned page --
// the service holds every anomaly before paging, so no extra read is needed and
// no partial evidence is presented as a whole. What the list itself cannot
// cover is still reported the same way: a truncated snapshot shows up in the
// gaps and in anomalies_total, and these counts inherit that limit.
type Summary struct {
	ByKind   Distribution `json:"by_kind"`
	ByReason Distribution `json:"by_reason"`
	// ByFailure is usually the most informative of the three. A completion kind
	// is shared by everything that ended badly, so counting it answers "how
	// many" and not "how many of what"; the failure classification separates one
	// broken dependency from a scattering of unrelated problems.
	ByFailure Distribution `json:"by_failure"`
	// ByFailureCode is the same classification one level down, and it is the
	// level the answer usually lives at: a category such as "evaluation" groups
	// conditions that call for opposite responses, and telling them apart from
	// the list means counting the codes by hand. It exists here, rather than
	// only in each anomaly's detail, because the code survives no longer than
	// the anomaly does -- once the population decays the question "which of
	// these was it" can no longer be answered at all, and that is exactly when
	// a self-healing fault is being investigated after the fact.
	//
	// It is not a metric label: the code set is open and would break the
	// cardinality bound in 07 section 9. Here it is bounded by the anomalies
	// actually present.
	ByFailureCode Distribution `json:"by_failure_code"`
	// ByFailureDetail is the level the answer usually stops at. A column of
	// objects sharing one code says how many; the symptom says what, and a
	// population that splits into "the backend returned 503" and "the connection
	// was refused" is two problems for two people rather than one number.
	ByFailureDetail Distribution `json:"by_failure_detail"`
	// ByCauseReason is the level below the cause and is where the answer to
	// "whose problem is this" lives. A column of objects sharing
	// LEVEL_OUTCOME_UNKNOWN is not one population: the contract requires that
	// cause to carry a reason of either the coverage class or the retryable
	// class, and those need opposite responses.
	ByCauseReason Distribution `json:"by_cause_reason"`
	// ByBusiness is the unit someone can act on. The rows are objects, which are
	// alarmd's own identities: an operator cannot look one up, cannot mention one
	// to the person who configured the strategy, and cannot tell from a list of
	// them whether this is one misconfiguration or fifty. Rolling the same
	// population up by business answers the question the list raises.
	ByBusiness Distribution `json:"by_business"`
	// Strategies is how many distinct strategies the rows cover. Fifty-five
	// objects over sixty-two strategies and over five strategies are the same
	// table and different conversations.
	Strategies int          `json:"strategies"`
	ByReplica  Distribution `json:"by_replica"`
	// Stalled counts the objects that are stuck rather than merely degraded. The
	// other three say how badly the last round went; this one says the rounds
	// stopped ending, which is the only one of the four that cannot resolve on
	// its own.
	Stalled int `json:"stalled"`
	// Partial says a replica truncated its published list, so these counts are
	// over a sample rather than over everything. They still answer "which of
	// these is it"; they no longer give a distribution, and the difference
	// matters because the reader who most needs the distribution is the one
	// looking at the deployment bad enough to have truncated.
	//
	// It is stated here rather than left to be inferred. The evidence was
	// already in the response -- anomalies_total against the list length -- and
	// an inference nobody makes is not a warning.
	Partial bool `json:"partial"`
	// Onset says when this population started, which the ordering cannot.
	Onset Onset `json:"onset"`
	// Ours and External split the list by whether capacity or design could have
	// prevented it. Ours is the one that decides the verdict; External is real
	// work that belongs to whoever owns the strategy or the data, and counting
	// them together is what made the verdict permanently DEGRADED.
	Ours     int `json:"ours"`
	External int `json:"external"`
	// Unattributed is the objects carrying no evidence either way, which is
	// neither of the other two and must not be folded into either.
	Unattributed int `json:"unattributed"`
	// OursUnclassified is how many of Ours got there by the fall-through. It is
	// the number that says the classification table has fallen behind what the
	// build emits, and it is the only signal for that: no test here can cover an
	// open input.
	OursUnclassified int `json:"ours_unclassified"`
	// WindowNeverFills is how many of External are there because their series
	// do not live long enough to fill the detection window, and have been that
	// way for longer than filling it could take.
	//
	// Counted separately from the rest of External because it is the only part
	// of this list with a settled answer. The others are open questions someone
	// still has to look into; these are working exactly as designed, will read
	// the same way tomorrow, and the design says an alert on them can be raised
	// and cannot clear itself. Leaving them in the general pile means every
	// reader re-investigates the same objects and reaches the same conclusion.
	WindowNeverFills int `json:"window_never_fills"`
	// WindowSeriesChurn is how many of WindowNeverFills are there because the
	// series themselves keep being replaced -- every short window this round
	// belonged to a series with no loaded history, and that has held for longer
	// than filling a window takes.
	//
	// A strict subset of WindowNeverFills and never shown beside it as a peer.
	// The rest of that count are long-lived series whose data is missing, and
	// the two are different work for different people: this half is a strategy
	// whose aggregation dimensions contain something that changes, the other
	// half is data that is not arriving. The page used to name the first as the
	// likely cause of all of them, which is an unmeasured guess pointed at a
	// population that contains both.
	WindowSeriesChurn int `json:"window_series_churn"`
}

// momentOrNil drops a zero time rather than sending it.
//
// encoding/json's omitempty has no effect on a struct, so a zero time.Time goes
// out as "0001-01-01T00:00:00Z" -- a string, and therefore true, to any reader
// that checks whether the field is there. Absence has to be its own value or
// every such check silently passes on the case it was written for.
func momentOrNil(when time.Time) *time.Time {
	if when.IsZero() {
		return nil
	}
	return &when
}

// Onset splits the list by how long ago each object went wrong.
//
// The list is ordered oldest-first so the longest-running objects stay visible,
// and that ordering puts whatever is happening right now at the very end. A
// deployment read while 45 of 83 objects had appeared within the last two hours
// showed none of them until page three: every row on the first two pages had
// been wrong for more than a day, and the page asserted in its own wording that
// the first page was the batch worth reading.
//
// Both readings are legitimate and one ordering cannot serve them, so the shape
// is stated before the rows: a reader asking "is something happening now" gets
// an answer without paging to the end to find it. It is counted over the whole
// filtered list, not the visible page, because a count taken from one page
// answers with whatever happened to be on screen.
type Onset struct {
	LastHour int `json:"last_hour"`
	LastDay  int `json:"last_day"`
	Older    int `json:"older"`
	// NewestSince is the most recent start time in the list. Zero when the list
	// is empty; a reader compares it against now to see whether the population
	// is still growing.
	NewestSince time.Time `json:"newest_since,omitempty"`
	// OldestSince is the other end, so the spread is readable without paging.
	// A population whose two ends are minutes apart started together, and then
	// the ordering between its rows carries no information at all -- which is
	// exactly the case the first page reads as a ranking.
	OldestSince time.Time `json:"oldest_since,omitempty"`
	// NewestFrom and OldestFrom say where those two timestamps came from, and
	// they are the difference between a moment and a bound.
	//
	// Without them the line over the table converted one into the other. A
	// filtered list of objects whose rows said, in the provenance column, that
	// the moment they went wrong was never recorded was summarised as "最新的一
	// 个是 2 小时 2 分前开始的" -- the roll-up asserting as a fact the one thing
	// every row underneath it had been careful not to claim.
	//
	// This is the shape that keeps recurring here: the field that discriminates
	// exists per row, is used per row, and is dropped on the way up.
	NewestFrom SinceSource `json:"newest_from,omitempty"`
	OldestFrom SinceSource `json:"oldest_from,omitempty"`
	// Bounded counts the rows in this list whose start is a bound rather than a
	// measurement, so the three buckets above can be read for how much of them
	// is knowable. A population that is mostly restored puts most of its rows in
	// whichever bucket the restart lands in, which says when the replica came
	// up and nothing about when anything went wrong.
	Bounded int `json:"bounded"`
}

func summarize(anomalies []Anomaly, at time.Time) Summary {
	kinds := map[string]int{}
	reasons := map[string]int{}
	failures := map[string]int{}
	codes := map[string]int{}
	details := map[string]int{}
	causeReasons := map[string]int{}
	businesses := map[string]int{}
	strategies := map[StrategyRef]struct{}{}
	replicas := map[string]int{}
	stalled := 0
	ours, external, unattributed, oursUnclassified := 0, 0, 0, 0
	neverFills := 0
	seriesChurn := 0
	onset := Onset{}
	for _, anomaly := range anomalies {
		if anomaly.Stalled {
			stalled++
		}
		if !anomaly.Since.IsZero() {
			age := at.Sub(anomaly.Since)
			switch {
			case age < time.Hour:
				onset.LastHour++
			case age < 24*time.Hour:
				onset.LastDay++
			default:
				onset.Older++
			}
			if onset.NewestSince.IsZero() || anomaly.Since.After(onset.NewestSince) {
				onset.NewestSince = anomaly.Since
				onset.NewestFrom = anomaly.SinceFrom
			}
			if onset.OldestSince.IsZero() || anomaly.Since.Before(onset.OldestSince) {
				onset.OldestSince = anomaly.Since
				onset.OldestFrom = anomaly.SinceFrom
			}
			// Carried up with the timestamps rather than left on the rows. The
			// sentence above the table read the newest of these back as a
			// moment, on rows whose own column said it is not one.
			if anomaly.SinceFrom.Bounded() {
				onset.Bounded++
			}
		}
		switch anomaly.Attribution {
		case AttributionExternal:
			external++
			// A subset of External, never a fourth column. It is external for
			// the same reason the rest are; what it adds is that this one has
			// stopped being a question.
			//
			// Gated on the reason as well as the counts. Persistent() reads
			// coverage alone, and coverage is reported for every object -- so
			// without this the count claimed any object whose windows had been
			// short for a while, and on a live deployment it claimed seven
			// while only two carried the reason it names. The other five were
			// HISTORY_GAPPED: data that arrived and then had holes, told back
			// to the reader as "this series does not live long enough to fill
			// its window", which is a different thing and sends them nowhere.
			if windowNeverFillsReason(anomaly.CauseReason) && anomaly.Coverage.Persistent() {
				neverFills++
				// A subset of a subset, and the only one of these with a named
				// owner. Persistent() says the shortfall will not resolve; it is
				// equally true of a strategy whose series identity churns and of
				// long-lived series whose data is missing, and the two are sent
				// to different people. Counting them together is how a summary
				// comes to recommend editing aggregation dimensions for objects
				// whose dimensions are fine.
				if anomaly.Coverage.Churning() {
					seriesChurn++
				}
			}
		case AttributionOurs:
			ours++
			if anomaly.Unclassified {
				oursUnclassified++
			}
		default:
			// AttributionUnknown, and anything that arrived without the field
			// set. Ours is named above rather than left as the default because
			// an unset field was landing in it: the demoted, undecidable and
			// by-design columns were served with attribution never filled, so
			// this line reported every one of their objects as the
			// deployment's own -- under a heading saying they are not.
			unattributed++
		}
		kinds[anomaly.Kind]++
		if anomaly.ReasonCode != "" {
			reasons[anomaly.ReasonCode]++
		}
		if anomaly.Failure != nil && anomaly.Failure.Category != "" {
			failures[anomaly.Failure.Category]++
		}
		if anomaly.Failure != nil && anomaly.Failure.Code != "" {
			codes[anomaly.Failure.Code]++
		}
		if anomaly.Failure != nil && anomaly.Failure.Detail != "" {
			details[anomaly.Failure.Detail]++
		}
		if anomaly.CauseReason != "" {
			causeReasons[anomaly.CauseReason]++
		}
		replicas[anomaly.Replica]++
		// Counted per object, not per reference: one object naming the same
		// business twice must not make that business look twice as affected.
		seenBusiness := map[string]struct{}{}
		for _, strategy := range anomaly.Strategies {
			strategies[strategy] = struct{}{}
			label := strategy.BusinessID
			if label == "" {
				label = "(未标业务)"
			}
			if _, dup := seenBusiness[label]; dup {
				continue
			}
			seenBusiness[label] = struct{}{}
			businesses[label]++
		}
	}
	return Summary{
		ByKind: rank(kinds), ByReason: rank(reasons),
		ByFailure: rank(failures), ByFailureCode: rank(codes), ByFailureDetail: rank(details), ByCauseReason: rank(causeReasons),
		ByBusiness: rank(businesses), Strategies: len(strategies),
		ByReplica: rank(replicas), Stalled: stalled, Onset: onset,
		Ours: ours, External: external, Unattributed: unattributed,
		OursUnclassified: oursUnclassified, WindowNeverFills: neverFills,
		WindowSeriesChurn: seriesChurn,
	}
}

// MarkStalled flags the objects whose rounds have not finished for longer than
// stallAfter, the deployment's own budget for terminating a Slot that cannot
// complete. Past that budget the object is not progressing slowly, it is not
// progressing at all, and nothing left in the deployment will end the round for
// it.
//
// The clock is FailingSince, the start of the current unbroken sequence of
// rounds that did not finish, and not Since, the start of the anomaly as a
// whole. Judging against Since flagged every object with a long degraded
// history the moment one round retried, and cleared it on the next degraded
// completion, so the count rose and fell with the last round's luck instead
// of saying which objects had stopped ending rounds. Completed rounds reset
// that clock because the round ended; blocked rounds reset it because they
// say a round never started, which an ordinary lease handover produces, and
// counting them would put a permanent label on a transient event. A zero
// FailingSince is therefore what an object whose last conclusive round ended
// looks like; it is also what a replica that does not report the field yet
// looks like, which under-reports during a rollout rather than over-reports.
// A zero budget turns the flag off rather than marking everything, so a
// deployment that has not wired one shows no flag instead of a wrong one.
func MarkStalled(anomalies []Anomaly, at time.Time, stallAfter time.Duration) {
	if stallAfter <= 0 {
		return
	}
	for index := range anomalies {
		failingSince := anomalies[index].FailingSince
		anomalies[index].Stalled = !failingSince.IsZero() && at.Sub(failingSince) > stallAfter
		if anomalies[index].Stalled {
			// Whatever this object's last reason code was, it has stopped
			// progressing, and nothing outside this deployment stops rounds from
			// ending or will start them again. The last code is usually the
			// external thing that happened just before it got stuck, so leaving
			// the finding alone would file a stalled object under someone
			// else's work -- and stalling is the one condition here that never
			// clears on its own. The whole finding is decided again, not the
			// attribution alone: rewriting one field left the other three
			// saying the backend's, and the page reads those.
			attribute(&anomalies[index], at)
		}
	}
}

// rank orders by count and then by value, so equal counts do not reorder
// between two reads of an unchanged deployment.
// MaxDistributionValues bounds how many groups a distribution ships.
//
// Some of these group by a vocabulary declared in code -- anomaly kinds,
// contract reason codes, failure categories -- and those are small and fixed.
// Others group by something sized by the installation: business or space,
// strategy, the backend's own failure code and symptom string. Those have no
// bound in this codebase at all, and on an install with tens of thousands of
// spaces the second kind would put one entry per space in every response, on a
// page that refreshes every few seconds.
//
// The bound clears the largest closed vocabulary here -- 55 contract reason
// codes -- so grouping by a vocabulary still ships whole, and grouping by an
// installation-sized set is cut with the remainder counted rather than
// silently dropped.
const MaxDistributionValues = 64

// Distribution is a grouped count whose size does not follow the deployment's.
//
// Distinct is the whole answer to the question the head cannot answer: five
// businesses with the objects spread over them and five thousand look nearly
// the same in a list of the top five, and they are completely different
// situations. It is counted before the cut, so it is right even when Top is not
// the whole story.
type Distribution struct {
	Top      []Count `json:"top"`
	Distinct int     `json:"distinct"`
	// TailObjects counts what the values past the cut hold between them, so the
	// head can be read as a share of the whole rather than as the whole.
	TailObjects int `json:"tail_objects,omitempty"`
}

// Total is how many objects this distribution accounts for, head and tail.
func (distribution Distribution) Total() int {
	total := distribution.TailObjects
	for _, count := range distribution.Top {
		total += count.Count
	}
	return total
}

func rank(counts map[string]int) Distribution {
	ranked := make([]Count, 0, len(counts))
	for value, count := range counts {
		ranked = append(ranked, Count{Value: value, Count: count})
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].Count == ranked[right].Count {
			return ranked[left].Value < ranked[right].Value
		}
		return ranked[left].Count > ranked[right].Count
	})
	distribution := Distribution{Distinct: len(ranked)}
	if len(ranked) > MaxDistributionValues {
		for _, count := range ranked[MaxDistributionValues:] {
			distribution.TailObjects += count.Count
		}
		ranked = ranked[:MaxDistributionValues]
	}
	distribution.Top = ranked
	return distribution
}

// DetailResponse is what one object's route answers with.
//
// Exported, unlike the other response bodies here, so the page's own tests can
// check that every field the page reads off it is a field this actually sends.
// Nothing else catches a name that does not match: it reads as undefined, the
// page renders a zero or a blank, and the response carried the right number the
// whole time.
type DetailResponse struct {
	Found   bool     `json:"found"`
	Anomaly *Anomaly `json:"anomaly,omitempty"`
	// Tracked is the answering replica's live row for an object it tracks
	// and does not list -- a healthy object, which no snapshot carries a row
	// for -- in the listed rows' shape with listed false. Absent when the
	// object has a listed row, which Anomaly and Facts carry.
	Tracked *Anomaly `json:"tracked,omitempty"`
	// LastQueryRanges is what the object's latest round's primary queries
	// were sent asking for, from the replica that answers - present when it
	// tracks the object, as the routed read's owner does. A grouped event
	// count that does not recover is read against its strategy's windows
	// here: it asks N + R periods and accepts one.
	LastQueryRanges *QueryRanges `json:"last_query_ranges,omitempty"`
	// TrackedBy is the replica whose latest snapshot lists the object as
	// owned, when the answering replica does not track it, and
	// TrackedByAsOf that snapshot's time. Best effort: right after the
	// object moves it can still name the old owner; read another pod, or use
	// the routed read, which goes by the lease.
	TrackedBy     string     `json:"tracked_by,omitempty"`
	TrackedByAsOf *time.Time `json:"tracked_by_as_of,omitempty"`
	// Anomaly is the first matching fact for old clients. Facts preserves
	// coexisting facts, capped by MaxPageSize; FactsTotal reports the full count.
	Facts      []Anomaly `json:"facts"`
	FactsTotal int       `json:"facts_total"`
	Check      Check     `json:"check,omitempty"`
	Group      string    `json:"group,omitempty"`
	// Existence is membership in the current authoritative active set, not
	// whether an observation or historical record happens to be retained.
	Existence string `json:"existence"`
	// Runtime says whether any fact about this object is held at all, under
	// whatever line -- it is the object's, not the context's. A request that
	// names a check the object is not under gets its facts under that check
	// (none), and ContextMatched false: the object was observed, the line the
	// reader clicked no longer holds it. Reading "not observed" there was a
	// false statement about an object with three facts on file.
	Runtime string `json:"runtime"`
	// ContextMatched is present only when a check was named: whether the
	// object has a fact under it. False with Runtime observed is a stale
	// click, not a quiet object.
	ContextMatched  *bool                  `json:"context_matched,omitempty"`
	RecordsStatus   string                 `json:"records_status"`
	RecordsScope    string                 `json:"records_scope,omitempty"`
	RecoveryContext *ObjectRecoveryContext `json:"recovery_context,omitempty"`
	// Health is the deployment's verdict, not the object's. On an object
	// proven absent it moves to DeploymentHealth: beside existence "absent"
	// a bare "health": "HEALTHY" read as the object being healthy.
	Health           Health `json:"health,omitempty"`
	DeploymentHealth Health `json:"deployment_health,omitempty"`
	Gaps             []Gap  `json:"gaps,omitempty"`
	Complete         bool   `json:"view_complete"`
	QueryGroup       string `json:"query_group"`
	// Records is what an observation window captured for this object, present
	// only when the caller asked for it. Its health travels with it so an empty
	// list can be read correctly: "nothing happened" and "nothing was recorded"
	// look identical and call for opposite next steps.
	Records      []json.RawMessage `json:"records,omitempty"`
	Diagnostics  *DiagnosticHealth `json:"diagnostics,omitempty"`
	RecordsError string            `json:"records_error,omitempty"`
	// RetentionSeconds is how long these records survive. It is sent rather than
	// written into the page, so the sentence the page puts under them cannot
	// outlive the constant it describes.
	RetentionSeconds int `json:"retention_seconds,omitempty"`
}

// ObjectRecoveryContext is aggregate evidence about the selected check/group.
// The publisher retains no object identities, so it cannot prove this QG recovered.
type ObjectRecoveryContext struct {
	Scope               string           `json:"scope"`
	ObjectRecoveryKnown bool             `json:"object_recovery_known"`
	Problem             RecoveredProblem `json:"problem"`
}

// NewHandler mounts the object API. The routes are deliberately few: a list,
// one object, the health judgment, the observation windows and the series
// curves. Anything beyond that needs a decision, not just a handler -- and
// the decisions taken so far are modes on those routes, not routes: the list
// answers scope=strategies (the strategy directory, whose
// include=effective_config carries the frozen Plan and, beside it, the output
// format frozen with it) and scope=cost (the cost candidates), the object
// answers samples= (criterion samples), and the windows take mode=sample.
// Each is recorded in the observability handoff
// contract, each is wrapped around this handler by the runtime rather than
// added here, and its memory grows under the runtime memory line rather than
// a share an operator allocates. A mode that is not in that
// contract is a sixth capability wearing a query parameter.
//
// Windows are the one place this API writes. The write is scoped to diagnostics
// -- it selects what gets recorded, never what gets evaluated -- and it is what
// keeps the choice of observed objects out of deployment configuration, where a
// choice made during one investigation outlives it and can only be changed by a
// release. A nil store leaves the route unmounted, so a deployment that has not
// wired one is missing the route rather than serving one that cannot work.
// stallAfter is how long an object's rounds may keep failing to finish before
// the API calls it stalled; it comes from the deployment's own replay budget so
// the flag means "past the point this deployment promised to end the round",
// not a number chosen here. Zero disables the flag.
// series is optional: a deployment that has not been told where its own metrics
// live cannot draw curves, and that must cost it the curves only -- the
// judgment and the object list are computed from the control plane and stay
// available either way.
func NewHandler(
	service *Service,
	windows *WindowStore,
	now func() time.Time,
	stallAfter time.Duration,
	series RangeProvider,
	diagnostics *DiagnosticStore,
	strategyLinkBase string,
) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("alarmd fleet: handler requires a service")
	}
	if now == nil {
		now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/objects", func(response http.ResponseWriter, request *http.Request) {
		listObjects(response, request, service, now, stallAfter)
	})
	mux.HandleFunc("/api/objects/", func(response http.ResponseWriter, request *http.Request) {
		objectDetail(response, request, service, now, stallAfter, diagnostics)
	})
	if windows != nil {
		mux.HandleFunc("/api/windows", func(response http.ResponseWriter, request *http.Request) {
			observationWindows(response, request, windows, now)
		})
	}
	if series != nil {
		mux.HandleFunc("/api/series", func(response http.ResponseWriter, request *http.Request) {
			seriesRange(response, request, series, now)
		})
	}
	mux.HandleFunc("/api/health", func(response http.ResponseWriter, request *http.Request) {
		// From the replicas' summaries: what the rows give is the parts they
		// add up to, and a snapshot is read only for a replica that published
		// no summary.
		view, part := service.Summarized(request.Context(), stallAfter)
		if request.Context().Err() != nil {
			// The caller has gone: there is nobody to answer, and what it
			// stopped waiting for is no verdict to record.
			return
		}
		at := now()
		service.RecordSummarizedVerdict(&view, part, at)
		history, since := service.VerdictHistory()
		health := healthOf(&view, part, at)
		health.StrategyLinkBase = strategyLinkBase
		health.VerdictHistory, health.VerdictHistorySince, health.VerdictHistoryReplica = history, momentOrNil(since), service.VerdictReplica()
		writeJSON(response, http.StatusOK, health)
	})
	return mux, nil
}

// healthOf is the health route's answer from a view of the replicas'
// summaries and the part their rows add up to (Summarized), at the moment
// of the read. The check lines and the to-do are the list route's, and
// this route answers neither.
func healthOf(view *View, part ReplicaPart, at time.Time) HealthResponse {
	return HealthResponse{
		Cohorts: cohortList(part.Cohorts(view.Schedule)), Cooling: part.CoolingAt(view.Schedule, at),
		Health: view.Health, Expected: view.Expected, Covered: view.Covered,
		Determined: view.Determined, Unknown: view.Unknown, Healthy: view.Healthy,
		AnomaliesTotal: view.AnomaliesTotal, DemotedTotal: view.DemotedTotal,
		UndecidableTotal: view.UndecidableTotal, ByDesignTotal: view.ByDesignTotal,
		EmptyEveryRoundTotal: view.EmptyEveryRoundTotal,
		Ours:                 part.Attribution.Ours,
		Unattributed:         part.Attribution.Unknown,
		Impact:               part.Impact.Impact(),
		DemotedDue:           view.DemotedDue, DemotedDueOldestSeconds: view.DemotedDueOldestSeconds,
		DemotionEntries:    view.DemotionEntries,
		DemotionExtensions: view.DemotionExtensions, DemotionExits: view.DemotionExits,
		DemotionRestored: view.DemotionRestored, DemotionHandovers: view.DemotionHandovers,
		DemotionReentries: view.DemotionReentries,
		LastDemotionExit:  momentOrNil(view.LastDemotionExit),
		PrunedSkips:       part.PrunedSkips, PrunedSkipsTotal: part.PrunedSkipsTotal,
		RetainedShare: part.RetainedShare, RetainedShareTotal: part.RetainedShareTotal,
		ReadEarly: part.ReadEarly, ReadEarlyTotal: part.ReadEarlyTotal,
		Coverage: view.Coverage, Handover: handoverOf(view.Coverage), PerReplica: view.PerReplica,
		PublishedVersion: view.PublishedVersion, Workers: view.Workers, Builds: view.Builds,
		OutputProtocols: outputProtocolList(view.OutputProtocols),
		Retentions:      retentionList(view.Retentions),
		OutputPath:      OutputPathOf(view),
		Degradations:    degradationList(view.Degradations),
		Activation:      view.Activation, ActivationReplica: view.ActivationReplica,
		NoDataHorizon: view.NoDataHorizon, PlatformSettingFields: view.PlatformSettingFields,
		Rebalance: view.Rebalance, RebalanceReplica: view.RebalanceReplica,
		AssignmentScope: view.AssignmentScope, AssignmentScopeReplica: view.AssignmentScopeReplica,
		AssignmentSweep: view.AssignmentSweep, AssignmentSweepReplica: view.AssignmentSweepReplica,
		LeaderRound: view.LeaderRound, LeaderRoundReplica: view.LeaderRoundReplica,
		ViewStream: view.ViewStream, ViewStreamReplica: view.ViewStreamReplica,
		Source: view.Source, SourceReplica: view.SourceReplica, SourceStanding: view.SourceStanding,
		NoDataTracking: view.NoDataTracking,
		Dependencies:   dependencyList(view.Dependencies), DependenciesReplica: view.DependenciesReplica,
		DependenciesReplicas: view.DependenciesReplicas, LinkdConsole: view.LinkdConsole, ReplicasNotReady: view.ReplicasNotReady,
		Overdue: view.Overdue, Dispatch: view.Dispatch, Schedule: view.Schedule,
		Gaps: view.Gaps, Capacity: view.Capacity,
		Load: part.Load(view),
	}
}

// HandoverMarker is the note beside the numbers the health route reads from
// the replicas' parts while an object is held by more than one replica.
// Each holder's part counts it, so the counts may run high for that moment,
// and a skip the old holder records while the new one has pooled the object
// reads as a loss in progress (ReplicaPart). Objects is how many are held
// by several; null when some replica's owned set was not read whole, which
// says it may be happening without saying how much. The catalogue plays no
// part: whether an object is held twice is the owned sets' alone. The page
// words it.
type HandoverMarker struct {
	Objects *int `json:"objects"`
}

// handoverOf is the marker the coverage calls for, or nil when no object is
// held by several replicas.
func handoverOf(coverage *Disagreement) *HandoverMarker {
	switch {
	case coverage == nil:
		return nil
	case !coverage.setsWhole:
		return &HandoverMarker{}
	case coverage.HeldBySeveralTotal > 0:
		objects := coverage.HeldBySeveralTotal
		return &HandoverMarker{Objects: &objects}
	}
	return nil
}

// cohortList is the cohorts as an empty list rather than null: no period seen
// anywhere is [] and is not the same statement as null.
func cohortList(cohorts []CohortView) []CohortView {
	if cohorts == nil {
		return []CohortView{}
	}
	return cohorts
}

// outputProtocolList is the view's protocol groups as an empty list rather
// than null, for the same reason as degradationList: no counted replica is
// [] and is not the same statement as null.
func outputProtocolList(groups []OutputProtocolGroup) []OutputProtocolGroup {
	if groups == nil {
		return []OutputProtocolGroup{}
	}
	return groups
}

// retentionList is the view's retention groups as an empty list rather than
// null, as outputProtocolList is.
func retentionList(groups []RetentionGroup) []RetentionGroup {
	if groups == nil {
		return []RetentionGroup{}
	}
	return groups
}

// degradationList is the view's degradations as an empty list rather than
// null: the page iterates it, and null and [] are two different statements.
func degradationList(degradations []Degradation) []Degradation {
	if degradations == nil {
		return []Degradation{}
	}
	return degradations
}

// dependencyList sends an empty list rather than null for a build that
// published no dependencies: the page then says "not reported" once, instead
// of failing on the field.
func dependencyList(dependencies []Endpoint) []Endpoint {
	if dependencies == nil {
		return []Endpoint{}
	}
	return dependencies
}

func listObjects(response http.ResponseWriter, request *http.Request, service *Service,
	now func() time.Time, stallAfter time.Duration) {
	offset, limit, err := paging(request)
	if err != nil {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Which column this request is about. It is a parameter rather than a second
	// route because the two lists are two answers from one read: served apart,
	// a reader could hold a pool from one moment beside anomalies from another
	// and find objects in both, or in neither.
	rawColumn := request.URL.Query().Get("column")
	column := rawColumn
	if column != "" && !knownColumn(column) {
		writeJSON(response, http.StatusBadRequest, map[string]string{
			"error": "column must be one of " + strings.Join(ObjectColumns, ", ")})
		return
	}
	if column == "" {
		column = ColumnAnomalies
	}
	// Refused rather than defaulted, for the same reason an unknown column is:
	// a typo that silently falls back returns the other ordering under the
	// heading the caller asked for, and the two orderings put opposite ends of
	// the population on the first page.
	order := request.URL.Query().Get("order")
	if order != "" && order != OrderOldest && order != OrderNewest {
		writeJSON(response, http.StatusBadRequest,
			map[string]string{"error": "order must be " + OrderOldest + " or " + OrderNewest})
		return
	}
	if order == "" {
		order = OrderOldest
	}
	// Held on the memory line until this page's answer is written: the
	// snapshots and the view built of them are garbage then.
	pageCtx, release := withPageHolds(request.Context())
	defer release()
	view := service.View(pageCtx)
	// Marked and settled before filtering, so a filtered response reports the
	// same flag for the same object as an unfiltered one, and before a column
	// is served, always on the anomaly list. Settling used to run after the
	// swap below, so asking for the demoted pool recomputed the deployment's
	// verdict and its per-replica breakdown over the pool instead -- and the
	// response carries both. Which list a reader is paging cannot be allowed
	// to change what the deployment's health is.
	at := now()
	Decide(&view, at, stallAfter)
	// The first screen, from every column before any of them is swapped in as
	// the rows. Drawn here so the line a reader clicks and the rows it opens
	// come from one read of the view -- and by the same call the metric
	// collector makes, so the line and the series agree.
	screen := Report(&view, at)
	columns, truncated, checks, todo := screen.Columns, screen.Truncated, screen.Checks, screen.Todo
	// Counted over every column for the same reason it survives a filter: these
	// are the objects that will not recover on their own, and a number that
	// shrinks because of what the reader is currently looking at reads as "there
	// is nothing to do here".
	stalledTotal := 0
	for _, list := range columns {
		for _, anomaly := range list {
			if anomaly.Stalled {
				stalledTotal++
			}
		}
	}
	// Serving a column replaces the rows this request is about, and nothing
	// else. The deployment-wide counts on the view are untouched, so the
	// response still carries every total and a reader paging one column can see
	// how many objects are not in it. On a bad enough deployment the list this
	// summary counts is already a sample; the counts stay useful for "which of
	// these is it" and stop being usable as a distribution, and the response
	// says so rather than leaving it to a reader comparing two other fields.
	summaryPartial := truncated[column]
	// A check is a line on the first screen, and the rows it opens come from
	// every column: the check decides membership, not the column. Its total is
	// how many objects it holds, and a group within it narrows to one fold. This
	// is navigation, not a filter -- the response does not say a filter was
	// applied, because the reader did not ask for a narrowing of anything; they
	// opened a line.
	check := Check(request.URL.Query().Get("check"))
	group := request.URL.Query().Get("group")
	// The period a cohort number was counted over, so the number and its
	// objects are one click apart. Refused when it is not a number: a typo
	// that fell back would return the whole list under a cohort heading.
	var interval *int64
	if raw := request.URL.Query().Get("interval"); raw != "" {
		seconds, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || seconds < 0 {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "interval must be a period in seconds, 0 for objects whose period is not known"})
			return
		}
		interval = &seconds
	}
	switch {
	case check != "":
		if !knownCheck(string(check)) {
			writeJSON(response, http.StatusBadRequest,
				map[string]string{"error": "check must be one of " + strings.Join(checkNames(), ", ")})
			return
		}
		view.Anomalies = UnderCheck(check, group, &view, at)
		view.AnomaliesTotal = len(view.Anomalies)
		summaryPartial = truncated[ColumnAnomalies] || truncated[ColumnDemoted] ||
			truncated[ColumnUndecidable] || truncated[ColumnByDesign]
		column = ""
	case interval != nil && rawColumn == "":
		// Opening a cohort is navigation like opening a check: the cohort
		// was counted over every column, so its rows come from every column.
		// On the live deployment the four fifteen-second objects the join was
		// built for were all in the demoted pool, and interval=15 on the
		// anomaly column alone answered zero rows under a cohort that said
		// four. A column named beside the interval narrows to that column.
		rows := []Anomaly{}
		for _, list := range columns {
			rows = append(rows, list...)
		}
		view.Anomalies = rows
		view.AnomaliesTotal = len(rows)
		summaryPartial = truncated[ColumnAnomalies] || truncated[ColumnDemoted] ||
			truncated[ColumnUndecidable] || truncated[ColumnByDesign]
		column = ""
	case column == ColumnDemoted:
		view.Anomalies = view.Demoted
		view.AnomaliesTotal = view.DemotedTotal
	case column == ColumnUndecidable:
		view.Anomalies = view.Undecidable
		view.AnomaliesTotal = view.UndecidableTotal
	case column == ColumnByDesign:
		view.Anomalies = view.ByDesign
		view.AnomaliesTotal = view.ByDesignTotal
	}
	replica := request.URL.Query().Get("replica")
	if replica != "" {
		// A name that belongs to no replica has to be refused rather than
		// answered. The coverage arithmetic stays deployment-scoped on purpose,
		// so filtering by a typo would otherwise return an empty anomaly list
		// beside a full-coverage HEALTHY verdict -- a green tile for a replica
		// that does not exist.
		if !knownReplica(view, replica) {
			writeJSON(response, http.StatusBadRequest,
				map[string]string{"error": "no replica named " + replica + " is part of this deployment"})
			return
		}
		view.Anomalies = filterByReplica(view.Anomalies, replica)
	}
	// Strategy and business are not refused when nothing matches, unlike an
	// unknown replica: the deployment's replicas are a short knowable list, while
	// a strategy that simply has no anomalies right now is the ordinary answer to
	// a reasonable question. The response says a filter was applied so an empty
	// table is not read as "nothing is wrong anywhere".
	strategy := request.URL.Query().Get("strategy")
	if strategy != "" {
		view.Anomalies = filterByStrategy(view.Anomalies, strategy)
	}
	business := request.URL.Query().Get("business")
	if business != "" {
		view.Anomalies = filterByBusiness(view.Anomalies, business)
	}
	if interval != nil {
		view.Anomalies = filterByInterval(view.Anomalies, *interval)
	}
	total := len(view.Anomalies)
	// Counted over the whole list this request is about, before it is cut into a
	// page. A reader's first question is whether a long list is one problem or
	// many, and counting only the visible page would answer it with whatever
	// happened to be on screen.
	summary := summarize(view.Anomalies, at)
	summary.Partial = summaryPartial
	// Ordered after filtering and before paging, so page two of a newest-first
	// read continues page one rather than resorting a slice of the list.
	if order == OrderNewest {
		SortAnomaliesNewestFirst(view.Anomalies)
	}
	view.Anomalies = pageOf(view.Anomalies, offset, limit)
	// The rows this request is not about are not sent. The served column is
	// paged; the other three, the no-data list and the retained records
	// were shipped whole under it on every response -- measured at 2074
	// objects with 350 in the pool: 177 KB of demoted rows and 41 KB of
	// records under a 50-row page, on a request the page makes every
	// thirty seconds for the lines and the arithmetic alone. The totals,
	// the lines and the arithmetic were all counted above from the whole
	// view and stay; a reader who wants the rows of another column asks
	// for that column, and gets them paged.
	view.Demoted, view.Undecidable, view.ByDesign, view.NoData, view.NoDataMemory = []Anomaly{}, []Anomaly{}, []Anomaly{}, []Anomaly{}, []Anomaly{}
	view.RetainedShare, view.ReadEarly, view.ReadHeld, view.LateSeries = []Anomaly{}, []Anomaly{}, []Anomaly{}, []Anomaly{}
	view.GapSkips, view.PrunedSkips = map[string]SkippedSpan{}, map[string]PrunedSkip{}
	// Each replica's dependency record is the verdict route's; here it would
	// ride on every thirty-second poll for rows this request is not about.
	// The copy is this request's own, so clearing it touches no other reader.
	for index := range view.PerReplica {
		view.PerReplica[index].Dependencies = nil
	}
	writeJSON(response, http.StatusOK, ListResponse{
		Summary: summary,
		View:    view, Replica: replica, Strategy: strategy, Business: business, Column: column,
		Interval:          interval,
		Applied:           replica != "" || strategy != "" || business != "" || interval != nil,
		StallAfterSeconds: int(stallAfter / time.Second),
		StalledTotal:      stalledTotal,
		Checks:            checks, Check: check, Group: group, Todo: todo,
		Cohorts: cohortList(Cohorts(&view, columns)), Cooling: Cooling(&view, columns, at),
		Order: order,
		Page:  Page{Offset: offset, Limit: limit, Total: total},
	})
}

// objectDetail answers for one object, optionally including what an observation
// window recorded for it.
//
// The records ride here rather than on an endpoint of their own because the API
// is capped at five routes (see NewHandler): the cap exists so this page cannot
// grow into a service that needs maintaining, and "read what my window produced"
// is part of looking at one object, not a sixth thing. They are opt-in so a
// reader who did not ask does not pay for them, and they outlive the window that
// produced them -- an investigation does not end when the window expires.
func objectDetail(response http.ResponseWriter, request *http.Request, service *Service,
	now func() time.Time, stallAfter time.Duration, diagnostics *DiagnosticStore) {
	queryGroup := strings.TrimPrefix(request.URL.Path, "/api/objects/")
	if queryGroup == "" {
		writeJSON(response, http.StatusBadRequest, map[string]string{"error": "query group is required"})
		return
	}
	check := Check(request.URL.Query().Get("check"))
	group := request.URL.Query().Get("group")
	if (check != "" && !knownCheck(string(check))) || (check == "" && group != "") {
		writeJSON(response, http.StatusBadRequest, map[string]string{
			"error": "check must be one of " + strings.Join(checkNames(), ", ") + "; group requires check"})
		return
	}
	records, health, recordErr := objectRecords(request, queryGroup, diagnostics)
	// Held on the memory line until this page's answer is written: the
	// snapshots and the view built of them are garbage then.
	pageCtx, release := withPageHolds(request.Context())
	defer release()
	view := service.View(pageCtx)
	at := now()
	Decide(&view, at, stallAfter)
	body := DetailResponse{
		Records: records, Diagnostics: health, RecordsError: recordErr,
		Check: check, Group: group, Facts: []Anomaly{},
		Existence: objectExistence(queryGroup, view.expectation), Runtime: "not_observed",
		RecordsStatus: "not_requested",
		Health:        view.Health,
		Gaps:          view.Gaps,
		Complete:      view.Health != HealthUnknown,
		QueryGroup:    queryGroup,
	}
	if health != nil {
		body.RetentionSeconds = int(DiagnosticRetention / time.Second)
	}
	if request.URL.Query().Get("records") != "" {
		body.RecordsScope = "historical"
		switch {
		case recordErr != "":
			body.RecordsStatus = "unavailable"
		case len(records) > 0:
			body.RecordsStatus = "available"
		default:
			body.RecordsStatus = "empty"
		}
	}
	walkObjectRows(check, group, queryGroup, &view, at, func(row Anomaly) {
		body.FactsTotal++
		if len(body.Facts) < MaxPageSize {
			body.Facts = append(body.Facts, row)
		}
	})
	if len(body.Facts) > 0 {
		body.Anomaly = &body.Facts[0]
	}
	if ranges, known := service.LocalQueryRanges(queryGroup); known {
		body.LastQueryRanges = &ranges
	}
	// A healthy object has a row in no snapshot. The replica that tracks it
	// builds one on request; another replica says which one does.
	if len(body.Facts) == 0 && check == "" {
		if row, tracked := service.LocalRow(queryGroup); tracked {
			body.Tracked = &row
		} else if owner, at, owned := view.OwnerOf(queryGroup); owned {
			body.TrackedBy = owner
			if !at.IsZero() {
				asOf := at
				body.TrackedByAsOf = &asOf
			}
		}
	}
	// Whether the object is observed is asked of every fact, not of the ones
	// under the named check: a check the object has left still leaves the
	// object observed, and the context says it did not match.
	observed := body.FactsTotal > 0
	if check != "" {
		matched := observed
		body.ContextMatched = &matched
		if !observed {
			walkObjectRows("", "", queryGroup, &view, at, func(Anomaly) { observed = true })
		}
	}
	if observed {
		body.Runtime = "observed"
	}
	if check != "" && group != "" {
		for _, problem := range view.Recovered {
			if problem.Check == check && problem.Key == group {
				body.RecoveryContext = &ObjectRecoveryContext{Scope: "check_group", Problem: problem}
				break
			}
		}
	}
	body.Found = body.FactsTotal > 0 || body.Existence == "active"
	if body.Found || len(records) > 0 {
		writeJSON(response, http.StatusOK, body)
		return
	}
	if body.Existence == "unknown" {
		writeJSON(response, http.StatusServiceUnavailable, body)
		return
	}
	// Proven absent: the verdict stays, named as the deployment's.
	body.DeploymentHealth, body.Health = body.Health, ""
	writeJSON(response, http.StatusNotFound, body)
}

func objectExistence(queryGroup string, expectation Expectation) string {
	if !expectation.Known {
		return "unknown"
	}
	for _, id := range expectation.IDs {
		if id == queryGroup {
			return "active"
		}
	}
	// Older sources can publish a count without IDs. A count is insufficient
	// to prove this particular object absent, even with complete observations.
	if len(expectation.IDs) != expectation.QueryGroups {
		return "unknown"
	}
	return "absent"
}

func paging(request *http.Request) (int, int, error) {
	query := request.URL.Query()
	offset := 0
	limit := DefaultPageSize
	if raw := query.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
		offset = parsed
	}
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return 0, 0, errors.New("limit must be a positive integer")
		}
		limit = parsed
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	return offset, limit, nil
}

func pageOf(anomalies []Anomaly, offset, limit int) []Anomaly {
	if offset >= len(anomalies) {
		return []Anomaly{}
	}
	end := offset + limit
	if end > len(anomalies) {
		end = len(anomalies)
	}
	return anomalies[offset:end]
}

// windowRequest opens or closes windows. Close is a separate verb rather than a
// zero TTL, because "observe this for no time" is not a thing anyone means.
type windowRequest struct {
	QueryGroups []string `json:"query_groups"`
	OpenedBy    string   `json:"opened_by"`
	TTLSeconds  int      `json:"ttl_seconds"`
	Close       bool     `json:"close"`
}

func observationWindows(response http.ResponseWriter, request *http.Request, windows *WindowStore, now func() time.Time) {
	switch request.Method {
	case http.MethodGet:
		open, err := windows.Load(request.Context(), now())
		if err != nil {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"error": "window store unavailable"})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"windows": open, "max_open": MaxOpenWindows, "max_ttl_seconds": int(MaxWindowTTL.Seconds())})
	case http.MethodPost:
		var body windowRequest
		if err := json.NewDecoder(http.MaxBytesReader(response, request.Body, 64<<10)).Decode(&body); err != nil {
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": "body must be a window request"})
			return
		}
		at := now()
		var (
			open []Window
			err  error
		)
		if body.Close {
			open, err = windows.Close(request.Context(), body.QueryGroups, at)
		} else {
			open, err = windows.Open(request.Context(), body.QueryGroups, body.OpenedBy,
				time.Duration(body.TTLSeconds)*time.Second, at)
		}
		if err != nil {
			// The rejections here are all about what the caller asked for --
			// an unknown identity, too many objects, too long a window -- so
			// the reason is the caller's to see. A store failure surfaces as
			// the same message, which is the one case worth improving later.
			writeJSON(response, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"windows": open, "max_open": MaxOpenWindows, "max_ttl_seconds": int(MaxWindowTTL.Seconds())})
	default:
		writeJSON(response, http.StatusMethodNotAllowed, map[string]string{"error": "GET to read windows, POST to open or close one"})
	}
}

// knownReplica reports whether the deployment contains a replica by this name.
// A replica that published nothing is still part of the deployment: it is named
// by the gap that says so, and asking about it is a legitimate question with the
// answer "it reported nothing".
func knownReplica(view View, replica string) bool {
	for _, contributor := range view.Replicas {
		if contributor == replica {
			return true
		}
	}
	for _, gap := range view.Gaps {
		if gap.Replica == replica {
			return true
		}
	}
	return false
}

// filterByStrategy keeps objects serving the given strategy. One object can
// serve several, so a match on any of them keeps it.
func filterByStrategy(anomalies []Anomaly, strategyID string) []Anomaly {
	return filterByStrategyField(anomalies, func(s StrategyRef) bool { return s.StrategyID == strategyID })
}

// filterByBusiness keeps objects serving any strategy of the given business.
func filterByBusiness(anomalies []Anomaly, businessID string) []Anomaly {
	return filterByStrategyField(anomalies, func(s StrategyRef) bool { return s.BusinessID == businessID })
}

func filterByStrategyField(anomalies []Anomaly, match func(StrategyRef) bool) []Anomaly {
	filtered := make([]Anomaly, 0, len(anomalies))
	for _, anomaly := range anomalies {
		for _, strategy := range anomaly.Strategies {
			if match(strategy) {
				filtered = append(filtered, anomaly)
				break
			}
		}
	}
	return filtered
}

func filterByReplica(anomalies []Anomaly, replica string) []Anomaly {
	filtered := make([]Anomaly, 0, len(anomalies))
	for _, anomaly := range anomalies {
		if anomaly.Replica == replica {
			filtered = append(filtered, anomaly)
		}
	}
	return filtered
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(body)
}

func seriesRange(
	response http.ResponseWriter,
	request *http.Request,
	provider RangeProvider,
	now func() time.Time,
) {
	// The window is chosen from a fixed list rather than sent as a duration: a
	// page that can name its own range can also name one nobody budgeted for.
	requested := request.URL.Query().Get("window")
	window, ok := lookupSeriesWindow(requested)
	if !ok {
		writeJSON(response, http.StatusBadRequest,
			map[string]string{"error": "unknown window " + requested})
		return
	}
	at := now()
	writeJSON(response, http.StatusOK, seriesResponse{
		Window: window.Key, WindowChoice: seriesWindowKeys(),
		StartUnixMs: at.Add(-window.Duration).UnixMilli(), EndUnixMs: at.UnixMilli(),
		StepSeconds: int(window.Step / time.Second),
		Series:      collectSeries(request.Context(), provider, window, at),
	})
}

func seriesWindowKeys() []string {
	keys := make([]string, 0, len(seriesWindows))
	for _, window := range seriesWindows {
		keys = append(keys, window.Key)
	}
	return keys
}

// objectRecords reads what a window recorded, when the caller asked for it.
//
// A failure here is reported beside the object rather than replacing it: the
// object's own state comes from the control plane and stays answerable whether
// or not the diagnostic store is reachable.
func objectRecords(request *http.Request, queryGroup string, store *DiagnosticStore) (
	[]json.RawMessage, *DiagnosticHealth, string,
) {
	raw := request.URL.Query().Get("records")
	if raw == "" {
		return nil, nil, ""
	}
	if store == nil {
		// Saying so beats an empty list: "not wired here" and "nothing was
		// recorded" call for different next steps.
		return nil, nil, "diagnostic records are not wired in this deployment"
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return nil, nil, "records must be a positive integer"
	}
	records, err := store.Load(request.Context(), queryGroup, limit)
	health := store.Health()
	if err != nil {
		// Classified rather than passed through: the dependency error carries
		// the store's address, and anyone who can reach this port would learn it.
		return nil, &health, "diagnostic records are unavailable"
	}
	return records, &health, ""
}

// RetainedShareRef is one object near its share of the retained pool, as the
// health response carries it: which object, whose strategies, and how close.
type RetainedShareRef struct {
	QueryGroup     string        `json:"query_group"`
	Strategies     []StrategyRef `json:"strategies,omitempty"`
	Replica        string        `json:"replica,omitempty"`
	RetainedBytes  uint64        `json:"retained_bytes"`
	ShareBytes     uint64        `json:"share_bytes"`
	PercentOfShare uint64        `json:"percent_of_share"`
	Since          time.Time     `json:"since"`
	// By phase, so a reader of this response can tell a share filled by the
	// strategy's retention (state) from one filled by what this build holds
	// per round (output), which call for different people.
	RetainedInputBytes  uint64 `json:"retained_input_bytes"`
	RetainedStateBytes  uint64 `json:"retained_state_bytes"`
	RetainedOutputBytes uint64 `json:"retained_output_bytes"`
	RetainedGapBytes    uint64 `json:"retained_gap_bytes"`
	ThresholdPercent    uint64 `json:"threshold_percent"`
}

// retainedShareList orders the objects fullest first, across replicas.
func retainedShareList(rows []Anomaly) []RetainedShareRef {
	if len(rows) == 0 {
		return nil
	}
	list := make([]RetainedShareRef, 0, len(rows))
	for _, row := range rows {
		facts := row.RetainedShare
		if facts == nil {
			continue
		}
		list = append(list, RetainedShareRef{
			QueryGroup: row.QueryGroup, Strategies: row.Strategies, Replica: row.Replica,
			RetainedBytes: facts.RetainedBytes, ShareBytes: facts.ShareBytes,
			PercentOfShare: facts.PercentOfShare, Since: facts.Since,
			RetainedInputBytes: facts.RetainedInputBytes, RetainedStateBytes: facts.RetainedStateBytes,
			RetainedOutputBytes: facts.RetainedOutputBytes, RetainedGapBytes: facts.RetainedGapBytes,
			ThresholdPercent: facts.ThresholdPercent,
		})
	}
	list = latestPerObject(list, func(ref RetainedShareRef) (string, time.Time) { return ref.QueryGroup, ref.Since }, retainedShareBefore)
	sort.Slice(list, func(left, right int) bool { return retainedShareBefore(list[left], list[right]) })
	return list
}

// retainedShareBefore is the list's order, the one each replica's own list
// is in (RetainedShareAnomalies): fullest first, then by object, then -- for
// the two entries of one object during a handover -- by replica.
func retainedShareBefore(left, right RetainedShareRef) bool {
	if left.PercentOfShare != right.PercentOfShare {
		return left.PercentOfShare > right.PercentOfShare
	}
	if left.QueryGroup != right.QueryGroup {
		return left.QueryGroup < right.QueryGroup
	}
	return left.Replica < right.Replica
}

// ReadEarlyRef is one object read before its data was complete, as the
// health response carries it: which object, whose strategies, and the
// time_delay it runs under beside the one that would have read it complete.
type ReadEarlyRef struct {
	QueryGroup            string        `json:"query_group"`
	Strategies            []StrategyRef `json:"strategies,omitempty"`
	Replica               string        `json:"replica,omitempty"`
	CurrentDelaySeconds   int64         `json:"current_time_delay_seconds"`
	SuggestedDelaySeconds int64         `json:"suggested_time_delay_seconds"`
	Since                 time.Time     `json:"since"`
}

// readEarlyList orders the objects furthest from their suggestion first,
// across replicas.
func readEarlyList(rows []Anomaly) []ReadEarlyRef {
	if len(rows) == 0 {
		return nil
	}
	list := make([]ReadEarlyRef, 0, len(rows))
	for _, row := range rows {
		if facts := row.ReadEarly; facts != nil {
			list = append(list, ReadEarlyRef{QueryGroup: row.QueryGroup, Strategies: row.Strategies, Replica: row.Replica,
				CurrentDelaySeconds: facts.CurrentDelaySeconds, SuggestedDelaySeconds: facts.SuggestedDelaySeconds, Since: facts.Since})
		}
	}
	list = latestPerObject(list, func(ref ReadEarlyRef) (string, time.Time) { return ref.QueryGroup, ref.Since }, readEarlyBefore)
	sort.Slice(list, func(left, right int) bool { return readEarlyBefore(list[left], list[right]) })
	return list
}

// readEarlyBefore is the list's order, the one each replica's own list is
// in (ReadEarlyAnomalies): the furthest from its suggestion first, then by
// object, then -- for the two entries of one object during a handover -- by
// replica.
func readEarlyBefore(left, right ReadEarlyRef) bool {
	if lg, rg := left.SuggestedDelaySeconds-left.CurrentDelaySeconds, right.SuggestedDelaySeconds-right.CurrentDelaySeconds; lg != rg {
		return lg > rg
	}
	if left.QueryGroup != right.QueryGroup {
		return left.QueryGroup < right.QueryGroup
	}
	return left.Replica < right.Replica
}

// PrunedSkipRef is one object's lost span, as the page receives it.
type PrunedSkipRef struct {
	QueryGroup string `json:"query_group"`
	// SpanSeconds is how long the span covers. The number of Slots inside it is
	// not reported because it is not knowable: the segments that would have
	// counted them are the segments that were pruned. A count would have to be
	// invented, and an invented one reads exactly like a measured one.
	SpanSeconds   int64     `json:"span_seconds"`
	At            time.Time `json:"at"`
	DiscardedSlot int64     `json:"discarded_slot,omitempty"`
	// Reason is the word the span was lost under (PrunedSkip.Code): a pruned
	// timeline, or one that would not decode and was rewritten at
	// RewrittenAt.
	Reason      string     `json:"reason,omitempty"`
	RewrittenAt *time.Time `json:"rewritten_at,omitempty"`
}

// prunedSkipList orders the lost spans longest first, because the length of the
// span is how much detection was lost and is the only thing here that ranks.
func prunedSkipList(skips map[string]PrunedSkip) []PrunedSkipRef {
	if len(skips) == 0 {
		return nil
	}
	list := make([]PrunedSkipRef, 0, len(skips))
	for queryGroup, skip := range skips {
		list = append(list, PrunedSkipRef{
			QueryGroup: queryGroup, SpanSeconds: int64(skip.Spanning() / time.Second),
			At: skip.At, DiscardedSlot: skip.DiscardedSlot, Reason: skip.Code(), RewrittenAt: skip.RewrittenAt,
		})
	}
	sort.Slice(list, func(left, right int) bool { return prunedSkipBefore(list[left], list[right]) })
	return list
}

// prunedSkipBefore is the list's order: the longest span first, then by
// object, then -- for the two entries of one object during a handover -- by
// the Slot discarded.
func prunedSkipBefore(left, right PrunedSkipRef) bool {
	if left.SpanSeconds != right.SpanSeconds {
		return left.SpanSeconds > right.SpanSeconds
	}
	if left.QueryGroup != right.QueryGroup {
		return left.QueryGroup < right.QueryGroup
	}
	return left.DiscardedSlot < right.DiscardedSlot
}

// latestPerObject keeps one entry per object, the later one, as the view
// keeps one record of an object's pruned span: during a handover two replicas
// can list the same object, and the list names objects, not replicas' rows.
// Of two at the same moment it keeps the one first in the list's order, so
// which is kept does not depend on the order the replicas' entries came in.
func latestPerObject[T any](list []T, keyOf func(T) (string, time.Time), before func(T, T) bool) []T {
	kept := make(map[string]int, len(list))
	out := make([]T, 0, len(list))
	for _, entry := range list {
		key, at := keyOf(entry)
		if index, seen := kept[key]; seen {
			if _, kept := keyOf(out[index]); at.After(kept) || (at.Equal(kept) && before(entry, out[index])) {
				out[index] = entry
			}
			continue
		}
		kept[key] = len(out)
		out = append(out, entry)
	}
	return out
}

// FirstScreenListBound is how many objects each of the health route's
// object lists carries: the first screen reads the first of one of them,
// and the objects route lists every one.
const FirstScreenListBound = 8

// firstScreenList is the first FirstScreenListBound of a list in its order.
func firstScreenList[T any](list []T) []T {
	if len(list) > FirstScreenListBound {
		return list[:FirstScreenListBound]
	}
	return list
}
