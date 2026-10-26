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
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The overview: whether this deployment detects what it can, on time and
// whole, in a few conclusions and numbers for a page that shows nothing
// else. It is a projection of the health route's answer, cached for
// OverviewMaxAge, and carries no key, Query Group or Slot: detail is for the
// CLI. A dependency carries its address, which names the instance a reader
// goes to next (the link's infrastructure page for one it shares) and is
// never a credential.

// OverviewMaxAge is how long one computation answers; OverviewMaxBytes
// bounds the answer at the largest fleet.
const (
	OverviewMaxAge   = 15 * time.Second
	OverviewMaxBytes = 256 << 10
	// overviewComputeTimeout bounds one computation, which no caller's
	// context does: they all wait on it.
	overviewComputeTimeout = 10 * time.Second
)

// The states a conclusion, a stage or a dependency is in, closed.
const (
	OverviewOK            = "ok"
	OverviewAttention     = "attention"
	OverviewUnknown       = "unknown"
	OverviewNotConfigured = "not_configured"
)

// OverviewNumber is a number with its unit, or the fact that it is not
// measured: never a zero standing in for "not known".
type OverviewNumber struct {
	Value    float64 `json:"value"`
	Unit     string  `json:"unit,omitempty"`
	Measured bool    `json:"measured"`
	// AtLeast is a quantile past a histogram's last bound: the value is that
	// bound, and the truth is at least it.
	AtLeast bool `json:"at_least,omitempty"`
}

func overviewMeasured(value float64, unit string) OverviewNumber {
	return OverviewNumber{Value: value, Unit: unit, Measured: true}
}

func overviewUnmeasured(unit string) OverviewNumber { return OverviewNumber{Unit: unit} }

type Overview struct {
	// StoreOutages is this deployment's own store outages as the page's
	// line: the windows Redis not answering cost, with the interval the
	// line speaks for (StoreOutagesResponse, without the rows; outage.list
	// lists them).
	StoreOutages  StoreOutageSummary `json:"store_outages"`
	GeneratedAt   time.Time          `json:"generated_at"`
	MaxAgeSeconds int                `json:"max_age_seconds"`
	Build         OverviewBuild      `json:"build"`
	Verdict       OverviewVerdict    `json:"verdict"`
	Capability    OverviewCapability `json:"capability"`
	Flow          []OverviewStage    `json:"flow"`
	// FlowCoverage is how many replicas the flow's numbers are from.
	FlowCoverage OverviewCoverage  `json:"flow_coverage"`
	Resources    OverviewResources `json:"resources"`
	Runtime      OverviewRuntime   `json:"runtime"`
	// Tracing is the span export the replicas run as one line: on with the
	// collector's host and sampling, not configured, mixed or unknown. Never
	// the endpoint's path or the token.
	Tracing OverviewTracing `json:"tracing"`
	// Late is the evaluate stage's late and overdue objects as the page's
	// one line, with the strategy page's selection for them.
	Late OverviewLate `json:"late"`
	// CauseReasons is the anomaly rows' cause reasons as /api/objects
	// summarizes them (Summary.ByCauseReason and its four origins), so the
	// one read every channel has carries whose each degraded round is
	// without a second route to the object list.
	CauseReasons OverviewCauseReasons `json:"cause_reasons"`
}

// OverviewCauseReasons is Summary's cause reasons: all of them, and split by
// whose the reason is - physical, carried, derived, unknown - the four
// adding up to the first. Partial says some replica's anomaly rows were cut
// to its budget, or a replica published no counts: the distributions are
// then a floor.
type OverviewCauseReasons struct {
	ByCauseReason         Distribution `json:"by_cause_reason"`
	ByCauseReasonPhysical Distribution `json:"by_cause_reason_physical"`
	ByCauseReasonCarried  Distribution `json:"by_cause_reason_carried"`
	ByCauseReasonDerived  Distribution `json:"by_cause_reason_derived"`
	ByCauseReasonUnknown  Distribution `json:"by_cause_reason_unknown"`
	Partial               bool         `json:"partial,omitempty"`
}

// overviewCauseReasonsOf is the replicas' merged counts as the overview
// carries them. The overview reads replica summaries, whose heads hold no
// rows, so the counts travel in each summary's part, counted at the replica
// from the rows it published (ReplicaPartOf).
func overviewCauseReasonsOf(part ReplicaPart) OverviewCauseReasons {
	// No counts at all - no replica read, the registry unreadable - is not
	// "nothing degraded": it reads partial, as a replica without counts does.
	counts, missing := part.CauseReasons, part.CauseReasonsMissing
	if counts == nil {
		counts, missing = newCauseReasonCounts(), true
	}
	return OverviewCauseReasons{ByCauseReason: rank(counts.All),
		ByCauseReasonPhysical: rank(counts.Physical), ByCauseReasonCarried: rank(counts.Carried),
		ByCauseReasonDerived: rank(counts.Derived), ByCauseReasonUnknown: rank(counts.Unknown),
		Partial: missing || columnCut(part.Truncated, ColumnAnomalies)}
}

// CauseReasonCounts counts anomaly rows' cause reasons: all of them, and by
// whose each is (Anomaly.CauseOrigin). Counts add across replicas, as each
// object is one replica's.
type CauseReasonCounts struct {
	All      map[string]int `json:"all"`
	Physical map[string]int `json:"physical"`
	Carried  map[string]int `json:"carried"`
	Derived  map[string]int `json:"derived"`
	Unknown  map[string]int `json:"unknown"`
}

func newCauseReasonCounts() *CauseReasonCounts {
	return &CauseReasonCounts{All: map[string]int{}, Physical: map[string]int{}, Carried: map[string]int{},
		Derived: map[string]int{}, Unknown: map[string]int{}}
}

// add counts one row's cause reason under its origin; a row without one is
// not counted. Unknown is never counted as physical.
func (counts *CauseReasonCounts) add(anomaly Anomaly) {
	if anomaly.CauseReason == "" {
		return
	}
	counts.All[anomaly.CauseReason]++
	origin := ""
	if anomaly.CauseOrigin != nil {
		origin = anomaly.CauseOrigin.Origin
	}
	switch origin {
	case observability.ReasonOriginPhysical:
		counts.Physical[anomaly.CauseReason]++
	case observability.ReasonOriginCarried:
		counts.Carried[anomaly.CauseReason]++
	case CauseOriginDerived:
		counts.Derived[anomaly.CauseReason]++
	default:
		counts.Unknown[anomaly.CauseReason]++
	}
}

