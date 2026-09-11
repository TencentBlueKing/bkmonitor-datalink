// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"os"
	"testing"

	"gorm.io/datatypes"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestCWStrategyClientIntegration(t *testing.T) {
	dsn := os.Getenv("LINKD_TEST_KINGEYE_MYSQL_DSN")
	if dsn == "" {
		t.Skip("LINKD_TEST_KINGEYE_MYSQL_DSN is not set")
	}
	database, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewCWStrategyClient(CWStrategyClientConfig{DB: database})
	if err != nil {
		t.Fatal(err)
	}
	strategy, found, err := client.GetByBKStrategyID(t.Context(), "system", 123)
	if err != nil || !found {
		t.Fatalf("strategy found=%t error=%v", found, err)
	}
	projection, err := strategy.StrategyItemProjection()
	if err != nil || projection.BKBizID != 2 || len(projection.QueryConfigs) == 0 {
		t.Fatalf("projection=%#v error=%v", projection, err)
	}
	if _, found, err := client.GetByBKStrategyID(t.Context(), "tenant-does-not-exist", 123); err != nil || found {
		t.Fatalf("cross-tenant lookup found=%t error=%v", found, err)
	}
}

func TestCWStrategyFromRowDecodesStrategyItemQueryProjection(t *testing.T) {
	t.Parallel()
	tenantID := "tenant-1"
	bizID := int64(2)
	row := cwStrategyRow{
		Kind: "Strategy", Name: "CPU 使用率", UID: "strategy-1",
		Annotations: datatypes.JSON(`{}`), BKTenantID: &tenantID, BKBizID: &bizID,
		Spec: datatypes.JSON(`{
			"name":"CPU 使用率","item_name":"system.cpu.usage","data_source":"system",
			"strategy_item":{"agg_method":"avg","agg_interval":60,"expression":"A","functions":[],
			"query_configs":[{"unit":"percent","alias":"A","agg_method":"avg","agg_interval":60,
			"metric_field":"usage","agg_condition":[],"agg_dimension":["bk_inst_id"],
			"result_table_id":"system.cpu","data_source_label":"bk_monitor","data_type_label":"time_series"}]}
		}`),
		Status: datatypes.JSON(`{"bk_strategy_id":123}`),
	}

	strategy, err := cwStrategyFromRow(row)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := strategy.StrategyItemProjection()
	if err != nil {
		t.Fatal(err)
	}
	if projection.BKBizID != 2 || projection.Name != "system.cpu.usage" || projection.Expression != "A" || len(projection.QueryConfigs) != 1 {
		t.Fatalf("projection=%#v", projection)
	}
	query := projection.QueryConfigs[0]
	if query.MetricField != "usage" || query.ResultTableID != "system.cpu" || query.AggregatePeriod != 60 ||
		len(query.AggregateBy) != 1 || query.AggregateBy[0] != "bk_inst_id" || len(query.Raw) == 0 {
		t.Fatalf("query=%#v", query)
	}
}
