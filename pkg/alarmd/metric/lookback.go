// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// lookbackCollector reads the late-data lookback at scrape time. A process
// that does not run it emits nothing: its families are registered, and a
// reader tells "not running" from "ran and saw nothing" by whether any
// series exists. A process that runs it emits every cell from the start.
type lookbackCollector struct {
	mu         sync.Mutex
	source     func() lookback.Stats
	firstReads *prometheus.Desc
	checks     *prometheus.Desc
	changed    *prometheus.Desc
	changes    *prometheus.Desc
	classes    *prometheus.Desc
	readEarly  *prometheus.Desc
	seriesLate *prometheus.Desc
	readBytes  *prometheus.Desc
	checkBytes *prometheus.Desc
	unknown    *prometheus.Desc
	coverage   *prometheus.Desc
	pending    *prometheus.Desc
	yields     *prometheus.Desc
	refused    *prometheus.Desc
	faults     *prometheus.Desc
	plans      *prometheus.Desc
	planHeld   *prometheus.Desc
	// The directed reads of series_late Query Groups and the supplements of
	// their Slots.
	supplementWindows    *prometheus.Desc
	supplementUnobserved *prometheus.Desc
	supplementSeries     *prometheus.Desc
	supplementPoints     *prometheus.Desc
	directedBytes        *prometheus.Desc
	supplementHold       *prometheus.Desc
	supplementHoldMax    *prometheus.Desc
	// The early reads of directed Slots, before their next Slot reads.
	earlyReads            *prometheus.Desc
	earlyUndecided        *prometheus.Desc
	earlyBytes            *prometheus.Desc
	earlierBytes          *prometheus.Desc
	earlierReads          *prometheus.Desc
	earlierOvertaken      *prometheus.Desc
	earlierPreparedLate   *prometheus.Desc
	earlierAhead          *prometheus.Desc
	earlierSkipped        *prometheus.Desc
	probationRereads      *prometheus.Desc
	probationCaught       *prometheus.Desc
	probationGroups       *prometheus.Desc
	floorProbeRounds      *prometheus.Desc
	floorProbes           *prometheus.Desc
	floorProbeGroups      *prometheus.Desc
	floorProbeChanges     *prometheus.Desc
	readHoldFloorProbes   *prometheus.Desc
	readHoldProbeProgress *prometheus.Desc
	readHoldFindings      *prometheus.Desc
	holdIgnored           *prometheus.Desc
	readHoldTransition    *prometheus.Desc
	readHoldOvertaken     *prometheus.Desc
	readHoldPredecessor   *prometheus.Desc
	readHoldSeed          *prometheus.Desc
	readHoldDuration      *prometheus.Desc
	readHoldEarlierSkip   *prometheus.Desc
	roundsReadAtLimit     *prometheus.Desc
	readHoldClamped       *prometheus.Desc
	readHoldCorrupt       *prometheus.Desc
	readHoldRetireClose   *prometheus.Desc
	readHoldCloseSkip     *prometheus.Desc
	readHoldDegraded      *prometheus.Desc
	readHoldGroups        *prometheus.Desc
	readHoldMax           *prometheus.Desc
}

