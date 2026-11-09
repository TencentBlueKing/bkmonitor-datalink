// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/testutil/routefixture"
)

func TestPartialSpaceRefreshQueries(t *testing.T) {
	fixture := routefixture.New(t)
	var spaces []space.Space
	require.NoError(t, fixture.DB.Find(&spaces).Error)
	full, err := preFetchSpaceTableIds(fixture.DB, spaces, nil)
	require.NoError(t, err)
	for _, sp := range spaces {
		if sp.BkTenantId != "system" || (sp.SpaceId != "1" && sp.SpaceId != "project" && sp.SpaceId != "app") {
			continue
		}
		t.Run(sp.SpaceTypeId, func(t *testing.T) {
			fixture.ResetOutput(t)
			queries := fixture.ObserveQueries(t)
			values, err := preFetchSpaceTableIds(fixture.DB.Where("bk_tenant_id = ?", sp.BkTenantId), []space.Space{sp}, &sp)
			require.NoError(t, err)
			key := service.SpaceRouteKeyWithTenant(sp.BkTenantId, sp.SpaceTypeId, sp.SpaceId)
			require.Equal(t, full[key], values[key])
			for _, table := range []string{"metadata_recordrule", "metadata_recordrulev4"} {
				entries := queries.ForTable(table)
				require.Len(t, entries, 1)
				require.Contains(t, entries[0].SQL, "space_type =")
				require.Contains(t, entries[0].SQL, "space_id =")
				require.LessOrEqual(t, entries[0].Rows, int64(3))
			}
			shortQueries := queries.ForTable("metadata_vmshortlinkrecord")
			require.Len(t, shortQueries, 1)
			require.Contains(t, shortQueries[0].SQL, "is_global =")
			require.Contains(t, shortQueries[0].SQL, " OR ")
			require.LessOrEqual(t, shortQueries[0].Rows, int64(4))
			if sp.SpaceTypeId == "bkcc" {
				for _, q := range queries.ForTable("metadata_resulttable") {
					require.NotContains(t, q.SQL, "LIKE")
				}
			}
			require.NotContains(t, values[key], "rule.v4_expired")
			if sp.SpaceTypeId == "bkcc" {
				require.Contains(t, values[key], "rule.v4_recent")
				require.Contains(t, values[key], "short.fallback")
			}
		})
	}
	t.Run("full_prefetch_reads_each_source_once", func(t *testing.T) {
		queries := fixture.ObserveQueries(t)
		_, err := preFetchSpaceTableIds(fixture.DB, spaces, nil)
		require.NoError(t, err)
		for _, table := range []string{"metadata_recordrule", "metadata_recordrulev4", "metadata_vmshortlinkrecord"} {
			entries := queries.ForTable(table)
			require.Len(t, entries, 1)
			require.Greater(t, entries[0].Rows, int64(100))
		}
	})
	t.Run("periodic_and_single_space_payloads_match", func(t *testing.T) {
		fixture.ResetOutput(t)
		patch := gomonkey.ApplyGlobalVar(&cfg.EnableMultiTenantMode, true)
		defer patch.Reset()
		require.NoError(t, PushAndPublishSpaceRouterInfo(context.Background(), nil))
		fullValues := fixture.Redis.HGetAll(cfg.SpaceToResultTableKey)
		fixture.Server.FlushAll()
		for _, sp := range spaces {
			if sp.BkTenantId != "system" || (sp.SpaceId != "1" && sp.SpaceId != "project" && sp.SpaceId != "app") {
				continue
			}
			key := service.SpaceRouteKeyWithTenant(sp.BkTenantId, sp.SpaceTypeId, sp.SpaceId)
			require.NotEmpty(t, fullValues[key])
			require.NoError(t, RefreshSpaceRoute(sp.BkTenantId, sp.SpaceTypeId, sp.SpaceId))
			require.JSONEq(t, fullValues[key], fixture.Redis.HGet(cfg.SpaceToResultTableKey, key))
			var values map[string]any
			require.NoError(t, json.Unmarshal([]byte(fullValues[key]), &values))
			for tableID := range values {
				require.False(t, strings.HasPrefix(tableID, "unrelated."), tableID)
			}
		}
		require.Len(t, fixture.Redis.HGetAll(cfg.SpaceToResultTableKey), 3)
	})
}
