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
		storage_cluster_id integer
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
