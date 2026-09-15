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
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

func TestStrategyBusinessMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		strategyBizID     int64
		alertBizID        int64
		isGlobal          bool
		businessFound     bool
		businessErr       error
		wantMatch         bool
		wantError         bool
		wantBusinessCalls int
	}{
		{name: "same business", strategyBizID: 23, alertBizID: 23, wantMatch: true},
		{name: "global business", strategyBizID: 524, alertBizID: 23, isGlobal: true, businessFound: true, wantMatch: true, wantBusinessCalls: 1},
		{name: "different regular business", strategyBizID: 24, alertBizID: 23, businessFound: true, wantBusinessCalls: 1},
		{name: "business missing", strategyBizID: 24, alertBizID: 23, wantError: true, wantBusinessCalls: 1},
		{name: "business query failed", strategyBizID: 24, alertBizID: 23, businessErr: errors.New("failed"), wantError: true, wantBusinessCalls: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			business := &strategyBusinessReader{isGlobal: test.isGlobal, found: test.businessFound, err: test.businessErr}
			scope, err := enrich.NewScope(strategyTestAlert(t, test.alertBizID), enrich.Sources{Business: business})
			if err != nil {
				t.Fatal(err)
			}
			matched, err := strategyBusinessMatches(context.Background(), scope, test.strategyBizID, test.alertBizID)
			if matched != test.wantMatch || (err != nil) != test.wantError || business.calls != test.wantBusinessCalls {
				t.Fatalf("matched=%t error=%v business calls=%d", matched, err, business.calls)
			}
		})
	}
}

func TestStrategyBusinessMatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		strategyBizID     int64
		alertBizID        int64
		isGlobal          bool
		businessFound     bool
		wantStatus        domain.EnrichStatus
		wantBusinessCalls int
	}{
		{name: "same business", strategyBizID: 23, alertBizID: 23, wantStatus: domain.EnrichStatusSucceeded},
		{name: "global business", strategyBizID: 524, alertBizID: 23, isGlobal: true, businessFound: true, wantStatus: domain.EnrichStatusSucceeded, wantBusinessCalls: 1},
		{name: "different regular business", strategyBizID: 24, alertBizID: 23, businessFound: true, wantStatus: domain.EnrichStatusPartial, wantBusinessCalls: 1},
		{name: "business missing", strategyBizID: 24, alertBizID: 23, wantStatus: domain.EnrichStatusPartial, wantBusinessCalls: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			business := &strategyBusinessReader{isGlobal: test.isGlobal, found: test.businessFound}
			scope, err := enrich.NewScope(strategyTestAlert(t, test.alertBizID), enrich.Sources{
				CWStrategy: strategyProcessorReader{strategy: strategyForBusiness(test.strategyBizID)},
				Business:   business,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (Strategy{}).Process(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.wantStatus || business.calls != test.wantBusinessCalls {
				t.Fatalf("status=%q business calls=%d diagnostics=%#v", result.Status, business.calls, result.Diagnostics)
			}
		})
	}
}

func TestBuildStrategyURLUsesConfiguredWebSaaSModuleURL(t *testing.T) {
	t.Parallel()
	processor, err := NewStrategy(map[string]any{"web_saas_module_url": "https://example.com/kingeye"})
	if err != nil {
		t.Fatal(err)
	}
	isDefault := true
	got, ok := processor.buildStrategyURL(context.Background(), nil, models.CWStrategy{IsDefault: &isDefault}, "config1", 42)
	if !ok || got != "https://example.com/kingeye/#/kmc/manage/monitorStrategy/monitorStrategyDetails?id=42&strategy_config_id=config1" {
		t.Fatalf("buildStrategyURL()=%q,%v", got, ok)
	}
}

func TestNewStrategyValidatesConfig(t *testing.T) {
	t.Parallel()
	for _, config := range []map[string]any{
		{"unknown": "value"},
		{"web_saas_module_url": 1},
		{"web_saas_module_url": "/relative"},
		{"web_saas_module_url": "ftp://example.com"},
	} {
		if _, err := NewStrategy(config); err == nil {
			t.Fatalf("NewStrategy(%#v) succeeded", config)
		}
	}
}

func TestBuildStrategyURLDefault(t *testing.T) {
	t.Parallel()
	isDefault := true
	monitorStrategyID := int64(42)
	processor, err := NewStrategy(nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := processor.buildStrategyURL(context.Background(), nil, models.CWStrategy{IsDefault: &isDefault}, "config1", monitorStrategyID)
	if !ok || got != "/#/kmc/manage/monitorStrategy/monitorStrategyDetails?id=42&strategy_config_id=config1" {
		t.Fatalf("buildStrategyURL()=%q,%v", got, ok)
	}
}

func TestBuildStrategyURLCloudUsesDefaultRoute(t *testing.T) {
	t.Parallel()
	isDefault := false
	processor, err := NewStrategy(nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := processor.buildStrategyURL(context.Background(), nil, models.CWStrategy{Kind: models.CWStrategyKindCloud, IsDefault: &isDefault}, "config1", 42)
	if !ok || !strings.Contains(got, "/#/kmc/manage/monitorStrategy/monitorStrategyDetails?") {
		t.Fatalf("buildStrategyURL()=%q,%v", got, ok)
	}
}

func TestBuildStrategyURLInstance(t *testing.T) {
	t.Parallel()
	isDefault := false
	modelCode := "cw-Host"
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	alert := domain.Alert{
		EventSourceVersion: 1,
		AlertID:            "alert-1", BKTenantID: "tenant-a", EventSourceID: "built_in_bk", Fingerprint: "fp",
		Title: "CPU", Severity: "warning", Status: domain.AlertStatusActive,
		Dimensions: domain.DimensionMap{"bk_inst_id": mustNumberScalar(t, 101)}, Labels: domain.DimensionMap{},
		ExtraData:     domain.JSONObject{"additional_dimensions": []byte(`{"ignored":true}`)},
		LatestEventID: "event-1", TriggerEventID: "event-1", SourceEventID: "source-event-1",
		LastOccurredAt: now, UpdateAt: now, BeginAt: now, CreateAt: now,
		EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
	scope, err := enrich.NewScope(alert, enrich.Sources{OneModel: strategyURLInstanceReader{}})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := NewStrategy(nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := processor.buildStrategyURL(context.Background(), scope, models.CWStrategy{
		IsDefault: &isDefault, ObjectModelCode: &modelCode,
	}, "config1", 42)
	if !ok {
		t.Fatalf("buildStrategyURL()=%q,%v", got, ok)
	}
	for _, expected := range []string{
		"/#/kmc/scene/monitorViewDetails?", "bk_obj_id=host", "bk_inst_id=101", "strategy_id=42",
		"strategy_item_id=config1", "classId=7", "object_model_code=cw-Host",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("buildStrategyURL()=%q missing %q", got, expected)
		}
	}
}

func strategyTestAlert(t *testing.T, bizID int64) domain.Alert {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Alert{
		EventSourceVersion: 1,
		AlertID:            "alert-1", BKTenantID: "system", EventSourceID: "built_in_bk", Fingerprint: "fp",
		Title: "CPU", Severity: "warning", Status: domain.AlertStatusActive,
		Dimensions: domain.DimensionMap{},
		Labels: domain.DimensionMap{
			"strategy_id": mustNumberScalar(t, 78), "strategy_version": mustNumberScalar(t, 1),
			"bk_biz_id": mustNumberScalar(t, float64(bizID)),
		},
		ExtraData: domain.JSONObject{}, LatestEventID: "event-1", TriggerEventID: "event-1", SourceEventID: "source-event-1",
		LastOccurredAt: now, UpdateAt: now, BeginAt: now, CreateAt: now,
		EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
}

func strategyForBusiness(bizID int64) models.CWStrategy {
	return models.CWStrategy{
		BKBizID: &bizID, Name: "CPU", Spec: models.CWStrategySpec{Name: "CPU"},
		Status: models.CWStrategyStatus{BKStrategyID: 78},
	}
}

type strategyProcessorReader struct{ strategy models.CWStrategy }

func (r strategyProcessorReader) GetByBKStrategyID(context.Context, string, int64) (models.CWStrategy, bool, error) {
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
