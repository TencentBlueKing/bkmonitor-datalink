// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func censusIdentity() execution.PlanCensusIdentity {
	return execution.PlanCensusIdentity{
		Plan:            execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"},
		StateGeneration: "generation",
	}
}

// Every series a census counted is either named or in the overflow. This is
// the reading the split planner does arithmetic on - how much of the strategy
// the named values cover - and it is wrong in both directions if a series can
// fall between the two.
func censusAccountsForEverySeries(t *testing.T, census execution.DimensionCensus, dimension string, observed uint32) {
	t.Helper()
	for _, entry := range census.Dimensions {
		if entry.Dimension != dimension {
			continue
		}
		var named uint32
		for _, value := range entry.Values {
			named += value.Series
		}
		if got := named + entry.OverflowSeries; got != observed {
			t.Fatalf("dimension %q accounts for %d series (%d named, %d in the overflow), want all %d: "+
				"a planner reads the named values as a share of the whole",
				dimension, got, named, entry.OverflowSeries, observed)
		}
		return
	}
	t.Fatalf("census names no dimension %q, it has %d", dimension, len(census.Dimensions))
}

// The undercounting direction first: a census that cannot name every value
// has to say how much it left out, or a planner reads 4096 values as the
// whole strategy and cuts pieces for a fraction of it.
func TestACensusPastTheValueBoundNamesTheHeaviestAndCountsTheRestWhole(t *testing.T) {
	builder := execution.NewDimensionCensusBuilder(censusIdentity(), execution.DimensionCensusFromRound)
	const heavy = 100
	const total = execution.MaxCensusValuesPerDimension + 104
	observed := uint32(0)
	for index := 0; index < total; index++ {
		value := fmt.Sprintf("host-%05d", index)
		builder.ObserveSeries(map[string]string{"ip": value})
		observed++
		if index < heavy {
			// A second series on the first hundred, so the top of the census
			// is decided by weight and not by the order they arrived in.
			builder.ObserveSeries(map[string]string{"ip": value})
			observed++
		}
	}
	census, taken, err := builder.Build(1000)
	if err != nil || !taken {
		t.Fatalf("Build() = %v, %v, want a census", taken, err)
	}
	entry := census.Dimensions[0]
	if len(entry.Values) != execution.MaxCensusValuesPerDimension {
		t.Fatalf("census names %d values, want the bound %d", len(entry.Values), execution.MaxCensusValuesPerDimension)
	}
	if entry.OverflowValues != 104 || entry.OverflowSeries != 104 {
		t.Fatalf("overflow = %d values on %d series, want 104 and 104: the values past the bound each carried one",
			entry.OverflowValues, entry.OverflowSeries)
	}
	for index := 0; index < heavy; index++ {
		if entry.Values[index].Series != 2 {
			t.Fatalf("value %d of the census carries %d series, want the two-series values first: "+
				"a split is planned around the heaviest values, so those are the ones a bounded census keeps",
				index, entry.Values[index].Series)
		}
	}
	censusAccountsForEverySeries(t, census, "ip", observed)
	if census.Series != observed {
		t.Fatalf("census counted %d series, want %d", census.Series, observed)
	}
}

// And the other direction: a census that did name everything must not claim
// an overflow. An overflow the planner cannot explain is the reading that
// stops a split, so a spurious one is as costly as a missing one.
func TestACensusInsideTheValueBoundReportsNoOverflow(t *testing.T) {
	builder := execution.NewDimensionCensusBuilder(censusIdentity(), execution.DimensionCensusFromRound)
	for index := 0; index < execution.MaxCensusValuesPerDimension; index++ {
		builder.ObserveSeries(map[string]string{"ip": fmt.Sprintf("host-%05d", index)})
	}
	census, taken, err := builder.Build(1000)
	if err != nil || !taken {
		t.Fatalf("Build() = %v, %v, want a census", taken, err)
	}
	entry := census.Dimensions[0]
	if len(entry.Values) != execution.MaxCensusValuesPerDimension {
		t.Fatalf("census names %d values, want all %d", len(entry.Values), execution.MaxCensusValuesPerDimension)
	}
	if entry.OverflowValues != 0 || entry.OverflowSeries != 0 {
		t.Fatalf("overflow = %d values on %d series, want none: every value was named",
			entry.OverflowValues, entry.OverflowSeries)
	}
}

// Past the tracked bound the builder stops holding new values, which is what
// keeps its memory off the strategy's cardinality. The series on them are
// still counted: the bound is on what can be named, not on what is known to
// exist.
func TestACensusPastTheTrackedBoundStillCountsTheSeriesItCannotName(t *testing.T) {
	builder := execution.NewDimensionCensusBuilder(censusIdentity(), execution.DimensionCensusFromRound)
	observed := uint32(0)
	for index := 0; index < execution.MaxCensusTrackedValuesPerDimension; index++ {
		builder.ObserveSeries(map[string]string{"ip": fmt.Sprintf("host-%06d", index)})
		observed++
	}
	const untracked = 5
	for index := 0; index < untracked; index++ {
		value := fmt.Sprintf("late-%03d", index)
		builder.ObserveSeries(map[string]string{"ip": value})
		builder.ObserveSeries(map[string]string{"ip": value})
		observed += 2
	}
	census, taken, err := builder.Build(1000)
	if err != nil || !taken {
		t.Fatalf("Build() = %v, %v, want a census", taken, err)
	}
	entry := census.Dimensions[0]
	if entry.OverflowValues < untracked {
		t.Fatalf("overflow names %d values, want at least the %d first seen past the tracked bound",
			entry.OverflowValues, untracked)
	}
	// Named nowhere, not merely counted somewhere. Each late value carries
	// two series where every tracked one carries one, so without the bound
	// they would be the heaviest values in the census and sit at its very
	// top - an assertion on the overflow total alone cannot tell the two
	// apart.
	for _, value := range entry.Values {
		if strings.HasPrefix(value.Value, "late-") {
			t.Fatalf("the census names %q, a value first seen after the tracked set was full: "+
				"the bound is on what the builder holds, and a value it never held cannot be named",
				value.Value)
		}
	}
	censusAccountsForEverySeries(t, census, "ip", observed)
}

