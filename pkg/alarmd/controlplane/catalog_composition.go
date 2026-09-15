// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

// SupportedSourceSemantics is every data source a query can be compiled from,
// as pollingSourceSupported admits them. A query config outside this set is
// not compiled at all, so a Query Group's semantics are always drawn from
// here: the label built from them is bounded by this list and not by what
// anybody configures.
//
// It is also the list the question "which data sources are onboarded" is
// asking about, which is why the composition pre-creates a series for each
// of them: a source with no Query Groups has to read as zero and not as
// nothing, or "not onboarded" and "not looked at" are the same reading.
var SupportedSourceSemantics = []string{
	"bk_monitor/time_series",
	"bk_monitor/log",
	"bk_data/time_series",
	"bk_log_search/time_series",
	"bk_log_search/log",
	"custom/time_series",
	"custom/event",
	"prometheus/time_series",
	"bk_fta/event",
}

// SourceSemanticsOther is the label for semantics outside the supported list.
// It exists because the list above is a closed list over an open input: the
// compiler refuses unknown sources today, so this should stay zero, and a
// rising other is a source that started compiling without being named here.
const SourceSemanticsOther = "other"

// SourceSemanticsMixed is the label for a Query Group whose query reads more
// than one data source. Such a group is one entry and not one per source,
// so the family stays a partition; and it is this one entry rather than the
// combination spelled out, so the label set is the supported list plus two
// and not every subset of it. What mixed a particular group is belongs in
// the object page, not in a label that would be bounded by 2^n.
const SourceSemanticsMixed = "mixed"

// LegacyTimeSeriesSemantics is what an empty semantics list means. The legacy
// compiler clears the list when every query config is plain time series, so
// the absent value is not "unknown", it is this one, and saying so is what
// keeps the largest class of Query Groups from reading as unlabelled.
const LegacyTimeSeriesSemantics = "bk_monitor/time_series"

// CatalogComposition is what one built Catalog is made of: which data
// sources its Query Groups query, and what became of every source object it
// considered. It is derived from a Catalog, holds no identities, and is
// meant to be published as counts.
type CatalogComposition struct {
	// QueryGroups counts Query Groups by the data sources their query
	// carries, as one partition: every group falls in exactly one entry and
	// the entries sum to the group count. A group whose query mixes sources
	// has them joined, sorted, with "+", so the partition stays closed
	// rather than counting such a group once per source.
	QueryGroups map[string]int
	// Plans counts the Plans in those groups the same way. A Query Group is
	// one query for many strategies, so these two answer different questions:
	// how much querying a source costs, and how many strategies want it.
	Plans map[string]int
	// Objects counts source objects by disposition -- also a partition, over
	// the objects the round recorded a disposition for.
	Objects map[Disposition]int
	// InertPlans counts the Plans whose schedule cannot hold the wait their
	// data needs to land. Such a Plan is ACCEPTED, is scheduled, and executes
	// -- and every round every one of its consumers is bound unavailable,
	// because its readiness boundary lands past its own deadline. It detects
	// nothing, forever, and says nothing while doing it: the per-round
	// unavailability is indistinguishable from any other unavailability, and
	// the disposition reads as a Plan that works.
	//
	// It is counted rather than refused because refusing it is a decision
	// about how many Slots may overlap, not about this inequality. Counting
	// it is what makes the residual readable from outside instead of only
	// from the test that pins it.
	InertPlans int
}

// SourceSemanticsLabel is the partition key for one Query Group's query: the
// data source it reads, or mixed where it reads more than one.
func SourceSemanticsLabel(semantics []string) string {
	if len(semantics) == 0 {
		return LegacyTimeSeriesSemantics
	}
	supported := make(map[string]struct{}, len(SupportedSourceSemantics))
	for _, known := range SupportedSourceSemantics {
		supported[known] = struct{}{}
	}
	labels := make([]string, 0, len(semantics))
	seen := make(map[string]struct{}, len(semantics))
	for _, semantic := range semantics {
		label := semantic
		if _, known := supported[label]; !known {
			label = SourceSemanticsOther
		}
		if _, duplicate := seen[label]; duplicate {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	switch len(labels) {
	case 0:
		return LegacyTimeSeriesSemantics
	case 1:
		return labels[0]
	default:
		return SourceSemanticsMixed
	}
}

// ComposeCatalog reads a built Catalog into counts. Every supported source
// is present at zero, so a source that compiles nothing is a reading.
func ComposeCatalog(catalog Catalog) CatalogComposition {
	composition := CatalogComposition{
		QueryGroups: make(map[string]int, len(SupportedSourceSemantics)+2),
		Plans:       make(map[string]int, len(SupportedSourceSemantics)+2),
		Objects:     make(map[Disposition]int, len(CatalogDispositions)+1),
	}
	for _, semantics := range SupportedSourceSemantics {
		composition.QueryGroups[semantics] = 0
		composition.Plans[semantics] = 0
	}
	for _, label := range []string{SourceSemanticsOther, SourceSemanticsMixed} {
		composition.QueryGroups[label] = 0
		composition.Plans[label] = 0
	}
	for _, disposition := range CatalogDispositions {
		composition.Objects[disposition] = 0
	}
	composition.Objects[DispositionOther] = 0
	for _, group := range catalog.QueryGroups {
		label := SourceSemanticsLabel(group.QueryPlan.SourceSemantics)
		composition.QueryGroups[label]++
		composition.Plans[label] += len(group.Plans)
		for _, plan := range group.Plans {
			if !plan.ScheduleSpec.AffordsSettlingWait() {
				composition.InertPlans++
			}
		}
	}
	for _, disposition := range catalog.Dispositions {
		kind := disposition.Disposition
		if _, named := composition.Objects[kind]; !named {
			// A disposition this list does not name still has to be counted
			// somewhere visible: dropping it would make the partition stop
			// adding up without saying so, and a partition that quietly
			// loses members is worse than one with an other in it.
			kind = DispositionOther
		}
		composition.Objects[kind]++
	}
	return composition
}

// DispositionOther collects a disposition CatalogDispositions does not name,
// so that the partition keeps adding up and a disposition added without
// being listed here shows as a rising other rather than as a missing count.
const DispositionOther Disposition = "other"

// CatalogDispositions is every disposition an object can be given, for the
// partition to pre-create and for a reader to bound the family by.
var CatalogDispositions = []Disposition{
	DispositionAccepted,
	DispositionSourceIncomplete,
	DispositionConfigRejected,
	DispositionStaleConfig,
	DispositionPendingRemoval,
	DispositionRemoved,
	DispositionUnsupported,
	DispositionCompatibilityIgnored,
}
