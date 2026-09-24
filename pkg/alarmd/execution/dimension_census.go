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
	"context"
	"errors"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A dimension census is what a strategy's series look like along each of its
// dimensions: which values there are and how many series carry each. It is
// the input a split needs (decision-020 section 4.7.3) - a strategy is cut
// into pieces by matching a dimension's values, and the pieces are only even
// if the planner knows the distribution.
//
// It is counted from the round's own series, not from the Plan's no-data
// roster. The roster is a memory: it deliberately remembers groups that have
// gone away, for as long as the tracking horizon says, which is what makes it
// useful for absence and wrong for this. Counting values out of it would put
// values that no longer have any series into the split's domain, and the
// pieces cut for them would be empty for as long as the roster remembers
// (section 4.7.3.1). The roster is the fallback for the one case the round
// cannot answer - a round that saw no series at all - and a census taken that
// way says so, because it is an upper bound and not a current reading.
type DimensionCensusSource string

const (
	// DimensionCensusFromRound is the ordinary source: the series this round
	// evaluated.
	DimensionCensusFromRound DimensionCensusSource = observability.DimensionCensusSourceRound
	// DimensionCensusFromRoster is the fallback: the Plan's no-data roster,
	// read when the round saw no series. Its values are an upper bound - the
	// roster holds groups that are gone - and a reader must treat it as one.
	DimensionCensusFromRoster DimensionCensusSource = observability.DimensionCensusSourceRoster
)

const (
	// MaxCensusValuesPerDimension is how many of a dimension's values one
	// census names. The values kept are the ones carrying the most series,
	// because those are what a split is planned around; everything else is
	// counted whole in the overflow, so the planner can see how much of the
	// strategy is in the tail it cannot name.
	MaxCensusValuesPerDimension = 4096
	// MaxCensusTrackedValuesPerDimension bounds what the builder holds while
	// it counts, which is larger than what it keeps: the top values are only
	// known once the round is over. Past it a value it has not seen before is
	// counted in the overflow rather than tracked, so the memory is bounded
	// by this and not by the strategy's cardinality.
	//
	// It is generous on purpose. A census is built only for a candidate Plan
	// - one or two per replica (section 4.7.3.1) - so this is transient
	// memory on a path that already holds the round's series, and the cost of
	// being stingy is an overflow that hides which values are heavy.
	MaxCensusTrackedValuesPerDimension = 32768
	// MaxCensusDimensions bounds how many dimensions one census names.
	MaxCensusDimensions = 16
)

// PlanCensusIdentity names one Plan's census. There is no shard in it: the
// census describes the strategy's series, which is what the split is planned
// from, and a piece's own view of them is not a different fact about the
// strategy (section 4.7.3.1).
type PlanCensusIdentity struct {
	Plan            PlanIdentity
	StateGeneration StateGeneration
}

func (identity PlanCensusIdentity) Validate() error {
	if err := identity.Plan.Validate(); err != nil {
		return err
	}
	if identity.StateGeneration == "" {
		return errors.New("alarmd execution: census identity requires a state generation")
	}
	return nil
}

// DimensionValueCount is one value of one dimension and how many series of
// this Plan carried it in the round the census was taken.
type DimensionValueCount struct {
	Value  string `json:"value"`
	Series uint32 `json:"series"`
}

// DimensionCensusEntry is one dimension's distribution: the values carrying
// the most series, and what was left out.
//
// OverflowValues and OverflowSeries are two numbers because they answer two
// questions: how many values could not be named, and how many series are on
// them. A tail of ten thousand values with one series each and a tail of ten
// values with a thousand each are the same OverflowValues and very different
// splits.
type DimensionCensusEntry struct {
	Dimension      string                `json:"dimension"`
	Values         []DimensionValueCount `json:"values"`
	OverflowValues uint32                `json:"overflow_values"`
	OverflowSeries uint32                `json:"overflow_series"`
}

// DimensionCensus is one Plan's census as it is persisted and read.
type DimensionCensus struct {
	Identity   PlanCensusIdentity     `json:"-"`
	Source     DimensionCensusSource  `json:"source"`
	ObservedAt int64                  `json:"observed_at"`
	Series     uint32                 `json:"series"`
	Dimensions []DimensionCensusEntry `json:"dimensions"`
}

// Validate refuses a census a reader could mistake for a complete one.
func (census DimensionCensus) Validate() error {
	if err := census.Identity.Validate(); err != nil {
		return err
	}
	switch census.Source {
	case DimensionCensusFromRound, DimensionCensusFromRoster:
	default:
		return errors.New("alarmd execution: census source is unknown")
	}
	if census.ObservedAt <= 0 {
		return errors.New("alarmd execution: census requires the round it was taken in")
	}
	if len(census.Dimensions) == 0 || len(census.Dimensions) > MaxCensusDimensions {
		return errors.New("alarmd execution: census names no dimension, or too many")
	}
	seen := make(map[string]struct{}, len(census.Dimensions))
	for _, entry := range census.Dimensions {
		if entry.Dimension == "" {
			return errors.New("alarmd execution: census names an unnamed dimension")
		}
		if _, duplicate := seen[entry.Dimension]; duplicate {
			return errors.New("alarmd execution: census names one dimension twice")
		}
		seen[entry.Dimension] = struct{}{}
		if len(entry.Values) > MaxCensusValuesPerDimension {
			return errors.New("alarmd execution: census names more values than the bound")
		}
		values := make(map[string]struct{}, len(entry.Values))
		for _, value := range entry.Values {
			if value.Series == 0 {
				return errors.New("alarmd execution: census names a value no series carried")
			}
			if _, duplicate := values[value.Value]; duplicate {
				return errors.New("alarmd execution: census names one value twice")
			}
			values[value.Value] = struct{}{}
		}
	}
	return nil
}

