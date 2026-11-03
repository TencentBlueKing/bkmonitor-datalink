// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import "sort"

// SourceCapability is the round's strategies counted on the capability
// matrix, by the Leader when it publishes: a reader of the summary, the
// overview among them, never reads the catalog for it.
type SourceCapability struct {
	// Cells is every area by every outcome, zero cells included.
	Cells []CapabilityCell `json:"cells"`
	// Reasons is every refusal that kept at least one strategy out of an
	// area, largest first, then by code.
	Reasons []CapabilityReason `json:"reasons"`
	// IgnoredByDesign is what alarmd leaves to the platform's own path by
	// design while the strategy runs on its own; in no cell.
	IgnoredByDesign []CapabilityReason `json:"ignored_by_design"`
}

// CapabilityCell is how many distinct strategies one area holds at one
// outcome.
type CapabilityCell struct {
	Area       string `json:"area"`
	Outcome    string `json:"outcome"`
	Strategies int    `json:"strategies"`
}

// CapabilityReason is one refusal code: its cell, how many distinct
// strategies it touches, and a few of them.
type CapabilityReason struct {
	Code       string   `json:"code"`
	Area       string   `json:"area"`
	Outcome    string   `json:"outcome"`
	Strategies int      `json:"strategies"`
	SampleIDs  []string `json:"sample_ids"`
}

// CapabilityRecord is one disposition record of the round, as the
// composition kept it.
type CapabilityRecord struct {
	StrategyID  string
	Disposition string
	Reason      string
	Scope       string
}

// capabilitySampleLimit bounds a reason's sample.
const capabilitySampleLimit = 3

// CountCapability counts the round's records on the matrix. listed is how
// many distinct strategies the source listed.
//
// A strategy is counted once per area, at its most severe outcome there; a
// strategy refused in two areas is in both, and leaves supported in both.
// supported is the listed strategies with nothing short of supported in the
// area. A record about the source's state (accepted, incomplete, stale,
// being removed) is no capability fact and is skipped; a reason the table
// does not know is the configuration's, not yet supported, never supported.
func CountCapability(records []CapabilityRecord, listed int) *SourceCapability {
	worst := make(map[string]map[string]string, len(CapabilityAreas))
	type reasonCount struct {
		area, outcome string
		strategies    map[string]struct{}
	}
	reasons := map[string]*reasonCount{}
	ignored := map[string]map[string]struct{}{}
	for _, record := range records {
		class, _ := ClassifyCapability(record.Disposition, record.Reason, record.Scope)
		code := record.Reason
		if code == "" {
			code = record.Disposition
		}
		if class.IgnoredByDesign {
			if ignored[code] == nil {
				ignored[code] = map[string]struct{}{}
			}
			ignored[code][record.StrategyID] = struct{}{}
			continue
		}
		if class.Area == "" {
			continue
		}
		area := worst[class.Area]
		if area == nil {
			area = map[string]string{}
			worst[class.Area] = area
		}
		// The first record of a strategy in the area is its outcome there;
		// MoreSevereCapability ranks an unknown word, the empty one too,
		// above every outcome.
		if held, seen := area[record.StrategyID]; seen {
			area[record.StrategyID] = MoreSevereCapability(held, class.Outcome)
		} else {
			area[record.StrategyID] = class.Outcome
		}
		reason := reasons[code]
		if reason == nil {
			reason = &reasonCount{area: class.Area, outcome: class.Outcome, strategies: map[string]struct{}{}}
			reasons[code] = reason
		}
		reason.outcome = MoreSevereCapability(reason.outcome, class.Outcome)
		reason.strategies[record.StrategyID] = struct{}{}
	}
	out := &SourceCapability{Cells: make([]CapabilityCell, 0, len(CapabilityAreas)*len(CapabilityOutcomes)),
		Reasons: []CapabilityReason{}, IgnoredByDesign: []CapabilityReason{}}
	for _, area := range CapabilityAreas {
		held := map[string]int{}
		for _, outcome := range worst[area] {
			held[outcome]++
		}
		for _, outcome := range CapabilityOutcomes {
			count := held[outcome]
			if outcome == CapabilitySupported {
				count = max(listed-len(worst[area]), 0)
			}
			out.Cells = append(out.Cells, CapabilityCell{Area: area, Outcome: outcome, Strategies: count})
		}
	}
	for code, reason := range reasons {
		out.Reasons = append(out.Reasons, CapabilityReason{Code: code, Area: reason.area, Outcome: reason.outcome,
			Strategies: len(reason.strategies), SampleIDs: capabilitySample(reason.strategies)})
	}
	for code, strategies := range ignored {
		out.IgnoredByDesign = append(out.IgnoredByDesign, CapabilityReason{Code: code, Strategies: len(strategies), SampleIDs: capabilitySample(strategies)})
	}
	for _, list := range [][]CapabilityReason{out.Reasons, out.IgnoredByDesign} {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Strategies != list[j].Strategies {
				return list[i].Strategies > list[j].Strategies
			}
			return list[i].Code < list[j].Code
		})
	}
	return out
}

// capabilitySample is up to capabilitySampleLimit of the strategies, in
// numeric order.
func capabilitySample(strategies map[string]struct{}) []string {
	ids := make([]string, 0, len(strategies))
	for id := range strategies {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return sortableStrategyID(ids[i]) < sortableStrategyID(ids[j]) })
	if len(ids) > capabilitySampleLimit {
		ids = ids[:capabilitySampleLimit]
	}
	return ids
}
