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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
)

func TestResolveAPMApplicationNames(t *testing.T) {
	valid := apmMetricApplicationRow{
		BkDataID: 1, BkBizID: 17, AppName: "checkout.v2-test", ResultTableID: "default.metrics",
		ApplicationMatchesTenant: true, DataSourceMatchesTenant: true,
	}
	sharedID := int64(0)
	tests := []struct {
		name       string
		alter      func(*apmMetricApplicationRow)
		candidates []apmMetricApplicationRow
		dataIDs    []uint
		want       string
	}{
		{name: "original name preserved", want: valid.AppName},
		{name: "same application multiple data IDs", dataIDs: []uint{1, 2}, candidates: []apmMetricApplicationRow{
			valid, {BkDataID: 2, BkBizID: 17, AppName: valid.AppName, ResultTableID: "older.metrics", ApplicationMatchesTenant: true, DataSourceMatchesTenant: true},
		}, want: valid.AppName},
		{name: "no APM candidate", candidates: []apmMetricApplicationRow{}},
		{name: "partially unknown data IDs", dataIDs: []uint{1, 2}},
		{name: "no data IDs", dataIDs: []uint{}},
		{name: "another application on data ID", candidates: []apmMetricApplicationRow{
			valid, {BkDataID: 1, BkBizID: 17, AppName: "inventory", ResultTableID: "other.metrics", ApplicationMatchesTenant: true, DataSourceMatchesTenant: true},
		}},
		{name: "another application on table", dataIDs: []uint{1, 2}, candidates: []apmMetricApplicationRow{
			valid, {BkDataID: 2, BkBizID: 17, AppName: "inventory", ResultTableID: "other.metrics", ApplicationMatchesTenant: true, DataSourceMatchesTenant: true},
		}},
		{name: "invalid candidate does not disappear", candidates: []apmMetricApplicationRow{
			valid, {BkDataID: 1, BkBizID: 17, AppName: valid.AppName, ResultTableID: "default.metrics", DataSourceMatchesTenant: true},
		}},
		{name: "application belongs to another tenant", alter: func(row *apmMetricApplicationRow) { row.ApplicationMatchesTenant = false }},
		{name: "data source belongs to another tenant", alter: func(row *apmMetricApplicationRow) { row.DataSourceMatchesTenant = false }},
		{name: "business on result table is not the ownership source", alter: func(row *apmMetricApplicationRow) { row.BkBizID = 18 }, want: valid.AppName},
		{name: "shared data source even if sole current owner", alter: func(row *apmMetricApplicationRow) { row.SharedDataSourceID = &sharedID }},
		{name: "empty application", alter: func(row *apmMetricApplicationRow) { row.AppName = "" }},
		{name: "blank application", alter: func(row *apmMetricApplicationRow) { row.AppName = " \t" }},
		{name: "unready application data source", alter: func(row *apmMetricApplicationRow) { row.ResultTableID = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := tt.candidates
			if candidates == nil {
				candidate := valid
				if tt.alter != nil {
					tt.alter(&candidate)
				}
				candidates = []apmMetricApplicationRow{candidate}
			}
			dataIDs := tt.dataIDs
			if dataIDs == nil {
				dataIDs = []uint{1}
			}
			rts := map[string]resulttable.ResultTable{"default.metrics": {}, "separate.metric": {}}
			var relations []resulttable.DataSourceResultTable
			for tableID := range rts {
				for _, dataID := range dataIDs {
					relations = append(relations, resulttable.DataSourceResultTable{TableId: tableID, BkDataId: dataID})
				}
			}
			names := resolveAPMApplicationNames(rts, relations, candidates)
			if tt.want == "" {
				assert.Empty(t, names)
			} else {
				assert.Equal(t, map[string]string{"default.metrics": tt.want, "separate.metric": tt.want}, names)
			}
		})
	}
}

func TestMetricRouteLabelsRebuildsOnlyApplicationOwnership(t *testing.T) {
	const original = `{"apm_application_name":"stale-owner","scene":"custom","other":{"key":1}}`
	rt := resulttable.ResultTable{Labels: json.RawMessage(original)}
	labels := metricRouteLabels("arbitrary.metric", rt, "checkout.v2-test")
	assert.Equal(t, "checkout.v2-test", labels[apmApplicationNameLabel])
	assert.Equal(t, "custom", labels["scene"])
	assert.Equal(t, map[string]any{"key": float64(1)}, labels["other"])
	assert.JSONEq(t, original, string(rt.Labels))

	labels = metricRouteLabels("arbitrary.metric", rt, "")
	assert.NotContains(t, labels, apmApplicationNameLabel)
	assert.Equal(t, "custom", labels["scene"])
	assert.Equal(t, map[string]any{apmApplicationNameLabel: "checkout"}, metricRouteLabels("arbitrary.metric", resulttable.ResultTable{}, "checkout"))
}