func newLookbackCollector() *lookbackCollector {
	desc := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName("", metricSubsystem, name), help, labels, nil)
	}
	return &lookbackCollector{
		readHoldTransition: desc("read_hold_transition_total", "Slots whose first readiness preserves a preceding segment's completion deadline."),
		readHoldOvertaken:  desc("read_hold_transition_overtaken_total", "Closed-segment attempts refused because newer state has already applied."),
		readHoldPredecessor: desc("read_hold_predecessor_total",
			"Moved Plans a successor Query Group seeded, by what it took the Plan's previous hold from: inherited (the "+
				"old group's closed record, exactly), zero (no record: a group never held), or -- each read as the hold "+
				"bound, which costs the successor's first Slots a later read and never a Slot -- record_open, "+
				"record_corrupt, record_unreadable; and the links it skipped: self_link, invalid_link, expired.", "reason"),
		roundsReadAtLimit: desc("rounds_read_at_limit_total",
			"Decided rounds (normal and retry) read at their Query Group's read hold limit while the group's data "+
				"lands past it, by the source the group reads (other while it is not known yet): each round was "+
				"decided before all of its data was there, and the hold waits no longer. A round is judged on its "+
				"group's record as it completes: the first round after a raise to the limit is marked on the "+
				"arrival that raised it, and a group quiet for its bar is tried lower and no longer counted. Every "+
				"such round's slot_completed line carries read_at_limit; the group's row on the strategy page "+
				"names the arrival age, the limit and the time_delay that would read it whole. Every source "+
				"exists from the start.", "source"),
		readHoldEarlierSkip: desc("read_hold_earlier_skipped_total",
			"Earlier-read trials not run, by why, one per Slot a trial would have been reserved for (its group's "+
				"sampling rest over, nothing of it in flight): at_limit (the group is held at its limit and was read "+
				"early within its quiet bar, so its data arrives past the longest hold it may have; a read below "+
				"the hold and the formal read both come before the data, and their agreeing would say nothing of "+
				"it). A group counted here keeps its hold at the limit; its trials resume once an arrival reading "+
				"wants a hold below the limit, or once it has gone its quiet bar without being read early. "+
				"search_done (the search below the hold has run out: the gap between its floor and the hold is no "+
				"wider than a step; the group's read-hold record says when the floor was learned, earlier_floor_at_ms). "+
				"A group whose floor is being probed is counted under neither: its Slots are "+
				"lookback_earlier_skipped_total{reason=\"floor_probe\"}. Every reason exists from the start.", "reason"),
		readHoldFindings: desc("read_hold_probation_findings_total",
			"Probation findings the read-hold controller took - a lowered group's reread that found its round read "+
				"early, whatever arrived - by what it made of them: undone (the lowering put back to the hold it came "+
				"from, the lowered hold the floor, the quiet started over) and quiet (the group was not lowered: its quiet "+
				"started over, no trial lowers it until it is quiet for its bar). Against "+
				"lookback_probation_rereads_total{outcome=\"window_read_early\"}, every one of which should be here. "+
				"Every outcome exists from the start.", "outcome"),
		readHoldFloorProbes: desc("read_hold_floor_probes_total",
			"Shadow probes whose end the read-hold controller took, by kind - verify (the candidate three trials in a "+
				"row chose, read before the hold moves; every lowering now comes through one), midpoint (halfway between "+
				"a floor and the hold), floor (the floor itself) - and what it made of it: lowered (the probe read "+
				"lookback.ProbationRounds rounds in a row the same at its hold as at the group's, and the hold comes down "+
				"to it, a probation after it as after every lowering), relearned (a round read differently at the floor: "+
				"the floor stays, dated now, and stands twice as long as before before the next probe), floor_raised (a "+
				"round read differently at the candidate or a midpoint: that hold is the floor now, and the next probe "+
				"reads the midpoint above it), stale (the record had moved since the probe began - its hold, its floor "+
				"or its search - and the probe changed nothing), moot (verify: the candidate was no longer below the hold "+
				"and above the floor, and was cleared unread). Probes that did not end are on "+
				"lookback_floor_probes_total. Every kind and outcome exists from the start.", "kind", "outcome"),
		readHoldProbeProgress: desc("read_hold_floor_probe_progress_writes_total",
			"Writes keeping a running shadow probe's clean rounds in its group's record, every "+
				"lookback.FloorProbeProgressEvery of them, so the probe goes on from them after a rollout or a takeover, by "+
				"what came of them: written, stale (the record's probe had ended or moved first, nothing written) and "+
				"conflict (the compare lost to another write, the progress dropped, the move that won kept). A probe that "+
				"goes on from them is lookback_floor_probes_total{event=\"resumed\"}. Every outcome exists from the start.", "outcome"),
		readHoldDuration: desc("read_hold_query_duration_writes_total",
			"Read-hold records written for the query duration they keep, by why: changed (the owner's measured "+
				"duration moved by a second, a minute or more after the last write) or refreshed (the same reading "+
				"rewritten at half its 24-hour life). A process that takes a group over judges its first rounds by "+
				"that reading. Read it as the write rate the reading costs.", "reason"),
		readHoldSeed: desc("read_hold_seeds_total",
			"Query Groups with no read-hold record of their own that started from a seeded arrival age, by where it "+
				"came from: peer (the 90th percentile of what the groups reading the same tables and data sources "+
				"measured) or cold_start (no peer had measured: the deployment's ready delay, as the group's schedule "+
				"allowed it). A seed that would hold nothing is not counted: such a group starts at no hold and keeps no "+
				"record. Read cold_start against peer: a fleet seeding mostly cold start is one whose new strategies "+
				"read later than their sources need until they measure their own.", "origin"),
		readHoldClamped: desc("read_hold_transition_clamped_total",
			"Slots frozen at the group's hold limit because a transition asked for more, by source: known (a known "+
				"previous hold asked for it; the old group's last Slot may overtake this one) or fallback (only the hold "+
				"bound standing in for an unknown one did).", "source"),
		readHoldCorrupt: desc("read_hold_record_corrupt_total",
			"Query Groups whose own read hold record did not decode: read as missing, the hold relearned, and the record "+
				"replaced at the group's next write."),
		readHoldCloseSkip: desc("read_hold_close_previous_skipped_total",
			"Previous Segments a Query Group's prepare did not close because its read hold record was already past them "+
				"without their closing facts -- a departed Plan's closure dropped after its lifetime while the Segment is still "+
				"retained. The group goes on; a successor reads that boundary as open, which is the hold bound."),
		readHoldDegraded: desc("read_hold_degraded_total",
			"New Slots frozen with the hold their Query Group last read -- zero for a group without a record -- because "+
				"its own could not be prepared or written, each freeze attempt once, by what failed: spec_unreadable, "+
				"previous_unreadable, predecessors_unreadable, spec_rejected, close_failed, stale_segment, hold_failed, "+
				"record_unreadable. "+
				"No Slot is refused for it; the group learns nothing until its hold prepares again.", "reason"),
		readHoldGroups: desc("read_hold_groups",
			"Query Groups this replica holds, by the source they read and their read hold: held (a hold of more than "+
				"none), at_limit (held at the group's limit), unknown (its hold not yet known - not counted under the "+
				"other two). Each replica counts only its own groups: the deployment's is the sum. Absent while the "+
				"replica holds none. The source partition covers groups that have prepared a Schedule; "+
				"alarmd_worker_owned_query_groups counts every owned group.", "source", "kind"),
		readHoldMax: desc("read_hold_max_seconds",
			"The largest known read hold among the Query Groups this replica holds that read the source; the "+
				"deployment's is the max. Absent for a source none of whose groups here has a known hold. The source "+
				"partition covers groups that have prepared a Schedule; alarmd_worker_owned_query_groups counts every "+
				"owned group.", "source"),
		readHoldRetireClose: desc("read_hold_retire_close_failed_total",
			"Retired Query Groups whose read hold closing failed; they retire all the same, and a successor reads the "+
				"unclosed record as the hold bound."),
		firstReads: desc("lookback_first_reads_total",
			"Formal first reads seen, by source - the data source the Query Group reads, labelled as the directory "+
				"labels its Query Groups, so every lookback family reads beside them: the denominator of the query "+
				"volume the lookback adds (lookback_rechecks_total over it).", "source"),
		checks: desc("lookback_rechecks_total",
			"Rechecks by source and outcome, over every rung; each rung's are in /api/lookback. Only compared is a "+
				"window observed; yielded, recheck_failed, partial and owner_lost are windows not observed.",
			"source", "outcome"),
		changed: desc("lookback_changed_windows_total",
			"Compared windows that changed since the read before, by source, over every rung: over the compared "+
				"rechecks, the share of windows whose data was still arriving. Each rung's are in /api/lookback.", "source"),
		changes: desc("lookback_changes_total",
			"Buckets of compared windows by how they changed since the read before, over every rung: points_added, "+
				"points_removed, series_changed (as many points from other series), values_changed. Each rung's are in "+
				"/api/lookback.", "source", "class"),
		classes: desc("lookback_sample_classes_total",
			"Completed samples by what their rungs found against the first read, by source: window_read_early (the "+
				"first read was empty and data came later, or a series it had came back changed or not at all - the "+
				"strategy's time_delay moves the read), partial_revised (some series it had came back changed with a "+
				"value, and others with a value came back as they were: the series were late, not the window; a "+
				"series zero or without a value in both reads decides nothing), series_late (every series it had came "+
				"back as it was, and others came later - supplementary detection fills them), unclassified, complete.",
			"source", "class"),
		readEarly: desc("lookback_read_early_groups",
			"The source's Query Groups whose window was read early in two completed samples in a row, each reported "+
				"with the time_delay that would have read it complete (lookback.get read_early).", "source"),
		seriesLate: desc("lookback_series_late_groups",
			"The source's Query Groups some of whose series were seen coming later than the first read.", "source"),
		supplementWindows: desc("lookback_supplement_windows_total",
			"Slots of series_late Query Groups read again for their late series, by source and what they came to: "+
				"supplemented (the supplement ran), nothing_late, flight_busy (the group's own Slot was executing), "+
				"contract_expired, kept_read_absent (the kept read did not hold the Slot's planned query, so nothing ran; "+
				"SUPPLEMENT_KEPT_READ_ABSENT on the supplement's line), failed, unobserved (not read). Coverage is "+
				"supplemented over supplemented and "+
				"unobserved.", "source", "outcome"),
		supplementUnobserved: desc("lookback_supplement_unobserved_total",
			"Slots of series_late Query Groups not read again, by source and why: yielded, read_failed, "+
				"first_read_incomplete, multi_query, memory_refused, named_outputs.", "source", "reason"),
		supplementSeries: desc("lookback_supplement_series_total",
			"(Plan, series) pairs the supplements that ran were given, by source and what each came to: candidates "+
				"in all, and admitted, crossed_t, no_data_fact, config_drift, input_incomplete, withheld.",
			"source", "outcome"),
		supplementPoints: desc("lookback_supplement_points_total",
			"Points the admitted series of the supplements were evaluated on, by source.", "source"),
		directedBytes: desc("lookback_directed_read_bytes_total",
			"Bytes the directed reads delivered, by source: a Slot's frozen query each, read against "+
				"lookback_first_read_bytes_total over lookback_first_reads_total.", "source"),
		supplementHold: desc("lookback_supplement_hold_total",
			"Supplements by how long each held its Query Group's flight, by source: from taking the flight to the "+
				"supplement's return - freezing the Slot's contract, evaluating, writing its State, and any wait on "+
				"Redis inside them - which is how long the group's own Slot waited behind it. Buckets close at their "+
				"bound: le_100ms, le_500ms, le_1s, le_5s, gt_5s. A supplement refused for the flight never held it "+
				"and is not counted.", "source", "bucket"),
		supplementHoldMax: desc("lookback_supplement_hold_max_seconds",
			"The longest a supplement held its Query Group's flight in this process, by source.", "source"),
		earlyReads: desc("lookback_directed_early_total",
			"Directed Slots by what their early read -- once more before the Query Group's next Slot reads -- came "+
				"to, by source and group: before_next, a supplement that ran with no later Slot of the group begun; "+
				"not_ahead, attempted and not ahead; outside_mechanism, did not apply. The mechanism works as far as "+
				"before_next is of before_next and not_ahead. Each outcome is in /api/lookback's early_reads.",
			"source", "group"),
		earlyUndecided: desc("lookback_directed_early_undecided_total",
			"(Plan, series) pairs early supplements left undecided -- withheld, input_incomplete, config_drift -- "+
				"which the read at the rung does not supplement again, by source.", "source"),
		earlyBytes: desc("lookback_directed_early_read_bytes_total",
			"Bytes the early reads delivered, by source: part of lookback_directed_read_bytes_total, the bytes reading "+
				"early added.", "source"),
		earlierBytes: desc("lookback_earlier_read_bytes_total", "Bytes candidate h/2 reads delivered, by source.", "source"),
		earlierReads: desc("lookback_earlier_reads_total",
			"Candidate h/2 read trials by how they ended, by source: equal or different from the formal first read, or why they supplied no evidence. "+
				"Reader: the read-hold acceptance after each deploy, which needs completed trials (equal plus different) above zero; "+
				"a deployment without the CLI reads it only here, since its lookback route is not public.",
			"source", "outcome"),
		earlierOvertaken: desc("lookback_earlier_overtaken_total",
			"Candidate h/2 read trials that ended overtaken, by source and what overtook them first. "+
				"Reader: the same acceptance, which names the cause when trials do not complete.", "source", "path"),
		earlierPreparedLate: desc("lookback_earlier_prepared_late_max_seconds",
			"The furthest past its candidate readiness point a trial's Slot was prepared since process start, by source; it never decays, and is zero while none was. "+
				"Reader: the same acceptance, beside prepared_after_readiness: how late the Slots arrive.", "source"),
		earlierAhead: desc("lookback_earlier_ahead_total",
			"Candidate h/2 read trials reserved at an earlier Slot's first attempt, by source and how many Slots ahead (1, 2, 3, 4+). "+
				"Reader: the same acceptance, which shows which sources' holds needed a trial reserved further than the next Slot.", "source", "slots"),
		earlierSkipped: desc("lookback_earlier_skipped_total",
			"Held Slots whose first attempt reserved no earlier-read trial, once a Slot, by source and why: "+
				"not_owned (the group is not this process's; counted at each attempt), no_hold (its hold came down "+
				"to none since the Slot was frozen), backend_failing (its queries are failing), no_candidate (the "+
				"read-hold controller gave none; read_hold_earlier_skipped_total says why), trial_reserved (a trial "+
				"is reserved for a later Slot), sample_in_flight (its sample is being taken or read), resting (its "+
				"sample rests past the Slot's readiness), floor_probe (its search has run out and its floor is being "+
				"probed: the Slot has a probe round, lookback_floor_probe_rounds_total). Reader: why a held group is "+
				"not tried; the group's own counts are on its lookback reading.", "source", "reason"),
		probationRereads: desc("lookback_probation_rereads_total",
			"Rounds of Query Groups in probation - their hold lowered, and not yet read clean lookback.ProbationRounds times - "+
				"read again at their first rung, by source and what they came to: clean, window_read_early (raises the hold when "+
				"the whole window came later), skipped_for_permits, read_failed, unclassified. Only clean counts towards the end "+
				"of a probation. Reader: what probation costs and what it catches.", "source", "outcome"),
		probationCaught: desc("lookback_probation_caught_total",
			"Rounds read early that probation caught - a reread at the first rung that found the whole window came "+
				"later, raising the hold - by source and the lowering the probation followed: verified (a hold a shadow "+
				"probe read clean round by round first, passed by chance; the lowered hold becomes a floor a probe failed "+
				"at) and trial (a lowering on earlier-read trials alone, as builds before every lowering was verified "+
				"made). Each is one round decided on data not yet "+
				"whole. Reader: how often a verified lowering was wrong, against how many there were "+
				"(read_hold_floor_probes_total{outcome=\"lowered\"}). Every value exists from the start.", "source", "after"),
		probationGroups: desc("lookback_probation_groups",
			"The source's Query Groups in probation now: their hold lowered, each of their rounds read again until "+
				"lookback.ProbationRounds of them read clean.", "source"),
		floorProbeRounds: desc("lookback_floor_probe_rounds_total",
			"Rounds of Query Groups whose floor is being probed - their search below the hold run out at a floor that "+
				"has stood long enough - read at the floor beside the formal read at the hold, which decides the round, "+
				"by source and what they came to: clean (the same as the formal read), different (ends the probe; the "+
				"floor stands longer), and, no evidence either way, skipped_for_permits, read_failed, unclassified (the "+
				"formal read not whole or a series table refused) and overtaken (not read in time, or its Slot froze "+
				"otherwise than it was reserved for). Reader: what the probes cost and how often their rounds count; "+
				"their reads' bytes are in lookback_earlier_read_bytes_total.", "source", "outcome"),
		floorProbes: desc("lookback_floor_probes_total",
			"Shadow probes by source, kind - verify (the candidate a group's trials chose, read before its hold "+
				"moves), midpoint (halfway between a floor and the hold), floor (the floor itself) - and what happened "+
				"to them: started, passed (lookback.ProbationRounds rounds in a row clean, more near a floor a probe "+
				"failed at), failed (a round different), hold_moved (the hold, the floor or the search under them moved "+
				"before the probe ended), owner_lost (the group left this process). A probe whose process stops first "+
				"ends with none of them: started rising while passed and failed do not is probes that never live to "+
				"finish, as under rollouts more frequent than a probe's rounds. Every lowering starts as a verify probe, "+
				"so verify started tracks the lowerings on their way.", "source", "kind", "event"),
		floorProbeGroups: desc("lookback_floor_probe_groups",
			"The source's Query Groups whose floor is being probed now.", "source"),
		floorProbeChanges: desc("lookback_floor_probe_changes_total",
			"Buckets the formal read of a different probe round differed in from the read at the probe's hold, by "+
				"source and change: points_added (the formal read had points the probe's read did not - a series or a "+
				"point not yet there, as a source that shows a point only seconds after its time reads), points_removed, "+
				"series_changed, values_changed. Reader: why probes fail; the failing round's are on its "+
				"floor_probe_ended line.", "source", "change"),
		holdIgnored: desc("lookback_read_hold_ignored_total", "Findings that cannot raise a whole-window read hold, by source and reason.", "source", "reason"),
		readBytes: desc("lookback_first_read_bytes_total",
			"Bytes the formal first reads delivered, by source, counted as the rechecks' are.", "source"),
		checkBytes: desc("lookback_recheck_bytes_total",
			"Bytes the rechecks read back, by source: over lookback_first_read_bytes_total, the query bytes the "+
				"lookback adds.", "source"),
		unknown: desc("lookback_unknown_lookback_total",
			"Samples whose query's lookback (a window, a range, an offset) could not be read, rechecked from one "+
				"step before the window's tail instead.", "source"),
		coverage: desc("lookback_coverage",
			"The Query Groups this process owns (owned), how many of them have a fresh measurement (covered), and how "+
				"many have never had a whole first read (never_complete_first_read) - never measurable, named by "+
				"lookback.get, and left out of what covered is read against: covered / (owned - "+
				"never_complete_first_read), the aim being all.",
			"what"),
		pending: desc("lookback_pending",
			"Samples in flight - one at most per owned Query Group, and one waiting for its deep recheck - and the "+
				"bytes their summaries hold.", "what"),
		yields: desc("lookback_preemptions_total",
			"Recheck reads stopped because a formal query had to wait for a query permit, by source, over every rung; "+
				"each rung's are in /api/lookback. The rung is tried again within its window and counted in "+
				"lookback_rechecks_total by what it comes to.",
			"source"),
		refused: desc("lookback_permit_refusals_total",
			"Lookback query permits refused, by reason: waiters (a formal query is waiting), full (every process "+
				"permit is held), disabled. A refused rung keeps its window.",
			"reason"),
		faults: desc("lookback_faults_total",
			"Reads not kept for a defect, by reason. Normal running never meets one; any count is a defect to fix.",
			"reason"),
		plans: desc("lookback_plan_total",
			"Control-plane lookback plan entries by what became of them: seeded (a Query Group read directed "+
				"at the plan's rung from its first read here), kept_local (its own samples here decide it), expired "+
				"(a seeded Query Group none of whose samples here saw late series left directed reading when its "+
				"entry expired or was withdrawn).",
			"outcome"),
		planHeld: desc("lookback_plan_entries",
			"Entries of the control plane's lookback plan set this process holds now: the Query Groups it was "+
				"told read late series. Zero where no Leader plans, or before the first set arrives."),
	}
}

