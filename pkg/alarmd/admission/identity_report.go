// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// IdentityReport describes, for one plan, an object-identity rejection and
// the two sides that disagreed.
//
// A counter says how often a plan rejected records on object identity. It
// cannot say why, and the two causes call for different fixes: a record
// with no identity dimensions points at the writer or the query, while an
// identity the target did not name points at the representation - a model
// code on one side and a model id on the other looks exactly like an
// ordinary record outside the target. The report carries what the counter
// cannot: the pairs expected against the dimensions seen, or the keys built
// against the keys targeted, so the fix is read off the report rather than
// reconstructed from the environment.
type IdentityReport struct {
	TenantID   string
	BusinessID string
	StrategyID string
	// Reason is contract.TargetScopeReasonObjectIdentityMissing or
	// contract.TargetScopeReasonObjectIdentityUnmatched.
	Reason string
	// Count is how many rejections this report stands for: every one since
	// the previous report for the same plan and reason.
	Count uint64
	// ExpectedPairs and DimensionNames are set for a missing identity: the
	// (model, instance) dimension pairs the strategy identifies its objects
	// by, and the dimension names the rejected record actually carried.
	ExpectedPairs  [][2]string
	DimensionNames []string
	// CandidateKeys and TargetKeys are set for an unmatched identity: the
	// keys the record built and a sample of the keys the target names, at
	// most SampleKeys of each.
	CandidateKeys []string
	TargetKeys    []string
}

// SampleKeys bounds the keys one report shows of either side. Three is
// enough to see a representation mismatch and few enough that the line
// stays a line.
const SampleKeys = 3

// IdentityReporter turns per-record rejections into per-plan reports at a
// bounded rate.
//
// Rejections arrive once per series per plan, so the reporter's own cost has
// to be a lock and a map lookup: the sample is only assembled when a report
// is actually emitted. Each plan emits at most one report per reason per
// window. For a missing identity that is the whole rule, because a missing
// identity is a defect every time it happens. For an unmatched identity it
// is not: a target that names some instances legitimately rejects the
// others, and reporting every plan every minute would describe normal
// operation. The unmatched report is therefore held back while the plan has
// admitted a record through its object identity within the window - a
// target that matches something is written in the data's representation,
// and its rejections are just records outside it. A plan that only ever
// rejects is the one worth a line.
type IdentityReporter struct {
	now    func() time.Time
	window time.Duration
	sink   func(IdentityReport)

	mutex sync.Mutex
	plans map[planKey]*planReportState
	// lastSweep is when plans not seen for a while were last dropped, so the
	// map follows the catalog rather than growing with every plan that ever
	// existed.
	lastSweep time.Time
}

type planKey struct {
	tenant, business, strategy string
}

type planReportState struct {
	lastSeen     time.Time
	lastAdmitted time.Time
	missing      reasonWindow
	unmatched    reasonWindow
}

type reasonWindow struct {
	lastReported time.Time
	pending      uint64
}

// NewIdentityReporter builds a reporter that emits through sink at most once
// per plan, reason and window. A nil sink or a non-positive window yields a
// reporter that counts and never emits, which is still a valid reporter.
func NewIdentityReporter(now func() time.Time, window time.Duration, sink func(IdentityReport)) *IdentityReporter {
	if now == nil {
		now = time.Now
	}
	return &IdentityReporter{now: now, window: window, sink: sink, plans: make(map[planKey]*planReportState)}
}

// Admitted records that a plan matched a record through its object identity.
func (reporter *IdentityReporter) Admitted(plan PlanContext) {
	if reporter == nil {
		return
	}
	now := reporter.now()
	reporter.mutex.Lock()
	state := reporter.state(plan, now)
	state.lastAdmitted = now
	reporter.mutex.Unlock()
}

