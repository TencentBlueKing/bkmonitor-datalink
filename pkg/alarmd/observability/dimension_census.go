// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// The dimension census's two closed vocabularies, kept here because this is
// the package both ends can see: the census itself is built in execution,
// which reads these, and the metric labels are pre-created from the same
// lists. Written twice they would be two derivations of one relation, and
// nothing would fail when one of them gained a value.
const (
	// DimensionCensusSourceRound is a census counted from the series the
	// round evaluated: what the strategy has.
	DimensionCensusSourceRound = "round"
	// DimensionCensusSourceRoster is a census counted from the synthetic
	// series a no-data round produced, for a round that saw none of its own.
	// Its values are an UPPER BOUND, not a current reading: the roster
	// deliberately remembers groups that have gone away.
	DimensionCensusSourceRoster = "roster"
	// DimensionCensusSourceUnknown is a census whose source this build does
	// not know. It is a label rather than a dropped reading because a census
	// arriving with no source is a writer that forgot to say, and that is
	// worth seeing on a dashboard rather than worth hiding.
	DimensionCensusSourceUnknown = "unknown"
)

const (
	// DimensionCensusStatusWritten is a census the store kept.
	DimensionCensusStatusWritten = "WRITTEN"
	// DimensionCensusStatusRejected is one it refused whole - too large, or
	// malformed. Never truncated: a census cut to fit reads like a
	// distribution, and a planner cannot tell the two apart.
	DimensionCensusStatusRejected = "REJECTED"
	// DimensionCensusStatusRetryable is the store not answering.
	DimensionCensusStatusRetryable = "RETRYABLE"
	// DimensionCensusStatusUnknown is a write that came back with no outcome
	// at all, which is this repository's own bug and not a state of the
	// store. Named for the same reason the unknown source is.
	DimensionCensusStatusUnknown = "UNKNOWN"
)

// DimensionCensusSources and DimensionCensusStatuses are the label values the
// census metrics are pre-created with, so every series exists from startup
// and a zero can be told from a series that was never written.
func DimensionCensusSources() []string {
	return []string{DimensionCensusSourceRound, DimensionCensusSourceRoster, DimensionCensusSourceUnknown}
}

func DimensionCensusStatuses() []string {
	return []string{
		DimensionCensusStatusWritten, DimensionCensusStatusRejected,
		DimensionCensusStatusRetryable, DimensionCensusStatusUnknown,
	}
}

// normalizeDimensionCensusFacts copies the facts and holds the two label
// fields to their vocabularies. A value outside them becomes the named
// unknown rather than reaching the metric: a label nobody declared is a
// series nobody pre-created, and one arriving at runtime is how a bounded
// label set stops being bounded.
func normalizeDimensionCensusFacts(facts *DimensionCensusFacts) *DimensionCensusFacts {
	if facts == nil {
		return nil
	}
	copied := *facts
	switch copied.Source {
	case DimensionCensusSourceRound, DimensionCensusSourceRoster:
	default:
		copied.Source = DimensionCensusSourceUnknown
	}
	switch copied.Status {
	case DimensionCensusStatusWritten, DimensionCensusStatusRejected, DimensionCensusStatusRetryable:
	default:
		copied.Status = DimensionCensusStatusUnknown
	}
	return &copied
}