// A round that observed nothing is not a census of nothing. Written as one it
// would read later as a strategy with no series, which is the reading that
// makes a planner split it into pieces holding nothing.
func TestARoundThatObservedNothingTakesNoCensus(t *testing.T) {
	builder := execution.NewDimensionCensusBuilder(censusIdentity(), execution.DimensionCensusFromRound)
	if _, taken, err := builder.Build(1000); taken || err != nil {
		t.Fatalf("Build() = %v, %v, want no census and no error", taken, err)
	}
	builder.ObserveSeries(map[string]string{})
	if _, taken, err := builder.Build(1000); taken || err != nil {
		t.Fatalf("Build() after an empty series = %v, %v, want no census", taken, err)
	}
}

// Two replicas counting the same round write the same census: the values are
// ordered by weight and then by name, so nothing depends on the order a Go
// map hands them back in.
func TestTwoBuildersOverTheSameRoundTakeTheSameCensus(t *testing.T) {
	round := []map[string]string{}
	for index := 0; index < 200; index++ {
		round = append(round, map[string]string{
			"ip":     fmt.Sprintf("host-%03d", index%37),
			"module": fmt.Sprintf("module-%d", index%5),
		})
	}
	encode := func() string {
		builder := execution.NewDimensionCensusBuilder(censusIdentity(), execution.DimensionCensusFromRound)
		for _, series := range round {
			builder.ObserveSeries(series)
		}
		census, taken, err := builder.Build(1000)
		if err != nil || !taken {
			t.Fatalf("Build() = %v, %v, want a census", taken, err)
		}
		encoded, err := json.Marshal(census)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	first, second := encode(), encode()
	if first != second {
		t.Fatalf("two censuses of one round differ:\n%s\n%s", first, second)
	}
}

// Validate is what stands between a malformed census and a planner reading it
// as a distribution. Each refusal is one a reader could not otherwise tell
// from a real census.
func TestACensusAReaderCouldMistakeForACompleteOneIsRefused(t *testing.T) {
	sound := execution.DimensionCensus{
		Identity: censusIdentity(), Source: execution.DimensionCensusFromRound, ObservedAt: 1000, Series: 3,
		Dimensions: []execution.DimensionCensusEntry{{
			Dimension: "ip", Values: []execution.DimensionValueCount{{Value: "192.0.2.1", Series: 3}},
		}},
	}
	if err := sound.Validate(); err != nil {
		t.Fatalf("the fixture census is refused: %v", err)
	}
	cases := map[string]func(census *execution.DimensionCensus){
		"no source":           func(c *execution.DimensionCensus) { c.Source = "" },
		"unknown source":      func(c *execution.DimensionCensus) { c.Source = "guessed" },
		"no round":            func(c *execution.DimensionCensus) { c.ObservedAt = 0 },
		"no state generation": func(c *execution.DimensionCensus) { c.Identity.StateGeneration = "" },
		"no dimension":        func(c *execution.DimensionCensus) { c.Dimensions = nil },
		"an unnamed dimension": func(c *execution.DimensionCensus) {
			c.Dimensions = append(c.Dimensions, execution.DimensionCensusEntry{
				Values: []execution.DimensionValueCount{{Value: "x", Series: 1}}})
		},
		"one dimension twice": func(c *execution.DimensionCensus) {
			c.Dimensions = append(c.Dimensions, c.Dimensions[0])
		},
		"a value no series carried": func(c *execution.DimensionCensus) {
			c.Dimensions[0].Values = append(c.Dimensions[0].Values, execution.DimensionValueCount{Value: "192.0.2.2"})
		},
		"one value twice": func(c *execution.DimensionCensus) {
			c.Dimensions[0].Values = append(c.Dimensions[0].Values, c.Dimensions[0].Values[0])
		},
	}
	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			census := sound
			census.Dimensions = append([]execution.DimensionCensusEntry(nil), sound.Dimensions...)
			census.Dimensions[0].Values = append([]execution.DimensionValueCount(nil), sound.Dimensions[0].Values...)
			break_(&census)
			if err := census.Validate(); err == nil {
				t.Fatalf("a census with %s is accepted, want it refused", name)
			}
		})
	}
}

// The census holds at most as many dimensions as it says it does, whatever
// the strategy has. A wide one is bounded where it is counted rather than
// refused at the end, because a refusal there would lose the whole reading.
func TestACensusHoldsAtMostItsDimensionBound(t *testing.T) {
	builder := execution.NewDimensionCensusBuilder(censusIdentity(), execution.DimensionCensusFromRound)
	series := make(map[string]string, execution.MaxCensusDimensions+8)
	for index := 0; index < execution.MaxCensusDimensions+8; index++ {
		series[fmt.Sprintf("dimension_%02d", index)] = "value"
	}
	builder.ObserveSeries(series)
	census, taken, err := builder.Build(1000)
	if err != nil || !taken {
		t.Fatalf("Build() = %v, %v, want a census", taken, err)
	}
	if len(census.Dimensions) != execution.MaxCensusDimensions {
		t.Fatalf("census names %d dimensions, want the bound %d", len(census.Dimensions), execution.MaxCensusDimensions)
	}
}