// Missing records a rejection because the record built no object identity.
// The dimensions are the caller's map; their names are read only when the
// report is emitted.
func (reporter *IdentityReporter) Missing(plan PlanContext, expected [][2]string, dimensions map[string]json.RawMessage) {
	if reporter == nil {
		return
	}
	now := reporter.now()
	reporter.mutex.Lock()
	state := reporter.state(plan, now)
	state.missing.pending++
	emit := reporter.sink != nil && reporter.window > 0 && state.missing.due(now, reporter.window)
	var report IdentityReport
	if emit {
		report = reporter.report(plan, contract.TargetScopeReasonObjectIdentityMissing, state.missing.pending)
		report.ExpectedPairs = append([][2]string(nil), expected...)
		report.DimensionNames = dimensionNames(dimensions)
		sort.Strings(report.DimensionNames)
		state.missing.reported(now)
	}
	reporter.sweep(now)
	reporter.mutex.Unlock()
	if emit {
		reporter.sink(report)
	}
}

// Unmatched records a rejection because the target named none of the keys
// the record built. The keys are the caller's; a sample of each side is
// taken only when the report is emitted.
func (reporter *IdentityReporter) Unmatched(plan PlanContext, candidates []string, targets map[string]struct{}) {
	if reporter == nil {
		return
	}
	now := reporter.now()
	reporter.mutex.Lock()
	state := reporter.state(plan, now)
	state.unmatched.pending++
	admittedRecently := !state.lastAdmitted.IsZero() && now.Sub(state.lastAdmitted) < reporter.window
	emit := reporter.sink != nil && reporter.window > 0 && !admittedRecently && state.unmatched.due(now, reporter.window)
	var report IdentityReport
	if emit {
		report = reporter.report(plan, contract.TargetScopeReasonObjectIdentityUnmatched, state.unmatched.pending)
		report.CandidateKeys = sampleKeys(candidates)
		report.TargetKeys = sampleKeySet(targets)
		state.unmatched.reported(now)
	}
	reporter.sweep(now)
	reporter.mutex.Unlock()
	if emit {
		reporter.sink(report)
	}
}

func (reporter *IdentityReporter) state(plan PlanContext, now time.Time) *planReportState {
	key := planKey{tenant: plan.TenantID, business: plan.BusinessID, strategy: plan.StrategyID}
	state, found := reporter.plans[key]
	if !found {
		state = &planReportState{}
		reporter.plans[key] = state
	}
	state.lastSeen = now
	return state
}

func (reporter *IdentityReporter) report(plan PlanContext, reason string, count uint64) IdentityReport {
	return IdentityReport{
		TenantID: plan.TenantID, BusinessID: plan.BusinessID, StrategyID: plan.StrategyID,
		Reason: reason, Count: count,
	}
}

// sweep drops plans not seen for ten windows. It runs under the lock and
// walks the map, so it is itself rate-limited to once per window.
func (reporter *IdentityReporter) sweep(now time.Time) {
	if reporter.window <= 0 || now.Sub(reporter.lastSweep) < reporter.window {
		return
	}
	reporter.lastSweep = now
	horizon := now.Add(-10 * reporter.window)
	for key, state := range reporter.plans {
		if state.lastSeen.Before(horizon) {
			delete(reporter.plans, key)
		}
	}
}

func (window *reasonWindow) due(now time.Time, span time.Duration) bool {
	return window.lastReported.IsZero() || now.Sub(window.lastReported) >= span
}

func (window *reasonWindow) reported(now time.Time) {
	window.lastReported = now
	window.pending = 0
}

func sampleKeys(keys []string) []string {
	sample := make([]string, 0, SampleKeys)
	for _, key := range keys {
		if len(sample) == SampleKeys {
			break
		}
		sample = append(sample, key)
	}
	return sample
}

// sampleKeySet takes the SampleKeys smallest keys, so the sample of one
// target is the same from one report to the next.
func sampleKeySet(keys map[string]struct{}) []string {
	all := make([]string, 0, len(keys))
	for key := range keys {
		all = append(all, key)
	}
	sort.Strings(all)
	return sampleKeys(all)
}
