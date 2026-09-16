// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "time"

// The verdict panel answers "is alarmd well" and nothing else, and that is not
// the question an operator opens it with.
//
// Every number there counts objects. An object is one of this deployment's own
// identities: it cannot be looked up, cannot be mentioned to whoever configured
// the strategy, and a count of them does not say whether this is one
// misconfiguration or fifty. So a reader could see HEALTHY beside 58 demoted and
// 18 undecidable objects and still not know which alerts are not being raised,
// which ones can be raised but will never clear, how many businesses that
// touches, or whether any of it needs doing something about today.
//
// "alarmd 自己健康" and "告警业务正常" are different claims and the page was
// making only the first while reading as though it made both. Conforming to the
// design and being a business risk are not exclusive.
//
// So the verdict travels with the same population counted in the unit the work
// is actually done in: strategies, and the businesses they belong to.

// ColumnImpact is one column expressed as strategies and businesses.
type ColumnImpact struct {
	Objects    int `json:"objects"`
	Strategies int `json:"strategies"`
	Businesses int `json:"businesses"`
	// Partial says the objects these were counted from are a sample: a replica
	// publishes at most what fits its byte budget, so on a bad enough deployment
	// the list is already cut before anything here runs.
	//
	// It matters more here than on the object list. A truncated list still shows
	// which failures are present; a strategy count taken from it is a lower
	// bound presented in the same shape as an exact one, on the line a reader
	// uses to decide whether to act tonight.
	Partial bool `json:"partial"`
}

// Impact is every column in those units, plus the sub-split of the anomaly
// column that decides the verdict.
//
// Ours is a part of Anomalies rather than a fifth column, and is here because it
// is the only one of these numbers that answers "is this mine to fix".
type Impact struct {
	Anomalies   ColumnImpact `json:"anomalies"`
	Ours        ColumnImpact `json:"ours"`
	Demoted     ColumnImpact `json:"demoted"`
	Undecidable ColumnImpact `json:"undecidable"`
	ByDesign    ColumnImpact `json:"by_design"`
	// Blind is the demoted and anomaly columns taken together: the strategies
	// getting no detection result right now, for as long as this lasts. The
	// by-design column is not in it -- one round was voided on purpose and the
	// next runs under the new configuration.
	//
	// Computed over both lists at once rather than added up from the two above,
	// because a strategy with objects in both columns is one strategy. Adding
	// them overstates exactly the number a reader would act on, and overstates
	// it most when the deployment is worst.
	Blind ColumnImpact `json:"blind"`
	// Alarmd, Undetermined, Strategy and Data cut every column by who acts,
	// from each object's line, plus the objects losing rounds now on the
	// record lines. Three parts and not two, because "not confirmed as this
	// deployment's" and "confirmed as somebody else's" are different
	// statements: the page said the rest were the strategy's or the data's
	// people while the lines under it still read 待确认. Undetermined is the
	// part a reader must not hand over -- and must not close as fine.
	Alarmd       ColumnImpact `json:"alarmd"`
	Undetermined ColumnImpact `json:"undetermined"`
	Strategy     ColumnImpact `json:"strategy"`
	Data         ColumnImpact `json:"data"`
	// NoStrategies is how many objects across all columns named no strategy at
	// all, so the strategy counts above can be read for how much of the
	// population they cover.
	//
	// A blocked round never got a Slot and the strategy references are read off
	// the Slot, so those rows carry none by construction. Without this, a column
	// of blocked objects reports "0 条策略受影响" -- which reads as "nothing is
	// affected" and means "we cannot say what is affected", and those are
	// opposite instructions.
	NoStrategies int `json:"no_strategies"`
}

// impactOf counts one or more lists as a single population.
//
// It takes lists rather than one list so that two columns can be counted
// together without adding their answers: a strategy with objects in both is one
// strategy, and adding overstates the number a reader acts on -- by most when
// the deployment is worst.
func impactOf(total int, lists ...[]Anomaly) (ColumnImpact, int) {
	strategies := map[StrategyRef]struct{}{}
	businesses := map[string]struct{}{}
	unnamed, listed := 0, 0
	for _, anomalies := range lists {
		listed += len(anomalies)
		for _, anomaly := range anomalies {
			if len(anomaly.Strategies) == 0 {
				unnamed++
				continue
			}
			for _, strategy := range anomaly.Strategies {
				strategies[strategy] = struct{}{}
				if strategy.BusinessID != "" {
					businesses[strategy.BusinessID] = struct{}{}
				}
			}
		}
	}
	return ColumnImpact{
		Objects:    total,
		Strategies: len(strategies),
		Businesses: len(businesses),
		Partial:    total > listed,
	}, unnamed
}

// ImpactOf expresses the whole view in strategies and businesses.
//
// Counted from the lists the replicas published rather than from the totals,
// because a strategy count has no other source -- and the difference between
// the two is reported as Partial rather than hidden, since the deployment bad
// enough to truncate is the one being read during an incident.
func ImpactOf(view View, now time.Time) Impact {
	impact := Impact{}
	var unnamed int
	impact.Anomalies, unnamed = impactOf(view.AnomaliesTotal, view.Anomalies)
	impact.NoStrategies += unnamed
	impact.Demoted, unnamed = impactOf(view.DemotedTotal, view.Demoted)
	impact.NoStrategies += unnamed
	impact.Undecidable, unnamed = impactOf(view.UndecidableTotal, view.Undecidable)
	impact.NoStrategies += unnamed
	impact.ByDesign, unnamed = impactOf(view.ByDesignTotal, view.ByDesign)
	impact.NoStrategies += unnamed
	// The union, not the sum. The objects count does add -- an object is in one
	// column only -- and the strategies do not.
	impact.Blind, _ = impactOf(view.AnomaliesTotal+view.DemotedTotal, view.Anomalies, view.Demoted)

	// The verdict's own subset. Counted from the same list in the same pass so
	// it cannot disagree with the column it is part of.
	ours := make([]Anomaly, 0, len(view.Anomalies))
	for _, anomaly := range view.Anomalies {
		if anomaly.Attribution == AttributionOurs {
			ours = append(ours, anomaly)
		}
	}
	impact.Ours, _ = impactOf(len(ours), ours)
	// Ours is counted off the published list, so it is a lower bound whenever
	// that list was cut -- the same limit as the column it sits in, and it has
	// to say so for the same reason.
	impact.Ours.Partial = impact.Anomalies.Partial

	// By who acts, over every column from each object's line, and the
	// objects losing rounds now from the records. Every part is a lower
	// bound when any column was cut: a line draws from all four.
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
	partial := impact.Anomalies.Partial || impact.Demoted.Partial || impact.Undecidable.Partial || impact.ByDesign.Partial
	part := func(owner Owner) ColumnImpact {
		list := byOwner[owner]
		column, _ := impactOf(len(distinctObjects(list)), list)
		column.Partial = partial
		return column
	}
	impact.Alarmd, impact.Undetermined = part(OwnerAlarmd), part(OwnerUndetermined)
	impact.Strategy, impact.Data = part(OwnerStrategy), part(OwnerData)
	return impact
}

// distinctObjects is the set of objects in a list: an object under two
// lines of one owner is one object.
func distinctObjects(list []Anomaly) map[string]struct{} {
	set := map[string]struct{}{}
	for _, anomaly := range list {
		set[anomaly.QueryGroup] = struct{}{}
	}
	return set
}
