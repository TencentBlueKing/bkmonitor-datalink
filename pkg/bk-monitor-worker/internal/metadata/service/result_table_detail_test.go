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
	"fmt"
	"testing"
	"time"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func setupResultTableDetailMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	session := &mysql.DBSession{}
	require.NoError(t, session.Open())
	db := session.DB
	// 每个测试使用单独连接及临时表：既复用真实 MySQL 行为，又不会读写
	// bmw_test 的持久表；连接关闭后临时表和夹具会由 MySQL 自动清理。
	db.DB().SetMaxOpenConns(1)
	db.DB().SetMaxIdleConns(1)
	db.LogMode(false)
	statements := []string{
		`CREATE TEMPORARY TABLE metadata_resulttable (table_id VARCHAR(128), default_storage VARCHAR(32), data_label VARCHAR(128), labels BLOB, bk_tenant_id VARCHAR(256), is_deleted BOOLEAN, is_enable BOOLEAN)`,
		`CREATE TEMPORARY TABLE metadata_resulttableoption (table_id VARCHAR(128), bk_tenant_id VARCHAR(256), name VARCHAR(128), value TEXT, value_type VARCHAR(32))`,
		`CREATE TEMPORARY TABLE metadata_datasourceresulttable (table_id VARCHAR(128), bk_data_id BIGINT, bk_tenant_id VARCHAR(256))`,
		`CREATE TEMPORARY TABLE metadata_datasourceoption (bk_data_id BIGINT, bk_tenant_id VARCHAR(256), name VARCHAR(128), value TEXT)`,
		`CREATE TEMPORARY TABLE metadata_esfieldqueryaliasoption (table_id VARCHAR(128), bk_tenant_id VARCHAR(256), field_path VARCHAR(255), query_alias VARCHAR(255), is_deleted BOOLEAN)`,
		`CREATE TEMPORARY TABLE metadata_esstorage (table_id VARCHAR(128), bk_tenant_id VARCHAR(256), storage_cluster_id INTEGER, source_type VARCHAR(32), index_set VARCHAR(128), origin_table_id VARCHAR(128))`,
		`CREATE TEMPORARY TABLE metadata_dorisstorage (table_id VARCHAR(128), bk_tenant_id VARCHAR(256), storage_cluster_id INTEGER, bkbase_table_id VARCHAR(128), origin_table_id VARCHAR(128))`,
		`CREATE TEMPORARY TABLE metadata_storageclusterrecord (id INTEGER, table_id VARCHAR(128), bk_tenant_id VARCHAR(256), cluster_id BIGINT, enable_time DATETIME(6), is_deleted BOOLEAN)`,
		`CREATE TEMPORARY TABLE metadata_clusterinfo (cluster_id INTEGER, bk_tenant_id VARCHAR(64), cluster_name VARCHAR(128), cluster_type VARCHAR(32))`,
		`CREATE TEMPORARY TABLE metadata_bcsclusterinfo (cluster_id VARCHAR(128), bk_tenant_id VARCHAR(256), K8sMetricDataID BIGINT, CustomMetricDataID BIGINT)`,
		"CREATE TEMPORARY TABLE metadata_influxdbstorage (table_id VARCHAR(128), bk_tenant_id VARCHAR(256), influxdb_proxy_storage_id INTEGER, `database` VARCHAR(128), real_table_name VARCHAR(128), partition_tag VARCHAR(128))",
		`CREATE TEMPORARY TABLE metadata_accessvmrecord (result_table_id VARCHAR(128), bk_tenant_id VARCHAR(256), vm_cluster_id INTEGER, vm_result_table_id VARCHAR(128))`,
		`CREATE TEMPORARY TABLE metadata_recordrule (table_id VARCHAR(128), bk_tenant_id VARCHAR(256), vm_cluster_id INTEGER, dst_vm_table_id VARCHAR(128), rule_metrics TEXT)`,
	}
	for _, statement := range statements {
		require.NoError(t, db.Exec(statement).Error)
	}

	patches := gomonkey.ApplyFunc(mysql.GetDBSession, func() *mysql.DBSession {
		return &mysql.DBSession{DB: db}
	})
	t.Cleanup(func() {
		patches.Reset()
		require.NoError(t, session.Close())
	})
	return db
}

func execResultTableDetailSQL(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	require.NoError(t, db.Exec(query, args...).Error)
}

func insertResultTable(t *testing.T, db *gorm.DB, tableID, tenantID, defaultStorage string, dataLabel *string, labels string) {
	insertResultTableWithState(t, db, tableID, tenantID, defaultStorage, dataLabel, labels, false, true)
}

func insertResultTableWithState(
	t *testing.T,
	db *gorm.DB,
	tableID, tenantID, defaultStorage string,
	dataLabel *string,
	labels string,
	isDeleted, isEnable bool,
) {
	t.Helper()
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_resulttable (table_id, default_storage, data_label, labels, bk_tenant_id, is_deleted, is_enable) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tableID, defaultStorage, dataLabel, []byte(labels), tenantID, isDeleted, isEnable,
	)
}

func insertESStorage(t *testing.T, db *gorm.DB, tableID, tenantID string, clusterID uint, indexSet, sourceType, originTableID string) {
	t.Helper()
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_esstorage (table_id, bk_tenant_id, storage_cluster_id, source_type, index_set, origin_table_id) VALUES (?, ?, ?, ?, ?, ?)`,
		tableID, tenantID, clusterID, sourceType, indexSet, originTableID,
	)
}

func insertDorisStorage(t *testing.T, db *gorm.DB, tableID, tenantID string, clusterID uint, bkbaseTableID, originTableID string) {
	t.Helper()
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_dorisstorage (table_id, bk_tenant_id, storage_cluster_id, bkbase_table_id, origin_table_id) VALUES (?, ?, ?, ?, ?)`,
		tableID, tenantID, clusterID, bkbaseTableID, originTableID,
	)
}

