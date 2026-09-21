// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package observability

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// CostPlanIdentity is supplied by the executing consumer, never inferred from
// a query group's members or an inherited trace's strategy field.
type CostPlanIdentity struct {
	TenantID   string `json:"tenant_id"`
	BusinessID string `json:"business_id"`
	StrategyID string `json:"strategy_id"`
}

func (p CostPlanIdentity) valid() bool {
	return p.TenantID != "" && p.BusinessID != "" && p.StrategyID != ""
}

// CostGroup is an existing managed object's identity-only projection. The
// caller reconciles it on its existing catalog/report tick, not per record.
type CostGroup struct {
	QueryGroupKey    string             `json:"query_group_key"`
	SnapshotRevision string             `json:"snapshot_revision"`
	QueryRevision    string             `json:"query_revision"`
	ScheduleRevision string             `json:"schedule_revision"`
	Members          []CostPlanIdentity `json:"members"`
	TotalMembers     int                `json:"total_members"`
}

type CostSummaryOptions struct {
	ProcessID     string
	Window        time.Duration
	GroupCapacity int
	PlanCapacity  int
	MetadataBytes int
	TopN          int
	Now           func() time.Time
}

// CostSummaryCapacityBytes estimates a conservative reservation, including two
// rolling counters, map entries, reconciliation replacement, and cached/read
// snapshots. Go allocator/map overhead varies: validate the reservation against
// the deployment's measured allocations. All population/metadata limits are
// supplied by the caller's resource budget; there is no permanent default cap.
// The per-entry allowance includes map buckets and membership slice capacity;
// BenchmarkCostSummaryReconcile measures the replacement generation as well.
func CostSummaryCapacityBytes(o CostSummaryOptions) int64 {
	if o.GroupCapacity <= 0 || o.PlanCapacity <= 0 || o.MetadataBytes <= 0 || o.TopN <= 0 {
		return 0
	}
	groups, plans := int64(o.GroupCapacity), int64(o.PlanCapacity)
	rows := min(groups+plans, 12*int64(o.TopN))
	return 2*(groups*(int64(unsafe.Sizeof(costGroupState{}))+256)+plans*(int64(unsafe.Sizeof(costPlanState{}))+256)+int64(o.MetadataBytes)) +
		3*(rows*int64(unsafe.Sizeof(CostContributor{}))+plans*int64(unsafe.Sizeof(CostPlanIdentity{}))+12*int64(o.TopN)*8)
}

type CostWall struct {
	ObservedNS int64  `json:"observed_ns"`
	Measured   uint64 `json:"measured"`
	Unknown    uint64 `json:"unknown"`
}

// CostScalars are observed work, not CPU. Query wall contains streaming
// callbacks; run wall is the outer Slot Execute call. These overlap evaluation
// and state wall and must not be added to one another.
type CostScalars struct {
	Observations             uint64   `json:"observations"`
	Attempts                 uint64   `json:"attempts"`
	RunReturns               uint64   `json:"run_returns"`
	FailedRunReturns         uint64   `json:"failed_run_returns"`
	ProgressCommits          uint64   `json:"progress_commits"`
	Evaluations              uint64   `json:"evaluations"`
	FailedEvaluations        uint64   `json:"failed_evaluations"`
	EvaluationRecords        uint64   `json:"evaluation_records"`
	EvaluationRecordsUnknown uint64   `json:"evaluation_records_unknown"`
	UnattributedEvaluations  uint64   `json:"unattributed_evaluations"`
	EvaluationWall           CostWall `json:"evaluation_wall"`
	QueryScopes              uint64   `json:"query_scopes"`
	QueryWall                CostWall `json:"query_wall"`
	StateCalls               uint64   `json:"state_calls"`
	StateKeys                uint64   `json:"state_keys"`
	StateKeysUnknown         uint64   `json:"state_keys_unknown"`
	StateWall                CostWall `json:"state_wall"`
	RunWall                  CostWall `json:"run_wall"`
	MaxLagNS                 int64    `json:"max_lag_ns"`
	LagMeasured              uint64   `json:"lag_measured"`
	LagUnknown               uint64   `json:"lag_unknown"`
	LastProgressUnix         int64    `json:"last_progress_unix"`
}

type costWindows struct {
	epoch    int64
	current  CostScalars
	previous CostScalars
}

func (w *costWindows) rotate(epoch int64) {
	if w.epoch == epoch {
		return
	}
	if w.epoch+1 == epoch {
		w.previous = w.current
	} else {
		w.previous = CostScalars{}
	}
	w.current = CostScalars{}
	w.epoch = epoch
}

