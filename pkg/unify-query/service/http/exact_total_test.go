// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestExactTotalAttestationRequiresOneOptedInESRawQuery(t *testing.T) {
	query := &structured.QueryTs{QueryList: []*structured.Query{{ExactTotal: true}}}
	routes := []metadata.RouteInfo{{StorageType: metadata.ElasticsearchStorageType}}
	require.True(t, exactTotalAttested(query, routes))

	query.QueryList[0].ExactTotal = false
	require.False(t, exactTotalAttested(query, routes))
	query.QueryList[0].ExactTotal = true
	require.False(t, exactTotalAttested(query, []metadata.RouteInfo{{StorageType: metadata.BkSqlStorageType}}))
	require.False(t, exactTotalAttested(query, nil))
	query.QueryList = append(query.QueryList, &structured.Query{ExactTotal: true})
	require.False(t, exactTotalAttested(query, routes))

	result := ListData{Total: 10001, TotalExact: true}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"total_exact":true`)
}

func TestExactTotalRequestRejectsNonESRouteInsteadOfDowngrading(t *testing.T) {
	query := &structured.QueryTs{QueryList: []*structured.Query{{ExactTotal: true}}}
	err := requireExactTotalAttestation(query, []metadata.RouteInfo{{StorageType: metadata.BkSqlStorageType}})
	require.ErrorContains(t, err, "exact total")
	require.NoError(t, requireExactTotalAttestation(query,
		[]metadata.RouteInfo{{StorageType: metadata.ElasticsearchStorageType}}))
	query.QueryList[0].ExactTotal = false
	require.NoError(t, requireExactTotalAttestation(query, nil))
}

func TestExactTotalRejectsPartiallyMissingESIndexPrefixes(t *testing.T) {
	routes := metadata.QueryReference{"a": {{
		QueryList: metadata.QueryList{
			{TableID: "summary_rt", StorageType: metadata.ElasticsearchStorageType, DB: "valid-prefix"},
			{TableID: "summary_rt", StorageType: metadata.ElasticsearchStorageType},
		},
	}}}
	query := &structured.QueryTs{QueryList: []*structured.Query{{ExactTotal: true}}}
	filtered, err := filterRawRoutesForExactTotal(context.Background(), routes, query)
	require.ErrorContains(t, err, "index prefix")
	require.Nil(t, filtered)

	query.QueryList[0].ExactTotal = false
	filtered, err = filterRawRoutesForExactTotal(context.Background(), routes, query)
	require.NoError(t, err)
	require.Equal(t, 1, filtered.Count())
}
