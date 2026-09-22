// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"
	"encoding/json"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func TestGlobalStrategyBusinessMatchesAllBusinessCheckingProcessors(t *testing.T) {
	t.Parallel()
	strategy := strategyForProcessorBusiness(524)
	processors := []enrich.Processor{Strategy{}, Display{}, Metric{}}
	for _, processor := range processors {
		t.Run(processor.Name(), func(t *testing.T) {
			business := &strategyBusinessReader{isGlobal: true, found: true}
			scope, err := enrich.NewScope(strategyTestAlert(t, 23), enrich.Sources{
				CWStrategy: strategyProcessorReader{strategy: strategy},
				Business:   business,
				Metric:     emptyMetricReader{},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := processor.Process(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != domain.EnrichStatusSucceeded || business.calls != 1 {
				t.Fatalf("status=%q business calls=%d diagnostics=%#v", result.Status, business.calls, result.Diagnostics)
			}
		})
	}
}

func strategyForProcessorBusiness(bizID int64) models.CWStrategy {
	return models.CWStrategy{
		BKBizID: &bizID,
		Name:    "CPU",
		Spec: models.CWStrategySpec{
			Name: "CPU", AliasName: "CPU", DataSource: "system",
			StrategyItem: &models.CWStrategyItem{
				Expression: "A", AggregateMethod: "avg", AggregatePeriod: json.RawMessage(`60`),
				Functions: json.RawMessage(`[]`),
				QueryConfigs: []models.StrategyQueryConfig{{
					Alias: "A", MetricField: "usage", ResultTableID: "system.cpu",
					AggregateMethod: "avg", AggregatePeriod: 60,
				}},
			},
		},
		Status: models.CWStrategyStatus{BKStrategyID: 78},
	}
}

type emptyMetricReader struct{}

func (emptyMetricReader) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	return models.MetricMetadata{}, false, nil
}