type costPlanState struct {
	identity CostPlanIdentity
	windows  costWindows
	since    time.Time
}

type costGroupState struct {
	group   CostGroup
	plans   map[CostPlanIdentity]*costPlanState
	windows costWindows
	since   time.Time
}

type CostCoverage struct {
	// ContentionDroppedTotal is process cumulative, not assigned to a guessed
	// event window. Once contention loses facts, completeness stays conservative.
	ContentionDroppedTotal  uint64 `json:"contention_dropped_total"`
	CatalogComplete         bool   `json:"catalog_complete"`
	TotalGroups             int    `json:"total_groups"`
	TrackedGroups           int    `json:"tracked_groups"`
	TotalPlans              int    `json:"total_plans"`
	TrackedPlans            int    `json:"tracked_plans"`
	ObservedGroups          int    `json:"observed_groups"`
	ObservedPlans           int    `json:"observed_plans"`
	PartialWindowGroups     int    `json:"partial_window_groups"`
	PartialWindowPlans      int    `json:"partial_window_plans"`
	UnknownWallObservations uint64 `json:"unknown_wall_observations"`
	MetadataBytes           int    `json:"metadata_bytes"`
	UntrackedObservations   uint64 `json:"untracked_observations"`
	UnattributedEvaluations uint64 `json:"unattributed_evaluations"`
	Incomplete              bool   `json:"incomplete"`
}

type CostContributor struct {
	Scope        string           `json:"scope"`
	Group        CostGroup        `json:"group"`
	Plan         CostPlanIdentity `json:"plan"`
	TrackedSince time.Time        `json:"tracked_since"`
	Observed     bool             `json:"observed"`
	Current      CostScalars      `json:"current"`
	Previous     CostScalars      `json:"previous"`
}

// Indexes refer to Contributors, which keeps members once even when a group
// ranks in several dimensions. These are candidates from this process only.
type CostRanking struct {
	Scope     string `json:"scope"`
	Dimension string `json:"dimension"`
	Indexes   []int  `json:"indexes"`
}

type CostSnapshot struct {
	Enabled                bool              `json:"enabled"`
	DisabledReason         string            `json:"disabled_reason,omitempty"`
	ProcessID              string            `json:"process_id"`
	Scope                  string            `json:"scope"`
	GeneratedAt            time.Time         `json:"generated_at"`
	WindowStart            time.Time         `json:"window_start"`
	CurrentWindowStart     time.Time         `json:"current_window_start"`
	WindowEnd              time.Time         `json:"window_end"`
	CapacityBytesEstimated int64             `json:"capacity_bytes_estimated"`
	Coverage               CostCoverage      `json:"coverage"`
	Contributors           []CostContributor `json:"contributors"`
	Rankings               []CostRanking     `json:"rankings"`
}

// CostSummary observes live events only. It never consumes diagnostic records
// or snapshots: rereads cannot double count, while genuine same-Slot retries
// (including different owners) remain separate attempts. No run/series set is
// retained. Reconcile and Publish belong to existing control/report ticks.
type CostSummary struct {
	mu                sync.Mutex
	options           CostSummaryOptions
	enabled           bool
	groups            map[string]*costGroupState
	coverage          CostCoverage
	dropped           costWindows
	snapshot          CostSnapshot
	contentionDropped atomic.Uint64
}

func NewCostSummary(o CostSummaryOptions) *CostSummary {
	if o.Now == nil {
		o.Now = time.Now
	}
	c := &CostSummary{options: o, enabled: o.ProcessID != "" && o.Window > 0 && CostSummaryCapacityBytes(o) > 0}
	c.snapshot = CostSnapshot{Enabled: c.enabled, ProcessID: o.ProcessID, Scope: "process_observed_candidates", CapacityBytesEstimated: CostSummaryCapacityBytes(o)}
	c.snapshot.Coverage.Incomplete = true
	if !c.enabled {
		c.snapshot.DisabledReason = "resource_budget_or_process_identity_missing"
		c.snapshot.Coverage.Incomplete = true
	}
	return c
}