func (c *lookbackCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.firstReads, c.checks, c.changed, c.changes, c.classes, c.readEarly, c.seriesLate, c.supplementWindows, c.supplementUnobserved, c.supplementSeries,
		c.supplementPoints, c.directedBytes, c.supplementHold, c.supplementHoldMax, c.earlyReads, c.earlyUndecided, c.earlyBytes, c.earlierBytes, c.earlierReads, c.earlierOvertaken, c.earlierPreparedLate, c.earlierAhead, c.earlierSkipped, c.probationRereads, c.probationCaught, c.probationGroups, c.floorProbeRounds, c.floorProbes, c.floorProbeGroups, c.floorProbeChanges, c.holdIgnored, c.readBytes, c.checkBytes, c.unknown, c.coverage,
		c.pending, c.yields, c.refused, c.faults, c.plans, c.planHeld, c.readHoldTransition, c.readHoldOvertaken,
		c.readHoldPredecessor, c.readHoldSeed, c.readHoldDuration, c.readHoldEarlierSkip, c.readHoldFloorProbes, c.readHoldProbeProgress, c.readHoldFindings, c.roundsReadAtLimit, c.readHoldClamped, c.readHoldCorrupt, c.readHoldRetireClose, c.readHoldCloseSkip, c.readHoldDegraded,
		c.readHoldGroups, c.readHoldMax} {
		ch <- desc
	}
}

