// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// QueryStatusFacts records what a UQ response's top-level status code was and
// what this deployment did about it.
//
// A code that answers "this table and field do not exist" can arrive beside a
// usable series, because an expression such as `... or vector(100)` computes an
// answer without reading any table. Both facts are true at once, and which one
// the deployment acts on decides whether the Slot completes or is thrown away.
//
// This exists because acting on that code without counting it makes a real
// routing breakage invisible. Before a code is allowed through, it forces the
// Slot to complete UNAVAILABLE -- loud, and how the condition gets noticed at
// all. After, the expression's fallback answer is accepted and the object
// reports a steady healthy value forever. That is a strictly quieter failure
// than the one it replaces, and quieter is worse: the 34-hour outage that
// prompted the change was found precisely because it was loud.
type QueryStatusFacts struct {
	// Code is UQ's top-level status code, normalized to the set UQ itself
	// declares. Unlike an alarmd failure code, that set is closed by another
	// component's own constants, which is what makes it safe as a metric label.
	Code string
	// Outcome is what this deployment did, not what UQ reported. The two must
	// be counted together: the code alone cannot say whether the series it
	// travelled with was used or discarded, and that is the whole question.
	Outcome string
}

// The status codes UQ declares. Keeping the list here rather than deriving it
// means a code UQ adds later lands in Other instead of appearing as a new label
// value, so the cardinality bound cannot be moved by another component.
const (
	QueryStatusExceedsMaximumLimit   = "EXCEEDS_MAXIMUM_LIMIT"
	QueryStatusExceedsMaximumSlimit  = "EXCEEDS_MAXIMUM_SLIMIT"
	QueryStatusSpaceIsNotExists      = "SPACE_IS_NOT_EXISTS"
	QueryStatusSpaceTableIDNotExists = "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS"
	QueryStatusSpaceTableIDFallback  = "SPACE_TABLE_ID_FIELD_MISSING_FALLBACK"
	QueryStatusTableIDProxyNotExists = "TABLE_ID_PROXY_IS_NOT_EXISTS"
	QueryStatusQueryRawError         = "QUERY_RAW_ERROR"
	QueryStatusQueryRawPartial       = "QUERY_RAW_PARTIAL"
	QueryStatusQueryTsPartial        = "QUERY_TS_PARTIAL"
	QueryStatusStorageTimeout        = "STORAGE_TIMEOUT"
	QueryStatusStorageError          = "STORAGE_ERROR"
	QueryStatusOther                 = "OTHER"
)

// What this deployment did with the response the code arrived on.
const (
	// QueryStatusOutcomeAllowed means the series travelled with the code and
	// was kept. This is the count that answers "is anything running on an
	// expression's fallback answer rather than on data", which nothing else
	// can answer once the code stops forcing a completion.
	QueryStatusOutcomeAllowed = "allowed"
	// QueryStatusOutcomeUnavailable means the code decided the completion and
	// whatever series arrived with it were discarded.
	QueryStatusOutcomeUnavailable = "unavailable"
	QueryStatusOutcomeOther       = "other"
)

func validQueryStatusCode(code string) bool {
	switch code {
	case QueryStatusExceedsMaximumLimit, QueryStatusExceedsMaximumSlimit,
		QueryStatusSpaceIsNotExists, QueryStatusSpaceTableIDNotExists,
		QueryStatusSpaceTableIDFallback, QueryStatusTableIDProxyNotExists,
		QueryStatusQueryRawError, QueryStatusQueryRawPartial, QueryStatusQueryTsPartial,
		QueryStatusStorageTimeout, QueryStatusStorageError, QueryStatusOther:
		return true
	}
	return false
}

// NormalizeQueryStatusCode maps a code onto the declared set. A code this
// build has never heard of is counted as Other rather than dropped: "something
// arrived that we do not recognise" is itself worth seeing, and dropping it
// would make an unknown code indistinguishable from no code at all.
func NormalizeQueryStatusCode(code string) string {
	if validQueryStatusCode(code) {
		return code
	}
	return QueryStatusOther
}

// normalizeQueryStatus keeps one entry per physical query rather than one per
// observation. A query group with several Plans completes one query_completed
// carrying several physical queries, each able to report its own status code,
// and projecting only the first would undercount. That is tolerable for a log
// field, which loses a line; it is not tolerable for a counter, which then
// reports a number that is simply wrong.
//
// Counting every entry does not widen the label set: the labels are the closed
// code set and the outcome, so more increments land on the same series. The
// length is bounded by the Plans in a query group, which configuration bounds.
func normalizeQueryStatus(component Component, stage Stage, input []QueryStatusFacts) []QueryStatusFacts {
	if component != ComponentAccess || stage != StageQueryCompleted || len(input) == 0 {
		return nil
	}
	normalized := make([]QueryStatusFacts, 0, len(input))
	for _, facts := range input {
		if facts.Code == "" {
			// No status code is the ordinary case and there is nothing to
			// count. Recording it as Other would bury the codes that matter
			// under the volume of responses that carried none.
			continue
		}
		facts.Code = NormalizeQueryStatusCode(facts.Code)
		switch facts.Outcome {
		case QueryStatusOutcomeAllowed, QueryStatusOutcomeUnavailable:
		default:
			facts.Outcome = QueryStatusOutcomeOther
		}
		normalized = append(normalized, facts)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}