// Reconcile accepts the caller's managed scope, not a new authority. Incomplete
// catalogs must pass complete=false. Input order determines admission when the
// allocation cannot cover the scope; coverage explicitly reports the shortfall.
// Version changes begin fresh counters rather than relabel old measurements.
func (c *CostSummary) Reconcile(groups []CostGroup, complete bool) {
	if c == nil || !c.enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	coverage := CostCoverage{CatalogComplete: complete, TotalGroups: len(groups)}
	next := make(map[string]*costGroupState, min(len(groups), c.options.GroupCapacity))
	now := c.options.Now()
	for _, input := range groups {
		coverage.TotalPlans += len(input.Members)
		bytes := len(input.QueryGroupKey) + len(input.QueryRevision) + len(input.SnapshotRevision) + len(input.ScheduleRevision)
		if input.QueryGroupKey == "" || len(next) >= c.options.GroupCapacity || coverage.MetadataBytes+bytes > c.options.MetadataBytes {
			continue
		}
		if _, exists := next[input.QueryGroupKey]; exists {
			continue
		}
		old := c.groups[input.QueryGroupKey]
		state := &costGroupState{since: now, plans: make(map[CostPlanIdentity]*costPlanState)}
		if old != nil && old.group.QueryRevision == input.QueryRevision && old.group.SnapshotRevision == input.SnapshotRevision && old.group.ScheduleRevision == input.ScheduleRevision {
			state.windows, state.since = old.windows, old.since
		} else {
			old = nil
		}
		state.group = CostGroup{QueryGroupKey: strings.Clone(input.QueryGroupKey), QueryRevision: strings.Clone(input.QueryRevision), SnapshotRevision: strings.Clone(input.SnapshotRevision), ScheduleRevision: strings.Clone(input.ScheduleRevision), TotalMembers: len(input.Members)}
		coverage.MetadataBytes += bytes
		for _, member := range input.Members {
			bytes := len(member.TenantID) + len(member.BusinessID) + len(member.StrategyID)
			if !member.valid() || coverage.TrackedPlans >= c.options.PlanCapacity || coverage.MetadataBytes+bytes > c.options.MetadataBytes {
				continue
			}
			if _, exists := state.plans[member]; exists {
				continue
			}
			identity := CostPlanIdentity{strings.Clone(member.TenantID), strings.Clone(member.BusinessID), strings.Clone(member.StrategyID)}
			plan := &costPlanState{identity: identity, since: now}
			if old != nil && old.plans[member] != nil {
				plan.windows, plan.since = old.plans[member].windows, old.plans[member].since
			}
			state.plans[identity] = plan
			state.group.Members = append(state.group.Members, identity)
			coverage.TrackedPlans++
			coverage.MetadataBytes += bytes
		}
		next[state.group.QueryGroupKey] = state
	}
	coverage.TrackedGroups = len(next)
	coverage.Incomplete = !complete || coverage.TrackedGroups != coverage.TotalGroups || coverage.TrackedPlans != coverage.TotalPlans
	c.groups, c.coverage = next, coverage
}

// Observe never waits for another observer, reconciliation, publication, or an
// API read. Contention loses only observability facts and is explicitly counted.
func (c *CostSummary) Observe(ctx context.Context, o Observation) {
	if c == nil || !c.enabled {
		return
	}
	switch o.Stage {
	case StageSlotStarted, StageSlotCompleted, StageProgressCommitted, StageEvaluationCompleted, StageQueryCompleted, StageStatePreflight, StageStateApplied:
	default:
		return
	}
	trace := mergeTraceFields(o.Trace, TraceFieldsFromContext(ctx))
	now := c.options.Now()
	epoch := now.UnixNano() / int64(c.options.Window)
	if !c.mu.TryLock() {
		c.contentionDropped.Add(1)
		return
	}
	defer c.mu.Unlock()
	g := c.groups[trace.QueryGroupKey]
	if g == nil || (trace.SnapshotRevision != "" && trace.SnapshotRevision != g.group.SnapshotRevision) || (trace.QueryRevision != "" && trace.QueryRevision != g.group.QueryRevision) || (trace.ScheduleRevision != "" && trace.ScheduleRevision != g.group.ScheduleRevision) {
		c.dropped.rotate(epoch)
		c.dropped.current.Observations++
		return
	}
	g.windows.rotate(epoch)
	addCost(&g.windows.current, o, trace, now)
	if o.Stage == StageEvaluationCompleted {
		if p := g.plans[o.EvaluationOwner]; o.EvaluationOwner.valid() && p != nil {
			p.windows.rotate(epoch)
			addCost(&p.windows.current, o, trace, now)
		} else {
			g.windows.current.UnattributedEvaluations++
		}
	}
}

func addWall(w *CostWall, o Observation) {
	if o.Duration >= 0 && (o.DurationKnown || o.Duration > 0) {
		w.Measured++
		w.ObservedNS += max(0, int64(o.Duration))
	} else {
		w.Unknown++
	}
}