func (counts *CauseReasonCounts) merge(other *CauseReasonCounts) {
	for _, pair := range [][2]map[string]int{{counts.All, other.All}, {counts.Physical, other.Physical},
		{counts.Carried, other.Carried}, {counts.Derived, other.Derived}, {counts.Unknown, other.Unknown}} {
		for reason, count := range pair[1] {
			pair[0][reason] += count
		}
	}
}

// BoardDrill is a strategy.board selection the overview writes for a count it
// shows: the board's parameters as they are passed, so the page links to the
// strategy page without composing one.
type BoardDrill map[string]string

// OverviewLate is "N 个检测对象迟到或超期": N is the scheduling census, the
// number the evaluate stage's failures carry. The strategies they touch (M)
// and whether either is a lower bound are read from strategy.board with
// Drill: the board's facets and the strategy page's list are one
// computation, so M there is the list's count by construction, which a
// number computed here would not be.
type OverviewLate struct {
	Objects OverviewNumber `json:"objects"`
	Line    string         `json:"line"`
	Drill   BoardDrill     `json:"drill"`
}

// overviewLateDrill selects the strategies with a late or overdue object.
var overviewLateDrill = BoardDrill{"punctuality": string(PunctualityLate) + "," + string(PunctualityOverdue)}

type OverviewBuild struct {
	Versions []OverviewVersion `json:"versions"`
	// Rolling is more than one build answering at once.
	Rolling bool `json:"rolling"`
}

type OverviewVersion struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Replicas int    `json:"replicas"`
}

type OverviewVerdict struct {
	State string `json:"state"`
	Line  string `json:"line"`
	// Stage is the part of the flow a degradation is about: pull,
	// evaluate, state, output or resources; empty when none is named.
	Stage string `json:"stage,omitempty"`
}

// OverviewReasonRows is how many refusal reasons the overview names; the
// rest are counted.
const OverviewReasonRows = 5

// OverviewCapability is the strategy funnel and the capability matrix: the
// Leader's published count of strategies per area and outcome
// (SourceFacts.Capability), unmeasured on a Leader that did not publish it.
type OverviewCapability struct {
	Funnel          OverviewFunnel          `json:"funnel"`
	Measured        bool                    `json:"measured"`
	Matrix          []OverviewCapabilityRow `json:"matrix"`
	Reasons         []OverviewReason        `json:"reasons"`
	ReasonsOmitted  OverviewOmitted         `json:"reasons_omitted"`
	IgnoredByDesign []OverviewIgnoredReason `json:"ignored_by_design"`
}

type OverviewCapabilityRow struct {
	Area                string `json:"area"`
	Supported           int    `json:"supported"`
	Partial             int    `json:"partial"`
	UnsupportedByDesign int    `json:"unsupported_by_design"`
	NotYetSupported     int    `json:"not_yet_supported"`
	// Drill is each non-zero cell's selection on the strategy page, by
	// outcome; the board counts the cell as the Leader does.
	Drill map[string]BoardDrill `json:"drill"`
}

// OverviewReason is one refusal reason with what it means, in the words the
// page shows for it elsewhere.
type OverviewReason struct {
	Code       string   `json:"code"`
	Area       string   `json:"area"`
	Outcome    string   `json:"outcome"`
	What       string   `json:"what"`
	Strategies int      `json:"strategies"`
	SampleIDs  []string `json:"sample_ids"`
	// Drill selects the strategies carrying this code on the strategy page.
	Drill BoardDrill `json:"drill"`
}

type OverviewOmitted struct {
	Kinds      int `json:"kinds"`
	Strategies int `json:"strategies"`
}

type OverviewIgnoredReason struct {
	Code       string `json:"code"`
	Strategies int    `json:"strategies"`
}

// OverviewFunnel is the strategy cache's count, how many of them became
// Plans, and how many are detecting -- the last from the replicas' strategy
// states, unmeasured when they did not report them.
type OverviewFunnel struct {
	Listed    int            `json:"listed"`
	Accepted  int            `json:"accepted"`
	Detecting OverviewNumber `json:"detecting"`
}

// OverviewStage is one part of the flow -- pull, evaluate, state, output --
// with its volume, failures and latency, and the dependencies it needs.
type OverviewStage struct {
	Stage        string         `json:"stage"`
	State        string         `json:"state"`
	Volume       OverviewNumber `json:"volume"`
	Failures     OverviewNumber `json:"failures"`
	LatencyP95MS OverviewNumber `json:"latency_p95_ms"`
	// FailureCauses splits the pull stage's failures by cause, per minute.
	FailureCauses []OverviewCauseCount `json:"failure_causes,omitempty"`
	// Basis names the counters the numbers are rates of, so they can be
	// read against the same counters' charts.
	Basis        string               `json:"basis"`
	Dependencies []OverviewDependency `json:"dependencies"`
}

type OverviewCauseCount struct {
	Cause string  `json:"cause"`
	Count float64 `json:"count"`
}

// OverviewDependency is one external system a stage needs, as the answering
// replica sees it: its state, where it is, and its latest answer and
// failure. LatencyP95MS and FailuresPerMinute are present only where the
// stage measures them for this dependency alone -- the query backend's
// queries, the runtime Redis's round trips -- and are the stage's own
// numbers; the other dependencies have no per-call histogram, and say so by
// leaving them out rather than borrowing one.
type OverviewDependency struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// Address is the entry's host:port (several joined with commas, or a
	// sentinel's master name and addresses); never a credential.
	Address    string `json:"address,omitempty"`
	SharedWith string `json:"shared_with,omitempty"`
	// LastSuccessAgeSeconds and LastFailureAgeSeconds are how long ago the
	// replica last had an answer from it and last had a failure; LastFailure
	// what that failure said, sanitised and bounded.
	LastSuccessAgeSeconds *float64        `json:"last_success_age_seconds,omitempty"`
	LastFailureAgeSeconds *float64        `json:"last_failure_age_seconds,omitempty"`
	LastFailure           string          `json:"last_failure,omitempty"`
	LatencyP95MS          *OverviewNumber `json:"latency_p95_ms,omitempty"`
	FailuresPerMinute     *OverviewNumber `json:"failures_per_minute,omitempty"`
	// ConsoleState is the Console's own word, on the Console only
	// (LinkdConsoleStates), and EventSourceID how the link names this
	// deployment's event source, as its Console last answered.
	ConsoleState  string `json:"console_state,omitempty"`
	EventSourceID string `json:"event_source_id,omitempty"`
}

