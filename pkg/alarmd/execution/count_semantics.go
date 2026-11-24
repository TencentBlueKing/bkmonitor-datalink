// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

// The count words of a query, read off its facts: whether it is an event
// count, whether its provider leaves empty groups out rather than zero, and
// whether its zero is an observation. The controlplane binds them to the
// primary requirement, and the strategy view says the recovery they make;
// both read them here, so the two cannot disagree.

// ReadsEventCountOnly is whether a query reads custom/event and nothing else.
func ReadsEventCountOnly(query QueryPlanFacts) bool {
	return len(query.SourceSemantics) == 1 && query.SourceSemantics[0] == EventCountSourceSemantics && query.StepMillis > 0
}

// ZeroCountIsNoObservation is whether a query's count of zero is no
// observation (DataRequirement.ZeroIsNoObservation): an event count, grouped
// or not, or a log count whose clauses group by nothing.
func ZeroCountIsNoObservation(query QueryPlanFacts) bool {
	if ReadsEventCountOnly(query) {
		return true
	}
	if !ReadsUnfilledCounts(query) {
		return false
	}
	for _, clause := range query.QueryList {
		if len(clause.Dimensions) > 0 {
			return false
		}
	}
	return true
}

// ReadsUnfilledCounts is whether a query's one source answers counts its
// provider does not zero-fill: a log count (bk_log_search/log, bk_monitor/log)
// or a log time series aggregated by count or a count-like distinct
// (bk_log_search/time_series), all read from an Elasticsearch date histogram
// whose groups are absent rather than zero while they have no document. Its
// range is not widened as an event count's is - the store already refuses
// the largest log groups at one period - so its empty answer is labelled for
// what it is instead. avg, sum, max and the rest keep their diagnosis.
func ReadsUnfilledCounts(query QueryPlanFacts) bool {
	if len(query.SourceSemantics) != 1 {
		return false
	}
	switch query.SourceSemantics[0] {
	case "bk_log_search/log", "bk_monitor/log":
		return true
	case "bk_log_search/time_series":
		return countsEveryClause(query.QueryList)
	}
	return false
}

// countsEveryClause is whether every clause of a query is aggregated by count
// or a count-like distinct: count_over_time or distinct_over_time over time,
// or count without time (compileAggregation).
func countsEveryClause(clauses []QueryClause) bool {
	if len(clauses) == 0 {
		return false
	}
	for _, clause := range clauses {
		switch clause.TimeAggregation.Method {
		case "count_over_time", "distinct_over_time":
			continue
		case "":
			if len(clause.Functions) == 1 && clause.Functions[0].Method == "count" {
				continue
			}
		}
		return false
	}
	return true
}

// EventCountSourceSemantics is the source an event count reads.
const EventCountSourceSemantics = "custom/event"
