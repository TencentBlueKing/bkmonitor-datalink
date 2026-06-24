// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
)

func TestComposeTableIDStorageClusterRecordsCompletesRouteFields(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.DB().SetMaxOpenConns(1)
	db.LogMode(false)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_storageclusterrecord (
		table_id text,
		cluster_id integer,
		is_deleted boolean,
		is_current boolean,
		create_time datetime,
		enable_time datetime
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_clusterinfo (
		cluster_id integer,
		cluster_name text,
		cluster_type text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_dorisstorage (
		table_id text,
		bkbase_table_id text,
		origin_table_id text,
		storage_cluster_id integer,
		index_set text,
		source_type text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_esstorage (
		table_id text,
		index_set text,
		origin_table_id text,
		source_type text
	)`).Error)

	realTableID := "bklog.compose_route_real"
	currentTableID := "bklog.compose_route_current"
	const (
		esClusterID    = uint(193001)
		dorisClusterID = uint(193002)
	)
	enableES := time.Unix(1000, 0)
	enableDoris := time.Unix(2000, 0)
	createES := time.Unix(1100, 0)
	createDoris := time.Unix(2100, 0)

	require.NoError(t, db.Exec(
		"INSERT INTO metadata_clusterinfo (cluster_id, cluster_name, cluster_type) VALUES (?, ?, ?), (?, ?, ?)",
		esClusterID, "es_compose_route", models.StorageTypeES,
		dorisClusterID, "doris_compose_route", models.StorageTypeDoris,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_dorisstorage (table_id, bkbase_table_id, storage_cluster_id) VALUES (?, ?, ?)",
		currentTableID, "bklog_compose_route_current", dorisClusterID,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_esstorage (table_id, index_set, source_type) VALUES (?, ?, ?)",
		currentTableID, "bklog_compose_route_es", models.EsSourceTypeBKDATA,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_storageclusterrecord (table_id, cluster_id, is_deleted, is_current, create_time, enable_time) VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?)",
		realTableID, esClusterID, false, false, createES, enableES,
		realTableID, dorisClusterID, false, true, createDoris, enableDoris,
	).Error)

	records, err := ComposeTableIDStorageClusterRecords(db, realTableID, currentTableID)
	require.NoError(t, err)
	require.Len(t, records, 2)

	byStorageID := make(map[int64]map[string]any, len(records))
	for _, record := range records {
		byStorageID[record["storage_id"].(int64)] = record
	}

	dorisRoute := byStorageID[int64(dorisClusterID)]
	require.NotNil(t, dorisRoute)
	assert.Equal(t, models.StorageTypeBkSql, dorisRoute["storage_type"])
	assert.Equal(t, "doris_compose_route", dorisRoute["storage_name"])
	assert.Equal(t, "doris_compose_route", dorisRoute["cluster_name"])
	assert.Equal(t, "bklog_compose_route_current", dorisRoute["db"])
	assert.Equal(t, models.DorisMeasurement, dorisRoute["measurement"])
	assert.Equal(t, enableDoris.Unix(), dorisRoute["enable_time"])

	esRoute := byStorageID[int64(esClusterID)]
	require.NotNil(t, esRoute)
	assert.Equal(t, models.StorageTypeES, esRoute["storage_type"])
	assert.Equal(t, "bklog_compose_route_es", esRoute["db"])
	assert.Equal(t, models.TSGroupDefaultMeasurement, esRoute["measurement"])
	assert.Equal(t, models.EsSourceTypeBKDATA, esRoute["source_type"])
	assert.Equal(t, enableES.Unix(), esRoute["enable_time"])
}

func TestComposeTableIDStorageClusterRecordsCompletesESRouteFromDorisStorage(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.DB().SetMaxOpenConns(1)
	db.LogMode(false)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_storageclusterrecord (
		table_id text,
		cluster_id integer,
		is_deleted boolean,
		is_current boolean,
		create_time datetime,
		enable_time datetime
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_clusterinfo (
		cluster_id integer,
		cluster_name text,
		cluster_type text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_dorisstorage (
		table_id text,
		bkbase_table_id text,
		origin_table_id text,
		storage_cluster_id integer,
		index_set text,
		source_type text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_esstorage (
		table_id text,
		index_set text,
		origin_table_id text,
		source_type text
	)`).Error)

	realTableID := "bklog.doris_with_history_es_real"
	currentTableID := "bklog.doris_with_history_es_current"
	const (
		esClusterID    = uint(194001)
		dorisClusterID = uint(194002)
	)
	enableES := time.Unix(1000, 0)
	enableDoris := time.Unix(2000, 0)

	require.NoError(t, db.Exec(
		"INSERT INTO metadata_clusterinfo (cluster_id, cluster_name, cluster_type) VALUES (?, ?, ?), (?, ?, ?)",
		esClusterID, "es_history_cluster", models.StorageTypeES,
		dorisClusterID, "doris_current_cluster", models.StorageTypeDoris,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_dorisstorage (table_id, bkbase_table_id, storage_cluster_id, index_set, source_type) VALUES (?, ?, ?, ?, ?)",
		currentTableID, "bklog_doris_with_history_es_current", dorisClusterID, "bklog_history_es_index", models.EsSourceTypeBKDATA,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_storageclusterrecord (table_id, cluster_id, is_deleted, is_current, create_time, enable_time) VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?)",
		realTableID, esClusterID, false, false, enableES.Add(time.Minute), enableES,
		realTableID, dorisClusterID, false, true, enableDoris.Add(time.Minute), enableDoris,
	).Error)

	records, err := ComposeTableIDStorageClusterRecords(db, realTableID, currentTableID)
	require.NoError(t, err)
	require.Len(t, records, 2)

	byStorageID := make(map[int64]map[string]any, len(records))
	for _, record := range records {
		byStorageID[record["storage_id"].(int64)] = record
	}

	esRoute := byStorageID[int64(esClusterID)]
	require.NotNil(t, esRoute)
	assert.Equal(t, models.StorageTypeES, esRoute["storage_type"])
	assert.Equal(t, "bklog_history_es_index", esRoute["db"])
	assert.Equal(t, models.TSGroupDefaultMeasurement, esRoute["measurement"])
	assert.Equal(t, models.EsSourceTypeBKDATA, esRoute["source_type"])
	assert.Equal(t, enableES.Unix(), esRoute["enable_time"])
}