// overviewDependencyOf is one endpoint as the overview shows it.
func overviewDependencyOf(endpoint Endpoint) OverviewDependency {
	dependency := OverviewDependency{Name: endpoint.Role, State: endpointState(endpoint), Address: endpoint.Address, SharedWith: endpoint.SharedWith,
		LastSuccessAgeSeconds: endpoint.LastSuccessAgeSeconds, LastFailureAgeSeconds: endpoint.LastFailureAgeSeconds, LastFailure: endpoint.LastFailure}
	if console := endpoint.Console; console != nil {
		dependency.ConsoleState = console.State
		if console.EventSource != nil {
			dependency.EventSourceID = console.EventSource.EventSourceID
		}
	}
	return dependency
}

type OverviewResources struct {
	Scale    OverviewScale             `json:"scale"`
	Replicas []OverviewReplicaResource `json:"replicas"`
	// Fleet is the replicas' memory and query permits added up, as shares of
	// their limits added up; MostConstrained the replica closest to a limit
	// past the line the overview names one at, and Line the sentence for
	// both, which is the page's one line about resources.
	Fleet           OverviewFleetShares  `json:"fleet"`
	MostConstrained *OverviewConstrained `json:"most_constrained,omitempty"`
	Line            string               `json:"line"`
	// Constraint is the load judgment's bottleneck, as the health route
	// decides it.
	Constraint string `json:"constraint"`
}

type OverviewScale struct {
	Strategies    OverviewNumber `json:"strategies"`
	MergedQueries OverviewNumber `json:"merged_queries"`
	Series        OverviewNumber `json:"series"`
	Hosts         OverviewNumber `json:"hosts"`
}

type OverviewReplicaResource struct {
	Name                string          `json:"name"`
	MemoryRatio         OverviewNumber  `json:"memory_ratio"`
	LiveHeapBytes       OverviewNumber  `json:"live_heap_bytes"`
	QueryPermits        OverviewPermits `json:"query_permits"`
	CPUThrottledSeconds OverviewNumber  `json:"cpu_throttled_seconds"`
}

type OverviewPermits struct {
	Held  int `json:"held"`
	Limit int `json:"limit"`
	// Peak is the most permits held at once in the last minute, and
	// MemorySource where the memory the limit is derived from came from
	// (fallback_default when not the container's); both absent when the
	// replica reported no capacity.
	Peak         *int   `json:"peak,omitempty"`
	MemorySource string `json:"memory_source,omitempty"`
}

type OverviewRuntime struct {
	Replicas  []OverviewReplica `json:"replicas"`
	Attention []OverviewNotice  `json:"attention"`
	// Summary counts the replicas by the roles they run and their version,
	// ready and not: what the page shows without opening the replica table.
	Summary []OverviewReplicaGroup `json:"summary"`
}

type OverviewReplicaGroup struct {
	Roles    []string `json:"roles"`
	Version  string   `json:"version"`
	Ready    int      `json:"ready"`
	NotReady int      `json:"not_ready"`
}

type OverviewFleetShares struct {
	MemoryRatio      OverviewNumber `json:"memory_ratio"`
	QueryPermitRatio OverviewNumber `json:"query_permit_ratio"`
}

// OverviewConstrained is the replica closest to one of its limits:
// memory, or the query permits.
type OverviewConstrained struct {
	Replica    string  `json:"replica"`
	Constraint string  `json:"constraint"`
	Ratio      float64 `json:"ratio"`
}

// The shares past which the overview names a replica as constrained: its
// memory at four fifths of its limit, the line the capacity budgets start
// trimming at, or its query permits nine in ten held.
const (
	overviewMemoryLine = 0.8
	overviewPermitLine = 0.9
)

type OverviewReplica struct {
	Name          string   `json:"name"`
	Roles         []string `json:"roles,omitempty"`
	Version       string   `json:"version"`
	UptimeSeconds float64  `json:"uptime_seconds"`
	Ready         bool     `json:"ready"`
	MergedQueries int      `json:"merged_queries"`
	State         string   `json:"state"`
	// Tracing is the span export this replica runs, as it published it.
	Tracing *TracingFacts `json:"tracing,omitempty"`
}

type OverviewNotice struct {
	Kind string `json:"kind"`
	Line string `json:"line"`
}

// overviewStages is the flow in order, and overviewStageOf the stage each
// dependency role hangs on: the query backend and the host cache feed the
// pull, the strategy and target sources the evaluation, the runtime store
// the state, and Kafka and the alert link the output.
var overviewStages = []string{"pull", "evaluate", "state", "output"}

var overviewStageOf = map[string]string{
	"query_backend": "pull", "cmdb_cache": "pull",
	"strategy_cache": "evaluate", "dynamic_config": "evaluate", "target_group": "evaluate",
	"state_redis":  "state",
	"output_kafka": "output", "compat_output_redis": "output", "linkd_console": "output", "open_alert_set": "output",
}

// overviewDegradationStage is the stage each degradation is about; one not
// listed is the evaluation's, which every other one ends up holding back.
var overviewDegradationStage = map[DegradationKind]string{
	DegradationOpenAlertSetStale:       "output",
	DegradationOpenAlertSetUnconfirmed: "output",
}

