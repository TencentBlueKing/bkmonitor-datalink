// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/recordrule"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/testutil/routefixture"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
)

// 源表初始化一次，所有用例只读共享；全局依赖 patch 不支持 t.Parallel。
func TestRouteRefresh(t *testing.T) {
	f := routefixture.New(t)
	run := func(name string, fn func(*testing.T)) { t.Run(name, func(t *testing.T) { f.ResetOutput(t); fn(t) }) }
	for _, tc := range []struct {
		name      string
		ids, want []string
	}{
		{"vm", []string{"vm.metric"}, []string{"vm.metric"}},
		{"influx_rt_with_vm_binding", []string{"cpu.used"}, []string{"cpu.used"}},
		{"dual_storage", []string{"dual.metric"}, []string{"dual.metric"}},
		{"missing", []string{"missing"}, nil}, {"unbound", []string{"unbound.metric"}, nil},
		{"duplicates", []string{"cpu.used", "cpu.used"}, []string{"cpu.used"}},
		{"mixed", []string{"cpu.used", "vm.metric", "missing"}, []string{"cpu.used", "vm.metric"}},
		{"empty_inner_query_is_noop", nil, nil},
	} {
		tc := tc
		run("metric_selection/"+tc.name, func(t *testing.T) {
			clusters, err := loadResultTableDetailClusterMap(f.DB, "system")
			require.NoError(t, err)
			got, err := NewSpacePusher().getTableInfoForAccessVMRecord("system", tc.ids, &routeDetailClusters{values: clusters, all: true})
			require.NoError(t, err)
			require.ElementsMatch(t, tc.want, routeKeys(got))
		})
	}
	for _, tc := range []struct {
		name      string
		ids, want []string
	}{
		{"shared_label", []string{"cpu.used"}, []string{"legacy", "system"}},
		{"duplicates", []string{"cpu.used", "cpu.used"}, []string{"legacy", "system"}},
		{"comma_and_wrong_option_type", []string{"multi.metric"}, []string{"alpha", "beta"}},
		{"alias_only", []string{"null.metric"}, []string{"alias_only"}},
		{"invalid_alias_json", []string{"empty.metric"}, nil},
		{"missing", []string{"missing"}, nil},
		{"disabled_rt_refreshes_remaining_label", []string{"disabled.metric"}, []string{"system"}},
		{"deleted_rt_refreshes_remaining_label", []string{"deleted.metric"}, []string{"system"}},
	} {
		tc := tc
		run("labels/"+tc.name, func(t *testing.T) {
			require.NoError(t, NewSpacePusher().PushDataLabelTableIds("system", tc.ids, true))
			got := f.Redis.HGetAll(cfg.DataLabelToResultTableKey)
			require.ElementsMatch(t, tc.want, routeKeys(got))
			if value, ok := got["system"]; ok {
				var ids []string
				require.NoError(t, json.Unmarshal([]byte(value), &ids))
				require.ElementsMatch(t, []string{"cpu.used", "cpu.idle"}, ids)
			}
		})
	}
	run("labels/direct_label_lookup_and_removed_label", func(t *testing.T) {
		got, err := NewSpacePusher().getDataLabelTableIdMap("system", []string{"alpha", "removed", "legacy"})
		require.NoError(t, err)
		require.Equal(t, []string{}, got["removed"])
		require.Equal(t, []string{"multi.metric"}, got["alpha"])
		require.ElementsMatch(t, []string{"cpu.used", "cpu.idle"}, got["legacy"])
	})
	run("labels/all_and_partial_preserve_other_fields", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushDataLabelTableIds("system", nil, true))
		before := f.Redis.HGetAll(cfg.DataLabelToResultTableKey)
		require.Contains(t, before, "alpha")
		require.Contains(t, before, "alias_only")
		require.NotContains(t, before, "")
		require.NoError(t, NewSpacePusher().PushDataLabelTableIds("system", []string{"cpu.used"}, true))
		require.Equal(t, before, f.Redis.HGetAll(cfg.DataLabelToResultTableKey))
	})
	run("tenant/same_rt_same_label_separate_outputs", func(t *testing.T) {
		patch := gomonkey.ApplyGlobalVar(&cfg.EnableMultiTenantMode, true)
		defer patch.Reset()
		for _, tenant := range []string{"system", "other"} {
			require.NoError(t, NewSpacePusher().PushTableIdDetail(tenant, []string{"cpu.used"}, false))
			require.NoError(t, NewSpacePusher().PushDataLabelTableIds(tenant, []string{"cpu.used"}, true))
		}
		require.ElementsMatch(t, []string{"cpu.used|system", "cpu.used|other"}, routeKeys(f.Redis.HGetAll(cfg.ResultTableDetailKey)))
		require.Equal(t, "vm_cpu.used", routeDetail(t, f, "cpu.used|system")["vm_rt"])
		require.Equal(t, "other_vm", routeDetail(t, f, "cpu.used|other")["vm_rt"])
		require.JSONEq(t, `["cpu.used"]`, f.Redis.HGet(cfg.DataLabelToResultTableKey, "system|other"))
	})
	run("tenant/unknown_tenant_no_foreign_data", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushTableIdDetail("unknown", nil, false))
		require.Empty(t, f.Redis.HGetAll(cfg.ResultTableDetailKey))
	})
	run("details/empty_tenant_rejected", func(t *testing.T) { require.Error(t, NewSpacePusher().PushTableIdDetail(" ", nil, false)) })
	run("details/blank_ids_do_not_become_full_refresh", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{""}, false))
		require.Empty(t, f.Redis.HGetAll(cfg.ResultTableDetailKey))
	})
	run("details/fields_independent_of_batch_composition", func(t *testing.T) {
		p := NewSpacePusher()
		require.NoError(t, p.PushTableIdDetail("system", []string{"cpu.used"}, false))
		single := f.Redis.HGet(cfg.ResultTableDetailKey, "cpu.used")
		require.Equal(t, []any{"usage"}, routeDetail(t, f, "cpu.used")["fields"])
		require.NoError(t, p.PushTableIdDetail("system", []string{"cpu.used", "vm.metric"}, false))
		require.JSONEq(t, single, f.Redis.HGet(cfg.ResultTableDetailKey, "cpu.used"))
		require.Equal(t, []any{"requests"}, routeDetail(t, f, "vm.metric")["fields"])
	})
	run("details/whitelist_current_fields_lost", func(t *testing.T) {
		got, err := NewSpacePusher().composeTableIdFields("system", []string{"null.metric"})
		require.NoError(t, err)
		require.Empty(t, got)
	})
	run("details/measurement_and_apm_ownership", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"cpu.used", "vm.metric", "cluster.metric", "shared.metric", "dual.metric"}, false))
		for id, want := range map[string]string{"cpu.used": models.MeasurementTypeBkTraditional, "vm.metric": models.MeasurementTypeBkStandardV2TimeSeries, "cluster.metric": models.MeasurementTypeBkSplit, "shared.metric": models.MeasurementTypeBkExporter} {
			require.Equal(t, want, routeDetail(t, f, id)["measurement_type"], id)
		}
		require.Equal(t, "checkout", routeDetail(t, f, "cpu.used")["labels"].(map[string]any)["apm_application_name"])
		require.Equal(t, "core", routeDetail(t, f, "cpu.used")["labels"].(map[string]any)["team"])
		require.NotContains(t, routeDetail(t, f, "vm.metric")["labels"], "apm_application_name")
		require.NotContains(t, routeDetail(t, f, "dual.metric")["labels"], "apm_application_name")
	})
	for _, tc := range []struct {
		id, field, kind, db string
		count               int
	}{
		{"logs", "logs.__default__", models.StorageTypeES, "logs-*", 1},
		{"doris.logs", "doris.logs", models.StorageTypeBkSql, "doris_doris.logs", 1},
		{"virtual.logs", "virtual.logs", models.StorageTypeBkSql, "logs_doris", 1},
		{"mixed.logs", "mixed.logs", models.StorageTypeES, "logs-*", 1},
		{"missing.origin", "", "", "", 0}, {"broken.logs", "", "", "", 0}, {"disabled.logs", "", "", "", 0}, {"bad.logs.name", "", "", "", 0},
	} {
		tc := tc
		run("log_detail/"+tc.id, func(t *testing.T) {
			require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{tc.id}, false))
			require.Len(t, f.Redis.HGetAll(cfg.ResultTableDetailKey), tc.count)
			if tc.count > 0 {
				got := routeDetail(t, f, tc.field)
				require.Equal(t, tc.kind, got["storage_type"])
				require.Equal(t, tc.db, got["db"])
			}
		})
	}
	run("log_detail/history_alias_and_option_whitelist", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"logs", "virtual.logs"}, false))
		got := routeDetail(t, f, "logs.__default__")
		history := got["storage_cluster_records"].([]any)
		require.Len(t, history, 3)
		ids := []any{}
		for _, h := range history {
			ids = append(ids, h.(map[string]any)["storage_id"])
		}
		require.ElementsMatch(t, []any{float64(29), float64(30), float64(40)}, ids)
		require.NotContains(t, got["options"], "data_label_aliases")
		require.Contains(t, got["options"], "time_field")
		require.Equal(t, map[string]any{"host": "raw.host"}, got["field_alias"])
		require.Equal(t, got["field_alias"], routeDetail(t, f, "virtual.logs")["field_alias"])
		require.Equal(t, "logs", routeDetail(t, f, "virtual.logs")["data_label"])
		require.Equal(t, map[string]any{"scope": "view"}, routeDetail(t, f, "virtual.logs")["labels"])
		require.NotContains(t, got["field_alias"], "removed")
	})
	run("details/full_partial_equivalence_and_unrelated_preservation", func(t *testing.T) {
		p := NewSpacePusher()
		require.NoError(t, p.PushTableIdDetail("system", nil, false))
		full := f.Redis.HGetAll(cfg.ResultTableDetailKey)
		require.Greater(t, len(full), 10)
		f.Server.FlushAll()
		require.NoError(t, f.Redis.HSet(cfg.ResultTableDetailKey, "sentinel", `{}`))
		require.NoError(t, p.PushTableIdDetail("system", []string{"cpu.used", "logs", "virtual.logs"}, false))
		partial := f.Redis.HGetAll(cfg.ResultTableDetailKey)
		require.Len(t, partial, 4)
		for _, id := range []string{"cpu.used", "logs.__default__", "virtual.logs"} {
			require.JSONEq(t, full[id], partial[id], id)
		}
	})
	run("details/501_log_tables_cross_batch_boundary", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushTableIdDetail("batch", nil, false))
		got := f.Redis.HGetAll(cfg.ResultTableDetailKey)
		require.Len(t, got, 501)
		require.Contains(t, got, "batch.000")
		require.Contains(t, got, "batch.500")
	})
	for _, tc := range []struct {
		name, id string
		opts     map[string]any
		exclude  []uint
		want     map[string]uint
	}{
		{"own_and_platform", "1", nil, nil, map[string]uint{"cpu.used": 101, "cpu.idle": 101, "vm.metric": 102, "platform.metric": 104}},
		{"authorized", "2", map[string]any{"includePlatformDataId": false}, nil, map[string]uint{"cpu.used": 101, "cpu.idle": 101}},
		{"exclude", "1", nil, []uint{101}, map[string]uint{"vm.metric": 102, "platform.metric": 104}},
		{"owned_only", "2", map[string]any{"includePlatformDataId": false, "fromAuthorization": false}, nil, map[string]uint{}},
		{"missing", "missing", map[string]any{"includePlatformDataId": false}, nil, map[string]uint{}},
	} {
		tc := tc
		run("space_selection/"+tc.name, func(t *testing.T) {
			got, err := NewSpacePusher().GetSpaceTableIdDataId("system", "bkcc", tc.id, nil, tc.exclude, optionx.NewOptions(tc.opts))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	run("space/authorized_filters", func(t *testing.T) {
		got, err := NewSpacePusher().composeData("system", "bkcc", "2", nil, nil, optionx.NewOptions(map[string]any{"includePlatformDataId": false}))
		require.NoError(t, err)
		routeJSON(t, `{"cpu.used":{"filters":[{"bk_biz_id":"2"}]},"cpu.idle":{"filters":[{"bk_biz_id":"2"}]}}`, got)
	})
	run("space/cluster_namespace_filters", func(t *testing.T) {
		got, err := NewSpacePusher().composeBcsSpaceClusterTableIds("bkci", "project")
		require.NoError(t, err)
		routeJSON(t, `{"cluster.metric":{"filters":[{"bcs_cluster_id":"BCS-single","namespace":null}]},"shared.metric":{"filters":[{"bcs_cluster_id":"BCS-shared","namespace":"ns-a"},{"bcs_cluster_id":"BCS-shared","namespace":"ns-b"}]}}`, got)
	})
	run("space/malformed_resource", func(t *testing.T) {
		_, err := NewSpacePusher().composeBcsSpaceClusterTableIds("bkci", "malformed")
		require.Error(t, err)
	})
	run("space/missing_resource", func(t *testing.T) {
		got, err := NewSpacePusher().composeBcsSpaceClusterTableIds("bkci", "missing")
		require.NoError(t, err)
		require.Empty(t, got)
	})
	for _, tc := range []struct{ kind, id string }{{"bkcc", "1"}, {"bkci", "project"}, {"bksaas", "app"}} {
		tc := tc
		run("space/write_"+tc.kind, func(t *testing.T) {
			require.NoError(t, NewSpacePusher().PushSpaceTableIds("system", tc.kind, tc.id, nil))
			require.NotEmpty(t, f.Redis.HGet(cfg.SpaceToResultTableKey, tc.kind+"__"+tc.id))
		})
	}
	run("prefetch/record_rules_old_and_v4_group_by_tenant", func(t *testing.T) {
		var old []recordrule.RecordRule
		var v4 []recordrule.RecordRuleV4
		require.NoError(t, f.DB.Find(&old).Error)
		require.NoError(t, f.DB.Find(&v4).Error)
		p := NewSpacePusher()
		require.Contains(t, p.ComposeRecordRuleTableIdValuesBySpace(old)["bkcc__1|system"], "rule.cpu")
		require.Contains(t, p.ComposeRecordRuleV4TableIdValuesBySpace(v4)["bkcc__1|system"], "rule.v4")
	})
	run("prefetch/shortlink_owner_global_project_and_fallback", func(t *testing.T) {
		var records []space.VMShortLinkRecord
		var spaces []space.Space
		require.NoError(t, f.DB.Where("is_enabled = ? AND is_deleted = ?", true, false).Find(&records).Error)
		require.NoError(t, f.DB.Find(&spaces).Error)
		got := NewSpacePusher().ComposeVMShortLinkTableIdValuesBySpace(records, spaces)
		routeJSON(t, `{"filters":[]}`, got["bkcc__1|system"]["short.global"])
		routeJSON(t, `{"filters":[{"owner":"2"}]}`, got["bkcc__2|system"]["short.global"])
		routeJSON(t, `{"filters":[{"project":"project"}]}`, got["bkci__project|system"]["short.project"])
		require.NotContains(t, got["bkcc__2|system"], "short.local")
		require.NotContains(t, got["bkcc__1|system"], "short.disabled")
		require.NotContains(t, got["bkcc__1|system"], "short.deleted")
		require.NotContains(t, got, "bkcc__20|other")
		require.Contains(t, got["bkcc__2|system"], "short.fallback")
	})
	run("prefetch/global_log_preserves_owner_and_filters_other_spaces", func(t *testing.T) {
		var opts []resulttable.ResultTableOption
		var rts []resulttable.ResultTable
		var spaces []space.Space
		require.NoError(t, f.DB.Where("name = ?", models.OptionQueryRouterConfig).Find(&opts).Error)
		require.NoError(t, f.DB.Find(&rts).Error)
		require.NoError(t, f.DB.Find(&spaces).Error)
		got := NewSpacePusher().ComposeLogGlobalTableIdValuesBySpace(opts, rts, spaces)
		require.NotContains(t, got["bkcc__1|system"], "global.logs")
		routeJSON(t, `{"filters":[{"owner":"2"}]}`, got["bkcc__2|system"]["global.logs"])
		routeJSON(t, `{"filters":[{"owner":"-4"}]}`, got["bkci__project|system"]["global.logs"])
		require.NotContains(t, got, "bkcc__20|other")
	})
	run("prefetch/apm_global_filters_and_tenant", func(t *testing.T) {
		var rts []resulttable.ResultTable
		var spaces []space.Space
		require.NoError(t, f.DB.Find(&rts).Error)
		require.NoError(t, f.DB.Find(&spaces).Error)
		got := NewSpacePusher().ComposeApmAllTypeTableIdValuesBySpace(rts, spaces)
		require.Contains(t, got["bkci__project|system"], "apm_global.metrics")
		routeJSON(t, `{"filters":[{"biz":"-4"}]}`, got["bkci__project|system"]["apm_global.metrics"])
		require.Contains(t, got["bksaas__app|system"], "apm_global.metrics")
		require.NotContains(t, got, "bkcc__1|system")
		require.NotContains(t, got, "bkci__other-project|other")
	})
	run("prefetch/merged_values_override_ordinary_route", func(t *testing.T) {
		prefetched := SpaceTableIdValues{"cpu.used": {"filters": []map[string]any{{"prefetch": "wins"}}}, "rule.v4": {"filters": []map[string]any{}}}
		require.NoError(t, NewSpacePusher().PushSpaceTableIds("system", "bkcc", "1", prefetched))
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(f.Redis.HGet(cfg.SpaceToResultTableKey, "bkcc__1")), &got))
		routeJSON(t, `{"filters":[{"prefetch":"wins"}]}`, got["cpu.used"])
		require.Contains(t, got, "rule.v4")
	})
	run("cleanup/normalized_log_key_retained_disabled_and_stale_removed", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"logs"}, false))
		for _, id := range []string{"missing", "deleted.metric"} {
			require.NoError(t, f.Redis.HSet(cfg.ResultTableDetailKey, id, `{}`))
		}
		NewSpaceRedisClearer().ClearRtDetail()
		require.ElementsMatch(t, []string{"logs.__default__"}, routeKeys(f.Redis.HGetAll(cfg.ResultTableDetailKey)))
	})
	run("cleanup/alias_and_tenant_fields_retained", func(t *testing.T) {
		patch := gomonkey.ApplyGlobalVar(&cfg.EnableMultiTenantMode, true)
		defer patch.Reset()
		require.NoError(t, NewSpacePusher().PushDataLabelTableIds("system", []string{"cpu.used"}, true))
		require.NoError(t, f.Redis.HSet(cfg.DataLabelToResultTableKey, "stale|system", `[]`))
		NewSpaceRedisClearer().ClearDataLabelToRt()
		require.Contains(t, f.Redis.HGetAll(cfg.DataLabelToResultTableKey), "legacy|system")
		require.NotContains(t, f.Redis.HGetAll(cfg.DataLabelToResultTableKey), "stale|system")
	})
	run("apps/one_app_multiple_spaces_disabled_empty", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushBkAppToSpace())
		require.JSONEq(t, `["bkcc__1","bkci__project"]`, f.Redis.HGet(cfg.BkAppToSpaceKey, "monitor"))
		require.JSONEq(t, `[]`, f.Redis.HGet(cfg.BkAppToSpaceKey, "disabled-app"))
	})
	run("publish/detail_false_then_changed_true", func(t *testing.T) {
		sub := f.Redis.Client.Subscribe(context.Background(), cfg.ResultTableDetailChannel)
		defer sub.Close()
		_, err := sub.Receive(context.Background())
		require.NoError(t, err)
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"cpu.used"}, false))
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err = sub.ReceiveMessage(ctx)
		require.Error(t, err)
		// fresh subscription after timeout; modify only the Redis output, never the shared source tables.
		sub.Close()
		sub = f.Redis.Client.Subscribe(context.Background(), cfg.ResultTableDetailChannel)
		defer sub.Close()
		_, err = sub.Receive(context.Background())
		require.NoError(t, err)
		require.NoError(t, f.Redis.HSet(cfg.ResultTableDetailKey, "cpu.used", `{}`))
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"cpu.used"}, true))
		ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
		defer cancel2()
		_, err = sub.ReceiveMessage(ctx2)
		require.NoError(t, err)
	})
	run("failure/source_query_error_does_not_overwrite_space", func(t *testing.T) {
		f.DB.Callback().Query().Before("gorm:query").Register("route_test_failure", func(scope *gorm.Scope) {
			if scope.TableName() == "metadata_resulttable" {
				scope.Err(errors.New("injected query failure"))
			}
		})
		defer f.DB.Callback().Query().Remove("route_test_failure")
		require.NoError(t, f.Redis.HSet(cfg.SpaceToResultTableKey, "bkcc__1", `{"previous":{}}`))
		require.Error(t, NewSpacePusher().PushSpaceTableIds("system", "bkcc", "1", nil))
		require.Equal(t, `{"previous":{}}`, f.Redis.HGet(cfg.SpaceToResultTableKey, "bkcc__1"))
	})
	run("failure/label_redis_write_error", func(t *testing.T) {
		f.Server.SetError("injected redis error")
		defer f.Server.SetError("")
		require.Error(t, NewSpacePusher().PushDataLabelTableIds("system", []string{"cpu.used"}, true))
	})
	run("failure/label_publish_retry_current_no_republish", func(t *testing.T) {
		count := 0
		patch := gomonkey.ApplyMethodFunc(&redis.Instance{}, "Publish", func(string, any) error { count++; return errors.New("injected publish failure") })
		defer patch.Reset()
		// vm.metric has one label so the retry cannot fail on a second not-yet-written label.
		require.Error(t, NewSpacePusher().PushDataLabelTableIds("system", []string{"vm.metric"}, true))
		require.NoError(t, NewSpacePusher().PushDataLabelTableIds("system", []string{"vm.metric"}, true))
		require.Equal(t, 1, count)
	})
	run("metrics/v3_expired_active_and_v4_active_retention", func(t *testing.T) {
		got, err := NewSpacePusher().composeTableIdFields("system", []string{"vm.metric", "shared.metric"})
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"requests"}, got["vm.metric"])
		require.ElementsMatch(t, []string{"active_old", "fresh"}, got["shared.metric"])
	})
	run("log_detail/default_storage_missing_does_not_select_alternate", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"missing.selected", "broken.logs"}, false))
		require.Empty(t, f.Redis.HGetAll(cfg.ResultTableDetailKey))
	})
	run("apm/ambiguous_ownership_is_omitted", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"platform.metric"}, false))
		require.NotContains(t, routeDetail(t, f, "platform.metric")["labels"], "apm_application_name")
	})
	run("labels/orphan_and_foreign_aliases_excluded", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushDataLabelTableIds("system", nil, true))
		got := f.Redis.HGetAll(cfg.DataLabelToResultTableKey)
		require.NotContains(t, got, "orphan_alias")
		require.NotContains(t, got, "foreign_alias")
	})
	run("prefetch/log_global_invalid_disabled_and_custom_project_filter", func(t *testing.T) {
		var opts []resulttable.ResultTableOption
		var rts []resulttable.ResultTable
		var spaces []space.Space
		require.NoError(t, f.DB.Where("name = ?", models.OptionQueryRouterConfig).Find(&opts).Error)
		require.NoError(t, f.DB.Find(&rts).Error)
		require.NoError(t, f.DB.Find(&spaces).Error)
		got := NewSpacePusher().ComposeLogGlobalTableIdValuesBySpace(opts, rts, spaces)
		routeJSON(t, `{"filters":[{"project":"project"}]}`, got["bkci__project|system"]["doris.logs"])
		for _, values := range got {
			require.NotContains(t, values, "disabled.logs")
			require.NotContains(t, values, "broken.logs")
		}
	})
	run("tenant/space_key_suffix_and_prefetch_tenant", func(t *testing.T) {
		patch := gomonkey.ApplyGlobalVar(&cfg.EnableMultiTenantMode, true)
		defer patch.Reset()
		require.NoError(t, NewSpacePusher().PushSpaceTableIds("system", "bkcc", "1", SpaceTableIdValues{"short.local": {"filters": []map[string]any{}}}))
		require.ElementsMatch(t, []string{"bkcc__1|system"}, routeKeys(f.Redis.HGetAll(cfg.SpaceToResultTableKey)))
	})
	run("record_rule/partial_metric_does_not_refresh_unrelated_rules", func(t *testing.T) {
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"cpu.used"}, false))
		require.Empty(t, f.Redis.HGet(cfg.ResultTableDetailKey, "rule.cpu"))
	})
	run("failure/log_write_error_is_tolerated_metric_write_error_returned", func(t *testing.T) {
		patch := gomonkey.ApplyMethodFunc(&redis.Instance{}, "HSetManyWithCompareAndPublish", func(string, map[string]string, string, bool) ([]string, error) {
			return nil, errors.New("injected batch write failure")
		})
		defer patch.Reset()
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"logs"}, true))
		require.ErrorContains(t, NewSpacePusher().PushTableIdDetail("system", []string{"cpu.used"}, true), "injected batch write failure")
	})
	run("publish/repeated_detail_refresh_no_duplicate_notification", func(t *testing.T) {
		p := NewSpacePusher()
		require.NoError(t, p.PushTableIdDetail("system", []string{"logs"}, true))
		sub := f.Redis.Client.Subscribe(context.Background(), cfg.ResultTableDetailChannel)
		defer sub.Close()
		_, err := sub.Receive(context.Background())
		require.NoError(t, err)
		require.NoError(t, p.PushTableIdDetail("system", []string{"logs"}, true))
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		_, err = sub.ReceiveMessage(ctx)
		require.Error(t, err)
	})

	run("query_scope/single_metric_reads_only_related_cluster_and_rule", func(t *testing.T) {
		queries := f.ObserveQueries(t)
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"cpu.used"}, false))
		clusterQueries := queries.ForTable("metadata_clusterinfo")
		require.Len(t, clusterQueries, 1)
		require.Contains(t, clusterQueries[0].SQL, " IN ")
		require.EqualValues(t, 1, clusterQueries[0].Rows)
		ruleQueries := queries.ForTable("metadata_recordrule")
		require.Len(t, ruleQueries, 1)
		require.Contains(t, ruleQueries[0].SQL, "table_id IN")
		require.Zero(t, ruleQueries[0].Rows)
		require.ElementsMatch(t, []string{"cpu.used"}, routeKeys(f.Redis.HGetAll(cfg.ResultTableDetailKey)))
	})
	run("query_scope/no_dependencies_skips_cluster_query", func(t *testing.T) {
		queries := f.ObserveQueries(t)
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"missing", "unbound.metric"}, false))
		require.Empty(t, queries.ForTable("metadata_clusterinfo"))
		require.Empty(t, f.Redis.HGetAll(cfg.ResultTableDetailKey))
	})
	run("query_scope/virtual_route_loads_current_origin_and_history_clusters_once", func(t *testing.T) {
		queries := f.ObserveQueries(t)
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", []string{"virtual.logs", "logs"}, false))
		clusterQueries := queries.ForTable("metadata_clusterinfo")
		require.Len(t, clusterQueries, 1)
		require.EqualValues(t, 3, clusterQueries[0].Rows)
		require.Contains(t, clusterQueries[0].SQL, " IN ")
		require.Empty(t, queries.ForTable("metadata_recordrule"))
	})
	run("query_scope/cluster_results_and_missing_ids_reused_across_batches", func(t *testing.T) {
		queries := f.ObserveQueries(t)
		clusters := &routeDetailClusters{tenantID: "system", values: make(map[uint]storage.ClusterInfo), queried: make(map[uint]bool)}
		p := NewSpacePusher()
		_, err := p.getTableInfoForAccessVMRecord("system", []string{"orphan.metric", "vm.metric"}, clusters)
		require.NoError(t, err)
		_, err = p.getTableInfoForAccessVMRecord("system", []string{"orphan.second", "dual.metric"}, clusters)
		require.NoError(t, err)
		require.Len(t, queries.ForTable("metadata_clusterinfo"), 1)
		require.NotContains(t, clusters.values, uint(999))
	})
	run("query_scope/full_501_tables_preloads_clusters_once", func(t *testing.T) {
		queries := f.ObserveQueries(t)
		require.NoError(t, NewSpacePusher().PushTableIdDetail("batch", nil, false))
		clusterQueries := queries.ForTable("metadata_clusterinfo")
		require.Len(t, clusterQueries, 1)
		require.NotContains(t, clusterQueries[0].SQL, " IN ")
		require.EqualValues(t, 1, clusterQueries[0].Rows)
		require.Len(t, queries.ForTable("metadata_esstorage"), 2)
		require.Len(t, f.Redis.HGetAll(cfg.ResultTableDetailKey), 501)
	})
	run("query_scope/full_tenant_reuses_clusters_across_log_metric_and_rules", func(t *testing.T) {
		queries := f.ObserveQueries(t)
		require.NoError(t, NewSpacePusher().PushTableIdDetail("system", nil, false))
		clusterQueries := queries.ForTable("metadata_clusterinfo")
		require.Len(t, clusterQueries, 1)
		require.Greater(t, clusterQueries[0].Rows, int64(100))
		ruleQueries := queries.ForTable("metadata_recordrule")
		require.Len(t, ruleQueries, 1)
		require.Greater(t, ruleQueries[0].Rows, int64(100))
	})

	run("record_rule/standalone_same_name_override_and_log_ownership", func(t *testing.T) {
		p := NewSpacePusher()
		require.NoError(t, p.PushTableIdDetail("system", []string{"rule.cpu", "dual.metric", "broken.logs"}, false))
		require.ElementsMatch(t, []string{"rule.cpu", "dual.metric"}, routeKeys(f.Redis.HGetAll(cfg.ResultTableDetailKey)))
		require.Equal(t, "rule_vm", routeDetail(t, f, "rule.cpu")["vm_rt"])
		require.Equal(t, "rule_override", routeDetail(t, f, "dual.metric")["vm_rt"])
		partial := f.Redis.HGetAll(cfg.ResultTableDetailKey)
		f.Server.FlushAll()
		require.NoError(t, p.PushTableIdDetail("system", nil, false))
		full := f.Redis.HGetAll(cfg.ResultTableDetailKey)
		for key, value := range partial {
			require.JSONEq(t, value, full[key])
		}
		require.Contains(t, full, "unrelated.rule99")
		patch := gomonkey.ApplyGlobalVar(&cfg.EnableMultiTenantMode, true)
		defer patch.Reset()
		require.NoError(t, p.PushTableIdDetail("other", []string{"rule.cpu"}, false))
		require.Equal(t, "other_rule", routeDetail(t, f, "rule.cpu|other")["vm_rt"])
	})
	run("space/query_scope_partial_cluster_exclusion_and_full_preload", func(t *testing.T) {
		queries := f.ObserveQueries(t)
		p := NewSpacePusher()
		partial, err := p.composeBkciOtherTableIds("system", "bkci", "project")
		require.NoError(t, err)
		require.Contains(t, partial, "vm.metric")
		require.NotContains(t, partial, "cluster.metric")
		clusterQueries := queries.ForTable("metadata_bcsclusterinfo")
		require.NotEmpty(t, clusterQueries)
		for _, q := range clusterQueries {
			require.Contains(t, q.SQL, " IN ")
			require.LessOrEqual(t, q.Rows, int64(3))
		}
		queries.Entries = nil
		var spaces []space.Space
		require.NoError(t, f.DB.Find(&spaces).Error)
		require.NoError(t, p.PreloadSpaceDependencies(spaces))
		require.Len(t, queries.ForTable("metadata_bcsclusterinfo"), 1)
		queries.Entries = nil
		full, err := p.composeBkciOtherTableIds("system", "bkci", "project")
		require.NoError(t, err)
		require.Equal(t, partial, full)
		_, err = p.composeBksaasOtherTableIds("system", "bksaas", "app")
		require.NoError(t, err)
		require.Empty(t, queries.ForTable("metadata_bcsclusterinfo"))
		queries.Entries = nil
		for i := 0; i < 2; i++ {
			_, err := p.composeBcsSpaceBizTableIds("bkci", "project")
			require.NoError(t, err)
		}
		require.Empty(t, queries.ForTable("metadata_resulttable"))
	})
	run("labels/query_scope_filters_and_mysql_explain", func(t *testing.T) {
		queries := f.ObserveQueries(t)
		require.NoError(t, NewSpacePusher().PushDataLabelTableIds("system", []string{"cpu.used"}, true))
		for _, q := range queries.ForTable("metadata_resulttable") {
			require.LessOrEqual(t, q.Rows, int64(2))
			require.True(t, strings.Contains(q.SQL, " IN ") || strings.Contains(q.SQL, "FIND_IN_SET"), q.SQL)
		}
		for _, q := range queries.ForTable("metadata_resulttableoption") {
			require.LessOrEqual(t, q.Rows, int64(2))
		}
		var rtPlan, aliasPlan []struct {
			Type  string
			Rows  int64
			Extra string
		}
		require.NoError(t, f.DB.Raw(`EXPLAIN FORMAT=TRADITIONAL SELECT table_id FROM metadata_resulttable WHERE bk_tenant_id='system' AND is_enable=1 AND is_deleted=0 AND FIND_IN_SET('system',data_label)>0`).Scan(&rtPlan).Error)
		require.NoError(t, f.DB.Raw(`EXPLAIN FORMAT=TRADITIONAL SELECT table_id FROM metadata_resulttableoption WHERE bk_tenant_id='system' AND name='data_label_aliases' AND value_type='list' AND JSON_CONTAINS(IF(JSON_VALID(value),value,'[]'),'"legacy"')`).Scan(&aliasPlan).Error)
		require.NotEmpty(t, rtPlan)
		require.NotEmpty(t, aliasPlan)
		t.Logf("MySQL fixture EXPLAIN (temporary tables, no production indexes): RT=%+v aliases=%+v", rtPlan, aliasPlan)
	})

}

func routeKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func routeJSON(t *testing.T, want string, got any) {
	t.Helper()
	b, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, want, string(b))
}

func routeDetail(t *testing.T, f *routefixture.Fixture, field string) map[string]any {
	t.Helper()
	raw := f.Redis.HGet(cfg.ResultTableDetailKey, field)
	require.NotEmpty(t, raw, fmt.Sprintf("missing detail %s", field))
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &got))
	return got
}