func TestComposeTableIDStorageClusterRecordsCompletesESRouteFromOriginDorisStorage(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.DB().SetMaxOpenConns(1)
	db.LogMode(false)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_storageclusterrecord (
		table_id text,
		cluster_id integer,
		is_deleted boolean,
		is_current boolean,
		create_time datetime,
		enable_time datetime
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_clusterinfo (
		cluster_id integer,
		cluster_name text,
		cluster_type text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_dorisstorage (
		table_id text,
		bkbase_table_id text,
		origin_table_id text,
		storage_cluster_id integer,
		index_set text,
		source_type text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_esstorage (
		table_id text,
		index_set text,
		origin_table_id text,
		source_type text
	)`).Error)

	realTableID := "bklog.origin_doris_history_es_real"
	currentTableID := "bklog.origin_doris_history_es_current"
	originTableID := "bklog.origin_doris_history_es_origin"
	const (
		esClusterID    = uint(195001)
		dorisClusterID = uint(195002)
	)
	enableES := time.Unix(1000, 0)
	enableDoris := time.Unix(2000, 0)

	require.NoError(t, db.Exec(
		"INSERT INTO metadata_clusterinfo (cluster_id, cluster_name, cluster_type) VALUES (?, ?, ?), (?, ?, ?)",
		esClusterID, "es_origin_history_cluster", models.StorageTypeES,
		dorisClusterID, "doris_origin_current_cluster", models.StorageTypeDoris,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_dorisstorage (table_id, origin_table_id) VALUES (?, ?)",
		currentTableID, originTableID,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_dorisstorage (table_id, bkbase_table_id, storage_cluster_id, index_set, source_type) VALUES (?, ?, ?, ?, ?)",
		originTableID, "bklog_origin_doris_history_es_current", dorisClusterID, "bklog_origin_history_es_index", models.EsSourceTypeBKDATA,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_storageclusterrecord (table_id, cluster_id, is_deleted, is_current, create_time, enable_time) VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?)",
		realTableID, esClusterID, false, false, enableES.Add(time.Minute), enableES,
		realTableID, dorisClusterID, false, true, enableDoris.Add(time.Minute), enableDoris,
	).Error)

	records, err := ComposeTableIDStorageClusterRecords(db, realTableID, currentTableID)
	require.NoError(t, err)
	require.Len(t, records, 2)

	byStorageID := make(map[int64]map[string]any, len(records))
	for _, record := range records {
		byStorageID[record["storage_id"].(int64)] = record
	}

	esRoute := byStorageID[int64(esClusterID)]
	require.NotNil(t, esRoute)
	assert.Equal(t, models.StorageTypeES, esRoute["storage_type"])
	assert.Equal(t, "bklog_origin_history_es_index", esRoute["db"])
	assert.Equal(t, models.TSGroupDefaultMeasurement, esRoute["measurement"])
	assert.Equal(t, models.EsSourceTypeBKDATA, esRoute["source_type"])
	assert.Equal(t, enableES.Unix(), esRoute["enable_time"])

	dorisRoute := byStorageID[int64(dorisClusterID)]
	require.NotNil(t, dorisRoute)
	assert.Equal(t, models.StorageTypeBkSql, dorisRoute["storage_type"])
	assert.Equal(t, "bklog_origin_doris_history_es_current", dorisRoute["db"])
	assert.Equal(t, models.DorisMeasurement, dorisRoute["measurement"])
}

func TestComposeTableIDStorageClusterRecordsBatchUsesSharedQueries(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.DB().SetMaxOpenConns(1)
	db.LogMode(false)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_storageclusterrecord (
		table_id text,
		cluster_id integer,
		is_deleted boolean,
		is_current boolean,
		create_time datetime,
		enable_time datetime
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_clusterinfo (
		cluster_id integer,
		cluster_name text,
		cluster_type text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_dorisstorage (
		table_id text,
		bkbase_table_id text,
		origin_table_id text,
		storage_cluster_id integer,
		index_set text,
		source_type text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_esstorage (
		table_id text,
		index_set text,
		origin_table_id text,
		source_type text
	)`).Error)

	const esClusterID = uint(196001)
	realTableID1 := "bklog.batch_route_real_1"
	currentTableID1 := "bklog.batch_route_current_1"
	realTableID2 := "bklog.batch_route_real_2"
	currentTableID2 := "bklog.batch_route_current_2"
	enable1 := time.Unix(1000, 0)
	enable2 := time.Unix(2000, 0)

	require.NoError(t, db.Exec(
		"INSERT INTO metadata_clusterinfo (cluster_id, cluster_name, cluster_type) VALUES (?, ?, ?)",
		esClusterID, "es_batch_route", models.StorageTypeES,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_esstorage (table_id, index_set, source_type) VALUES (?, ?, ?), (?, ?, ?)",
		currentTableID1, "bklog_batch_route_es_1", models.EsSourceTypeBKDATA,
		currentTableID2, "bklog_batch_route_es_2", models.EsSourceTypeBKDATA,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_storageclusterrecord (table_id, cluster_id, is_deleted, is_current, create_time, enable_time) VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?)",
		realTableID1, esClusterID, false, true, enable1.Add(time.Minute), enable1,
		realTableID2, esClusterID, false, true, enable2.Add(time.Minute), enable2,
	).Error)

	queryCount := make(map[string]int)
	callbackName := "test:count_storage_cluster_record_batch_query"
	db.Callback().Query().Before("gorm:query").Register(callbackName, func(scope *gorm.Scope) {
		switch scope.TableName() {
		case "metadata_storageclusterrecord", "metadata_clusterinfo", "metadata_dorisstorage", "metadata_esstorage":
			queryCount[scope.TableName()]++
		}
	})
	defer db.Callback().Query().Remove(callbackName)

	request1 := TableIDStorageClusterRecordRequest{TableID: realTableID1, CurrentTableID: currentTableID1}
	request2 := TableIDStorageClusterRecordRequest{TableID: realTableID2, CurrentTableID: currentTableID2}
	recordsMap, err := ComposeTableIDStorageClusterRecordsBatch(db, []TableIDStorageClusterRecordRequest{request1, request2})
	require.NoError(t, err)

	records1 := recordsMap[request1]
	require.Len(t, records1, 1)
	assert.Equal(t, "bklog_batch_route_es_1", records1[0]["db"])
	assert.Equal(t, enable1.Unix(), records1[0]["enable_time"])
	records2 := recordsMap[request2]
	require.Len(t, records2, 1)
	assert.Equal(t, "bklog_batch_route_es_2", records2[0]["db"])
	assert.Equal(t, enable2.Unix(), records2[0]["enable_time"])

	assert.Equal(t, 1, queryCount["metadata_storageclusterrecord"])
	assert.Equal(t, 1, queryCount["metadata_clusterinfo"])
	assert.Equal(t, 1, queryCount["metadata_dorisstorage"])
	assert.Equal(t, 1, queryCount["metadata_esstorage"])
}