// overviewOf projects the health route's answer, and the replicas'
// strategy states when they reported them, onto the overview. Runtime.Summary
// and Resources.Line are filled only by overviewFlows.apply, so a caller of
// overviewOf alone gets them empty.
func overviewOf(health HealthResponse, running map[StateWord]int, runningKnown bool, at time.Time) Overview {
	overview := Overview{GeneratedAt: at.UTC(), MaxAgeSeconds: int(OverviewMaxAge / time.Second),
		Flow: make([]OverviewStage, 0, len(overviewStages))}
	for _, group := range health.Builds {
		overview.Build.Versions = append(overview.Build.Versions, OverviewVersion{Version: group.Build.Version, Commit: group.Build.Commit, Replicas: len(group.Replicas)})
	}
	overview.Build.Rolling = len(overview.Build.Versions) > 1
	if overview.Build.Versions == nil {
		overview.Build.Versions = []OverviewVersion{}
	}

	funnel := OverviewFunnel{Detecting: overviewUnmeasured("strategies")}
	if standing := health.SourceStanding; standing != nil {
		funnel.Listed, funnel.Accepted = standing.Listed, standing.Accepted
	}
	if runningKnown {
		funnel.Detecting = overviewMeasured(float64(running[StateDetecting]), "strategies")
	}
	overview.Capability = overviewCapabilityOf(health.Source)
	overview.Capability.Funnel = funnel
	overview.Verdict = overviewVerdictOf(health, funnel)
	overview.Late = OverviewLate{Objects: overviewUnmeasured("objects"), Line: "迟到或超期的检测对象数还没读到", Drill: overviewLateDrill}
	if health.Schedule != nil {
		late := health.Schedule.Late + health.Schedule.Overdue
		overview.Late.Objects = overviewMeasured(float64(late), "objects")
		overview.Late.Line = fmt.Sprintf("%d 个检测对象迟到或超期", late)
	}

	degradedReplicas := map[string]bool{}
	for _, degradation := range health.Degradations {
		degradedReplicas[degradation.Replica] = true
	}
	for _, stage := range overviewStages {
		row := OverviewStage{Stage: stage, State: OverviewOK, Volume: overviewUnmeasured("per_second"), Failures: overviewUnmeasured("count"),
			LatencyP95MS: overviewUnmeasured("ms"), Dependencies: []OverviewDependency{}}
		for _, endpoint := range health.Dependencies {
			if overviewStageOf[endpoint.Role] != stage {
				continue
			}
			dependency := overviewDependencyOf(endpoint)
			row.Dependencies = append(row.Dependencies, dependency)
			if dependency.State == OverviewAttention {
				row.State = OverviewAttention
			}
		}
		if stage == "evaluate" && health.Schedule != nil {
			row.Volume = overviewMeasured(float64(health.Schedule.Completed1h)/3600, "per_second")
			row.Failures = overviewMeasured(float64(health.Schedule.Late+health.Schedule.Overdue), "count")
			if health.Schedule.Overdue > 0 {
				row.State = OverviewAttention
			}
		}
		if overview.Verdict.Stage == stage {
			row.State = OverviewAttention
		}
		overview.Flow = append(overview.Flow, row)
	}

	overview.Resources = OverviewResources{Constraint: string(health.Load.Bottleneck.Resource), Replicas: []OverviewReplicaResource{},
		Scale: OverviewScale{Strategies: overviewUnmeasured("strategies"), MergedQueries: overviewUnmeasured("merged_queries"), Series: overviewUnmeasured("series"), Hosts: overviewUnmeasured("hosts")}}
	if health.SourceStanding != nil {
		overview.Resources.Scale.Strategies = overviewMeasured(float64(health.SourceStanding.Accepted), "strategies")
	}
	if health.Expected != nil {
		overview.Resources.Scale.MergedQueries = overviewMeasured(float64(*health.Expected), "merged_queries")
	}
	overview.Tracing = overviewTracingOf(health.Tracings)
	overview.Runtime = OverviewRuntime{Replicas: []OverviewReplica{}, Attention: []OverviewNotice{}}
	for _, replica := range health.PerReplica {
		resource := OverviewReplicaResource{Name: replica.Replica, MemoryRatio: overviewUnmeasured("ratio"),
			LiveHeapBytes: overviewUnmeasured("bytes"), CPUThrottledSeconds: overviewUnmeasured("seconds")}
		if capacity := replica.Capacity; capacity != nil {
			peak := capacity.PermitsPeak
			resource.QueryPermits = OverviewPermits{Held: capacity.PermitsHeld, Limit: capacity.PermitBudget, Peak: &peak, MemorySource: capacity.MemorySource}
			if capacity.MemoryLimit > 0 {
				resource.MemoryRatio = overviewMeasured(float64(capacity.MemoryUsed)/float64(capacity.MemoryLimit), "ratio")
			}
			if capacity.ThrottledKnown {
				resource.CPUThrottledSeconds = overviewMeasured(capacity.ThrottledSeconds, "seconds")
			}
		}
		overview.Resources.Replicas = append(overview.Resources.Replicas, resource)
		row := OverviewReplica{Name: replica.Replica, UptimeSeconds: replica.UptimeSeconds, MergedQueries: replica.Owned, State: OverviewOK,
			Tracing: CopyTracingFacts(replica.Tracing)}
		if replica.Build != nil {
			row.Version = replica.Build.Version
		}
		if replica.Readiness != nil {
			row.Ready = replica.Readiness.Ready
		}
		if degradedReplicas[replica.Replica] || !row.Ready {
			row.State = OverviewAttention
		}
		overview.Runtime.Replicas = append(overview.Runtime.Replicas, row)
	}
	if notice, tight := overviewTightIntervalNotice(health); tight {
		overview.Runtime.Attention = append(overview.Runtime.Attention, notice)
	}
	if activation := health.Activation; activation != nil && activation.BehindBeyondBound {
		overview.Runtime.Attention = append(overview.Runtime.Attention, OverviewNotice{Kind: "config_behind",
			Line: fmt.Sprintf("已发布的策略配置还没在检测里生效：连续 %d 轮没有应用成功", activation.ConsecutiveFailures)})
	}
	return overview
}

