// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ReasonGlobalStrategyUnsupported withholds a global strategy this build
// cannot run as one. The detail names which condition it failed
// (reason=<word>); the words are below.
const ReasonGlobalStrategyUnsupported = "GLOBAL_STRATEGY_UNSUPPORTED"

// Why a global business strategy is withheld.
const (
	// GlobalBusinessLegacyTarget: the strategy names its target in the old
	// form. That path's no-data roster keeps only the hosts of the Plan's
	// own business, so every other business's hosts would lose no-data
	// detection without a word.
	GlobalBusinessLegacyTarget = "legacy_target"
	// GlobalBusinessQueryKind: the query is not a structured time series
	// query over the metric router. PromQL is scoped only by the space the
	// provider is told, and how logs, events and the computing platform's
	// tables are routed with the space skipped has not been established.
	GlobalBusinessQueryKind = "query_kind"
	// GlobalBusinessQueryTable: a query names no table or data label. With
	// the space skipped the provider has no space to find tables in, answers
	// no series with SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS, and the query is
	// unavailable on every round - the strategy would never be evaluated,
	// and would say so only as a query failure.
	GlobalBusinessQueryTable = "query_table"
	// GlobalBusinessOutputProtocol: the Plan would publish the compatible
	// event, which has no place for the business an alert is about.
	GlobalBusinessOutputProtocol = "output_protocol"
)

// GlobalBusinessRefusalWords is every word a GLOBAL_STRATEGY_UNSUPPORTED
// refusal names, the reason label's closed set in the global composition.
var GlobalBusinessRefusalWords = []string{
	GlobalBusinessLegacyTarget, GlobalBusinessQueryKind, GlobalBusinessQueryTable, GlobalBusinessOutputProtocol,
}

// globalBusinessQuerySemantics are the query sources a global business Plan
// may read: the structured time series the metric router serves. An empty
// SourceSemantics is the first of them alone.
var globalBusinessQuerySemantics = map[string]struct{}{
	"bk_monitor/time_series": {},
	"custom/time_series":     {},
}

// GlobalQuerySourcePromQL labels a global strategy whose query is PromQL.
// PromQL names no source semantics of its own and is refused for a reason of
// its own - the space is its only scope - so it is counted under this label
// rather than under the time series it may happen to read.
const GlobalQuerySourcePromQL = "promql"

// GlobalQuerySource is the label a global strategy's query is counted under in
// the global composition and named by in its refusal: promql, or the source
// semantics partition label (SourceSemanticsLabel), so the set stays closed.
//
// It exists because the first reading after global strategies reach a
// deployment is which query sources they were refused for, and that decides
// which source alarmd learns to run globally next. Until this the refusal
// said only the word; the source was in the compiled facts and went nowhere.
func GlobalQuerySource(facts execution.QueryPlanFacts) string {
	if facts.PromQL != nil {
		return GlobalQuerySourcePromQL
	}
	return SourceSemanticsLabel(facts.SourceSemantics)
}

// globalBusinessRefusal is the admission a global business strategy passes
// before its Plan is compiled, and nil for one that passes or is not global.
// The word it was refused for is returned beside the record, for the round's
// global composition to count without reading it back out of the detail.
// The output protocol is decided later and checked where it is.
func globalBusinessRefusal(
	sourceID string, identity SourceIdentity, targetScope *contract.TargetScopeV2, facts execution.QueryPlanFacts,
) (*ObjectDisposition, string) {
	if !identity.GlobalBusiness {
		return nil, ""
	}
	source := GlobalQuerySource(facts)
	refuse := func(reason, fieldPath string) (*ObjectDisposition, string) {
		refusal := globalBusinessUnsupported(sourceID, reason, fieldPath, source)
		return &refusal, reason
	}
	if targetScope != nil {
		return refuse(GlobalBusinessLegacyTarget, "items[0].target")
	}
	if facts.PromQL != nil {
		return refuse(GlobalBusinessQueryKind, "items[0].query_configs")
	}
	for _, semantics := range facts.SourceSemantics {
		if _, supported := globalBusinessQuerySemantics[semantics]; !supported {
			return refuse(GlobalBusinessQueryKind, "items[0].query_configs")
		}
	}
	for _, clause := range facts.QueryList {
		if !namesTableOrDataLabel(clause.TableID) {
			return refuse(GlobalBusinessQueryTable, "items[0].query_configs")
		}
	}
	return nil, ""
}

// namesTableOrDataLabel reports whether a query's table id routes on its
// own: the provider splits it at the first dot and finds tables by the part
// before it, a data label or the database of a table.
func namesTableOrDataLabel(tableID string) bool {
	database, _, _ := strings.Cut(tableID, ".")
	return strings.TrimSpace(database) != ""
}

// globalBusinessUnsupported names the word and the query source in the
// detail, so the object page says which source a strategy was refused for
// without a second read.
func globalBusinessUnsupported(sourceID, reason, fieldPath, source string) ObjectDisposition {
	return ObjectDisposition{
		SourceID: sourceID, Scope: "PLAN", Disposition: DispositionUnsupported,
		Reason: ReasonGlobalStrategyUnsupported, FieldPath: fieldPath, Detail: "reason=" + reason + " source=" + source,
	}
}
