// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// StateSupersededSite is what a Slot skipped because a later Slot had
// already written it: a series' State (state), or a Plan's gap marker (gap).
type StateSupersededSite string

const (
	StateSupersededAtState StateSupersededSite = "state"
	StateSupersededAtGap   StateSupersededSite = "gap"
	stateSupersededOther   StateSupersededSite = "other"
)

// AllStateSupersededSites is every site the counter publishes from the start.
func AllStateSupersededSites() []StateSupersededSite {
	return []StateSupersededSite{StateSupersededAtState, StateSupersededAtGap, stateSupersededOther}
}

// NormalizeStateSupersededSite folds a site this build does not name into
// other, so the label set cannot grow with its input.
func NormalizeStateSupersededSite(site StateSupersededSite) StateSupersededSite {
	switch site {
	case StateSupersededAtState, StateSupersededAtGap:
		return site
	}
	return stateSupersededOther
}

// StateSupersededOperations are the operations a skip can be counted under:
// every Slot operation but the supplement, which skips a series that has
// reached its Slot by its own rule and never reaches this one.
var StateSupersededOperations = []Operation{OperationNormal, OperationRetry, OperationReplay, OperationProbe, OperationOther}

// NormalizeStateSupersededOperation keeps the Slot operations and folds the
// rest into other.
func NormalizeStateSupersededOperation(operation Operation) Operation {
	switch operation {
	case OperationNormal, OperationRetry, OperationReplay, OperationProbe:
		return operation
	}
	return OperationOther
}

// StateSupersededFacts is what one skip site skipped: the site, how many, and
// for a series one sample. Carried on the skip's own observation, which is
// the one the counter counts; the Slot's total goes on its completion line
// (SlotSupersededFacts), which is not counted again.
type StateSupersededFacts struct {
	Site   StateSupersededSite    `json:"site"`
	Count  int64                  `json:"count"`
	Sample *StateSupersededSample `json:"sample,omitempty"`
}

// StateSupersededSample is one superseded series: its digest, the version
// this Slot would have written, and the later one the store holds. The two
// versions side by side are what tell a rollback (the stored one a Slot or
// more ahead, same epoch) from a replay that crossed a later write.
type StateSupersededSample struct {
	Series string            `json:"series_identity_digest"`
	Slot   ApplyVersionFacts `json:"slot_apply_version"`
	Stored ApplyVersionFacts `json:"stored_apply_version"`
}

// ApplyVersionFacts is an ApplyVersion as a line carries it.
type ApplyVersionFacts struct {
	StateApplyEpoch uint64 `json:"state_apply_epoch"`
	EvaluationTime  int64  `json:"evaluation_time"`
	SlotDigest      string `json:"slot_digest"`
}

// SlotSupersededFacts is a Slot attempt's skipped series on its completion
// line: how many, and the first. Rendered only; the skips were counted where
// they happened.
type SlotSupersededFacts struct {
	Series uint32                 `json:"series"`
	Sample *StateSupersededSample `json:"sample,omitempty"`
}