func TestComposeTableIDStorageClusterRecordsBatchSeparatesRealAndCurrentTableIDKeys(t *testing.T) {
	db, err := gorm.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	db.DB().SetMaxOpenConns(1)
	db.LogMode(false)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_storageclusterrecord (
		table_id text,
		cluster_id integer,
		is_deleted boolean,
		is_current boolean,
		create_time datetime,
		enable_time datetime
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_clusterinfo (
		cluster_id integer,
		cluster_name text,
		cluster_type text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_dorisstorage (
		table_id text,
		bkbase_table_id text,
		origin_table_id text,
		storage_cluster_id integer,
		index_set text,
		source_type text
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE metadata_esstorage (
		table_id text,
		index_set text,
		origin_table_id text,
		source_type text
	)`).Error)

	directTableID := "bklog.batch_result_key_shared"
	realTableID := "bklog.batch_result_key_real"
	currentTableID := directTableID
	const (
		directClusterID  = uint(197001)
		virtualClusterID = uint(197002)
	)
	directEnable := time.Unix(1000, 0)
	virtualEnable := time.Unix(2000, 0)

	require.NoError(t, db.Exec(
		"INSERT INTO metadata_clusterinfo (cluster_id, cluster_name, cluster_type) VALUES (?, ?, ?), (?, ?, ?)",
		directClusterID, "es_batch_result_key_direct", models.StorageTypeES,
		virtualClusterID, "es_batch_result_key_virtual", models.StorageTypeES,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_esstorage (table_id, index_set, source_type) VALUES (?, ?, ?)",
		currentTableID, "bklog_batch_result_key_es", models.EsSourceTypeBKDATA,
	).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO metadata_storageclusterrecord (table_id, cluster_id, is_deleted, is_current, create_time, enable_time) VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?)",
		directTableID, directClusterID, false, true, directEnable.Add(time.Minute), directEnable,
		realTableID, virtualClusterID, false, true, virtualEnable.Add(time.Minute), virtualEnable,
	).Error)

	directRequest := TableIDStorageClusterRecordRequest{TableID: directTableID}
	virtualRequest := TableIDStorageClusterRecordRequest{TableID: realTableID, CurrentTableID: currentTableID}
	recordsMap, err := ComposeTableIDStorageClusterRecordsBatch(db, []TableIDStorageClusterRecordRequest{directRequest, virtualRequest})
	require.NoError(t, err)

	directRecords := recordsMap[directRequest]
	require.Len(t, directRecords, 1)
	assert.Equal(t, int64(directClusterID), directRecords[0]["storage_id"])
	assert.Equal(t, directEnable.Unix(), directRecords[0]["enable_time"])

	virtualRecords := recordsMap[virtualRequest]
	require.Len(t, virtualRecords, 1)
	assert.Equal(t, int64(virtualClusterID), virtualRecords[0]["storage_id"])
	assert.Equal(t, virtualEnable.Unix(), virtualRecords[0]["enable_time"])
}
