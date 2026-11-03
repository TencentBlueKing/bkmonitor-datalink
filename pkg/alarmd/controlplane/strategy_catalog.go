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
	"sort"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// StrategyCatalogPage is one page of every strategy the published catalog
// accepted, read from the Leader's in-memory index alone: no store read, so
// a page costs only its own rows. Revision names the publication; a reader
// paging on changes when it does. A page ends between strategies, never
// inside one.
type StrategyCatalogPage struct {
	Revision   string                 `json:"revision"`
	Total      int                    `json:"total"`
	Strategies []StrategyCatalogEntry `json:"strategies"`
	Reason     string                 `json:"reason,omitempty"`
}

// StrategyCatalogEntry is one accepted strategy and every Plan it has.
type StrategyCatalogEntry struct {
	StrategyID string                `json:"strategy_id"`
	Tenant     string                `json:"tenant"`
	Business   string                `json:"business"`
	Plans      []StrategyCatalogPlan `json:"plans"`
}

// StrategyCatalogPlan is the Query Group a Plan runs in and the dimensions
// its series are told apart by, bounded as a lookup bounds them.
type StrategyCatalogPlan struct {
	QueryGroup   execution.QueryGroupIdentity `json:"query_group"`
	GroupBy      []string                     `json:"group_by"`
	GroupByTotal int                          `json:"group_by_total"`
}

// StrategyCatalog is a page of the published catalog's strategies.
func (view *DirectoryView) StrategyCatalog(offset, limit int) StrategyCatalogPage {
	return view.reconciler.publishedIndex().catalogPage(offset, limit)
}

func (index *strategyIndex) catalogPage(offset, limit int) StrategyCatalogPage {
	if index == nil {
		return StrategyCatalogPage{Reason: "LEADER_CATALOG_NOT_READY", Strategies: []StrategyCatalogEntry{}}
	}
	identities := index.allIdentities()
	page := StrategyCatalogPage{Revision: string(index.publication.SnapshotRevision) + ":" + strconv.FormatUint(uint64(index.publication.PublicationEpoch), 10),
		Total: len(identities), Strategies: []StrategyCatalogEntry{}}
	if offset < 0 {
		offset = 0
	}
	for _, identity := range identities[min(offset, len(identities)):] {
		if len(page.Strategies) >= limit {
			break
		}
		entry := StrategyCatalogEntry{StrategyID: identity.StrategyID, Tenant: identity.TenantID, Business: identity.BusinessID}
		for _, at := range index.plans[identity.StrategyID] {
			group := &index.groups[at.group]
			if group.Plans[at.plan].Identity != identity {
				continue
			}
			plan := StrategyCatalogPlan{QueryGroup: group.Identity}
			plan.GroupBy, plan.GroupByTotal = planGroupBy(group.QueryPlan)
			entry.Plans = append(entry.Plans, plan)
		}
		sort.Slice(entry.Plans, func(i, j int) bool { return entry.Plans[i].QueryGroup < entry.Plans[j].QueryGroup })
		page.Strategies = append(page.Strategies, entry)
	}
	return page
}
