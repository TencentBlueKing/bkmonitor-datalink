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
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func TestStrategyAssociationDoesNotRequireBusinessMatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                      string
		strategyBizID, alertBizID int64
		missing                   bool
	}{
		{name: "same business", strategyBizID: 23, alertBizID: 23},
		{name: "different business", strategyBizID: 24, alertBizID: 23},
		{name: "non-BKCC space", strategyBizID: -255, alertBizID: -255},
		{name: "resource business differs from strategy space", strategyBizID: -255, alertBizID: 2},
		{name: "zero business", alertBizID: 0},
		{name: "missing business", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := strategyTestAlert(t, tc.alertBizID)
			if tc.missing {
				delete(event.Labels, "bk_biz_id")
			}
			strategy := strategyForBusiness(tc.strategyBizID)
			if tc.missing {
				strategy.BKBizID = nil
			}
			business := &strategyBusinessReader{err: errors.New("must not query business for strategy association")}
			scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: strategyProcessorReader{strategy: strategy}, Business: business})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (Strategy{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != domain.EnrichStatusSucceeded || business.calls != 0 || string(result.Value["strategy_id"]) != "78" || string(result.Value["strategy_version"]) != "1" || string(result.Value["strategy_name"]) != `"CPU"` {
				t.Fatalf("result=%+v business calls=%d", result, business.calls)
			}
		})
	}
}

