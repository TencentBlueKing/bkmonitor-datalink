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
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/alicebob/miniredis/v2"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	redisUtils "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/register/redis"
)

type dataLabelQueryCounter struct {
	count   int
	queries []string
}

func (c *dataLabelQueryCounter) Print(values ...interface{}) {
	if len(values) > 0 && values[0] == "sql" {
		c.count++
		c.queries = append(c.queries, values[3].(string))
	}
}

// 默认复用 bmw_test.yaml 的连接配置，仅创建独立连接的临时表。
// 也可以显式指定独立 MySQL 实例：
// BMW_ROUTE_TEST_DSN='root@unix(/path/mysql.sock)/test?parseTime=true' go test -run TestDataLabelAliasRoutes ./internal/metadata/service
func TestDataLabelAliasRoutes(t *testing.T) {
	var db *gorm.DB
	var err error
	if dsn := os.Getenv("BMW_ROUTE_TEST_DSN"); dsn != "" {
		db, err = gorm.Open("mysql", dsn)
	} else {
		mocker.InitTestDBConfig("../../../bmw_test.yaml")
		session := &mysql.DBSession{}
		err = session.Open()
		db = session.DB
	}
	require.NoError(t, err)
	defer db.Close()
	db.DB().SetMaxOpenConns(1)
	tenantId := "system"
	oldMultiTenantMode := cfg.EnableMultiTenantMode
	cfg.EnableMultiTenantMode = false
	defer func() { cfg.EnableMultiTenantMode = oldMultiTenantMode }()
	require.NoError(t, db.Exec(`CREATE TEMPORARY TABLE metadata_resulttable (
		table_id VARCHAR(128), data_label VARCHAR(128),
		is_enable BOOL NOT NULL, is_deleted BOOL NOT NULL,
		bk_tenant_id VARCHAR(256) DEFAULT 'system', PRIMARY KEY (bk_tenant_id, table_id))`).Error)
	require.NoError(t, db.Exec(`CREATE TEMPORARY TABLE metadata_resulttableoption (
		table_id VARCHAR(128), name VARCHAR(128), value_type VARCHAR(64), value TEXT,
		bk_tenant_id VARCHAR(256) DEFAULT 'system', UNIQUE KEY (bk_tenant_id, table_id, name))`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_resulttable (table_id, data_label, is_enable, is_deleted) VALUES
		('test.first', 'primary,shared', 1, 0), ('test.second', 'shared', 1, 0),
		('test.legacy', 'shared', 1, 0),
		('test.alias_only', NULL, 1, 0), ('test.empty', '', 1, 0),
		('test.disabled', 'disabled', 0, 0), ('test.deleted', 'deleted', 1, 1),
		('test.no_labels', NULL, 1, 0), ('test.wrong_type', NULL, 1, 0)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_resulttableoption (table_id, name, value_type, value) VALUES
		('test.first', 'data_label_aliases', 'list', '["legacy","shared","legacy",""]'),
		('test.second', 'data_label_aliases', 'list', '["legacy"]'),
		('test.alias_only', 'data_label_aliases', 'list', '["legacy","alias_only","quote\\"label"]'),
		('test.empty', 'data_label_aliases', 'list', '["empty_alias"]'),
		('test.disabled', 'data_label_aliases', 'list', '["disabled_alias"]'),
		('test.deleted', 'data_label_aliases', 'list', '["deleted_alias"]'),
		('test.wrong_type', 'data_label_aliases', 'string', 'ignored'),
		('missing.rt', 'data_label_aliases', 'list', '["orphan"]')`).Error)

	require.NoError(t, db.Exec(`INSERT INTO metadata_resulttable VALUES ('test.first', 'other_primary', 1, 0, 'other')`).Error)
	require.NoError(t, db.Exec(`INSERT INTO metadata_resulttableoption VALUES ('test.first', 'data_label_aliases', 'list', '["other_alias","legacy"]', 'other')`).Error)

	counter := &dataLabelQueryCounter{}
	db.SetLogger(counter)
	db.LogMode(true)
	expected := map[string][]string{
		"primary": {"test.first"}, "shared": {"test.first", "test.second", "test.legacy"},
		"legacy":     {"test.first", "test.second", "test.alias_only"},
		"alias_only": {"test.alias_only"}, "quote\"label": {"test.alias_only"},
		"empty_alias": {"test.empty"},
	}
	t.Run("full_refresh_two_queries", func(t *testing.T) {
		counter.count = 0
		counter.queries = nil
		actual, err := getDataLabelTableIds(db, &tenantId, nil, nil)
		require.NoError(t, err)
		assert.Len(t, actual, len(expected))
		for label, tableIds := range expected {
			assert.ElementsMatch(t, tableIds, actual[label], label)
		}
		assert.Equal(t, 2, counter.count)
		for _, query := range counter.queries {
			sql := strings.ToUpper(query)
			assert.NotContains(t, sql, "JOIN")
			assert.NotContains(t, sql, " IN (")
			assert.NotContains(t, sql, "JSON_CONTAINS")
			assert.NotContains(t, sql, "FIND_IN_SET")
		}
	})
	t.Run("requested_labels", func(t *testing.T) {
		actual, err := getDataLabelTableIds(db, &tenantId, []string{"primary,shared", "legacy", "quote\"label", "missing", "prim", "disabled_alias"}, nil)
		require.NoError(t, err)
		assert.Len(t, actual, 7)
		for _, label := range []string{"primary", "shared", "legacy", "quote\"label"} {
			assert.ElementsMatch(t, expected[label], actual[label], label)
		}
		for _, label := range []string{"missing", "prim", "disabled_alias"} {
			assert.Equal(t, []string{}, actual[label], label)
		}
	})
	t.Run("table_filter", func(t *testing.T) {
		actual, err := getDataLabelTableIds(db, &tenantId, nil, []string{"test.alias_only"})
		require.NoError(t, err)
		assert.Equal(t, map[string][]string{
			"legacy": {"test.alias_only"}, "alias_only": {"test.alias_only"}, "quote\"label": {"test.alias_only"},
		}, actual)
	})
	t.Run("batched_labels_no_duplicate_tables", func(t *testing.T) {
		labels := make([]string, cfg.DefaultDBFilterSize+1)
		for i := range labels {
			labels[i] = fmt.Sprintf("batch_%d", i)
		}
		labels[0], labels[len(labels)-1] = "primary", "legacy"
		counter.count = 0
		actual, err := getDataLabelTableIds(db, &tenantId, labels, nil)
		require.NoError(t, err)
		assert.ElementsMatch(t, expected["primary"], actual["primary"])
		assert.ElementsMatch(t, expected["legacy"], actual["legacy"])
		assert.Equal(t, 4, counter.count)
	})

	server := miniredis.RunT(t)
	port, err := strconv.Atoi(server.Port())
	require.NoError(t, err)
	client := redis.GetInstance(&redisUtils.Option{Mode: redisUtils.StandAlone, Host: server.Host(), Port: port})
	defer client.Close()
	patches := gomonkey.ApplyFunc(mysql.GetDBSession, func() *mysql.DBSession { return &mysql.DBSession{DB: db} })
	defer patches.Reset()
	patches.ApplyFunc(redis.GetStorageRedisInstance, func() *redis.Instance { return client })
	oldKey, oldChannel := cfg.DataLabelToResultTableKey, cfg.DataLabelToResultTableChannel
	cfg.DataLabelToResultTableKey, cfg.DataLabelToResultTableChannel = "test:data_label_routes", "test:label_channel"
	defer func() {
		cfg.DataLabelToResultTableKey, cfg.DataLabelToResultTableChannel = oldKey, oldChannel
	}()
	pusher := NewSpacePusher()
	readRoute := func(t *testing.T, label string) []string {
		value, err := client.Client.HGet(context.Background(), cfg.DataLabelToResultTableKey, label).Result()
		require.NoError(t, err)
		var tableIds []string
		require.NoError(t, json.Unmarshal([]byte(value), &tableIds))
		return tableIds
	}
	t.Run("invalid_options_do_not_block_routes", func(t *testing.T) {
		cases := []struct {
			tableId string
			label   string
			value   string
		}{
			{"bad.json", "bad_json_primary", `["broken"`},
			{"bad.object", "bad_object_primary", `{"key":"legacy"}`},
			{"bad.scalar", "bad_scalar_primary", `"legacy"`},
			{"bad.mixed", "bad_mixed_primary", `["legacy",123]`},
			{"bad.alias_only", "", `not-json`},
		}
		var tableIds []string
		labels := []string{"legacy", "broken"}
		for _, c := range cases {
			require.NoError(t, db.Exec(`INSERT INTO metadata_resulttable (table_id, data_label, is_enable, is_deleted) VALUES (?, ?, 1, 0)`, c.tableId, c.label).Error)
			require.NoError(t, db.Exec(`INSERT INTO metadata_resulttableoption (table_id, name, value_type, value) VALUES (?, 'data_label_aliases', 'list', ?)`, c.tableId, c.value).Error)
			tableIds = append(tableIds, c.tableId)
			if c.label != "" {
				labels = append(labels, c.label)
			}
		}
		t.Cleanup(func() {
			require.NoError(t, db.Exec("DELETE FROM metadata_resulttableoption WHERE table_id IN (?)", tableIds).Error)
			require.NoError(t, db.Exec("DELETE FROM metadata_resulttable WHERE table_id IN (?)", tableIds).Error)
		})
		for _, requested := range [][]string{nil, labels} {
			actual, err := getDataLabelTableIds(db, &tenantId, requested, nil)
			require.NoError(t, err)
			assert.ElementsMatch(t, expected["legacy"], actual["legacy"])
			assert.Empty(t, actual["broken"])
			for _, c := range cases {
				if c.label != "" {
					assert.Equal(t, []string{c.tableId}, actual[c.label])
				}
			}
		}
		actual, err := getDataLabelTableIds(db, &tenantId, nil, append(tableIds, "test.first"))
		require.NoError(t, err)
		assert.Equal(t, []string{"test.first"}, actual["legacy"])
		server.FlushAll()
		require.NoError(t, pusher.PushDataLabelTableIds(tenantId, nil, true))
		assert.ElementsMatch(t, expected["legacy"], readRoute(t, "legacy"))
		assert.Equal(t, []string{"bad.json"}, readRoute(t, "bad_json_primary"))
		clearer := &SpaceRedisClearer{redisClient: client, dbClient: db}
		clearer.ClearDataLabelToRt()
		assert.ElementsMatch(t, expected["legacy"], readRoute(t, "legacy"))
		assert.Equal(t, []string{"bad.mixed"}, readRoute(t, "bad_mixed_primary"))
	})
	t.Run("publish_full_and_by_table", func(t *testing.T) {
		require.NoError(t, pusher.PushDataLabelTableIds(tenantId, nil, true))
		assert.ElementsMatch(t, expected["legacy"], readRoute(t, "legacy"))
		server.FlushAll()
		require.NoError(t, pusher.PushDataLabelTableIds(tenantId, []string{"test.first"}, true))
		assert.ElementsMatch(t, expected["legacy"], readRoute(t, "legacy"))
		assert.ElementsMatch(t, expected["shared"], readRoute(t, "shared"))
		keys, err := client.HKeys(cfg.DataLabelToResultTableKey)
		require.NoError(t, err)
		assert.NotContains(t, keys, "alias_only")
	})
	t.Run("table_without_labels_skips_full_refresh", func(t *testing.T) {
		server.FlushAll()
		counter.count = 0
		require.NoError(t, pusher.PushDataLabelTableIds(tenantId, []string{"test.no_labels"}, true))
		assert.Empty(t, server.Keys())
		assert.Equal(t, 2, counter.count)
	})
	t.Run("disabled_table_refresh_clears_old_routes", func(t *testing.T) {
		server.FlushAll()
		require.NoError(t, pusher.PushDataLabelTableIds(tenantId, []string{"test.disabled", "test.deleted"}, true))
		for _, label := range []string{"disabled", "deleted", "disabled_alias", "deleted_alias"} {
			assert.Equal(t, []string{}, readRoute(t, label))
		}
	})
	t.Run("lookup_by_alias_and_removed_alias", func(t *testing.T) {
		actual, err := pusher.getDataLabelTableIdMap(tenantId, []string{"legacy", "removed"})
		require.NoError(t, err)
		assert.ElementsMatch(t, expected["legacy"], actual["legacy"])
		assert.Equal(t, []string{}, actual["removed"])
	})
	t.Run("clearer_preserves_live_aliases", func(t *testing.T) {
		server.FlushAll()
		require.NoError(t, client.Client.HSet(context.Background(), cfg.DataLabelToResultTableKey,
			"legacy", "[]", "removed", "[]", "alias_only", "[]", "disabled_alias", "[]", "orphan", "[]").Err())
		clearer := &SpaceRedisClearer{redisClient: client, dbClient: db}
		clearer.ClearDataLabelToRt()
		keys, err := client.HKeys(cfg.DataLabelToResultTableKey)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"legacy", "alias_only"}, keys)
		require.NoError(t, db.Exec("DELETE FROM metadata_resulttableoption WHERE table_id = ?", "test.alias_only").Error)
		t.Cleanup(func() {
			require.NoError(t, db.Exec(`INSERT INTO metadata_resulttableoption (table_id, name, value_type, value) VALUES (?, 'data_label_aliases', 'list', ?)`, "test.alias_only", `["legacy","alias_only","quote\"label"]`).Error)
		})
		clearer.ClearDataLabelToRt()
		keys, err = client.HKeys(cfg.DataLabelToResultTableKey)
		require.NoError(t, err)
		assert.Equal(t, []string{"legacy"}, keys)
	})
	t.Run("multi_tenant_publish_and_clear", func(t *testing.T) {
		cfg.EnableMultiTenantMode = true
		defer func() { cfg.EnableMultiTenantMode = false }()
		server.FlushAll()
		actual, err := pusher.getDataLabelTableIdMap(tenantId, []string{"legacy", "removed"})
		require.NoError(t, err)
		assert.ElementsMatch(t, expected["legacy"], actual["legacy|system"])
		assert.Equal(t, []string{}, actual["removed|system"])
		assert.NotContains(t, actual, "other_alias|other")
		require.NoError(t, pusher.PushDataLabelTableIds(tenantId, []string{"test.first"}, true))
		assert.ElementsMatch(t, expected["legacy"], readRoute(t, "legacy|system"))
		require.NoError(t, pusher.PushDataLabelTableIds("other", nil, true))
		assert.ElementsMatch(t, []string{"test.first"}, readRoute(t, "other_alias|other"))
		require.NoError(t, client.Client.HSet(context.Background(), cfg.DataLabelToResultTableKey, "removed|system", "[]").Err())
		clearer := &SpaceRedisClearer{redisClient: client, dbClient: db}
		clearer.ClearDataLabelToRt()
		keys, err := client.HKeys(cfg.DataLabelToResultTableKey)
		require.NoError(t, err)
		assert.Contains(t, keys, "legacy|system")
		assert.Contains(t, keys, "other_alias|other")
		assert.NotContains(t, keys, "removed|system")
	})
	t.Run("multi_tenant_builtin_routes", func(t *testing.T) {
		cfg.EnableMultiTenantMode = true
		defer func() { cfg.EnableMultiTenantMode = false }()
		require.NoError(t, db.Exec(`INSERT INTO metadata_resulttable (table_id, data_label, is_enable, is_deleted) VALUES ('system_2_sys.cpu_summary', NULL, 1, 0)`).Error)
		t.Cleanup(func() {
			require.NoError(t, db.Exec("DELETE FROM metadata_resulttable WHERE table_id = ?", "system_2_sys.cpu_summary").Error)
		})
		actual, err := pusher.getAllDataLabelTableId(tenantId)
		require.NoError(t, err)
		assert.Equal(t, []string{"system_2_sys.cpu_summary"}, actual["system.cpu_summary|system"])
		assert.ElementsMatch(t, expected["legacy"], actual["legacy|system"])
	})

}