// overviewTightIntervalNotice is the warning that some strategies' interval
// does not hold the measured post-query tail: their queries ran under the
// plan's frozen reserve in the last hour instead of the live one. They keep
// running - the notice says so - and it names them, so the strategy owner can
// lengthen the interval or the operator can look at what makes the tail long.
func overviewTightIntervalNotice(health HealthResponse) (OverviewNotice, bool) {
	seen := map[StrategyRef]bool{}
	var strategies []StrategyRef
	groups := 0
	var tail int64
	for _, replica := range health.PerReplica {
		if replica.Capacity == nil || replica.Capacity.Shape == nil {
			continue
		}
		shape := replica.Capacity.Shape
		groups += len(shape.TightQueryGroups)
		tail = max(tail, shape.PostQueryTailMillis)
		for _, ref := range shape.TightStrategies {
			if !seen[ref] {
				seen[ref] = true
				strategies = append(strategies, ref)
			}
		}
	}
	if groups == 0 {
		return OverviewNotice{}, false
	}
	sort.Slice(strategies, func(i, j int) bool {
		if strategies[i].BusinessID != strategies[j].BusinessID {
			return strategies[i].BusinessID < strategies[j].BusinessID
		}
		return strategies[i].StrategyID < strategies[j].StrategyID
	})
	names := make([]string, len(strategies))
	for index, ref := range strategies {
		names[index] = ref.StrategyID
	}
	line := fmt.Sprintf("%d 个检测对象的周期放不下查询后的实际耗时（%s），近一小时这些查询改按固定预留执行，检测照常进行", groups, time.Duration(tail)*time.Millisecond)
	if len(names) > 0 {
		line += "；涉及策略 " + strings.Join(names, "、")
	}
	return OverviewNotice{Kind: "interval_tight", Line: line}, true
}

func overviewVerdictOf(health HealthResponse, funnel OverviewFunnel) OverviewVerdict {
	switch health.Health {
	case HealthHealthy:
		line := fmt.Sprintf("%d 条可用策略都在检测范围内", funnel.Accepted)
		if funnel.Detecting.Measured {
			line = fmt.Sprintf("%d 条可用策略中 %d 条在检测，其余在等数据或按配置不检测", funnel.Accepted, int(funnel.Detecting.Value))
		}
		return OverviewVerdict{State: OverviewOK, Line: line}
	case HealthDegraded:
		verdict := OverviewVerdict{State: OverviewAttention, Line: "检测需要关注：alarmd 自己有问题要处理", Stage: "evaluate"}
		if len(health.Degradations) > 0 {
			first := health.Degradations[0]
			if stage, named := overviewDegradationStage[first.Kind]; named {
				verdict.Stage = stage
			}
			verdict.Line = fmt.Sprintf("检测需要关注：%d 项运行状态超出设计界", len(health.Degradations))
		}
		return verdict
	default:
		return OverviewVerdict{State: OverviewUnknown, Line: "有策略现在说不出状态：刚启动或刚滚动时在预热，或者有副本没报告"}
	}
}

// endpointState is a dependency's state on the overview: not configured,
// failing (its latest word is a failure), working, or not yet known.
func endpointState(endpoint Endpoint) string {
	switch {
	case !endpoint.Configured:
		return OverviewNotConfigured
	case endpoint.Console != nil && (endpoint.Console.State == LinkdConsoleUnreadable || endpoint.Console.State == LinkdConsoleLinkUnhealthy):
		// The Console's own word: an operation whose latest call failed
		// fails a close whatever answered last, and a link that says it is
		// behind lists a roster that may miss strategies.
		return OverviewAttention
	case endpoint.LastFailureAgeSeconds != nil && (endpoint.LastSuccessAgeSeconds == nil || *endpoint.LastFailureAgeSeconds < *endpoint.LastSuccessAgeSeconds):
		return OverviewAttention
	case endpoint.LastSuccessAgeSeconds != nil || (endpoint.Ready != nil && *endpoint.Ready):
		return OverviewOK
	default:
		return OverviewUnknown
	}
}

// overviewCache answers from one computation for OverviewMaxAge; callers
// arriving while it is computed wait for it rather than computing again.
type overviewCache struct {
	now     func() time.Time
	compute func() Overview

	mu       sync.Mutex
	value    Overview
	at       time.Time
	inFlight *sync.WaitGroup
}

func newOverviewCache(now func() time.Time, compute func() Overview) *overviewCache {
	return &overviewCache{now: now, compute: compute}
}

func (cache *overviewCache) get() Overview {
	cache.mu.Lock()
	for {
		if !cache.at.IsZero() && cache.now().Sub(cache.at) < OverviewMaxAge {
			value := cache.value
			cache.mu.Unlock()
			return value
		}
		if cache.inFlight == nil {
			break
		}
		wait := cache.inFlight
		cache.mu.Unlock()
		wait.Wait()
		cache.mu.Lock()
	}
	wait := &sync.WaitGroup{}
	wait.Add(1)
	cache.inFlight = wait
	cache.mu.Unlock()
	var value Overview
	computed := false
	// Released however compute ends: a computation that panics caches
	// nothing, its waiters compute again, and the panic goes on to its own
	// caller -- left set, every later caller would wait on it forever,
	// each holding a request slot of the channel.
	defer func() {
		cache.mu.Lock()
		cache.inFlight = nil
		if computed {
			cache.value, cache.at = value, cache.now()
		}
		cache.mu.Unlock()
		wait.Done()
	}()
	value = cache.compute()
	value.MaxAgeSeconds = int(OverviewMaxAge / time.Second)
	computed = true
	return value
}

func overviewCapabilityOf(source *SourceFacts) OverviewCapability {
	capability := OverviewCapability{Matrix: []OverviewCapabilityRow{}, Reasons: []OverviewReason{}, IgnoredByDesign: []OverviewIgnoredReason{}}
	if source == nil || source.Capability == nil {
		return capability
	}
	capability.Measured = true
	capability.Matrix = make([]OverviewCapabilityRow, len(CapabilityAreas))
	rows := make(map[string]*OverviewCapabilityRow, len(CapabilityAreas))
	for index, area := range CapabilityAreas {
		capability.Matrix[index].Area = area
		capability.Matrix[index].Drill = map[string]BoardDrill{}
		rows[area] = &capability.Matrix[index]
	}
	for _, cell := range source.Capability.Cells {
		row := rows[cell.Area]
		if row == nil {
			continue
		}
		switch cell.Outcome {
		case CapabilitySupported:
			row.Supported = cell.Strategies
		case CapabilityPartial:
			row.Partial = cell.Strategies
		case CapabilityUnsupportedByDesign:
			row.UnsupportedByDesign = cell.Strategies
		case CapabilityNotYetSupported:
			row.NotYetSupported = cell.Strategies
		default:
			continue
		}
		if cell.Strategies > 0 {
			row.Drill[cell.Outcome] = BoardDrill{"area": cell.Area, "capability": cell.Outcome}
		}
	}
	for index, reason := range source.Capability.Reasons {
		if index >= OverviewReasonRows {
			capability.ReasonsOmitted.Kinds++
			capability.ReasonsOmitted.Strategies += reason.Strategies
			continue
		}
		capability.Reasons = append(capability.Reasons, OverviewReason{Code: reason.Code, Area: reason.Area, Outcome: reason.Outcome,
			What: WithheldWordsOf(reason.Code).What, Strategies: reason.Strategies, SampleIDs: reason.SampleIDs,
			Drill: BoardDrill{"reason": reason.Code}})
	}
	for _, reason := range source.Capability.IgnoredByDesign {
		capability.IgnoredByDesign = append(capability.IgnoredByDesign, OverviewIgnoredReason{Code: reason.Code, Strategies: reason.Strategies})
	}
	return capability
}

