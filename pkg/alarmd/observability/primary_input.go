// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// PrimaryInputFacts is what the Slot's PRIMARY query answered with, as the
// completion recorded it: Completeness is FULL, PARTIAL or UNAVAILABLE, and
// DataState DATA or EMPTY -- whether the answer carried any record at all.
//
// The pair is what separates two holes that look the same on a window. A
// round whose primary answered FULL with DATA ran the series that were in
// the answer; a series not among them had no record at that minute as far as
// the query could see. A round whose primary was PARTIAL or UNAVAILABLE did
// not see the minute whole, and a series missing from it may have had data
// nobody asked for. The counts on the window cannot tell these apart; the
// completion can, and this is where it says so.
type PrimaryInputFacts struct {
	Completeness string `json:"completeness"`
	DataState    string `json:"data_state,omitempty"`
}

// PrimaryCompletenesses and PrimaryDataStates are the closed lists a
// completion may carry, the contract's own words.
var (
	PrimaryCompletenesses = []string{"FULL", "PARTIAL", "UNAVAILABLE"}
	PrimaryDataStates     = []string{"DATA", "EMPTY"}
)

// PrimaryAnsweredWhole reports whether the primary answered the whole
// window with records: the reading under which a series absent from the
// round was absent from the data.
func (facts *PrimaryInputFacts) PrimaryAnsweredWhole() bool {
	return facts != nil && facts.Completeness == "FULL" && facts.DataState == "DATA"
}

func normalizePrimaryInputFacts(facts *PrimaryInputFacts) *PrimaryInputFacts {
	if facts == nil {
		return nil
	}
	if !inList(facts.Completeness, PrimaryCompletenesses) || (facts.DataState != "" && !inList(facts.DataState, PrimaryDataStates)) {
		return nil
	}
	copied := *facts
	return &copied
}

func inList(value string, list []string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