func insertCluster(t *testing.T, db *gorm.DB, tenantID string, clusterID uint, clusterName, clusterType string) {
	t.Helper()
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_clusterinfo (cluster_id, bk_tenant_id, cluster_name, cluster_type) VALUES (?, ?, ?, ?)`,
		clusterID, tenantID, clusterName, clusterType,
	)
}

func insertClusterRecord(t *testing.T, db *gorm.DB, id uint, tableID, tenantID string, clusterID int64, enableTime *time.Time, isDeleted bool) {
	t.Helper()
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_storageclusterrecord (id, table_id, bk_tenant_id, cluster_id, enable_time, is_deleted) VALUES (?, ?, ?, ?, ?, ?)`,
		id, tableID, tenantID, clusterID, enableTime, isDeleted,
	)
}

func loadRouteClustersForTest(t *testing.T, db *gorm.DB, tenantID string) map[uint]storage.ClusterInfo {
	t.Helper()
	clusterMap, err := loadResultTableDetailClusterMap(db, tenantID)
	require.NoError(t, err)
	return clusterMap
}

func seedMixedLogRoute(t *testing.T, db *gorm.DB, tenantID, tableID, defaultStorage string) {
	t.Helper()
	dataLabel := "mixed-label"
	insertResultTable(t, db, tableID, tenantID, defaultStorage, &dataLabel, `{"scene":"mixed"}`)
	insertESStorage(t, db, tableID, tenantID, 1, "mixed-es-index", "log", "")
	insertDorisStorage(t, db, tableID, tenantID, 2, "mixed_doris_table", "")
	insertCluster(t, db, tenantID, 1, "es-prod", models.StorageTypeES)
	insertCluster(t, db, tenantID, 2, "doris-prod", models.StorageTypeDoris)
	newer := time.Unix(200, 0)
	older := time.Unix(100, 0)
	insertClusterRecord(t, db, 11, tableID, tenantID, 1, &newer, false)
	insertClusterRecord(t, db, 10, tableID, tenantID, 2, &older, false)
	// 删除状态和其他租户记录都不能进入当前租户的历史分段。
	insertClusterRecord(t, db, 12, tableID, tenantID, 2, &newer, true)
	insertCluster(t, db, "other-tenant", 20, "other-es", models.StorageTypeES)
	// 同名 Storage 必须严格按租户隔离，不能覆盖当前租户的顶层配置。
	insertESStorage(t, db, tableID, "other-tenant", 21, "wrong-index", "wrong-source", "")
	insertDorisStorage(t, db, tableID, "other-tenant", 22, "wrong_doris_table", "")
	insertCluster(t, db, "other-tenant", 21, "wrong-es", models.StorageTypeES)
	insertCluster(t, db, "other-tenant", 22, "wrong-doris", models.StorageTypeDoris)
	insertClusterRecord(t, db, 13, tableID, tenantID, 20, &older, false)
	insertClusterRecord(t, db, 14, tableID, "other-tenant", 20, &newer, false)
}

func expectedMixedHistory() []map[string]any {
	return []map[string]any{
		{
			"storage_id": int64(1), "storage_type": models.StorageTypeES,
			"db": "mixed-es-index", "measurement": models.TSGroupDefaultMeasurement,
			"source_type": "log", "enable_time": int64(200),
		},
		{
			"storage_id": int64(2), "storage_type": models.StorageTypeBkSql,
			"storage_name": "doris-prod", "cluster_name": "doris-prod",
			"db": "mixed_doris_table", "measurement": models.DorisMeasurement,
			"enable_time": int64(100),
		},
	}
}

func TestSpacePusherPushTableIdDetailRequiresTenant(t *testing.T) {
	err := NewSpacePusher().PushTableIdDetail(" \t", nil, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bk_tenant_id is required")
	// 非空增量列表中的空值不能被清洗成“全量刷新”。此调用无需数据库即可返回。
	require.NoError(t, NewSpacePusher().PushTableIdDetail("tenant-a", []string{""}, false))
}

func TestGetTableIdCutterReusesDataIDMap(t *testing.T) {
	db := setupResultTableDetailMySQL(t)
	const tenantID = "tenant-cutter"
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_datasourceoption (bk_data_id, bk_tenant_id, name, value) VALUES (?, ?, ?, ?)`,
		uint(1001), tenantID, models.OptionDisableMetricCutter, "true",
	)
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_datasourceoption (bk_data_id, bk_tenant_id, name, value) VALUES (?, ?, ?, ?)`,
		uint(1002), "other-tenant", models.OptionDisableMetricCutter, "true",
	)

	queryCount := 0
	db.Callback().Query().Before("gorm:query").Register("test:count_table_id_cutter_queries", func(*gorm.Scope) {
		queryCount++
	})
	tableIDs := []string{"metric.enabled", "metric.other_tenant", "metric.without_data_id"}
	result, err := NewResultTableSvc(nil).GetTableIdCutter(tenantID, tableIDs, map[string]uint{
		"metric.enabled":      1001,
		"metric.other_tenant": 1002,
	})

	require.NoError(t, err)
	assert.Equal(t, map[string]bool{
		"metric.enabled":         true,
		"metric.other_tenant":    false,
		"metric.without_data_id": false,
	}, result)
	// 只查询 DataSourceOption；DataSourceResultTable 临时表保持为空。
	assert.Equal(t, 1, queryCount)
}

