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
			require.Len(t, shortQueries, 2)
			require.Contains(t, shortQueries[1].SQL, "NOT EXISTS")
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
				require.NotContains(t, values[key], "short.fallback")
			}
		})
	}
	t.Run("full_prefetch_reads_each_source_once", func(t *testing.T) {
		queries := fixture.ObserveQueries(t)
		_, err := preFetchSpaceTableIds(fixture.DB, spaces, nil)
		require.NoError(t, err)
		for _, table := range []string{"metadata_recordrule", "metadata_recordrulev4", "metadata_vmshortlinkrecord"} {
			entries := queries.ForTable(table)
			if table == "metadata_vmshortlinkrecord" {
				require.Len(t, entries, 2)
				require.Contains(t, entries[1].SQL, "NOT EXISTS")
			} else {
				require.Len(t, entries, 1)
			}
			require.Greater(t, entries[0].Rows, int64(100))
		}
	})
	t.Run("periodic_and_single_space_payloads_match", func(t *testing.T) {
		fixture.ResetOutput(t)
		patch := gomonkey.ApplyGlobalVar(&cfg.EnableMultiTenantMode, true)
		defer patch.Reset()
		db := fixture.DB
		for _, table := range []string{"shared.owner_all", "shared.owner_filtered", "short.local", "short.global", "short.project", "short.disabled", "bad.exempt", "bad.key"} {
			require.NoError(t, db.Exec(`INSERT INTO metadata_resulttable(table_id,bk_tenant_id,default_storage,is_enable,is_deleted,bk_biz_id)
                VALUES (?,'system','victoria_metrics',1,0,1)`, table).Error)
			config := `{"space_type":"all","filter_key":"appid","filter_value":"space_id","exempt_owner":true}`
			switch table {
			case "shared.owner_filtered":
				config = `{"space_type":"all","filter_key":"appid","filter_value":"space_id","exempt_owner":false}`
			case "bad.exempt":
				config = `{"exempt_owner":null}`
			case "bad.key":
				config = `{"filter_key":[]}`
			}
			require.NoError(t, db.Exec(`INSERT INTO metadata_resulttableoption(bk_tenant_id,table_id,name,value,value_type)
                VALUES ('system',?,'query_router_config',?,'dict')`, table, config).Error)
		}
		require.NoError(t, db.Exec(`UPDATE metadata_vmshortlinkrecord SET query_router_config=?
            WHERE table_id='short.project'`, `{"space_type":"bkci","filter_key":"project","filter_value":"space_id","exempt_owner":false}`).Error)
		require.NoError(t, db.Exec(`INSERT INTO metadata_vmshortlinkrecord(bk_tenant_id,table_id,is_enabled,is_deleted)
            VALUES ('other','shared.owner_all',1,0), ('system','shared.owner_filtered',0,1)`).Error)
		require.NoError(t, PushAndPublishSpaceRouterInfo(context.Background(), nil))
		fullValues := fixture.Redis.HGetAll(cfg.SpaceToResultTableKey)
		fixture.Server.FlushAll()
		for _, sp := range spaces {
			if sp.BkTenantId != "system" || (sp.SpaceId != "1" && sp.SpaceId != "2" && sp.SpaceId != "project" && sp.SpaceId != "app") {
				continue
			}
			key := service.SpaceRouteKeyWithTenant(sp.BkTenantId, sp.SpaceTypeId, sp.SpaceId)
			require.NotEmpty(t, fullValues[key])
			require.NoError(t, RefreshSpaceRoute(sp.BkTenantId, sp.SpaceTypeId, sp.SpaceId))
			require.JSONEq(t, fullValues[key], fixture.Redis.HGet(cfg.SpaceToResultTableKey, key))
			var values map[string]any
			require.NoError(t, json.Unmarshal([]byte(fullValues[key]), &values))
			require.NotContains(t, values, "short.disabled")
			require.NotContains(t, values, "short.fallback")
			require.NotContains(t, values, "bad.exempt")
			require.NotContains(t, values, "bad.key")
			filter := func(key, value string) map[string]any {
				return map[string]any{"filters": []any{map[string]any{key: value}}}
			}
			require.Equal(t, filter("appid", sp.SpaceId), values["shared.owner_filtered"])
			if sp.SpaceId == "1" {
				require.Equal(t, map[string]any{"filters": []any{}}, values["shared.owner_all"])
				require.Equal(t, map[string]any{"filters": []any{}}, values["short.global"])
				require.Contains(t, values, "short.local")
				require.Equal(t, filter("project", "1"), values["short.project"])
			} else {
				require.Equal(t, filter("appid", sp.SpaceId), values["shared.owner_all"])
				require.NotContains(t, values, "short.local")
				if sp.SpaceTypeId == "bkci" {
					require.Equal(t, filter("project", sp.SpaceId), values["short.project"])
				} else {
					require.NotContains(t, values, "short.project")
				}
			}
			for tableID := range values {
				require.False(t, strings.HasPrefix(tableID, "unrelated."), tableID)
			}
		}
		require.Len(t, fixture.Redis.HGetAll(cfg.SpaceToResultTableKey), 4)
	})
}