// overviewStageBasis is the counters each stage's numbers are rates of,
// over the last two computations.
var overviewStageBasis = map[string]string{
	"pull": "worker_work_total{work_kind=\"physical_query\"}、access_query_failure_total、access_query_duration_seconds，取最近两次计算的差",
	"evaluate": "progress_completed_total（不含 GAP_SKIPPED、SNAPSHOT_UNAVAILABLE）、slot_decision_lag_seconds（只含正常轮次及其重试，不含回放、探测与补充检测），取最近两次计算的差；" +
		"失败是此刻调度普查里迟到和超期的检测",
	"state":  "worker_work_total{work_kind=\"state_apply\"}、state_admission_total（非 success）与 state_version_conflict_total、redis_command_duration_seconds（运行态 Redis 往返；运行态独立连接时 client=\"runtime\"，与策略源共用连接时 client=\"source\"），取最近两次计算的差",
	"output": "output_events_by_kind_total、output_events_rejected_all_total，取最近两次计算的差；发送耗时没有度量",
}

// overviewReading is one replica's counters as one computation read them,
// with when they were taken and the process they belong to.
type overviewReading struct {
	takenAt, startedAt time.Time
	flow               FlowFacts
}

// overviewFlows turns the replicas' counters into the stages' numbers: the
// difference from the previous computation over the time between the two
// readings of each replica. A replica whose reading moved, in the same
// process, with no counter gone back, contributes that difference. One
// whose reading did not -- a missed publish, a restart -- contributes the
// rate it had the computation before, once, and is counted as carried;
// after that it contributes nothing, and the flow covers fewer replicas
// than publish (OverviewCoverage), which holds the verdict.
type overviewFlows struct {
	readings map[string]overviewReading
	// last is each replica's latest fresh contribution and whether it has
	// already been carried once.
	last map[string]carriedContribution
}

type carriedContribution struct {
	contribution flowContribution
	carried      bool
}

func newOverviewFlows() *overviewFlows {
	return &overviewFlows{readings: map[string]overviewReading{}, last: map[string]carriedContribution{}}
}

// flowContribution is one replica's rates and histogram differences between
// two of its readings.
type flowContribution struct {
	queries, detections, stateWrites, stateFailures, emitted, rejected float64
	failures                                                           map[string]float64
	queryBuckets, lagBuckets, redisBuckets                             []uint64
	queryCount, lagCount, redisCount                                   uint64
}

type flowSum struct {
	contributed bool
	flowContribution
}

// OverviewCoverage is how many replicas the flow's numbers are from, of the
// replicas that publish the counters: Carried of Counted are a missed
// publish's last rate.
type OverviewCoverage struct {
	Counted  int `json:"counted"`
	Carried  int `json:"carried,omitempty"`
	Expected int `json:"expected"`
	// Warming are the replicas seen for the first time by this process -- its
	// first computation, a scale-out, a rollout's new pods -- which have no
	// earlier reading to be a difference from. They are not expected yet and
	// do not hold the verdict; from their second computation they are.
	Warming int `json:"warming,omitempty"`
	// LayoutMismatched are the expected replicas whose histograms carry a
	// different number of buckets from this process's bounds - a build with
	// other bucket bounds, as during a rollout that changes them. Their
	// buckets cannot be added to this process's by position, so they are not
	// counted, and Counted falls short of Expected by them.
	LayoutMismatched int `json:"layout_mismatched,omitempty"`
}

