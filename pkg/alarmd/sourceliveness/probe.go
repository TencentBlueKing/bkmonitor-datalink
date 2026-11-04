// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package sourceliveness asks, before a Level written to end on no data
// (status_setter -nodata) ends its alert, whether the source the strategy
// reads is alive at all: the same table in the same space, the strategy's own
// conditions removed, counted over the most recent periods at the strategy's
// own query offset. No data from a source that is itself down is "the link is
// broken", not "it stopped happening", and does not end the alert.
//
// The probe is derived at run time from the primary query's facts and never
// stored in them: QueryPlanFacts' digest is the frozen query revision
// (execution/query_plan.go), and a field added there would fail every
// published Plan.
//
// The first version has two outcomes that decide anything: alive ends the
// Level as configured, and unknown - everything else - keeps it open. There
// is no "silent" yet: an empty answer without a status is the shape a source
// that really stopped should have, but no such answer has been seen from a
// real outage, so it is unknown (empty_without_status) and claims no owner.
// A source this version cannot express a probe for is refused by name
// (SOURCE_LIVENESS_UNSUPPORTED) and ends as it did before, counted.
package sourceliveness

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Kind is the source a probe reads, as alarmd can see it from the facts. The
// storage behind it is not named: the facts carry none for a time series
// (every clause's driver is influxdb, and the routing map names ES only), and
// the query service picks InfluxDB or VictoriaMetrics itself, for the probe as
// for the strategy's own query (unify-query query/structured/query_ts.go
// 1182-1189). Logs are the same: ES or Doris is the query service's choice.
type Kind string

const (
	KindLog        Kind = "log"
	KindEvent      Kind = "event"
	KindTimeSeries Kind = "time_series"
)

// Kinds is the closed set, a metric label.
var Kinds = []Kind{KindLog, KindEvent, KindTimeSeries}

// Result is a probe's verdict.
type Result string

const (
	// ResultAlive: the source has data in the probe's window. The Level ends
	// on no data as configured.
	ResultAlive Result = "alive"
	// ResultUnknown: the probe could not say the source is alive. The Level
	// stays open (contract.RecoveryWithheldSourceUnknown).
	ResultUnknown Result = "unknown"
	// ResultUnsupported: no probe can be expressed for this source in this
	// version. The Level ends as it did before the probe existed.
	ResultUnsupported Result = "unsupported"
)

// Results is the closed set, a metric label.
var Results = []Result{ResultAlive, ResultUnknown, ResultUnsupported}

// Reason says why a probe is unknown or unsupported; empty on alive.
type Reason string

// The reasons of an unknown probe, closed.
const (
	// ReasonStatus: the answer carried a status (a space, table or field the
	// query service does not know, among others). An empty answer with a
	// status is not an empty source.
	ReasonStatus Reason = "status"
	// ReasonPartial: some of the routes failed (QUERY_TS_PARTIAL, or the
	// storage's is_partial).
	ReasonPartial Reason = "partial"
	// ReasonTimeout: no answer in time: the probe ran out of its own bound,
	// or had not come back when the round asked. The probe runs beside the
	// round, never inside it, so a probe still running keeps the Level open
	// this round, and a later round reads its answer.
	ReasonTimeout Reason = "timeout"
	// ReasonError: the probe failed: transport, an HTTP status, an answer that
	// does not decode.
	ReasonError Reason = "error"
	// ReasonEmptyWithoutStatus: answered whole, no status, no data. The shape
	// a source that stopped should have; not yet confirmed by a real sample,
	// so not called silent.
	ReasonEmptyWithoutStatus Reason = "empty_without_status"
)

// UnknownReasons is the closed set of an unknown probe's reasons.
var UnknownReasons = []Reason{ReasonStatus, ReasonPartial, ReasonTimeout, ReasonError, ReasonEmptyWithoutStatus}

// The reasons of an unsupported source, closed.
const (
	// ReasonPromQL: a PromQL strategy. Its facts hold the user's expression
	// alone, and nothing here parses PromQL into the table it reads.
	ReasonPromQL Reason = "promql"
	// ReasonBKData: a direct bkbase query (data_source bkdata).
	ReasonBKData Reason = "bkdata"
	// ReasonAPM: an APM metric or trace table.
	ReasonAPM Reason = "apm"
	// ReasonMultiTable: the query reads more than one table: which of them
	// would have to be alive is the expression's to say, not a probe's.
	ReasonMultiTable Reason = "multi_table"
	// ReasonDataLabel: a data label, which the query service expands into
	// several tables, possibly on several storages, and splits by time.
	ReasonDataLabel Reason = "data_label"
	// ReasonEventSparse: an event source. Events are sparse by nature: a
	// whole event table with nothing in two periods is as often a quiet
	// table as a broken one, and the probe cannot tell them apart, so an
	// event strategy's no-data end goes ahead as configured.
	ReasonEventSparse Reason = "event_sparse"
)

// UnsupportedReasons is the closed set of an unsupported source's reasons.
var UnsupportedReasons = []Reason{ReasonPromQL, ReasonBKData, ReasonAPM, ReasonMultiTable, ReasonDataLabel, ReasonEventSparse}

