// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ftaclean

import (
	"context"
	"os"
	"testing"

	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

func TestDiscovery(t *testing.T) {
	dsn := os.Getenv("BMW_FTA_TEST_DSN")
	if dsn == "" {
		t.Skip("BMW_FTA_TEST_DSN is unset")
	}
	db, err := gorm.Open("mysql", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.DB().SetMaxOpenConns(1)
	// Temporary tables shadow any names in the test database for this connection.
	for _, sql := range []string{
		"CREATE TEMPORARY TABLE metadata_datasource (bk_data_id INT,etl_config VARCHAR(32),is_enable BOOL)",
		"CREATE TEMPORARY TABLE event_plugin_instance (data_id INT, is_enabled BOOL, is_deleted BOOL)",
		"INSERT INTO event_plugin_instance VALUES (2,1,0),(2,1,0),(2,0,0),(3,1,0),(4,1,0),(6,1,0),(8,1,0),(10,0,0),(12,1,1)",
		"CREATE TEMPORARY TABLE metadata_kafkatopicinfo (id INT,bk_data_id INT)",
		"INSERT INTO metadata_datasource VALUES (2,'bk_fta_event',1),(4,'bk_fta_event',0),(3,'bk_fta_event',1),(6,'custom',1),(8,'bk_fta_event',1),(10,'bk_fta_event',1),(12,'bk_fta_event',1),(14,'bk_fta_event',1)",
	} {
		require.NoError(t, db.Exec(sql).Error)
	}
	saved := config.FTAClean
	defer func() { config.FTAClean = saved }()
	config.FTAClean.Shards = 2
	config.FTAClean.DataIDs = []int{2, 3, 4, 6, 10, 12, 14}
	active := map[uint]*consumer{}
	for _, id := range []uint{2, 3, 4, 6, 8, 10, 12, 14} {
		done := make(chan struct{})
		active[id] = &consumer{done: done, cancel: func() { close(done) }}
	}
	configFailures := make(map[uint]*failureWindow)
	require.NoError(t, reconcile(context.Background(), db, 0, active, configFailures))
	require.Len(t, active, 1)
	require.Contains(t, active, uint(2)) // Per-source metadata read failed; old worker kept.
	sources, err := discoverSources(db, 0)
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, uint(2), sources[0].BkDataId)

	// 最后一个有效实例停用时，即使 DataSource 仍启用，也应释放消费者。
	require.NoError(t, db.Exec("UPDATE event_plugin_instance SET is_enabled=0 WHERE data_id=2").Error)
	require.NoError(t, reconcile(context.Background(), db, 0, active, configFailures))
	require.Empty(t, active)
	require.NoError(t, db.Exec("UPDATE event_plugin_instance SET is_enabled=1 WHERE data_id=2 LIMIT 1").Error)
	sources, err = discoverSources(db, 0)
	require.NoError(t, err)
	require.Len(t, sources, 1)
	require.Equal(t, uint(2), sources[0].BkDataId)

	// 单表故障应保留当前消费关系，成功查询到实例已删除时才释放。
	done := make(chan struct{})
	active[2] = &consumer{done: done, cancel: func() { close(done) }}
	require.NoError(t, db.Exec("ALTER TABLE event_plugin_instance CHANGE COLUMN is_enabled unavailable BOOL").Error)
	require.Error(t, reconcile(context.Background(), db, 0, active, configFailures))
	require.Contains(t, active, uint(2))
	require.NoError(t, db.Exec("ALTER TABLE event_plugin_instance CHANGE COLUMN unavailable is_enabled BOOL").Error)
	require.NoError(t, db.Exec("DELETE FROM event_plugin_instance WHERE data_id=2").Error)
	require.NoError(t, reconcile(context.Background(), db, 0, active, configFailures))
	require.Empty(t, active)
}