func (flows *overviewFlows) apply(overview *Overview, health HealthResponse, at time.Time) {
	sum := flowSum{flowContribution: flowContribution{failures: map[string]float64{}, queryBuckets: make([]uint64, len(FlowQueryBounds)),
		lagBuckets: make([]uint64, len(FlowDecisionBounds)), redisBuckets: make([]uint64, len(FlowRedisBounds))}}
	next := make(map[string]overviewReading, len(health.PerReplica))
	last := make(map[string]carriedContribution, len(health.PerReplica))
	coverage := OverviewCoverage{}
	var hosts uint64
	hostsKnown := false
	for index, replica := range health.PerReplica {
		if replica.Flow == nil {
			continue
		}
		reading := overviewReading{takenAt: at.Add(-time.Duration(replica.AgeSeconds * float64(time.Second))), startedAt: replica.StartedAt, flow: *replica.Flow}
		next[replica.Replica] = reading
		if replica.Flow.LiveHeapBytes != nil && index < len(overview.Resources.Replicas) {
			overview.Resources.Replicas[index].LiveHeapBytes = overviewMeasured(float64(*replica.Flow.LiveHeapBytes), "bytes")
		}
		if index < len(overview.Runtime.Replicas) {
			overview.Runtime.Replicas[index].Roles = replica.Flow.Roles
		}
		if replica.Flow.Hosts > hosts {
			hosts, hostsKnown = replica.Flow.Hosts, true
		}
		// A replica this process has not read before has nothing to be a
		// difference from: it is warming, not missing (OverviewCoverage).
		previous, found := flows.readings[replica.Replica]
		if !found {
			coverage.Warming++
			continue
		}
		coverage.Expected++
		if !sameLayout(previous.flow) || !sameLayout(reading.flow) {
			// Not a difference this process can read: bucket i of the
			// replica's histogram is not bucket i of these bounds. The
			// reading is still the next one to measure from (a replica that
			// has rolled to these bounds is counted from its second reading),
			// and no earlier rate is carried for it.
			coverage.LayoutMismatched++
			continue
		}
		if contribution, fresh := contributionOf(previous, reading); fresh {
			sum.add(contribution)
			coverage.Counted++
			last[replica.Replica] = carriedContribution{contribution: contribution}
			continue
		}
		if moved := reading.takenAt.After(previous.takenAt); !moved {
			// The reading did not move: keep the older one, so the next
			// reading that does is measured from it.
			next[replica.Replica] = previous
		}
		if kept, had := flows.last[replica.Replica]; had && !kept.carried {
			sum.add(kept.contribution)
			coverage.Counted++
			coverage.Carried++
			last[replica.Replica] = carriedContribution{contribution: kept.contribution, carried: true}
		}
	}
	flows.readings, flows.last = next, last
	overview.FlowCoverage = coverage
	if coverage.Counted < coverage.Expected && overview.Verdict.State == OverviewOK {
		overview.Verdict = OverviewVerdict{State: OverviewAttention, Stage: "",
			Line: fmt.Sprintf("数据流只覆盖 %d/%d 个副本：有副本没有按时发布计数，下面各段的量少算了它的份", coverage.Counted, coverage.Expected)}
	}
	if hostsKnown {
		overview.Resources.Scale.Hosts = overviewMeasured(float64(hosts), "hosts")
	}
	overviewSummarize(overview, health)
	for index := range overview.Flow {
		stage := &overview.Flow[index]
		stage.Basis = overviewStageBasis[stage.Stage]
		if !sum.contributed {
			continue
		}
		switch stage.Stage {
		case "pull":
			stage.Volume = overviewMeasured(sum.queries, "per_second")
			total := 0.0
			for _, cause := range overviewQueryFailureOrder {
				if count := sum.failures[cause]; count > 0 {
					stage.FailureCauses = append(stage.FailureCauses, OverviewCauseCount{Cause: cause, Count: count})
					total += count
				}
			}
			stage.Failures = overviewMeasured(total, "per_minute")
			stage.LatencyP95MS = overviewQuantile(FlowQueryBounds, sum.queryBuckets, sum.queryCount)
		case "evaluate":
			stage.Volume = overviewMeasured(sum.detections, "per_second")
			stage.LatencyP95MS = overviewQuantile(FlowDecisionBounds, sum.lagBuckets, sum.lagCount)
		case "state":
			stage.Volume = overviewMeasured(sum.stateWrites, "per_second")
			stage.Failures = overviewMeasured(sum.stateFailures, "per_minute")
			stage.LatencyP95MS = overviewQuantile(FlowRedisBounds, sum.redisBuckets, sum.redisCount)
		case "output":
			stage.Volume = overviewMeasured(sum.emitted, "per_second")
			stage.Failures = overviewMeasured(sum.rejected, "per_minute")
		}
		switch stage.Stage {
		case "pull":
			overviewDependencyNumbers(stage, EndpointQueryBackend, &stage.LatencyP95MS, &stage.Failures)
		case "state":
			overviewDependencyNumbers(stage, EndpointStateRedis, &stage.LatencyP95MS, nil)
		}
		if stage.Failures.Measured && stage.Failures.Value > 0 && stage.State == OverviewOK {
			stage.State = OverviewAttention
		}
	}
}

// overviewDependencyNumbers puts the stage's numbers on the one dependency
// they measure alone: the pull's latency and failures are the query
// backend's, the state stage's latency the runtime Redis's round trips. A
// latency with no observations behind it is left off; the failures are
// always measured once the flow has contributed.
func overviewDependencyNumbers(stage *OverviewStage, role string, latency, failures *OverviewNumber) {
	for index := range stage.Dependencies {
		dependency := &stage.Dependencies[index]
		if dependency.Name != role {
			continue
		}
		if latency != nil && latency.Measured {
			value := *latency
			dependency.LatencyP95MS = &value
		}
		if failures != nil {
			value := *failures
			dependency.FailuresPerMinute = &value
		}
	}
}

// overviewQueryFailureOrder is the order the pull stage lists causes in.
var overviewQueryFailureOrder = []string{"timeout", "refused", "not_found", "transport", "decode", "other"}

// sameLayout says a replica's flow histograms have one bucket per bound of
// this process's: the only layout whose buckets can be added to this
// process's by position. An older build with ten decision-lag bounds read
// against fifteen would put its (10, 30] bucket at 15 s, and an overview
// with every replica counted would show a measured P95 the replica never had.
func sameLayout(flow FlowFacts) bool {
	return len(flow.QueryDuration.Buckets) == len(FlowQueryBounds) && len(flow.DecisionLag.Buckets) == len(FlowDecisionBounds) &&
		len(flow.RuntimeRedis.Buckets) == len(FlowRedisBounds)
}

// contributionOf is a replica's rates between two readings, and whether
// they are a difference of anything: the same process, a reading that
// moved, no counter gone back.
func contributionOf(previous, current overviewReading) (flowContribution, bool) {
	elapsed := current.takenAt.Sub(previous.takenAt).Seconds()
	was, now := previous.flow, current.flow
	if !previous.startedAt.Equal(current.startedAt) || elapsed <= 0 || now.Queries < was.Queries || now.Detections < was.Detections ||
		now.StateWrites < was.StateWrites || now.EventsEmitted < was.EventsEmitted || now.QueryDuration.Count < was.QueryDuration.Count {
		return flowContribution{}, false
	}
	contribution := flowContribution{failures: map[string]float64{}, queryBuckets: make([]uint64, len(FlowQueryBounds)),
		lagBuckets: make([]uint64, len(FlowDecisionBounds)), redisBuckets: make([]uint64, len(FlowRedisBounds))}
	contribution.queries = float64(now.Queries-was.Queries) / elapsed
	contribution.detections = float64(now.Detections-was.Detections) / elapsed
	contribution.stateWrites = float64(now.StateWrites-was.StateWrites) / elapsed
	contribution.emitted = float64(now.EventsEmitted-was.EventsEmitted) / elapsed
	contribution.stateFailures = float64(subtract(now.StateFailures, was.StateFailures)) / elapsed * 60
	contribution.rejected = float64(subtract(now.EventsRejected, was.EventsRejected)) / elapsed * 60
	for cause, count := range now.QueryFailures {
		contribution.failures[cause] = float64(subtract(count, was.QueryFailures[cause])) / elapsed * 60
	}
	contribution.queryCount = addBuckets(contribution.queryBuckets, now.QueryDuration, was.QueryDuration)
	contribution.lagCount = addBuckets(contribution.lagBuckets, now.DecisionLag, was.DecisionLag)
	contribution.redisCount = addBuckets(contribution.redisBuckets, now.RuntimeRedis, was.RuntimeRedis)
	return contribution, true
}