func addCost(s *CostScalars, o Observation, trace TraceFields, now time.Time) {
	s.Observations++
	switch o.Stage {
	case StageSlotStarted:
		s.Attempts++
	case StageSlotCompleted:
		s.RunReturns++
		if o.Err != nil || o.Result == ResultFailed {
			s.FailedRunReturns++
		}
		addWall(&s.RunWall, o)
		if trace.EvaluationTime > 0 {
			s.LagMeasured++
			s.MaxLagNS = max(s.MaxLagNS, max(0, now.Sub(time.Unix(trace.EvaluationTime, 0)).Nanoseconds()))
		} else {
			s.LagUnknown++
		}
	case StageProgressCommitted:
		if o.Err == nil && ValidProgressCompletionKind(o.ProgressCompletionKind) {
			s.ProgressCommits++
			s.LastProgressUnix = now.Unix()
		}
	case StageEvaluationCompleted:
		s.Evaluations++
		if o.Err != nil || o.Result == ResultFailed {
			s.FailedEvaluations++
		}
		if o.EvaluationRecordsKnown || o.Counts.Records > 0 {
			s.EvaluationRecords += uint64(max(0, o.Counts.Records))
		} else {
			s.EvaluationRecordsUnknown++
		}
		addWall(&s.EvaluationWall, o)
	case StageQueryCompleted:
		s.QueryScopes++
		addWall(&s.QueryWall, o)
	case StageStatePreflight, StageStateApplied:
		s.StateCalls++
		if o.Counts.Keys > 0 {
			s.StateKeys += uint64(o.Counts.Keys)
		} else {
			s.StateKeysUnknown++
		}
		addWall(&s.StateWall, o)
	}
}

var costDimensions = [...]string{"evaluation_records", "evaluation_wall_observed", "state_calls", "state_keys", "run_wall", "lag"}

func costRank(w costWindows, dimension int) int64 {
	a, b := w.current, w.previous
	switch dimension {
	case 0:
		return int64(a.EvaluationRecords + b.EvaluationRecords)
	case 1:
		return a.EvaluationWall.ObservedNS + b.EvaluationWall.ObservedNS
	case 2:
		return int64(a.StateCalls + b.StateCalls)
	case 3:
		return int64(a.StateKeys + b.StateKeys)
	case 4:
		return a.RunWall.ObservedNS + b.RunWall.ObservedNS
	default:
		return max(a.MaxLagNS, b.MaxLagNS)
	}
}

type costCandidate struct {
	group *costGroupState
	plan  *costPlanState
	value int64
}