func TestStrategyUsesLegacyKACDisplayName(t *testing.T) {
	t.Parallel()
	bizID := int64(2)
	scope, err := enrich.NewScope(strategyTestAlert(t, bizID), enrich.Sources{
		CWStrategy: strategyProcessorReader{strategy: models.CWStrategy{
			BKBizID: &bizID, MonitorTemplateName: "日志高级",
			Spec: models.CWStrategySpec{
				Name: "keyword_num", AliasName: "日志关键字丨mt-es接入任务场景正常-080601",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Strategy{}).Process(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(result.Value["strategy_name"]); got != `"日志高级-日志关键字丨mt-es接入任务场景正常-080601"` {
		t.Fatalf("strategy_name=%s", got)
	}
}

func TestStrategyUsesOnlyTemplateNameForPromQL(t *testing.T) {
	t.Parallel()
	bizID := int64(2)
	scope, err := enrich.NewScope(strategyTestAlert(t, bizID), enrich.Sources{
		CWStrategy: strategyProcessorReader{strategy: models.CWStrategy{
			BKBizID: &bizID, MonitorTemplateName: "日志高级",
			Spec: models.CWStrategySpec{
				Name: "keyword_num", AliasName: "日志关键字丨mt-es接入任务场景正常-080601",
				StrategyItem: &models.CWStrategyItem{QueryConfigs: []models.StrategyQueryConfig{{PromQL: "sum(rate(log_total[5m]))"}}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Strategy{}).Process(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(result.Value["strategy_name"]); got != `"日志高级"` {
		t.Fatalf("strategy_name=%s", got)
	}
}

// Strategy 没有生效的配置项；已废弃配置的存在、形状或取值都不能改变事实输出。
func TestNewStrategyIgnoresUnusedConfig(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		config map[string]any
	}{
		{name: "absent"},
		{name: "empty", config: map[string]any{}},
		{name: "absolute URL", config: map[string]any{"web_saas_module_url": "https://example.test/app/"}},
		{name: "relative URL", config: map[string]any{"web_saas_module_url": "/relative"}},
		{name: "unsupported URL scheme", config: map[string]any{"web_saas_module_url": "ftp://example.test"}},
		{name: "malformed URL", config: map[string]any{"web_saas_module_url": "http://[invalid"}},
		{name: "numeric value", config: map[string]any{"web_saas_module_url": 1}},
		{name: "null value", config: map[string]any{"web_saas_module_url": nil}},
		{name: "structured value", config: map[string]any{"web_saas_module_url": map[string]any{"invalid": true}}},
		{name: "unused field", config: map[string]any{"unknown": "value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			processor, err := NewStrategy(tc.config)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := enrich.NewScope(strategyTestAlert(t, 2), enrich.Sources{CWStrategy: strategyProcessorReader{strategy: strategyForBusiness(2)}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := processor.Process(t.Context(), scope)
			if err != nil || result.Status != domain.EnrichStatusSucceeded || string(result.Value["strategy_name"]) != `"CPU"` || string(result.Value["strategy_id"]) != "78" || string(result.Value["strategy_version"]) != "1" {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if _, exists := result.Value["url"]; exists {
				t.Fatal("unused configuration generated a product URL")
			}
		})
	}
}

func TestStrategyInstanceFacts(t *testing.T) {
	t.Parallel()
	isDefault := false
	modelCode := "cw-Host"
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	alert := domain.Event{
		EventSourceVersion: 1, EventID: "alert-1", BKTenantID: "tenant-a", EventSourceID: "built_in_bk", Fingerprint: "fp",
		Title: "CPU", Evaluations: []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}, Dimensions: domain.DimensionMap{"bk_inst_id": mustNumberScalar(t, 101)}, Labels: domain.DimensionMap{},
		ExtraData:     domain.JSONObject{"additional_dimensions": []byte(`{"ignored":true}`)},
		SourceEventID: "source-event-1", OccurredAt: now, ProducedAt: now, ReceivedAt: now, CreateAt: now, SourceRawData: domain.JSONObject{}, EventEnrichment: domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending}, Values: domain.EventValues{},
	}
	scope, err := enrich.NewScope(alert, enrich.Sources{OneModel: strategyURLInstanceReader{}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := strategyInstanceFacts(context.Background(), scope, models.CWStrategy{
		IsDefault: &isDefault, ObjectModelCode: &modelCode,
	})
	want := models.StrategyInstance{ObjectModelCode: "cw-Host", BKObjID: "host", BKInstID: "101", ObjectModelGroupID: "7"}
	if !ok || got == nil || *got != want {
		t.Fatalf("strategyInstanceFacts()=%#v,%v", got, ok)
	}

}

func strategyTestAlert(t *testing.T, bizID int64) domain.Event {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Event{
		EventSourceVersion: 1, EventID: "alert-1", BKTenantID: "system", EventSourceID: "built_in_bk", Fingerprint: "fp",
		Title: "CPU", Evaluations: []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}, Dimensions: domain.DimensionMap{},
		Labels: domain.DimensionMap{
			"strategy_id": mustNumberScalar(t, 78), "strategy_version": mustNumberScalar(t, 1),
			"bk_biz_id": mustNumberScalar(t, float64(bizID)),
		},
		ExtraData: domain.JSONObject{}, SourceEventID: "source-event-1", OccurredAt: now, ProducedAt: now, ReceivedAt: now, CreateAt: now, EventEnrichment: domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending}, Values: domain.EventValues{}, SourceRawData: domain.JSONObject{},
	}
}

func strategyForBusiness(bizID int64) models.CWStrategy {
	return models.CWStrategy{
		BKBizID: &bizID, Name: "CPU", Spec: models.CWStrategySpec{Name: "CPU"},
		Status: models.CWStrategyStatus{BKStrategyID: 78},
	}
}

type strategyProcessorReader struct{ strategy models.CWStrategy }

func (r strategyProcessorReader) GetByStrategyID(context.Context, models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
	return r.strategy, true, nil
}

type strategyBusinessReader struct {
	isGlobal bool
	found    bool
	err      error
	calls    int
}

func (r *strategyBusinessReader) IsGlobalBusiness(context.Context, string, int64) (bool, bool, error) {
	r.calls++
	return r.isGlobal, r.found, r.err
}

func mustNumberScalar(t *testing.T, value float64) domain.Scalar {
	t.Helper()
	scalar, err := domain.NewNumberScalar(value)
	if err != nil {
		t.Fatal(err)
	}
	return scalar
}

type strategyURLInstanceReader struct{}

func (strategyURLInstanceReader) FindInstance(
	context.Context,
	string,
	enrich.InstanceQuery,
) (enrich.Instance, bool, error) {
	return enrich.Instance{
		ModelCode: "cw-Host", InstanceID: "101",
		Fields: map[string]any{"model_id": "cw-Host", "model_inst_id": "101", "entity_uid": "cw-Host|101"},
		Attributes: map[string]any{
			"bk_obj_id": "host", "bk_host_id": int64(101), "object_model_group_id": int64(7),
		},
	}, true, nil
}
