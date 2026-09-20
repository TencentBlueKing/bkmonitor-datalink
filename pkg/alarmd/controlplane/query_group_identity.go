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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// queryGroupIdentityFacts is what makes one Query Group one query: every
// fact a Plan's query carries except the revision that names the whole of
// it. Two Plans agreeing here run the same query once for both, which is
// what a Query Group is for.
//
// The set has to be the whole of QueryPlanFacts, not a chosen part of it.
// The revision digests all the facts, and the Catalog refuses a group whose
// Plans disagree on the revision; so a fact that the revision reads and the
// identity does not is a pair of Plans that are one group and two queries,
// and the Catalog stops building - not for those Plans, for the deployment.
// That is not a theory: adding a per-item query delay to the facts without
// adding it here stopped every Catalog build on a running deployment for
// three releases, and nothing but the last good Catalog kept executing.
//
// So this type is total over QueryPlanFacts, and
// TestQueryGroupIdentityReadsEveryQueryPlanFact fails on the next field that
// is added to one and not the other. It is a build-time failure in place of
// a deployment-time one.
//
// Every field added after the original set is omitempty, and the fields are
// in declaration order, so a Plan that carries none of them encodes exactly
// as it did before this type existed and keeps the identity it already has.
// Identity is how a Query Group is named in Ownership, in the schedule and
// in Runtime State; changing it for groups whose query did not change would
// retire all of them and activate the same queries under new names.
type queryGroupIdentityFacts struct {
	Provider      execution.ProviderKind             `json:"provider"`
	Route         execution.ProviderRouteRef         `json:"route"`
	Tenant        string                             `json:"tenant"`
	Business      string                             `json:"business"`
	Space         string                             `json:"space"`
	QueryList     []execution.QueryClause            `json:"query_list"`
	MetricMerge   string                             `json:"metric_merge"`
	StepMillis    int64                              `json:"step_millis"`
	Alignment     int64                              `json:"alignment_millis"`
	DownSample    execution.DownSampleRange          `json:"down_sample_range"`
	Timezone      string                             `json:"timezone"`
	NotTimeAlign  bool                               `json:"not_time_align"`
	Normalization execution.DatasetNormalizationSpec `json:"normalization"`
	// QueryDelay holds the query window back by the item's configured delay,
	// so two Plans that disagree on it ask for different windows and are not
	// one query.
	QueryDelay int64 `json:"query_delay_seconds,omitempty"`
	// SourceSemantics, PromQL and TSDBMap are the polling sources' facts:
	// which source semantics the query carries, the PromQL it runs instead
	// of a clause list, and the storage each reference resolves to. Each of
	// them makes the query a different query.
	SourceSemantics []string                            `json:"source_semantics,omitempty"`
	PromQL          *execution.PromQLQuery              `json:"promql,omitempty"`
	TSDBMap         map[string][]execution.QueryStorage `json:"tsdb_map,omitempty"`
}

// queryGroupIdentityFactsOf projects a Plan's facts onto the identity. The
// revision is the one fact left out: it is the digest of all the others, so
// including it would make every Plan its own group.
func queryGroupIdentityFactsOf(facts execution.QueryPlanFacts) queryGroupIdentityFacts {
	return queryGroupIdentityFacts{
		Provider: facts.Provider, Route: facts.ProviderRouteRef, Tenant: facts.TenantID,
		Business: facts.BusinessID, Space: facts.SpaceScope, QueryList: facts.QueryList,
		MetricMerge: facts.MetricMerge, StepMillis: facts.StepMillis, Alignment: facts.AlignmentMillis,
		DownSample: facts.DownSampleRange, Timezone: facts.Timezone, NotTimeAlign: facts.NotTimeAlign,
		Normalization: facts.Normalization, QueryDelay: facts.QueryDelaySeconds,
		SourceSemantics: facts.SourceSemantics, PromQL: facts.PromQL, TSDBMap: facts.TSDBMap,
	}
}

func deriveQueryGroupIdentity(facts execution.QueryPlanFacts) (execution.QueryGroupIdentity, error) {
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-query-group-identity-v1", queryGroupIdentityFactsOf(facts))
	return execution.QueryGroupIdentity(digest), err
}
