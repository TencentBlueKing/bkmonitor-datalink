// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models

import (
	"encoding/json"
	"testing"

	"linkd/internal/domain"
)

func TestStrategyItemProjectionNormalizesDeclarativeQuery(t *testing.T) {
	t.Parallel()
	bizID := int64(2)
	strategy := CWStrategy{
		BKBizID: &bizID,
		Spec: CWStrategySpec{
			TableID: "system.cpu", FieldName: "usage",
			StrategyItem: &CWStrategyItem{
				AggregateMethod: "avg", AggregatePeriod: json.RawMessage(`"60s"`),
				AggregateBy: []string{"bk_inst_id"}, AggregateFilter: []domain.JSONObject{},
				Functions: json.RawMessage(`[]`), QueryConfigs: []StrategyQueryConfig{{Alias: "A"}},
			},
		},
	}
	projection, err := strategy.StrategyItemProjection()
	if err != nil {
		t.Fatal(err)
	}
	query := projection.QueryConfigs[0]
	if query.ResultTableID != "system.cpu" || query.MetricField != "usage" || query.AggregateMethod != "avg" ||
		query.AggregatePeriod != 60 || query.DataSourceLabel != "bk_monitor" || query.DataTypeLabel != "time_series" ||
		len(query.AggregateBy) != 1 || string(query.AggregateFilter) != "[]" {
		t.Fatalf("query=%#v", query)
	}
}

func TestStrategyItemProjectionRequiresCompleteKingeyeStrategy(t *testing.T) {
	t.Parallel()
	bizID := int64(2)
	valid := CWStrategy{
		BKBizID: &bizID,
		Spec: CWStrategySpec{ItemName: "system.cpu.usage", StrategyItem: &CWStrategyItem{
			Expression: "A", Functions: json.RawMessage(`[]`),
			QueryConfigs: []StrategyQueryConfig{{MetricField: "usage", ResultTableID: "system.cpu"}},
		}},
	}
	projection, err := valid.StrategyItemProjection()
	if err != nil || projection.BKBizID != 2 || projection.Name != "system.cpu.usage" || len(projection.QueryConfigs) != 1 {
		t.Fatalf("projection=%#v error=%v", projection, err)
	}

	withoutBiz := valid
	withoutBiz.BKBizID = nil
	if _, err := withoutBiz.StrategyItemProjection(); err == nil {
		t.Fatal("strategy without bk_biz_id was accepted")
	}
	withoutQuery := valid
	withoutQuery.Spec.StrategyItem = &CWStrategyItem{}
	if _, err := withoutQuery.StrategyItemProjection(); err == nil {
		t.Fatal("strategy without query_configs was accepted")
	}
}