// RefusalCode is the name an unsupported source is refused by.
const RefusalCode = "SOURCE_LIVENESS_UNSUPPORTED"

// ReasonsOf is the reasons a result may carry: none for alive.
func ReasonsOf(result Result) []Reason {
	switch result {
	case ResultUnknown:
		return UnknownReasons
	case ResultUnsupported:
		return UnsupportedReasons
	}
	return []Reason{""}
}

// Verdict is what a probe decided.
type Verdict struct {
	Result Result
	Reason Reason
}

// Ends is whether a no-data end goes ahead on the verdict: on alive, and on
// an unsupported source, which ends as it did before the probe existed.
// Unknown keeps the Level open.
func (verdict Verdict) Ends() bool {
	return verdict.Result == ResultAlive || verdict.Result == ResultUnsupported
}

// Key names one probe: one source in one scope over one window. Two Levels,
// series or Plans that read the same table in the same space, over the same
// window - the same first empty period, the same judged period - share it.
type Key struct {
	TenantID       string
	SpaceScope     string
	GlobalBusiness bool
	DataSource     string
	TableID        string
	FieldName      string
	Start, End     int64
	StepSeconds    int64
}

// Probe is one source liveness query.
type Probe struct {
	Key  Key
	Kind Kind
	// Clause is the probe's one clause: the source's table, data source,
	// driver and time field, counted, with none of the strategy's
	// conditions, dimensions, query string or offsets.
	Clause execution.QueryClause
	// TSDBMap is the source clause's own storage routing, under the probe's
	// reference; nil when the strategy's query carries none.
	TSDBMap  map[string][]execution.QueryStorage
	Timezone string
	// QueryStart is where the query starts: Key.Start, or later where the
	// window would be longer than the Level's span (Derive). Key.End is
	// where it ends.
	QueryStart int64
}

// ProbeReference is the reference name of the probe's clause, and its
// metric_merge.
const ProbeReference = "a"

// apmTable is access/uq's APM table (apm_app.go): an APM metric or trace
// table, one per application. Held to it by a test there.
var apmTable = regexp.MustCompile(`^(?:space_)?\d+_bkapm_(metric|trace)_[A-Za-z0-9_-]+(?:\.__default__)?$`)

// APMTablePattern is apmTable's expression, for that test.
func APMTablePattern() string { return apmTable.String() }

// KindOf is the source kind of a query's facts: event for a custom event
// count, log for a log index set or a keyword log, time series otherwise.
func KindOf(facts execution.QueryPlanFacts) Kind {
	for _, semantic := range facts.SourceSemantics {
		if strings.HasSuffix(semantic, "/event") {
			return KindEvent
		}
	}
	for _, semantic := range facts.SourceSemantics {
		if strings.HasSuffix(semantic, "/log") || strings.HasPrefix(semantic, "bk_log_search/") {
			return KindLog
		}
	}
	for _, clause := range facts.QueryList {
		if clause.DataSource == "bklog" {
			return KindLog
		}
	}
	return KindTimeSeries
}