func TestGetTableIdClusterIdReusesDataIDMap(t *testing.T) {
	db := setupResultTableDetailMySQL(t)
	const tenantID = "tenant-bcs"
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_bcsclusterinfo (cluster_id, bk_tenant_id, K8sMetricDataID, CustomMetricDataID) VALUES (?, ?, ?, ?)`,
		"BCS-K8S-00001", tenantID, uint(1001), uint(2001),
	)
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_bcsclusterinfo (cluster_id, bk_tenant_id, K8sMetricDataID, CustomMetricDataID) VALUES (?, ?, ?, ?)`,
		"BCS-K8S-WRONG", "other-tenant", uint(1001), uint(2001),
	)
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_resulttableoption (table_id, bk_tenant_id, name, value, value_type) VALUES (?, ?, ?, ?, ?)`,
		"metric.binding", tenantID, models.BindingBcsClusterId, "BCS-K8S-BINDING", "string",
	)

	queryCount := 0
	db.Callback().Query().Before("gorm:query").Register("test:count_table_id_cluster_queries", func(*gorm.Scope) {
		queryCount++
	})
	tableIDs := []string{"metric.k8s", "metric.custom", "metric.binding", "metric.without_data_id"}
	result, err := NewSpacePusher().getTableIdClusterId(tenantID, tableIDs, map[string]uint{
		"metric.k8s":     1001,
		"metric.custom":  2001,
		"metric.binding": 3001,
	})

	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"metric.k8s":     "BCS-K8S-00001",
		"metric.custom":  "BCS-K8S-00001",
		"metric.binding": "BCS-K8S-BINDING",
	}, result)
	// 保留两次 BCSClusterInfo 和一次 binding option 查询；DataSourceResultTable
	// 临时表保持为空，确保 helper 只消费调用方传入的映射。
	assert.Equal(t, 3, queryCount)
}

func TestListLogTableIDsOnlyKeepsActiveResultTables(t *testing.T) {
	db := setupResultTableDetailMySQL(t)
	const tenantID = "tenant-log-list"
	const otherTenantID = "other-tenant"

	insertResultTable(t, db, "active.log", tenantID, models.StorageTypeES, nil, "")
	insertESStorage(t, db, "active.log", tenantID, 1, "active-index", "log", "")
	insertResultTable(t, db, "active.doris", tenantID, models.StorageTypeDoris, nil, "")
	insertDorisStorage(t, db, "active.doris", tenantID, 2, "active_doris", "")

	insertResultTableWithState(t, db, "deleted.log", tenantID, models.StorageTypeES, nil, "", true, true)
	insertESStorage(t, db, "deleted.log", tenantID, 1, "deleted-index", "log", "")
	insertResultTableWithState(t, db, "disabled.log", tenantID, models.StorageTypeDoris, nil, "", false, false)
	insertDorisStorage(t, db, "disabled.log", tenantID, 2, "disabled_doris", "")

	insertESStorage(t, db, "orphan_es", tenantID, 1, "orphan-index", "log", "")
	insertDorisStorage(t, db, "orphan_doris", tenantID, 2, "orphan_doris", "")
	// 另一租户的 RT 和当前租户没有 RT 的 Storage 都不能进入候选集合。
	insertResultTable(t, db, "shared_orphan", otherTenantID, models.StorageTypeES, nil, "")
	insertESStorage(t, db, "shared_orphan", tenantID, 1, "shared-index", "log", "")

	queryCount := 0
	db.Callback().Query().Before("gorm:query").Register("test:count_list_log_table_ids", func(*gorm.Scope) {
		queryCount++
	})
	tableIDs, err := NewSpacePusher().listLogTableIDs(tenantID, nil)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"active.log", "active.doris"}, tableIDs)
	assert.Equal(t, 1, queryCount)
}

func TestComposeLogTableIdDetailMixedStoragePayloads(t *testing.T) {
	tests := []struct {
		name           string
		defaultStorage string
		expected       map[string]any
	}{
		{
			name: "ES current with Doris history", defaultStorage: models.StorageTypeES,
			expected: map[string]any{
				"storage_type": models.StorageTypeES, "storage_id": uint(1),
				"db": "mixed-es-index", "measurement": models.TSGroupDefaultMeasurement,
				"source_type": "log", "options": map[string]any{},
				"storage_cluster_records": expectedMixedHistory(), "data_label": "mixed-label",
				"labels": map[string]any{"scene": "mixed"}, "field_alias": map[string]string{},
			},
		},
		{
			name: "Doris current with ES history", defaultStorage: models.StorageTypeDoris,
			expected: map[string]any{
				"storage_type": models.StorageTypeBkSql, "storage_id": uint(2),
				"storage_name": "doris-prod", "cluster_name": "doris-prod",
				"db": "mixed_doris_table", "measurement": models.DorisMeasurement,
				"storage_cluster_records": expectedMixedHistory(), "data_label": "mixed-label",
				"labels": map[string]any{"scene": "mixed"}, "field_alias": map[string]string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupResultTableDetailMySQL(t)
			const tenantID = "tenant-a"
			const tableID = "2_bklog.mixed"
			seedMixedLogRoute(t, db, tenantID, tableID, tt.defaultStorage)
			clusterMap := loadRouteClustersForTest(t, db, tenantID)

			queryCount := 0
			db.Callback().Query().Before("gorm:query").Register("test:count_result_table_detail_queries", func(*gorm.Scope) {
				queryCount++
			})
			details, claimed, err := NewSpacePusher().composeLogTableIdDetail(
				tenantID, []string{tableID}, clusterMap,
			)

			require.NoError(t, err)
			assert.Equal(t, map[string]struct{}{tableID: {}}, claimed)
			assert.Equal(t, tt.expected, details[tableID])
			// ClusterInfo 已在批次外按租户加载；实体表批次只查询 RT、两类
			// Storage、option、alias 和 Record，origin 命中当前批次时不重复查询。
			assert.Equal(t, 6, queryCount)
		})
	}
}

func TestAccessVMRecordAndRecordRulePayloadsRemainCompatible(t *testing.T) {
	t.Run("metric routes only use AccessVMRecord", func(t *testing.T) {
		db := setupResultTableDetailMySQL(t)
		const tenantID = "tenant-metric"
		// 没有 AccessVMRecord 的候选表不生成指标查询路由。
		execResultTableDetailSQL(t, db,
			"INSERT INTO metadata_influxdbstorage (table_id, bk_tenant_id, influxdb_proxy_storage_id, `database`, real_table_name, partition_tag) VALUES (?, ?, ?, ?, ?, ?)",
			"metric.influx_only", tenantID, 1, "metric_db", "metric_measurement", "bk_target_ip,bk_cloud_id",
		)
		// default_storage 兼容旧值时，payload 仍以 AccessVMRecord 为准。
		execResultTableDetailSQL(t, db,
			"INSERT INTO metadata_influxdbstorage (table_id, bk_tenant_id, influxdb_proxy_storage_id, `database`, real_table_name, partition_tag) VALUES (?, ?, ?, ?, ?, ?)",
			"metric.vm", tenantID, 1, "must_not_leak", "must_not_leak", "must_not_leak",
		)
		insertCluster(t, db, tenantID, 6, "vm-prod", models.StorageTypeVM)
		// ClusterInfo 现在按租户全量预加载；VM 记录引用到同租户的
		// 非 VM 集群时，不能把它的名称当作 storage_name。
		insertCluster(t, db, tenantID, 9, "not-vm", models.StorageTypeES)
		insertCluster(t, db, "other-tenant", 7, "vm-other", models.StorageTypeVM)
		execResultTableDetailSQL(t, db,
			`INSERT INTO metadata_accessvmrecord (result_table_id, bk_tenant_id, vm_cluster_id, vm_result_table_id) VALUES (?, ?, ?, ?)`,
			"metric.vm", tenantID, 6, "vm_metric_target",
		)
		execResultTableDetailSQL(t, db,
			`INSERT INTO metadata_accessvmrecord (result_table_id, bk_tenant_id, vm_cluster_id, vm_result_table_id) VALUES (?, ?, ?, ?)`,
			"metric.vm", "other-tenant", 7, "wrong_vm_target",
		)
		execResultTableDetailSQL(t, db,
			`INSERT INTO metadata_accessvmrecord (result_table_id, bk_tenant_id, vm_cluster_id, vm_result_table_id) VALUES (?, ?, ?, ?)`,
			"metric.vm_wrong_type", tenantID, 9, "vm_wrong_type_target",
		)
		execResultTableDetailSQL(t, db,
			`INSERT INTO metadata_resulttableoption (table_id, bk_tenant_id, name, value, value_type) VALUES (?, ?, ?, ?, ?)`,
			"metric.vm", tenantID, models.CmdbLevelVmrt, "vm_cmdb_level", "string",
		)
		execResultTableDetailSQL(t, db,
			`INSERT INTO metadata_resulttableoption (table_id, bk_tenant_id, name, value, value_type) VALUES (?, ?, ?, ?, ?)`,
			"metric.vm", "other-tenant", models.CmdbLevelVmrt, "wrong_cmdb_level", "string",
		)
		metricTableIDs, err := NewSpacePusher().listMetricTableIDs(tenantID, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"metric.vm", "metric.vm_wrong_type"}, metricTableIDs)

		queryCount := 0
		db.Callback().Query().Before("gorm:query").Register("test:count_metric_storage_queries", func(*gorm.Scope) {
			queryCount++
		})
		clusterMap := loadRouteClustersForTest(t, db, tenantID)
		details, err := NewSpacePusher().getTableInfoForAccessVMRecord(
			tenantID, []string{"metric.influx_only", "metric.vm", "metric.vm_wrong_type"}, clusterMap,
		)

		require.NoError(t, err)
		assert.Equal(t, map[string]map[string]any{
			"metric.vm": {
				"vm_rt": "vm_metric_target", "storage_name": "vm-prod", "storage_id": uint(6),
				"cmdb_level_vm_rt": "vm_cmdb_level", "cluster_name": "", "db": "",
				"measurement": "", "tags_key": []string{}, "storage_type": models.StorageTypeVM,
			},
			"metric.vm_wrong_type": {
				"vm_rt": "vm_wrong_type_target", "storage_name": "", "storage_id": uint(9),
				"cmdb_level_vm_rt": "", "cluster_name": "", "db": "",
				"measurement": "", "tags_key": []string{}, "storage_type": models.StorageTypeVM,
			},
		}, details)
		assert.Equal(t, 3, queryCount)
	})

	t.Run("RecordRule payload", func(t *testing.T) {
		db := setupResultTableDetailMySQL(t)
		const tenantID = "tenant-record-rule"
		insertCluster(t, db, tenantID, 8, "record-vm", models.StorageTypeVM)
		execResultTableDetailSQL(t, db,
			`INSERT INTO metadata_recordrule (table_id, bk_tenant_id, vm_cluster_id, dst_vm_table_id, rule_metrics) VALUES (?, ?, ?, ?, ?)`,
			"record.rule", tenantID, 8, "vm_record_target", `{"first":"metric_one","second":"metric_two"}`,
		)
		execResultTableDetailSQL(t, db,
			`INSERT INTO metadata_recordrule (table_id, bk_tenant_id, vm_cluster_id, dst_vm_table_id, rule_metrics) VALUES (?, ?, ?, ?, ?)`,
			"record.other", "other-tenant", 8, "wrong_target", `{"wrong":"wrong_metric"}`,
		)

		clusterMap := loadRouteClustersForTest(t, db, tenantID)
		details, err := NewSpacePusher().composeRecordRuleTableIdDetail(tenantID, clusterMap)

		require.NoError(t, err)
		assert.Equal(t, map[string]map[string]any{
			"record.rule": {
				"vm_rt": "vm_record_target", "storage_id": 8, "cluster_name": "",
				"storage_name": "record-vm", "db": "", "measurement": "", "tags_key": []string{},
				"fields":           []string{"metric_one", "metric_two"},
				"measurement_type": models.MeasurementTypeBkSplit, "bcs_cluster_id": "",
				"data_label": "", "labels": map[string]any{}, "storage_type": models.StorageTypeVM,
				"bk_data_id": nil,
			},
		}, details)
	})
}

func TestComposeLogTableIdDetailRequiresResultTableAndKeepsClaimedFallback(t *testing.T) {
	t.Run("virtual route uses origin history and keeps virtual metadata", func(t *testing.T) {
		db := setupResultTableDetailMySQL(t)
		const tenantID = "tenant-virtual"
		const virtualTableID = "2_bklog.virtual"
		const originTableID = "2_bklog.entity"
		virtualLabel := "virtual-label"
		insertResultTable(t, db, virtualTableID, tenantID, models.StorageTypeES, &virtualLabel, `{"kind":"virtual"}`)
		insertESStorage(t, db, virtualTableID, tenantID, 1, "", "", originTableID)
		insertESStorage(t, db, originTableID, tenantID, 1, "entity-index", "entity-source", "")
		insertDorisStorage(t, db, originTableID, tenantID, 2, "entity_doris", "")
		insertCluster(t, db, tenantID, 1, "virtual-es", models.StorageTypeES)
		insertCluster(t, db, tenantID, 2, "virtual-doris", models.StorageTypeDoris)
		esTime := time.Unix(100, 0)
		dorisTime := time.Unix(200, 0)
		insertClusterRecord(t, db, 1, originTableID, tenantID, 1, &esTime, false)
		insertClusterRecord(t, db, 2, originTableID, tenantID, 2, &dorisTime, false)
		execResultTableDetailSQL(t, db,
			`INSERT INTO metadata_resulttableoption (table_id, bk_tenant_id, name, value, value_type) VALUES (?, ?, ?, ?, ?)`,
			virtualTableID, tenantID, "virtual_option", "virtual-value", "string",
		)
		execResultTableDetailSQL(t, db,
			`INSERT INTO metadata_esfieldqueryaliasoption (table_id, bk_tenant_id, field_path, query_alias, is_deleted) VALUES (?, ?, ?, ?, ?)`,
			virtualTableID, tenantID, "__ext.pod", "pod", false,
		)

		clusterMap := loadRouteClustersForTest(t, db, tenantID)
		details, claimed, err := NewSpacePusher().composeLogTableIdDetail(
			tenantID, []string{virtualTableID}, clusterMap,
		)
		require.NoError(t, err)
		assert.Contains(t, claimed, virtualTableID)
		detail := details[virtualTableID]
		assert.Equal(t, uint(1), detail["storage_id"])
		assert.Equal(t, "entity-index", detail["db"])
		assert.Equal(t, "entity-source", detail["source_type"])
		assert.Equal(t, "virtual-label", detail["data_label"])
		assert.Equal(t, map[string]any{"kind": "virtual"}, detail["labels"])
		assert.Equal(t, map[string]any{"virtual_option": "virtual-value"}, detail["options"])
		assert.Equal(t, map[string]string{"pod": "__ext.pod"}, detail["field_alias"])
		assert.Len(t, detail["storage_cluster_records"], 2)
	})

	t.Run("storage without ResultTable is ignored", func(t *testing.T) {
		db := setupResultTableDetailMySQL(t)
		const tenantID = "tenant-orphan"
		const tableID = "legacy_orphan"
		insertESStorage(t, db, tableID, tenantID, 1, "legacy-index", "legacy-source", "")
		queryCount := 0
		db.Callback().Query().Before("gorm:query").Register("test:count_storage_without_result_table_queries", func(*gorm.Scope) {
			queryCount++
		})

		details, claimed, err := NewSpacePusher().composeLogTableIdDetail(
			tenantID, []string{tableID}, map[uint]storage.ClusterInfo{},
		)
		require.NoError(t, err)
		assert.Empty(t, claimed)
		assert.Empty(t, details)
		// ResultTable 未命中后立即结束，不再查询 ES/Doris Storage。
		assert.Equal(t, 1, queryCount)
	})

	t.Run("incomplete claimed log route cannot fall back to metrics", func(t *testing.T) {
		db := setupResultTableDetailMySQL(t)
		const tenantID = "tenant-incomplete"
		const tableID = "2_bklog.incomplete"
		insertResultTable(t, db, tableID, tenantID, models.StorageTypeES, nil, "")

		clusterMap := loadRouteClustersForTest(t, db, tenantID)
		details, claimed, err := NewSpacePusher().composeLogTableIdDetail(
			tenantID, []string{tableID}, clusterMap,
		)
		require.NoError(t, err)
		assert.Empty(t, details)
		assert.Contains(t, claimed, tableID)
	})

	for _, tt := range []struct {
		name      string
		tableID   string
		isDeleted bool
		isEnable  bool
	}{
		{name: "deleted RT", tableID: "2_bklog.deleted", isDeleted: true, isEnable: true},
		{name: "disabled RT", tableID: "2_bklog.disabled", isDeleted: false, isEnable: false},
	} {
		t.Run(tt.name+" is not composed", func(t *testing.T) {
			db := setupResultTableDetailMySQL(t)
			const tenantID = "tenant-inactive"
			insertResultTableWithState(
				t, db, tt.tableID, tenantID, models.StorageTypeES, nil, "", tt.isDeleted, tt.isEnable,
			)
			insertESStorage(t, db, tt.tableID, tenantID, 1, "inactive-index", "log", "")

			details, claimed, err := NewSpacePusher().composeLogTableIdDetail(
				tenantID, []string{tt.tableID}, map[uint]storage.ClusterInfo{},
			)

			require.NoError(t, err)
			assert.Empty(t, details)
			// 生命周期失效的日志 RT 仍由日志侧拦截，避免增量刷新时回退到指标路由。
			assert.Contains(t, claimed, tt.tableID)
		})
	}
}

func TestClearRtDetailRemovesInactiveAndOrphanRoutes(t *testing.T) {
	db := setupResultTableDetailMySQL(t)
	setupStorageRedisForTest(t)
	setMultiTenantModeForTest(t, true)

	oldKey := cfg.ResultTableDetailKey
	cfg.ResultTableDetailKey = "test:result_table_detail:clear_orphan"
	t.Cleanup(func() { cfg.ResultTableDetailKey = oldKey })

	const tenantID = "tenant-orphan-clear"
	const esOrphanField = "legacy_es_orphan.__default__|tenant-orphan-clear"
	const dorisOrphanField = "legacy_doris_orphan.__default__|tenant-orphan-clear"
	const activeField = "active.log|tenant-orphan-clear"
	const deletedField = "deleted.log|tenant-orphan-clear"
	const disabledField = "disabled.log|tenant-orphan-clear"
	const staleField = "stale.__default__|tenant-orphan-clear"
	insertESStorage(t, db, "legacy_es_orphan", tenantID, 1, "legacy-index", "legacy-source", "")
	insertDorisStorage(t, db, "legacy_doris_orphan", tenantID, 2, "legacy_doris_table", "")
	insertResultTable(t, db, "active.log", tenantID, models.StorageTypeES, nil, "")
	insertResultTableWithState(t, db, "deleted.log", tenantID, models.StorageTypeES, nil, "", true, true)
	insertESStorage(t, db, "deleted.log", tenantID, 1, "deleted-index", "log", "")
	insertResultTableWithState(t, db, "disabled.log", tenantID, models.StorageTypeDoris, nil, "", false, false)
	insertDorisStorage(t, db, "disabled.log", tenantID, 2, "disabled_doris", "")

	client := redis.GetStorageRedisInstance()
	require.NoError(t, client.Delete(cfg.ResultTableDetailKey))
	require.NoError(t, client.HSet(cfg.ResultTableDetailKey, esOrphanField, `{"storage_type":"elasticsearch"}`))
	require.NoError(t, client.HSet(cfg.ResultTableDetailKey, dorisOrphanField, `{"storage_type":"bk_sql"}`))
	require.NoError(t, client.HSet(cfg.ResultTableDetailKey, activeField, `{"storage_type":"elasticsearch"}`))
	require.NoError(t, client.HSet(cfg.ResultTableDetailKey, deletedField, `{"storage_type":"elasticsearch"}`))
	require.NoError(t, client.HSet(cfg.ResultTableDetailKey, disabledField, `{"storage_type":"bk_sql"}`))
	require.NoError(t, client.HSet(cfg.ResultTableDetailKey, staleField, `{"storage_type":"elasticsearch"}`))

	(&SpaceRedisClearer{redisClient: client, dbClient: db}).ClearRtDetail()

	assert.Empty(t, client.HGet(cfg.ResultTableDetailKey, esOrphanField))
	assert.Empty(t, client.HGet(cfg.ResultTableDetailKey, dorisOrphanField))
	assert.NotEmpty(t, client.HGet(cfg.ResultTableDetailKey, activeField))
	assert.Empty(t, client.HGet(cfg.ResultTableDetailKey, deletedField))
	assert.Empty(t, client.HGet(cfg.ResultTableDetailKey, disabledField))
	assert.Empty(t, client.HGet(cfg.ResultTableDetailKey, staleField))
}

func TestPushTableIdDetailWith501LogRoutesUsesTwoFixedQueryBatches(t *testing.T) {
	db := setupResultTableDetailMySQL(t)
	setupStorageRedisForTest(t)
	setMultiTenantModeForTest(t, true)

	oldKey := cfg.ResultTableDetailKey
	cfg.ResultTableDetailKey = "test:result_table_detail:501"
	t.Cleanup(func() { cfg.ResultTableDetailKey = oldKey })

	const tenantID = "tenant-501"
	insertCluster(t, db, tenantID, 1, "batch-es", models.StorageTypeES)
	tableIDs := make([]string, resultTableDetailBatchSize+1)
	tx := db.Begin()
	require.NoError(t, tx.Error)
	for index := range tableIDs {
		tableID := fmt.Sprintf("batch_%03d.log", index)
		tableIDs[index] = tableID
		require.NoError(t, tx.Exec(
			`INSERT INTO metadata_resulttable (table_id, default_storage, data_label, labels, bk_tenant_id, is_deleted, is_enable) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			tableID, models.StorageTypeES, nil, []byte(""), tenantID, false, true,
		).Error)
		require.NoError(t, tx.Exec(
			`INSERT INTO metadata_esstorage (table_id, bk_tenant_id, storage_cluster_id, source_type, index_set, origin_table_id) VALUES (?, ?, ?, ?, ?, ?)`,
			tableID, tenantID, 1, "log", fmt.Sprintf("index_%03d", index), "",
		).Error)
	}
	require.NoError(t, tx.Commit().Error)

	queryCount := 0
	clusterQueryCount := 0
	db.Callback().Query().Before("gorm:query").Register("test:count_501_route_queries", func(scope *gorm.Scope) {
		queryCount++
		if scope.TableName() == (storage.ClusterInfo{}).TableName() {
			clusterQueryCount++
		}
	})
	client := redis.GetStorageRedisInstance()
	require.NoError(t, client.Delete(cfg.ResultTableDetailKey))

	require.NoError(t, NewSpacePusher().PushTableIdDetail(tenantID, tableIDs, false))

	// ClusterInfo 在租户入口只查询一次；每个日志批次固定查询 RT、ES、Doris、
	// option、alias、Record 共 6 次。claimed 表不再进入指标组装。
	assert.Equal(t, 13, queryCount)
	assert.Equal(t, 1, clusterQueryCount)
	assert.Len(t, client.HGetAll(cfg.ResultTableDetailKey), resultTableDetailBatchSize+1)
}

