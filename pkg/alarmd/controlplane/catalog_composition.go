// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

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
	// Withheld counts the objects that did not become a Plan, by the pair
	// (disposition, reason). The disposition alone cannot be acted on: a
	// CONFIG_REJECTED strategy has stopped detecting, while a STALE_CONFIG one
	// is still running its last good Plan, and the reason says which change
	// caused either. It is a partition of every object whose disposition is not
	// ACCEPTED, carrying the
	// reason the disposition partition has one.
	//
	// It exists because a reason nobody can read is not a diagnosis. These
	// reasons were written to be read on a live deployment, and until this they
	// lived only in the object page's Redis snapshot, which the read tooling
	// does not reach - so the only way to count what a rejection had withheld
	// was to publish it and watch a disposition total move.
	//
	// The reason is carried as it was written, with no list of accepted values
	// and no other to fold the rest into. A list would have to be exactly the
	// set the package attaches or the majority of objects would count as other
	// - the first version of this had twenty of the forty-odd reasons this
	// package writes, which would have put most of a deployment's rejections
	// under a label that names nothing. The set is finite because every reason
	// is a literal in this package's source; what bounds the metric is a count
	// of those literals, taken by a test that reads the source rather than a
	// list someone has to remember to extend.
	Withheld map[WithheldKey]int
	// WithheldObjects is the same objects the counts above are made of, one
	// record each, so a reader can ask which strategy rather than how many.
	//
	// It is filled by the same pass that fills Withheld, and a test holds the
	// two to the same total. Counting in one place and listing in another is
	// the shape where the page says forty and the log names thirty-nine and
	// nothing is wrong with either.
	WithheldObjects []ObjectDisposition
	// NoDataPlans counts the Plans that detect no-data, by where their expected
	// set comes from. Only accepted Plans are in it - a Plan that was withheld
	// is in Withheld under the reason that withheld it.
	//
	// The two together are one partition over the items that asked for no-data
	// detection: a source here, or a reason there. NoDataPlansPartition states
	// the sum, because a count of what is working answers nothing on its own -
	// three sources adding to fewer items than are configured is the reading
	// that matters, and it is only visible against what the other side holds.
	NoDataPlans map[nodata.RosterSource]int
	// NoDataPlansUnclassified counts an accepted Plan whose expected set cannot
	// be classified. The compiler refuses those, so this is zero and is here to
	// say so: were it not, the partition would lose a member silently and the
	// three sources would simply read low.
	NoDataPlansUnclassified int
	// NoDataNotConfigured counts the Plans that never asked for no-data
	// detection. It is not published beside the others -- the family answers
	// "what is no-data detection doing for the items that asked for it" -- and
	// it is computed all the same, because it is the third member of the
	// partition and a partition whose members are not all computed cannot be
	// checked. The test adds the three against the Plan count; without this
	// one, a suspended Plan filed as "never asked" would keep the published
	// family adding up while the objects disappeared.
	NoDataNotConfigured int
	// SuspendedNoDataObjects names the strategies whose no-data half is off,
	// one record each.
	//
	// They are ObjectDispositions because that is what the changed-only line
	// machinery takes, and because the fields are the same four an operator
	// reads -- which strategy, at what scope, why. They are deliberately not
	// in Catalog.Dispositions: that list is a partition of what happened to
	// each object, every one of these is ACCEPTED in it, and putting them in
	// twice would break the equation the partition exists to make checkable.
	SuspendedNoDataObjects []ObjectDisposition
	// RevisionedPlans counts the accepted Plans whose strategy carries an
	// authoritative snapshot revision, and PlansTotal every accepted Plan.
	// The revision is what makes a strategy publishable as the standard raw
	// event under the automatic protocol choice: a deployment whose source
	// publishes no revisions sends every event the Python-compatible way, and
	// its standard output path is unreachable however the sink is wired. On a
	// live deployment that fact took two lines of investigation and a Kafka
	// read to establish, from a gate counter that only ever said not gated.
	RevisionedPlans int
	PlansTotal      int
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
		Withheld:    make(map[WithheldKey]int),
		NoDataPlans: make(map[nodata.RosterSource]int, 3),
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
	for _, source := range NoDataRosterSources {
		composition.NoDataPlans[source] = 0
	}
	for _, key := range AlwaysReportedWithheld {
		composition.Withheld[key] = 0
	}
	for _, group := range catalog.QueryGroups {
		label := SourceSemanticsLabel(group.QueryPlan.SourceSemantics)
		composition.QueryGroups[label]++
		composition.Plans[label] += len(group.Plans)
		for _, plan := range group.Plans {
			composition.PlansTotal++
			if plan.Plan.StrategyRef.SnapshotRevision > 0 {
				composition.RevisionedPlans++
			}
			if !plan.ScheduleSpec.AffordsSettlingWait() {
				composition.InertPlans++
			}
			// Three states, read from two fields and never inferred from one.
			// A suspended Plan carries no no-data section either, so deciding
			// "not configured" by the section being absent would file every
			// suspended Plan as one that never asked -- the count would still
			// look like a partition and would have lost exactly the objects
			// this change exists to make visible.
			switch {
			case plan.NoDataSuspended != "":
				// Named as well as counted. A count of suspensions tells an
				// operator that some strategies are detecting thresholds and
				// not absence, and nothing at all about which -- and the
				// coverage list the migration is read from is a list of
				// strategies, not a number.
				composition.SuspendedNoDataObjects = append(composition.SuspendedNoDataObjects,
					ObjectDisposition{SourceID: plan.Identity.StrategyID, Scope: "PLAN",
						Disposition: DispositionAccepted, Reason: plan.NoDataSuspended})
				source, known := suspendedNoDataSource(plan.NoDataSuspended)
				if !known {
					// A reason nobody mapped. Counted where an unclassifiable
					// Plan is already counted rather than given a label of its
					// own: a label minted from whatever string arrived would
					// make the family's cardinality follow the reasons anyone
					// adds, and a reason that reaches here is a gap between
					// two vocabularies rather than a state of the fleet.
					composition.NoDataPlansUnclassified++
					continue
				}
				composition.NoDataPlans[source]++
			case plan.Plan.NoData == nil:
				composition.NoDataNotConfigured++
			default:
				class, err := nodata.ClassifyRoster(plan.Plan.TargetScope, plan.Plan.NoData.AggDimension)
				if err != nil {
					composition.NoDataPlansUnclassified++
					continue
				}
				composition.NoDataPlans[class.Source]++
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
		if kind == DispositionAccepted {
			continue
		}
		composition.Withheld[WithheldKey{Disposition: kind, Reason: disposition.Reason}]++
		// The same record, kept whole. The count says how many; this says
		// which, and the two cannot disagree because this is the line that
		// made the count.
		withheld := disposition
		withheld.Disposition = kind
		composition.WithheldObjects = append(composition.WithheldObjects, withheld)
	}
	return composition
}

// NoDataRosterSources is every source an accepted no-data Plan can declare, for
// the partition to pre-create and for a reader to bound the family by.
var NoDataRosterSources = []nodata.RosterSource{
	nodata.RosterTargetStatic,
	nodata.RosterHistory,
	nodata.RosterWhole,
	SuspendedNoDataConfigInvalid,
	SuspendedNoDataRosterUnsupported,
}

// The two states a Plan's no-data detection is in when it is configured and
// not running. They sit in the same family as the roster sources because the
// question the family answers is "what is this Plan's no-data detection
// doing", and "nothing, because this build cannot compile it" is one of the
// answers -- kept out, it would be an item that asked for detection and
// appears nowhere.
const (
	SuspendedNoDataConfigInvalid     = nodata.RosterSource("SUSPENDED_CONFIG_INVALID")
	SuspendedNoDataRosterUnsupported = nodata.RosterSource("SUSPENDED_ROSTER_UNSUPPORTED")
)

// suspendedNoDataSource maps the reason a Plan's no-data half was suspended to
// the bucket it is counted in.
//
// Two vocabularies, one mapping, in one place. The reason is what the line
// names to an operator -- the same word the withheld line used to carry, so a
// reader who knew it still knows it -- and the bucket is what the family is
// labelled by. Casting one to the other would work today and would make every
// future reason a new label nobody chose, on a family whose labels are meant
// to be a closed set.
func suspendedNoDataSource(reason string) (nodata.RosterSource, bool) {
	switch reason {
	case contract.ReasonNoDataConfigInvalid:
		return SuspendedNoDataConfigInvalid, true
	case contract.ReasonNoDataRosterUnsupported:
		return SuspendedNoDataRosterUnsupported, true
	}
	return "", false
}

// AlwaysReportedWithheld are the pairs the composition publishes even when
// nothing was withheld under them.
//
// Most pairs are not pre-created, and that is deliberate: the cross product of
// every disposition with every reason is mostly combinations that cannot
// happen, and publishing them would bury the ones that do. These two are here
// because their zero is itself a claim somebody acts on -- "no strategy in
// this deployment asks for a Snapshot kept longer than we keep one", "no
// strategy leaves its own queries no time to run" -- and a claim that reads
// identically to "this build does not produce that reason" is not one. Both
// are new enough that a reader has no way to tell those apart, and both are
// the acceptance reading for a change that withheld a Plan instead of
// refusing the whole Catalog.
var AlwaysReportedWithheld = []WithheldKey{
	{Disposition: DispositionUnsupported, Reason: contract.ReasonSnapshotRetentionInsufficient},
	{Disposition: DispositionUnsupported, Reason: contract.ReasonCompletionOffsetBelowReserve},
}

// NoDataReasons is the set of reasons that withhold a Plan from no-data
// detection. The partition below sums over them.
var NoDataReasons = []string{"NO_DATA_CONFIG_INVALID", "NO_DATA_ROSTER_UNSUPPORTED"}

// NoDataPlansPartition is how many items asked for no-data detection: the ones
// that got it, by source, plus the ones a no-data reason withheld.
//
// The withheld half is summed by reason across dispositions rather than read at
// one of them. A strategy refused for the first time is CONFIG_REJECTED, and
// the same strategy is STALE_CONFIG once a previous good Plan is retained for
// it - same reason, different disposition, on different rounds. Reading only
// CONFIG_REJECTED would make the total drop by one the round a strategy starts
// running its last good Plan, which reads as a gauge that lost a count rather
// than as a strategy that changed state.
func (composition CatalogComposition) NoDataPlansPartition() int {
	total := composition.NoDataPlansUnclassified
	for _, count := range composition.NoDataPlans {
		total += count
	}
	for key, count := range composition.Withheld {
		for _, reason := range NoDataReasons {
			if key.Reason == reason {
				total += count
			}
		}
	}
	return total
}

// WithheldKey pairs what happened to an object with why. Neither half answers
// on its own: the disposition says whether the strategy is still detecting, the
// reason says which configuration caused it.
type WithheldKey struct {
	Disposition Disposition
	Reason      string
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