// Derive is the probe of a query's source for the period judged at source
// time judged (the start of the period the round judged, in seconds), or the
// reason no probe can be expressed for it, over the Level's no-data span
// and nothing before it.
//
// The window is [lastRealPoint + 2 steps, judged + 1 step), at the
// strategy's own query offset: judged is the period the evaluation read,
// which already sits the query delay behind the evaluation time, and
// lastRealPoint is the source time of the series' last real point. The span
// is timed from it (trigger noDataEndDueV2, worker noDataEndDue: due once
// judged - lastRealPoint is (R + T + 1) steps), and its first empty period
// begins one step after it; the window starts one step after that because a count over a window of one step, evaluated at the
// window's first instant, reaches one step back (count_over_time on
// VictoriaMetrics covers (t - W, t]): started at the first empty period it
// would count the samples late in the last period that had data, and read a
// source that stopped there as alive. That would end an alert during a
// real outage. On a store that buckets [t, t + W),
// the window only loses the first empty period, which holds nothing either.
// So the window never holds the last period with data, however the store
// reads it. At the round a no-data end is first due, judged - lastRealPoint
// is the span, (R + T + 1) steps, so the window is R + T steps.
//
// The query reads at most the span, ending at the judged period: [max(
// lastRealPoint + 2 steps, judged + 1 step - span), judged + 1 step). The
// window's identity - Key.Start, what an answer is kept and found by - stays
// lastRealPoint + 2 steps on every round, or the start would move each round,
// no answer would ever be found, and the Level would read unknown by timeout
// forever. Bounded, the query never starts before lastRealPoint + 2 steps
// either. So a Level held on an unknown source asks whether the source is
// delivering now, over its last span: one that read unknown early and whose
// source died later stays held, rather than ending on data from early in its
// no-data span.
//
// No business condition is added for any source, and none of the strategy's
// conditions are kept: the scope is the space the query service puts into
// the query's conditions itself, for every source. The facts cannot say
// whether a table carries bk_biz_id at all, and a condition on a field a
// table does not have answers empty forever - for an index set without it,
// as for a metric without the label (access/uq/client.go, the PromQL body).
// A global business Plan probes as its query reads, with the space skipped:
// its probe counts the table across every space of the tenant, the most
// expensive shape there is - on VictoriaMetrics, every series of the metric.
func Derive(facts execution.QueryPlanFacts, lastRealPoint, judged, spanSeconds int64) (Probe, Verdict, bool) {
	kind := KindOf(facts)
	refuse := func(reason Reason) (Probe, Verdict, bool) {
		return Probe{Kind: kind}, Verdict{Result: ResultUnsupported, Reason: reason}, false
	}
	if facts.PromQL != nil {
		return refuse(ReasonPromQL)
	}
	if len(facts.QueryList) == 0 {
		// No facts to derive from: nothing says the source is alive.
		return Probe{Kind: kind}, Verdict{Result: ResultUnknown, Reason: ReasonError}, false
	}
	if kind == KindEvent {
		return refuse(ReasonEventSparse)
	}
	source := facts.QueryList[0]
	for _, clause := range facts.QueryList[1:] {
		if clause.TableID != source.TableID || clause.DataSource != source.DataSource {
			return refuse(ReasonMultiTable)
		}
	}
	switch {
	case source.DataSource == "bkdata":
		return refuse(ReasonBKData)
	case apmTable.MatchString(source.TableID):
		return refuse(ReasonAPM)
	case kind == KindTimeSeries && !strings.Contains(source.TableID, "."):
		// A result table is db.measurement; a name without the dot is a
		// data label.
		return refuse(ReasonDataLabel)
	}
	step := facts.StepMillis / 1000
	if step <= 0 || source.TableID == "" {
		return Probe{Kind: kind}, Verdict{Result: ResultUnknown, Reason: ReasonError}, false
	}
	start, end := lastRealPoint+2*step, judged+step
	if lastRealPoint <= 0 || start >= end {
		// No span to read: the last real point unknown, or too recent.
		return Probe{Kind: kind}, Verdict{Result: ResultUnknown, Reason: ReasonError}, false
	}
	window := durationSeconds(step)
	clause := execution.QueryClause{
		DataSource: source.DataSource, Driver: source.Driver, TableID: source.TableID, TimeField: source.TimeField,
		ReferenceName: ProbeReference, Functions: []execution.QueryFunction{{Method: "sum"}},
		TimeAggregation: execution.QueryFunction{Method: "count_over_time", Window: window},
	}
	if kind == KindTimeSeries {
		clause.FieldName = source.FieldName
	} else {
		// Documents, not a field a document may lack.
		clause.FieldName, clause.QueryString = "_index", "*"
	}
	queryStart := start
	if spanSeconds > 0 {
		queryStart = max(start, end-spanSeconds)
	}
	probe := Probe{Kind: kind, Clause: clause, Timezone: facts.Timezone, QueryStart: queryStart,
		Key: Key{TenantID: facts.TenantID, SpaceScope: facts.SpaceScope, GlobalBusiness: facts.GlobalBusiness,
			DataSource: clause.DataSource, TableID: clause.TableID, FieldName: clause.FieldName,
			Start: start, End: end, StepSeconds: step}}
	if storages := facts.TSDBMap[source.ReferenceName]; len(storages) > 0 {
		probe.TSDBMap = map[string][]execution.QueryStorage{ProbeReference: append([]execution.QueryStorage(nil), storages...)}
	}
	return probe, Verdict{}, true
}

func durationSeconds(seconds int64) string {
	return strconv.FormatInt(seconds, 10) + "s"
}

// Answer is what the query service said to a probe.
type Answer struct {
	// Status is the answer's status code, empty when it carried none.
	Status string
	// Partial: a route failed (QUERY_TS_PARTIAL, or is_partial).
	Partial bool
	// Timeout: the probe ran out of time. Err is then set too.
	Timeout bool
	Err     error
	// Points is how many points with a count above zero the answer held.
	Points int
}

// Classify is a probe's verdict on its answer. A status, a partial answer, a
// failure or a timeout is unknown, whatever came with it; data is alive; an
// empty answer without a status is unknown too in this version
// (empty_without_status).
func Classify(answer Answer) Verdict {
	switch {
	case answer.Timeout:
		return Verdict{Result: ResultUnknown, Reason: ReasonTimeout}
	case answer.Err != nil:
		return Verdict{Result: ResultUnknown, Reason: ReasonError}
	case answer.Partial || strings.HasSuffix(answer.Status, "_PARTIAL"):
		return Verdict{Result: ResultUnknown, Reason: ReasonPartial}
	case answer.Status != "":
		return Verdict{Result: ResultUnknown, Reason: ReasonStatus}
	case answer.Points > 0:
		return Verdict{Result: ResultAlive}
	}
	return Verdict{Result: ResultUnknown, Reason: ReasonEmptyWithoutStatus}
}