func TestClaimedLogRouteDoesNotTriggerMetricSideEffects(t *testing.T) {
	db := setupResultTableDetailMySQL(t)
	setupStorageRedisForTest(t)
	setMultiTenantModeForTest(t, true)

	oldKey := cfg.ResultTableDetailKey
	cfg.ResultTableDetailKey = "test:result_table_detail:claimed_metric"
	t.Cleanup(func() { cfg.ResultTableDetailKey = oldKey })

	const tenantID = "tenant-claimed-metric"
	const claimedTableID = "2_bklog.claimed"
	const claimedRedisField = claimedTableID + "|" + tenantID
	const recordRuleTableID = "record.side_effect"
	const recordRuleRedisField = recordRuleTableID + "|" + tenantID
	insertResultTable(t, db, claimedTableID, tenantID, models.StorageTypeES, nil, "")
	// 即使异常残留了 AccessVMRecord，claimed 日志表也会直接从指标候选中排除，
	// 不再触发指标组装或附带刷新 RecordRule。
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_accessvmrecord (result_table_id, bk_tenant_id, vm_cluster_id, vm_result_table_id) VALUES (?, ?, ?, ?)`,
		claimedTableID, tenantID, 8, "stale_vm_target",
	)
	insertCluster(t, db, tenantID, 8, "record-vm", models.StorageTypeVM)
	execResultTableDetailSQL(t, db,
		`INSERT INTO metadata_recordrule (table_id, bk_tenant_id, vm_cluster_id, dst_vm_table_id, rule_metrics) VALUES (?, ?, ?, ?, ?)`,
		recordRuleTableID, tenantID, 8, "vm_record_target", `{"metric":"metric_name"}`,
	)

	client := redis.GetStorageRedisInstance()
	require.NoError(t, client.Delete(cfg.ResultTableDetailKey))
	const oldLogRoute = `{"storage_type":"elasticsearch","old":true}`
	require.NoError(t, client.HSet(cfg.ResultTableDetailKey, claimedRedisField, oldLogRoute))

	require.NoError(t, NewSpacePusher().PushTableIdDetail(tenantID, []string{claimedTableID}, false))

	assert.Equal(t, oldLogRoute, client.HGet(cfg.ResultTableDetailKey, claimedRedisField))
	assert.Empty(t, client.HGet(cfg.ResultTableDetailKey, recordRuleRedisField))
}

func TestPushTableIdDetailNormalizesTenantKeyAndHonorsPublishSwitch(t *testing.T) {
	db := setupResultTableDetailMySQL(t)
	setupStorageRedisForTest(t)
	setMultiTenantModeForTest(t, true)

	oldKey := cfg.ResultTableDetailKey
	oldChannel := cfg.ResultTableDetailChannel
	cfg.ResultTableDetailKey = "test:result_table_detail:push"
	cfg.ResultTableDetailChannel = "test:result_table_detail:push:channel"
	t.Cleanup(func() {
		cfg.ResultTableDetailKey = oldKey
		cfg.ResultTableDetailChannel = oldChannel
	})

	const tenantID = "tenant-publish"
	const tableID = "legacy_publish"
	const redisField = "legacy_publish.__default__|tenant-publish"
	insertResultTable(t, db, tableID, tenantID, models.StorageTypeES, nil, "")
	insertESStorage(t, db, tableID, tenantID, 1, "publish-index-v1", "log", "")
	insertCluster(t, db, tenantID, 1, "publish-es", models.StorageTypeES)

	client := redis.GetStorageRedisInstance()
	require.NoError(t, client.Delete(cfg.ResultTableDetailKey))
	pubsub := client.Client.Subscribe(context.Background(), cfg.ResultTableDetailChannel)
	t.Cleanup(func() { _ = pubsub.Close() })
	_, err := pubsub.Receive(context.Background())
	require.NoError(t, err)

	require.NoError(t, NewSpacePusher().PushTableIdDetail(tenantID, []string{tableID}, false))
	assert.NotEmpty(t, client.HGet(cfg.ResultTableDetailKey, redisField))
	noPublishContext, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, receiveErr := pubsub.ReceiveMessage(noPublishContext)
	cancel()
	assert.Error(t, receiveErr)
	require.NoError(t, pubsub.Close())

	execResultTableDetailSQL(t, db,
		`UPDATE metadata_esstorage SET index_set = ? WHERE table_id = ? AND bk_tenant_id = ?`,
		"publish-index-v2", tableID, tenantID,
	)
	pubsub = client.Client.Subscribe(context.Background(), cfg.ResultTableDetailChannel)
	_, err = pubsub.Receive(context.Background())
	require.NoError(t, err)
	require.NoError(t, NewSpacePusher().PushTableIdDetail(tenantID, []string{tableID}, true))
	publishContext, publishCancel := context.WithTimeout(context.Background(), time.Second)
	message, receiveErr := pubsub.ReceiveMessage(publishContext)
	publishCancel()
	require.NoError(t, receiveErr)
	assert.Equal(t, redisField, message.Payload)
}

func TestNormalizeLogTableID(t *testing.T) {
	tests := []struct {
		name    string
		tableID string
		want    string
		valid   bool
	}{
		{name: "empty", tableID: "", want: "", valid: false},
		{name: "single part", tableID: "bklog_index", want: "bklog_index.__default__", valid: true},
		{name: "two parts", tableID: "bklog.index", want: "bklog.index", valid: true},
		{name: "too many parts", tableID: "bklog.index.__default__", want: "", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := normalizeLogTableID(tt.tableID)
			assert.Equal(t, tt.valid, valid)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStorageRecordEnableTimestamp(t *testing.T) {
	enableTime := time.Unix(1_720_000_000, 987_654_321)

	assert.Equal(t, int64(0), storageRecordEnableTimestamp(storage.ClusterRecord{}))
	assert.Equal(t, enableTime.Unix(), storageRecordEnableTimestamp(storage.ClusterRecord{EnableTime: &enableTime}))
}

func TestSortStorageClusterRecordsKeepsRepeatedSegmentsAndUsesIDAsTieBreaker(t *testing.T) {
	older := time.Unix(100, 0)
	newerLowPrecision := time.Unix(200, 100)
	newerHighPrecision := time.Unix(200, 200)
	sameHighPrecision := time.Unix(200, 200)
	records := []storage.ClusterRecord{
		{ID: 10, ClusterID: 10, EnableTime: &older},
		{ID: 99, ClusterID: 20, EnableTime: &newerLowPrecision},
		{ID: 1, ClusterID: 10, EnableTime: &newerHighPrecision},
		{ID: 2, ClusterID: 40, EnableTime: &sameHighPrecision},
		{ID: 100, ClusterID: 30, EnableTime: nil},
	}

	sortStorageClusterRecords(records)

	// 完整时间精度优先；时间完全相同时才按 ID DESC。ID=1 虽小于 99，
	// 但 enable_time 晚 100ns，仍应排在其前面。
	assert.Equal(t, []uint{2, 1, 99, 10, 100}, []uint{records[0].ID, records[1].ID, records[2].ID, records[3].ID, records[4].ID})
	assert.Equal(t, []int64{40, 10, 20, 10, 30}, []int64{records[0].ClusterID, records[1].ClusterID, records[2].ClusterID, records[3].ClusterID, records[4].ClusterID})
}

func TestChunkTableIDDetailsWith501Records(t *testing.T) {
	details := make(map[string]map[string]any, resultTableDetailBatchSize+1)
	for index := 0; index <= resultTableDetailBatchSize; index++ {
		tableID := fmt.Sprintf("table_%03d.__default__", index)
		details[tableID] = map[string]any{"index": index}
	}

	batches := chunkTableIDDetails(details, resultTableDetailBatchSize)
	require.Len(t, batches, 2)
	assert.Len(t, batches[0], resultTableDetailBatchSize)
	assert.Len(t, batches[1], 1)
	assert.Contains(t, batches[0], "table_000.__default__")
	assert.Contains(t, batches[1], fmt.Sprintf("table_%03d.__default__", resultTableDetailBatchSize))
}

func TestChunkResultTableIDsWith501Records(t *testing.T) {
	tableIDs := make([]string, resultTableDetailBatchSize+1)
	for index := range tableIDs {
		tableIDs[index] = fmt.Sprintf("table_%03d.__default__", index)
	}

	batches := chunkResultTableIDs(tableIDs)
	require.Len(t, batches, 2)
	assert.Len(t, batches[0], resultTableDetailBatchSize)
	assert.Equal(t, []string{fmt.Sprintf("table_%03d.__default__", resultTableDetailBatchSize)}, batches[1])
}

func TestUniqueSortedTableIDs(t *testing.T) {
	assert.Equal(
		t,
		[]string{"a.__default__", "b.__default__"},
		uniqueSortedTableIDs([]string{"b.__default__", "", "a.__default__", "b.__default__"}),
	)
}