// DimensionCensusBuilder counts a Plan's series by dimension value over one
// round. One per candidate Plan per Slot; a Plan that is not a candidate
// builds none, which is the whole of what keeps this off the ordinary path.
type DimensionCensusBuilder struct {
	identity PlanCensusIdentity
	source   DimensionCensusSource
	series   uint32
	counts   map[string]map[string]uint32
	// overflow is what each dimension could not track: values first seen
	// after the tracked set was full, and the series on them.
	overflowValues map[string]map[string]struct{}
	overflowSeries map[string]uint32
}

func NewDimensionCensusBuilder(identity PlanCensusIdentity, source DimensionCensusSource) *DimensionCensusBuilder {
	return &DimensionCensusBuilder{identity: identity, source: source,
		counts:         make(map[string]map[string]uint32),
		overflowValues: make(map[string]map[string]struct{}),
		overflowSeries: make(map[string]uint32)}
}

// ObserveSeries counts one series by the values it carries. Called once per
// series, with the dimensions of any one of its records: every record of a
// series carries the same identity dimensions, which is what makes the series
// that series.
func (builder *DimensionCensusBuilder) ObserveSeries(dimensions map[string]string) {
	if builder == nil || len(dimensions) == 0 {
		return
	}
	builder.series++
	for dimension, value := range dimensions {
		if dimension == "" {
			continue
		}
		values := builder.counts[dimension]
		if values == nil {
			if len(builder.counts) >= MaxCensusDimensions {
				continue
			}
			values = make(map[string]uint32)
			builder.counts[dimension] = values
		}
		if _, tracked := values[value]; !tracked && len(values) >= MaxCensusTrackedValuesPerDimension {
			// Past the tracked bound: counted whole rather than named. The
			// distinct count is kept apart from the series count because a
			// wide tail and a heavy tail are different splits.
			dropped := builder.overflowValues[dimension]
			if dropped == nil {
				dropped = make(map[string]struct{})
				builder.overflowValues[dimension] = dropped
			}
			dropped[value] = struct{}{}
			builder.overflowSeries[dimension]++
			continue
		}
		values[value]++
	}
}

// Build takes the census: each dimension's heaviest values, the rest counted
// whole. Nil when the round observed nothing, which is not a census and must
// not be written as one - an empty census read later would say the strategy
// has no series.
func (builder *DimensionCensusBuilder) Build(at int64) (DimensionCensus, bool, error) {
	if builder == nil || builder.series == 0 || len(builder.counts) == 0 {
		return DimensionCensus{}, false, nil
	}
	census := DimensionCensus{Identity: builder.identity, Source: builder.source, ObservedAt: at, Series: builder.series}
	dimensions := make([]string, 0, len(builder.counts))
	for dimension := range builder.counts {
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	for _, dimension := range dimensions {
		entry := DimensionCensusEntry{Dimension: dimension,
			OverflowValues: uint32(len(builder.overflowValues[dimension])),
			OverflowSeries: builder.overflowSeries[dimension]}
		values := make([]DimensionValueCount, 0, len(builder.counts[dimension]))
		for value, series := range builder.counts[dimension] {
			values = append(values, DimensionValueCount{Value: value, Series: series})
		}
		// Heaviest first, then by value: the values a split is planned around
		// are the ones carrying series, and the tie rule is there so two
		// replicas counting the same round write the same census.
		sort.Slice(values, func(left, right int) bool {
			if values[left].Series != values[right].Series {
				return values[left].Series > values[right].Series
			}
			return values[left].Value < values[right].Value
		})
		if len(values) > MaxCensusValuesPerDimension {
			for _, dropped := range values[MaxCensusValuesPerDimension:] {
				entry.OverflowValues++
				entry.OverflowSeries += dropped.Series
			}
			values = values[:MaxCensusValuesPerDimension]
		}
		entry.Values = values
		census.Dimensions = append(census.Dimensions, entry)
	}
	if err := census.Validate(); err != nil {
		return DimensionCensus{}, false, err
	}
	return census, true, nil
}

// CensusWriteStatus is what one census write did.
type CensusWriteStatus string

const (
	CensusWritten CensusWriteStatus = observability.DimensionCensusStatusWritten
	// CensusRejected is a census the store will not keep: it does not encode,
	// or it is larger than a value may be. Named rather than truncated - a
	// census cut to fit reads like a distribution, and a planner cannot tell
	// a cut one from a strategy that really has those values.
	CensusRejected CensusWriteStatus = observability.DimensionCensusStatusRejected
	// CensusRetryable is the store not answering.
	CensusRetryable CensusWriteStatus = observability.DimensionCensusStatusRetryable
)

// CensusWriteOutcome is one write's result, with the bytes it wrote or would
// have written so a refusal says how far over the bound it was.
type CensusWriteOutcome struct {
	Status     CensusWriteStatus
	ReasonCode ReasonCode
	Bytes      int
	Limit      int
}

// PlanCensusStore is where a Slot leaves the census it took.
type PlanCensusStore interface {
	WriteCensus(context.Context, DimensionCensus) (CensusWriteOutcome, error)
}

// CensusOverflow is what a census could not name, summed over its dimensions:
// the reading that says whether the bound was reached at all.
func (census DimensionCensus) CensusOverflow() (values uint32, series uint32) {
	for _, entry := range census.Dimensions {
		values += entry.OverflowValues
		series += entry.OverflowSeries
	}
	return values, series
}

// CensusValues is how many values the census names across its dimensions.
func (census DimensionCensus) CensusValues() int {
	total := 0
	for _, entry := range census.Dimensions {
		total += len(entry.Values)
	}
	return total
}