func (c *lookbackCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	source := c.source
	c.mu.Unlock()
	if source == nil {
		return
	}
	stats := source()
	counter := func(desc *prometheus.Desc, value uint64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, float64(value), labels...)
	}
	gauge := func(desc *prometheus.Desc, value float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
	}
	counter(c.readHoldTransition, stats.ReadHoldTransitions)
	counter(c.readHoldOvertaken, stats.ReadHoldTransitionOvertaken)
	// Every reason and source at zero, whatever the source filled in: an
	// absent series reads as a build without it.
	for _, reasons := range [][]string{readhold.PredecessorReasons, readhold.LinkSkipReasons} {
		for _, reason := range reasons {
			counter(c.readHoldPredecessor, stats.ReadHoldPredecessors[reason], reason)
		}
	}
	for _, origin := range readhold.SeedOrigins {
		counter(c.readHoldSeed, stats.ReadHoldSeeds[origin], origin)
	}
	for _, reason := range readhold.QueryDurationWriteReasons {
		counter(c.readHoldDuration, stats.ReadHoldQueryDurationWrites[reason], reason)
	}
	for _, reason := range readhold.EarlierSkipReasons {
		counter(c.readHoldEarlierSkip, stats.ReadHoldEarlierSkipped[reason], reason)
	}
	for _, kind := range readhold.FloorProbeKinds {
		for _, outcome := range readhold.FloorProbeOutcomes {
			counter(c.readHoldFloorProbes, stats.ReadHoldFloorProbesByKind[kind][outcome], kind, outcome)
		}
	}
	for _, outcome := range readhold.ProbationFindingOutcomes {
		counter(c.readHoldFindings, stats.ReadHoldProbationFindings[outcome], outcome)
	}
	for _, outcome := range readhold.ProbeProgressOutcomes {
		counter(c.readHoldProbeProgress, stats.ReadHoldFloorProbeProgress[outcome], outcome)
	}
	for _, source := range lookback.Sources {
		counter(c.roundsReadAtLimit, stats.RoundsReadAtLimit[source], source)
	}
	for _, source := range readhold.ClampSources {
		counter(c.readHoldClamped, stats.ReadHoldClamped[source], source)
	}
	counter(c.readHoldCorrupt, stats.ReadHoldOwnCorrupt)
	counter(c.readHoldRetireClose, stats.ReadHoldRetireCloseFailed)
	counter(c.readHoldCloseSkip, stats.ReadHoldCloseSkipped)
	for _, reason := range readhold.DegradedReasons {
		counter(c.readHoldDegraded, stats.ReadHoldDegraded[reason], reason)
	}
	for _, source := range lookback.Sources {
		groups, held := stats.ReadHoldGroups[source]
		if !held {
			continue
		}
		gauge(c.readHoldGroups, float64(groups.Held), source, "held")
		gauge(c.readHoldGroups, float64(groups.AtLimit), source, "at_limit")
		gauge(c.readHoldGroups, float64(groups.Unknown), source, "unknown")
		if groups.MaxKnown {
			gauge(c.readHoldMax, float64(groups.MaxMillis)/1000, source)
		}
	}
	for name, source := range stats.Sources {
		counter(c.firstReads, source.FirstReads, name)
		for _, class := range lookback.SampleClasses {
			counter(c.classes, source.Classes[class], name, class)
		}
		gauge(c.readEarly, float64(source.ReadEarlyGroups), name)
		gauge(c.seriesLate, float64(source.SeriesLateGroups), name)
		for _, outcome := range lookback.DirectedOutcomes {
			counter(c.supplementWindows, source.SupplementWindows[outcome], name, outcome)
		}
		for _, reason := range lookback.DirectedUnobservedReasons {
			counter(c.supplementUnobserved, source.SupplementUnobserved[reason], name, reason)
		}
		for _, outcome := range lookback.SupplementSeriesOutcomes {
			counter(c.supplementSeries, source.SupplementSeries[outcome], name, outcome)
		}
		counter(c.supplementPoints, source.SupplementPoints, name)
		counter(c.directedBytes, source.DirectedReadBytes, name)
		for _, bucket := range lookback.SupplementHoldBuckets {
			counter(c.supplementHold, source.SupplementHold[bucket], name, bucket)
		}
		gauge(c.supplementHoldMax, source.SupplementHoldMaxSeconds, name)
		early := make(map[string]uint64, len(lookback.EarlyGroups))
		for _, outcome := range lookback.EarlyOutcomes {
			early[lookback.EarlyGroupOf(outcome)] += source.EarlyReads[outcome]
		}
		for _, group := range lookback.EarlyGroups {
			counter(c.earlyReads, early[group], name, group)
		}
		counter(c.earlyUndecided, source.EarlyUndecided, name)
		counter(c.earlyBytes, source.EarlyReadBytes, name)
		counter(c.earlierBytes, source.EarlierReadBytes, name)
		for _, outcome := range lookback.EarlierReadOutcomes {
			counter(c.earlierReads, source.EarlierReads[outcome], name, outcome)
		}
		for _, path := range lookback.EarlierOvertakenPaths {
			counter(c.earlierOvertaken, source.EarlierOvertaken[path], name, path)
		}
		for _, outcome := range lookback.ProbationOutcomes {
			counter(c.probationRereads, source.ProbationRereads[outcome], name, outcome)
		}
		for _, after := range lookback.ProbationAfters {
			counter(c.probationCaught, source.ProbationCaught[after], name, after)
		}
		gauge(c.probationGroups, float64(source.ProbationGroups), name)
		for _, outcome := range lookback.FloorProbeRoundOutcomes {
			counter(c.floorProbeRounds, source.FloorProbeRounds[outcome], name, outcome)
		}
		for _, kind := range lookback.FloorProbeKinds {
			for _, event := range lookback.FloorProbeEvents {
				counter(c.floorProbes, source.FloorProbesByKind[kind][event], name, kind, event)
			}
		}
		gauge(c.floorProbeGroups, float64(source.FloorProbeGroups), name)
		for _, change := range lookback.Changes {
			counter(c.floorProbeChanges, source.FloorProbeChanges[change], name, change)
		}
		gauge(c.earlierPreparedLate, source.EarlierPreparedLateMaxSeconds, name)
		for _, slots := range lookback.EarlierAheadSlots {
			counter(c.earlierAhead, source.EarlierAhead[slots], name, slots)
		}
		for _, reason := range lookback.EarlierSkipReasons {
			counter(c.earlierSkipped, source.EarlierSkipped[reason], name, reason)
		}
		for _, reason := range lookback.ReadHoldIgnoredReasons {
			if reason == lookback.ClassPartialRevised {
				// Every one of these is a partial_revised sample, counted in
				// lookback_sample_classes_total already.
				continue
			}
			counter(c.holdIgnored, source.ReadHoldIgnored[reason], name, reason)
		}
		// Over every rung: each rung's counts are /api/lookback's.
		var changed, preempted uint64
		checks := make(map[string]uint64, len(lookback.RecheckOutcomes))
		changes := make(map[string]uint64, len(lookback.Changes))
		for _, rung := range lookback.RungNames {
			for _, outcome := range lookback.RecheckOutcomes {
				checks[outcome] += source.Rechecks[rung][outcome]
			}
			changed += source.ChangedWindows[rung]
			for _, class := range lookback.Changes {
				changes[class] += source.Changes[rung][class]
			}
			preempted += source.Preempted[rung]
		}
		for _, outcome := range lookback.RecheckOutcomes {
			counter(c.checks, checks[outcome], name, outcome)
		}
		counter(c.changed, changed, name)
		for _, class := range lookback.Changes {
			counter(c.changes, changes[class], name, class)
		}
		counter(c.yields, preempted, name)
		counter(c.readBytes, source.FirstReadBytes, name)
		counter(c.checkBytes, source.RecheckBytes, name)
		counter(c.unknown, source.UnknownLookback, name)
	}
	for reason, n := range stats.PermitRefusals {
		counter(c.refused, n, reason)
	}
	for reason, n := range stats.Faults {
		counter(c.faults, n, reason)
	}
	for outcome, n := range stats.Plans {
		counter(c.plans, n, outcome)
	}
	gauge(c.planHeld, float64(stats.PlanEntries))
	gauge(c.coverage, float64(stats.Coverage.Owned), "owned")
	gauge(c.coverage, float64(stats.Coverage.Covered), "covered")
	gauge(c.coverage, float64(stats.Coverage.NeverCompleteFirstRead), "never_complete_first_read")
	gauge(c.pending, float64(stats.Pending), "samples")
	gauge(c.pending, float64(stats.PendingBytes), "bytes")
}

// SetLookbackSource binds the process's lookback to the collector. A process
// that does not run the lookback never binds it and emits nothing.
func (r *Recorder) SetLookbackSource(source func() lookback.Stats) {
	if r == nil || r.phaseTwo.lookback == nil {
		return
	}
	r.phaseTwo.lookback.mu.Lock()
	r.phaseTwo.lookback.source = source
	r.phaseTwo.lookback.mu.Unlock()
}