func insertAPMMetricOwner(t *testing.T, db *gorm.DB, tenantID, appName string, bizID int, dataID uint) {
	t.Helper()
	execResultTableDetailSQL(t, db,
		`INSERT INTO apm_metricdatasource (bk_data_id, bk_biz_id, app_name, result_table_id) VALUES (?, ?, ?, ?)`,
		dataID, bizID, appName, "default.metrics",
	)
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_datasource (bk_data_id, bk_tenant_id, etl_config) VALUES (?, ?, ?)`,
		dataID, tenantID, models.ETLConfigTypeBkStandardV2TimeSeries,
	)
	execResultTableDetailSQL(t, db,
		`INSERT INTO apm_apmapplication (bk_tenant_id, bk_biz_id, app_name, is_deleted, is_enabled) VALUES (?, ?, ?, ?, ?)`,
		tenantID, bizID, appName, true, false,
	)
}

func TestLoadAPMApplicationNamesTenantValidationAndBatching(t *testing.T) {
	db := setupResultTableDetailMySQL(t)
	const tenantID = "tenant-apm"
	rts := make(map[string]resulttable.ResultTable)
	var relations []resulttable.DataSourceResultTable
	for index := 0; index < resultTableDetailBatchSize+1; index++ {
		tableID := fmt.Sprintf("metric_%03d.separate", index)
		dataID := uint(index + 1)
		rts[tableID] = resulttable.ResultTable{BkBizId: 17}
		relations = append(relations, resulttable.DataSourceResultTable{TableId: tableID, BkDataId: dataID})
		insertAPMMetricOwner(t, db, tenantID, "checkout.v2-test", 17, dataID)
	}
	queryCount := 0
	db.Callback().Query().Before("gorm:query").Register("test:count_apm_ownership_queries", func(scope *gorm.Scope) {
		queryCount++
	})
	names, err := loadAPMApplicationNames(db, tenantID, rts, relations)
	require.NoError(t, err)
	assert.Len(t, names, resultTableDetailBatchSize+1)
	assert.Equal(t, 2, queryCount)
	assert.Equal(t, "checkout.v2-test", names["metric_000.separate"])

	// A candidate for another tenant must invalidate this DataID, rather than being
	// filtered out before deciding that the remaining candidate is unique.
	execResultTableDetailSQL(t, db,
		`INSERT INTO apm_metricdatasource (bk_data_id, bk_biz_id, app_name, result_table_id) VALUES (?, ?, ?, ?)`,
		1, 18, "foreign", "default.metrics",
	)
	execResultTableDetailSQL(t, db,
		`INSERT INTO apm_apmapplication (bk_tenant_id, bk_biz_id, app_name) VALUES (?, ?, ?)`,
		"other-tenant", 18, "foreign",
	)
	names, err = loadAPMApplicationNames(db, tenantID, rts, relations[:1])
	require.NoError(t, err)
	assert.Empty(t, names)

	execResultTableDetailSQL(t, db, `UPDATE metadata_datasource SET bk_tenant_id = ? WHERE bk_data_id = ?`, "other-tenant", 2)
	names, err = loadAPMApplicationNames(db, tenantID, rts, relations[1:2])
	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestPushTableIdDetailRefreshesExistingAPMApplicationOwnership(t *testing.T) {
	db := setupResultTableDetailMySQL(t)
	setupStorageRedisForTest(t)
	setMultiTenantModeForTest(t, true)
	oldKey := cfg.ResultTableDetailKey
	cfg.ResultTableDetailKey = "test:result_table_detail:apm_ownership"
	t.Cleanup(func() { cfg.ResultTableDetailKey = oldKey })
	const tenantID = "tenant-apm"
	const tableID = "separate.metric"
	const redisField = tableID + "|" + tenantID
	insertResultTable(t, db, tableID, tenantID, models.StorageTypeInfluxdb, nil, `{"apm_application_name":"stale-owner","scene":"custom"}`)
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_accessvmrecord (result_table_id, bk_tenant_id, vm_cluster_id, vm_result_table_id) VALUES (?, ?, ?, ?)`,
		tableID, tenantID, 7, "vm_app_metric",
	)
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_datasourceresulttable (table_id, bk_data_id, bk_tenant_id) VALUES (?, ?, ?)`,
		tableID, 1, tenantID,
	)
	// The same table ID in another tenant must not add a second DataID to this route.
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_datasourceresulttable (table_id, bk_data_id, bk_tenant_id) VALUES (?, ?, ?)`,
		tableID, 2, "other-tenant",
	)
	insertAPMMetricOwner(t, db, tenantID, "checkout.v2-test", 17, 1)
	insertCluster(t, db, tenantID, 7, "vm-app", models.StorageTypeVM)
	client := redis.GetStorageRedisInstance()
	require.NoError(t, client.HSet(cfg.ResultTableDetailKey, redisField, `{"labels":{"scene":"old"}}`))

	// nil is the same full-refresh entry used by the existing periodic task.
	require.NoError(t, NewSpacePusher().PushTableIdDetail(tenantID, nil, false))
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(client.HGet(cfg.ResultTableDetailKey, redisField)), &detail))
	assert.Equal(t, map[string]any{"scene": "custom", apmApplicationNameLabel: "checkout.v2-test"}, detail["labels"])
	assert.Equal(t, "vm_app_metric", detail["vm_rt"])

	// Sharing can change after publication; the next full refresh removes stale ownership.
	execResultTableDetailSQL(t, db, `UPDATE apm_metricdatasource SET shared_datasource_id = ? WHERE bk_data_id = ?`, 9, 1)
	require.NoError(t, NewSpacePusher().PushTableIdDetail(tenantID, nil, false))
	require.NoError(t, json.Unmarshal([]byte(client.HGet(cfg.ResultTableDetailKey, redisField)), &detail))
	assert.Equal(t, map[string]any{"scene": "custom"}, detail["labels"])

	// A missing optional APM schema still publishes the base route and clears old ownership.
	execResultTableDetailSQL(t, db, `DROP TEMPORARY TABLE apm_metricdatasource`)
	require.NoError(t, client.HSet(cfg.ResultTableDetailKey, redisField, `{"labels":{"apm_application_name":"stale-owner"}}`))
	require.NoError(t, NewSpacePusher().PushTableIdDetail(tenantID, nil, false))
	require.NoError(t, json.Unmarshal([]byte(client.HGet(cfg.ResultTableDetailKey, redisField)), &detail))
	assert.Equal(t, map[string]any{"scene": "custom"}, detail["labels"])
	assert.Equal(t, "vm_app_metric", detail["vm_rt"])
}