// Publish computes bounded candidate rankings once on the existing report tick.
// Snapshot only reads this cache. No Redis, JSON, series copies, or I/O occurs
// here or on Observe. Unobserved catalog members are not measured-zero rows.
func (c *CostSummary) Publish(now time.Time) {
	if c == nil || !c.enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	epoch := now.UnixNano() / int64(c.options.Window)
	start := time.Unix(0, epoch*int64(c.options.Window))
	snapshot := CostSnapshot{Enabled: true, ProcessID: c.options.ProcessID, Scope: "process_observed_candidates", GeneratedAt: now, WindowStart: start.Add(-c.options.Window), CurrentWindowStart: start, WindowEnd: now, CapacityBytesEstimated: CostSummaryCapacityBytes(c.options), Coverage: c.coverage}
	c.dropped.rotate(epoch)
	snapshot.Coverage.UntrackedObservations = c.dropped.current.Observations + c.dropped.previous.Observations
	snapshot.Coverage.ContentionDroppedTotal = c.contentionDropped.Load()
	var rankings [2 * len(costDimensions)][]costCandidate
	add := func(g *costGroupState, p *costPlanState, w costWindows) {
		for dim := range costDimensions {
			value := costRank(w, dim)
			if value <= 0 {
				continue
			}
			candidate := costCandidate{g, p, value}
			if p != nil {
				dim += len(costDimensions)
			}
			rows := rankings[dim]
			at := sort.Search(len(rows), func(i int) bool {
				if rows[i].value != value {
					return rows[i].value < value
				}
				return costCandidateLess(candidate, rows[i])
			})
			if at >= c.options.TopN {
				continue
			}
			if len(rows) < c.options.TopN {
				rows = append(rows, costCandidate{})
			}
			copy(rows[at+1:], rows[at:len(rows)-1])
			rows[at] = candidate
			rankings[dim] = rows
		}
	}
	for _, g := range c.groups {
		g.windows.rotate(epoch)
		if g.windows.current.Observations+g.windows.previous.Observations > 0 {
			snapshot.Coverage.ObservedGroups++
		}
		if g.since.After(snapshot.WindowStart) {
			snapshot.Coverage.PartialWindowGroups++
		}
		for _, s := range [2]CostScalars{g.windows.current, g.windows.previous} {
			snapshot.Coverage.UnknownWallObservations += s.EvaluationWall.Unknown + s.StateWall.Unknown + s.QueryWall.Unknown + s.RunWall.Unknown
		}
		snapshot.Coverage.UnattributedEvaluations += g.windows.current.UnattributedEvaluations + g.windows.previous.UnattributedEvaluations
		add(g, nil, g.windows)
		for _, p := range g.plans {
			p.windows.rotate(epoch)
			if p.windows.current.Observations+p.windows.previous.Observations > 0 {
				snapshot.Coverage.ObservedPlans++
			}
			if p.since.After(snapshot.WindowStart) {
				snapshot.Coverage.PartialWindowPlans++
			}
			add(g, p, p.windows)
		}
	}
	snapshot.Coverage.Incomplete = snapshot.Coverage.Incomplete || snapshot.Coverage.ContentionDroppedTotal > 0 || snapshot.Coverage.UntrackedObservations > 0 || snapshot.Coverage.UnattributedEvaluations > 0 || snapshot.Coverage.UnknownWallObservations > 0 || snapshot.Coverage.PartialWindowGroups > 0 || snapshot.Coverage.ObservedGroups != snapshot.Coverage.TrackedGroups || snapshot.Coverage.ObservedPlans != snapshot.Coverage.TrackedPlans
	indexes := make(map[costCandidate]int)
	for dim, rows := range rankings {
		ranking := CostRanking{Dimension: costDimensions[dim%len(costDimensions)], Scope: "query_group"}
		if dim >= len(costDimensions) {
			ranking.Scope = "strategy_owned"
		}
		for _, candidate := range rows {
			candidate.value = 0
			index, exists := indexes[candidate]
			if !exists {
				w, since := candidate.group.windows, candidate.group.since
				row := CostContributor{Scope: "query_group", Group: candidate.group.group}
				if candidate.plan != nil {
					row.Scope, row.Plan = "strategy_owned", candidate.plan.identity
					row.Group.Members = nil
					w, since = candidate.plan.windows, candidate.plan.since
				} else {
					row.Group.Members = append([]CostPlanIdentity(nil), row.Group.Members...)
				}
				row.TrackedSince, row.Observed, row.Current, row.Previous = since, w.current.Observations+w.previous.Observations > 0, w.current, w.previous
				index = len(snapshot.Contributors)
				indexes[candidate] = index
				snapshot.Contributors = append(snapshot.Contributors, row)
			}
			ranking.Indexes = append(ranking.Indexes, index)
		}
		snapshot.Rankings = append(snapshot.Rankings, ranking)
	}
	c.snapshot = snapshot
}

func costCandidateLess(a, b costCandidate) bool {
	if a.group.group.QueryGroupKey != b.group.group.QueryGroupKey {
		return a.group.group.QueryGroupKey < b.group.group.QueryGroupKey
	}
	if a.plan == nil || b.plan == nil {
		return a.plan == nil && b.plan != nil
	}
	aID, bID := a.plan.identity, b.plan.identity
	if aID.TenantID != bID.TenantID {
		return aID.TenantID < bID.TenantID
	}
	if aID.BusinessID != bID.BusinessID {
		return aID.BusinessID < bID.BusinessID
	}
	return aID.StrategyID < bID.StrategyID
}

// Snapshot returns an independent copy of the last published candidates, not a
// fold over live objects. Callers can serialize/mutate it without racing Observe.
func (c *CostSummary) Snapshot() CostSnapshot {
	if c == nil {
		return CostSnapshot{DisabledReason: "resource_budget_missing", Coverage: CostCoverage{Incomplete: true}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.snapshot
	out.Contributors = append([]CostContributor(nil), out.Contributors...)
	for i := range out.Contributors {
		out.Contributors[i].Group.Members = append([]CostPlanIdentity(nil), out.Contributors[i].Group.Members...)
	}
	out.Rankings = append([]CostRanking(nil), out.Rankings...)
	for i := range out.Rankings {
		out.Rankings[i].Indexes = append([]int(nil), out.Rankings[i].Indexes...)
	}
	return out
}
