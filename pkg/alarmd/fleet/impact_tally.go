// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ImpactPart names one of the counts Impact is made of.
type ImpactPart string

const (
	ImpactAnomalies    ImpactPart = "anomalies"
	ImpactOurs         ImpactPart = "ours"
	ImpactDemoted      ImpactPart = "demoted"
	ImpactUndecidable  ImpactPart = "undecidable"
	ImpactByDesign     ImpactPart = "by_design"
	ImpactBlind        ImpactPart = "blind"
	ImpactAlarmd       ImpactPart = "alarmd"
	ImpactUndetermined ImpactPart = "undetermined"
	ImpactStrategy     ImpactPart = "strategy"
	ImpactData         ImpactPart = "data"
)

// ImpactParts is every part, in Impact's order.
var ImpactParts = []ImpactPart{ImpactAnomalies, ImpactOurs, ImpactDemoted, ImpactUndecidable, ImpactByDesign,
	ImpactBlind, ImpactAlarmd, ImpactUndetermined, ImpactStrategy, ImpactData}

// ImpactTally is what Impact is counted from, kept in the shape that adds
// across replicas: the tally of each replica's rows, merged, is the tally of
// all of them. Objects and rows add, because an object is one replica's;
// strategies are a set, because a strategy with objects on two replicas is
// one strategy -- the reason impactOf takes a union and not a sum.
//
// Partial is not kept: a part is a sample when its objects outnumber the
// rows it was counted from, which is decided on the merged counts.
type ImpactTally struct {
	Parts        map[ImpactPart]*ImpactPartTally
	NoStrategies int
}

// ImpactPartTally is one part: how many objects it has, how many rows it
// was counted from, and the strategies those rows named.
type ImpactPartTally struct {
	Objects    int
	Listed     int
	Strategies map[StrategyRef]struct{}
}

// impactTallyWire is a tally as a replica publishes it: each strategy its
// parts name once, in order, and each part's strategies as positions in
// that list. A strategy is named by most of the parts a row is counted in,
// and written out in every one it took four times the bytes -- nine tenths
// of a replica's summary.
type impactTallyWire struct {
	Strategies   [][2]string                   `json:"strategies,omitempty"`
	Parts        map[ImpactPart]impactPartWire `json:"parts"`
	NoStrategies int                           `json:"no_strategies"`
}

type impactPartWire struct {
	Objects    int   `json:"objects"`
	Listed     int   `json:"listed"`
	Strategies []int `json:"strategies,omitempty"`
}

// MarshalJSON writes the tally with its strategies once, in order, so one
// tally always encodes to the same bytes.
func (tally ImpactTally) MarshalJSON() ([]byte, error) {
	distinct := map[StrategyRef]int{}
	for _, part := range tally.Parts {
		for strategy := range part.Strategies {
			distinct[strategy] = 0
		}
	}
	ordered := make([]StrategyRef, 0, len(distinct))
	for strategy := range distinct {
		ordered = append(ordered, strategy)
	}
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].StrategyID != ordered[right].StrategyID {
			return ordered[left].StrategyID < ordered[right].StrategyID
		}
		return ordered[left].BusinessID < ordered[right].BusinessID
	})
	wire := impactTallyWire{Strategies: make([][2]string, 0, len(ordered)), Parts: make(map[ImpactPart]impactPartWire, len(tally.Parts)),
		NoStrategies: tally.NoStrategies}
	for position, strategy := range ordered {
		distinct[strategy] = position
		wire.Strategies = append(wire.Strategies, [2]string{strategy.StrategyID, strategy.BusinessID})
	}
	for name, part := range tally.Parts {
		positions := make([]int, 0, len(part.Strategies))
		for strategy := range part.Strategies {
			positions = append(positions, distinct[strategy])
		}
		sort.Ints(positions)
		wire.Parts[name] = impactPartWire{Objects: part.Objects, Listed: part.Listed, Strategies: positions}
	}
	return json.Marshal(wire)
}

// UnmarshalJSON reads a published tally back into its sets, refusing a
// position its list does not have.
func (tally *ImpactTally) UnmarshalJSON(data []byte) error {
	var wire impactTallyWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	tally.NoStrategies = wire.NoStrategies
	tally.Parts = make(map[ImpactPart]*ImpactPartTally, len(wire.Parts))
	for name, part := range wire.Parts {
		read := &ImpactPartTally{Objects: part.Objects, Listed: part.Listed, Strategies: make(map[StrategyRef]struct{}, len(part.Strategies))}
		for _, position := range part.Strategies {
			if position < 0 || position >= len(wire.Strategies) {
				return fmt.Errorf("impact part %s names strategy %d of %d", name, position, len(wire.Strategies))
			}
			strategy := wire.Strategies[position]
			read.Strategies[StrategyRef{StrategyID: strategy[0], BusinessID: strategy[1]}] = struct{}{}
		}
		tally.Parts[name] = read
	}
	return nil
}

func newImpactTally() ImpactTally {
	tally := ImpactTally{Parts: make(map[ImpactPart]*ImpactPartTally, len(ImpactParts))}
	for _, part := range ImpactParts {
		tally.Parts[part] = &ImpactPartTally{Strategies: map[StrategyRef]struct{}{}}
	}
	return tally
}