func (sum *flowSum) add(contribution flowContribution) {
	sum.contributed = true
	sum.queries += contribution.queries
	sum.detections += contribution.detections
	sum.stateWrites += contribution.stateWrites
	sum.emitted += contribution.emitted
	sum.stateFailures += contribution.stateFailures
	sum.rejected += contribution.rejected
	for cause, count := range contribution.failures {
		sum.failures[cause] += count
	}
	for index := range sum.queryBuckets {
		sum.queryBuckets[index] += contribution.queryBuckets[index]
	}
	for index := range sum.lagBuckets {
		sum.lagBuckets[index] += contribution.lagBuckets[index]
	}
	for index := range sum.redisBuckets {
		sum.redisBuckets[index] += contribution.redisBuckets[index]
	}
	sum.queryCount += contribution.queryCount
	sum.lagCount += contribution.lagCount
	sum.redisCount += contribution.redisCount
}

func subtract(now, was uint64) uint64 {
	if now < was {
		return 0
	}
	return now - was
}

// addBuckets adds one replica's bucket differences into the sum and returns
// its count's difference.
func addBuckets(into []uint64, now, was FlowHistogram) uint64 {
	for index := range into {
		var current, previous uint64
		if index < len(now.Buckets) {
			current = now.Buckets[index]
		}
		if index < len(was.Buckets) {
			previous = was.Buckets[index]
		}
		into[index] += subtract(current, previous)
	}
	return subtract(now.Count, was.Count)
}

// overviewQuantile is the 95th percentile in milliseconds from cumulative
// bucket counts, interpolated within its bucket; past the last bound it is
// that bound, said as at least. No observations is no number.
func overviewQuantile(bounds []float64, buckets []uint64, count uint64) OverviewNumber {
	if count == 0 {
		return overviewUnmeasured("ms")
	}
	target := 0.95 * float64(count)
	lower, below := 0.0, uint64(0)
	for index, bound := range bounds {
		if float64(buckets[index]) >= target {
			inBucket := buckets[index] - below
			fraction := 1.0
			if inBucket > 0 {
				fraction = (target - float64(below)) / float64(inBucket)
			}
			return overviewMeasured((lower+(bound-lower)*fraction)*1000, "ms")
		}
		lower, below = bound, buckets[index]
	}
	number := overviewMeasured(bounds[len(bounds)-1]*1000, "ms")
	number.AtLeast = true
	return number
}

// overviewSummarize writes the page's one resource line and its runtime
// count, from the replicas as the overview already has them.
func overviewSummarize(overview *Overview, health HealthResponse) {
	var used, limit uint64
	held, budget := 0, 0
	var most *OverviewConstrained
	for _, replica := range health.PerReplica {
		capacity := replica.Capacity
		if capacity == nil {
			continue
		}
		if capacity.MemoryLimit > 0 {
			used, limit = used+capacity.MemoryUsed, limit+capacity.MemoryLimit
			if ratio := float64(capacity.MemoryUsed) / float64(capacity.MemoryLimit); ratio >= overviewMemoryLine && (most == nil || ratio > most.Ratio) {
				most = &OverviewConstrained{Replica: replica.Replica, Constraint: "memory", Ratio: ratio}
			}
		}
		if capacity.PermitBudget > 0 {
			held, budget = held+capacity.PermitsHeld, budget+capacity.PermitBudget
			if ratio := float64(capacity.PermitsHeld) / float64(capacity.PermitBudget); ratio >= overviewPermitLine && (most == nil || ratio > most.Ratio) {
				most = &OverviewConstrained{Replica: replica.Replica, Constraint: "query_permits", Ratio: ratio}
			}
		}
	}
	fleet := OverviewFleetShares{MemoryRatio: overviewUnmeasured("ratio"), QueryPermitRatio: overviewUnmeasured("ratio")}
	if limit > 0 {
		fleet.MemoryRatio = overviewMeasured(float64(used)/float64(limit), "ratio")
	}
	if budget > 0 {
		fleet.QueryPermitRatio = overviewMeasured(float64(held)/float64(budget), "ratio")
	}
	overview.Resources.Fleet, overview.Resources.MostConstrained = fleet, most
	switch {
	case most != nil && most.Constraint == "memory":
		overview.Resources.Line = fmt.Sprintf("副本 %s 的内存到了上限的 %.0f%%，最先受限", most.Replica, most.Ratio*100)
	case most != nil:
		overview.Resources.Line = fmt.Sprintf("副本 %s 的查询许可占用到了 %.0f%%，查询并发最先受限", most.Replica, most.Ratio*100)
	case limit == 0 && budget == 0:
		overview.Resources.Line = "副本没有报告资源占用"
	default:
		overview.Resources.Line = fmt.Sprintf("各副本内存都在上限的 %.0f%% 以下、查询许可都在 %.0f%% 以下，资源不是约束", overviewMemoryLine*100, overviewPermitLine*100)
	}

	groups := []OverviewReplicaGroup{}
	index := map[string]int{}
	for position, replica := range overview.Runtime.Replicas {
		roles := append([]string(nil), replica.Roles...)
		sort.Strings(roles)
		key := strings.Join(roles, ",") + "|" + replica.Version
		at, seen := index[key]
		if !seen {
			at = len(groups)
			index[key] = at
			groups = append(groups, OverviewReplicaGroup{Roles: roles, Version: replica.Version})
		}
		if overview.Runtime.Replicas[position].Ready {
			groups[at].Ready++
		} else {
			groups[at].NotReady++
		}
	}
	overview.Runtime.Summary = groups
}
