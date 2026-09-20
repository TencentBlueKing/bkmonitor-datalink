// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// QueryUnavailableFacts records, for one physical query that completed
// UNAVAILABLE, where its reason code came from. The code itself stays in the
// log line and the fleet view; what is counted here is whether the code was
// named by an attempt, guessed because no attempt named one, or produced with
// no attempt at all. Those are three situations behind one word, and two of
// them send a reader to look at a provider that was never asked anything.
//
// The values mirror execution.UnavailableAttribution. They are restated here
// rather than imported because execution's tests import this package, and a
// label set that another package could widen would not be a bound.
type QueryUnavailableFacts struct {
	Attribution string
}

const (
	// QueryUnavailableFromAttempt: an attempt named the reason; the code means
	// what it says.
	QueryUnavailableFromAttempt = "attempt"
	// QueryUnavailableNoAttemptReason: attempts were made and none named a
	// reason; the code is a fallback guess about a provider that was reached.
	QueryUnavailableNoAttemptReason = "no_attempt_reason"
	// QueryUnavailableNoAttempts: no attempt was made; nothing about the
	// provider is being reported, the query did not get as far as being sent.
	QueryUnavailableNoAttempts = "no_attempts"
	QueryUnavailableOther      = "other"
)

// QueryUnavailableAttributions is the closed set, for a reader that pre-creates
// one series per value so that zero is a reading.
var QueryUnavailableAttributions = []string{
	QueryUnavailableFromAttempt, QueryUnavailableNoAttemptReason, QueryUnavailableNoAttempts, QueryUnavailableOther,
}

func validQueryUnavailableAttribution(attribution string) bool {
	switch attribution {
	case QueryUnavailableFromAttempt, QueryUnavailableNoAttemptReason, QueryUnavailableNoAttempts, QueryUnavailableOther:
		return true
	}
	return false
}

// normalizeQueryUnavailable keeps one entry per unavailable physical query,
// for the same reason normalizeQueryStatus does: a query group with several
// Plans completes one query_completed carrying several physical queries, and
// a counter that took only the first would report a number that is simply
// wrong. An attribution outside the set lands in other rather than being
// dropped, so a value added without being listed shows as a rising other.
func normalizeQueryUnavailable(component Component, stage Stage, input []QueryUnavailableFacts) []QueryUnavailableFacts {
	if component != ComponentAccess || stage != StageQueryCompleted || len(input) == 0 {
		return nil
	}
	normalized := make([]QueryUnavailableFacts, 0, len(input))
	for _, facts := range input {
		if !validQueryUnavailableAttribution(facts.Attribution) {
			facts.Attribution = QueryUnavailableOther
		}
		normalized = append(normalized, facts)
	}
	return normalized
}
