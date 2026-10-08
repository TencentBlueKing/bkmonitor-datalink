// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"errors"
	"fmt"
)

// MaxSupplementSeries bounds how many series one supplement execution is
// given. A Slot whose late read found more is supplemented up to it and the
// rest go unsupplemented, counted by whoever chose them.
const MaxSupplementSeries = 1 << 16

// SupplementScope is what a supplement execution of one Slot T may evaluate:
// the series a later read of T's frozen query returned and T's own read did
// not, named by their full identity.
//
// A supplement is not a second evaluation of T. Every series outside the
// scope was T's to decide and is left as T left it. A series inside it is
// evaluated at T only when nothing has been decided for it at T or after:
// its State has not reached T, and its no-data group was not recorded absent
// at T. It writes the series' events and then its State, as a Slot does, and
// nothing else a Slot writes: no Progress, no gap marker, no no-data memory,
// no execution evidence and no census. Gap markers are read and applied as
// they stand, so a Plan whose guard withholds NORMAL at T has it withheld
// here too.
type SupplementScope struct {
	// Series is sorted and holds no repeats.
	Series []SeriesIdentityDigest
}

func (scope SupplementScope) Validate() error {
	if len(scope.Series) == 0 || len(scope.Series) > MaxSupplementSeries {
		return fmt.Errorf("alarmd execution: a supplement names between 1 and %d series", MaxSupplementSeries)
	}
	for index, series := range scope.Series {
		if series == "" || (index > 0 && scope.Series[index-1] >= series) {
			return errors.New("alarmd execution: supplement series must be named, sorted and distinct")
		}
	}
	return nil
}

// SupplementFacts is what one supplement execution did with each series of
// its scope that the read returned, counted once per due Plan the series was
// read for: every such pair ends in exactly one of the named outcomes, so
// they add up to Candidates.
type SupplementFacts struct {
	// Candidates is the (Plan, series) pairs the execution was given and
	// the read returned.
	Candidates int `json:"candidates"`
	// Admitted pairs had their events written and then their State at T.
	// Points is how many points of the read those series were evaluated on:
	// what the supplement filled in.
	Admitted int    `json:"admitted"`
	Points   uint64 `json:"points"`
	// CrossedT pairs had State at T or later already; nothing is decided
	// for them a second time.
	CrossedT int `json:"crossed_t"`
	// NoDataFact pairs belong to a no-data group recorded absent at T.
	NoDataFact int `json:"no_data_fact"`
	// ConfigDrift pairs belong to a Plan whose activation is no longer the
	// one T was frozen with, or whose side effects are no longer admitted.
	ConfigDrift int `json:"config_drift"`
	// InputIncomplete pairs were read without a whole primary input.
	InputIncomplete int `json:"input_incomplete"`
	// Withheld pairs reached their evaluation and had nothing written: a
	// load they depend on did not answer, the evaluation decided nothing, or
	// their output or State was refused.
	Withheld int `json:"withheld"`
}

// Decided is how many pairs have an outcome; equal to Candidates when every
// pair was accounted for.
func (facts SupplementFacts) Decided() int {
	return facts.Admitted + facts.CrossedT + facts.NoDataFact + facts.ConfigDrift + facts.InputIncomplete + facts.Withheld
}