// add counts rows into the part and says how many named no strategy.
func (part *ImpactPartTally) add(rows ...[]Anomaly) int {
	unnamed := 0
	for _, list := range rows {
		part.Listed += len(list)
		for _, row := range list {
			if len(row.Strategies) == 0 {
				unnamed++
				continue
			}
			for _, strategy := range row.Strategies {
				part.Strategies[strategy] = struct{}{}
			}
		}
	}
	return unnamed
}

// ImpactTallyOf tallies a view's rows; a view of one replica's snapshot
// gives that replica's part.
func ImpactTallyOf(view View, now time.Time) ImpactTally {
	tally := newImpactTally()
	for _, column := range []struct {
		part  ImpactPart
		total int
		rows  []Anomaly
	}{
		{ImpactAnomalies, view.AnomaliesTotal, view.Anomalies},
		{ImpactDemoted, view.DemotedTotal, view.Demoted},
		{ImpactUndecidable, view.UndecidableTotal, view.Undecidable},
		{ImpactByDesign, view.ByDesignTotal, view.ByDesign},
	} {
		part := tally.Parts[column.part]
		part.Objects = column.total
		tally.NoStrategies += part.add(column.rows)
	}
	blind := tally.Parts[ImpactBlind]
	blind.Objects = view.AnomaliesTotal + view.DemotedTotal
	blind.add(view.Anomalies, view.Demoted)

	ours := make([]Anomaly, 0, len(view.Anomalies))
	for _, anomaly := range view.Anomalies {
		if anomaly.Attribution == AttributionOurs {
			ours = append(ours, anomaly)
		}
	}
	tally.Parts[ImpactOurs].Objects = len(ours)
	tally.Parts[ImpactOurs].add(ours)

	for owner, list := range impactByOwner(view, now) {
		part := tally.Parts[ownerPart[owner]]
		if part == nil {
			continue
		}
		part.Objects = len(distinctObjects(list))
		part.add(list)
	}
	return tally
}

// ownerPart is the part each owner's objects are counted in.
var ownerPart = map[Owner]ImpactPart{
	OwnerAlarmd: ImpactAlarmd, OwnerUndetermined: ImpactUndetermined, OwnerStrategy: ImpactStrategy, OwnerData: ImpactData,
}

// impactByOwner is every column's rows by who acts, from each object's
// line, and the objects losing rounds now from the records.
func impactByOwner(view View, now time.Time) map[Owner][]Anomaly {
	byOwner := map[Owner][]Anomaly{}
	for _, column := range [][]Anomaly{view.Anomalies, view.Demoted, view.Undecidable, view.ByDesign, view.NoData} {
		for _, anomaly := range column {
			if anomaly.Finding.Check == "" {
				continue
			}
			owner := checkAnswers[anomaly.Finding.Check].Owner
			byOwner[owner] = append(byOwner[owner], anomaly)
		}
	}
	rows, _ := skippedRows(&view, map[string]struct{}{}, now)
	for _, row := range rows {
		if row.Loss == LossOngoing || row.Loss == LossAfterRestart {
			byOwner[OwnerAlarmd] = append(byOwner[OwnerAlarmd], row)
		}
	}
	return byOwner
}

// MergeImpactTallies adds replicas' tallies into one.
func MergeImpactTallies(tallies ...ImpactTally) ImpactTally {
	merged := newImpactTally()
	for _, tally := range tallies {
		merged.NoStrategies += tally.NoStrategies
		for name, part := range tally.Parts {
			into := merged.Parts[name]
			if into == nil {
				continue
			}
			into.Objects += part.Objects
			into.Listed += part.Listed
			for strategy := range part.Strategies {
				into.Strategies[strategy] = struct{}{}
			}
		}
	}
	return merged
}

// Impact is the tally as the page reads it.
func (tally ImpactTally) Impact() Impact {
	column := func(part ImpactPart) ColumnImpact {
		// A tally with no parts -- the part of a view nothing was read for --
		// counts nothing in any column.
		counted := tally.Parts[part]
		if counted == nil {
			counted = &ImpactPartTally{}
		}
		businesses := map[string]struct{}{}
		for strategy := range counted.Strategies {
			if strategy.BusinessID != "" {
				businesses[strategy.BusinessID] = struct{}{}
			}
		}
		return ColumnImpact{Objects: counted.Objects, Strategies: len(counted.Strategies), Businesses: len(businesses),
			Partial: counted.Objects > counted.Listed}
	}
	impact := Impact{
		Anomalies: column(ImpactAnomalies), Demoted: column(ImpactDemoted), Undecidable: column(ImpactUndecidable),
		ByDesign: column(ImpactByDesign), Blind: column(ImpactBlind), Ours: column(ImpactOurs),
		NoStrategies: tally.NoStrategies,
	}
	// Ours is counted off the published list, so it is a lower bound whenever
	// that list was cut -- the same limit as the column it sits in.
	impact.Ours.Partial = impact.Anomalies.Partial
	// Every owner part is a lower bound when any column was cut: a line draws
	// from all four.
	partial := impact.Anomalies.Partial || impact.Demoted.Partial || impact.Undecidable.Partial || impact.ByDesign.Partial
	owner := func(part ImpactPart) ColumnImpact {
		counted := column(part)
		counted.Partial = partial
		return counted
	}
	impact.Alarmd, impact.Undetermined = owner(ImpactAlarmd), owner(ImpactUndetermined)
	impact.Strategy, impact.Data = owner(ImpactStrategy), owner(ImpactData)
	return impact
}
