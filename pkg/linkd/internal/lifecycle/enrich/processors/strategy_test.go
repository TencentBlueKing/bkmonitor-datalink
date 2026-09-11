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
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

func TestBuildStrategyURLDefault(t *testing.T) {
	t.Parallel()
	isDefault := true
	monitorStrategyID := int64(42)
	got, ok := buildStrategyURL(context.Background(), nil, models.CWStrategy{IsDefault: &isDefault}, "config1", monitorStrategyID)
	if !ok || got != "/#/kmc/manage/monitorStrategy/monitorStrategyDetails?id=42&strategy_config_id=config1" {
		t.Fatalf("buildStrategyURL()=%q,%v", got, ok)
	}
}

func TestBuildStrategyURLCloudUsesDefaultRoute(t *testing.T) {
	t.Parallel()
	isDefault := false
	got, ok := buildStrategyURL(context.Background(), nil, models.CWStrategy{Kind: models.CWStrategyKindCloud, IsDefault: &isDefault}, "config1", 42)
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
	got, ok := buildStrategyURL(context.Background(), scope, models.CWStrategy{
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
		Fields: map[string]any{"bk_obj_id": "host", "bk_host_id": int64(101), "object_model_group_id": int64(7)},
	}, true, nil
}
