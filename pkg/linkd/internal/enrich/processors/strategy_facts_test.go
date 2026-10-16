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
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func TestStrategyPublishesAssociationFactsWithoutFrontendURL(t *testing.T) {
	t.Parallel()
	for _, kind := range []models.CWStrategyKind{models.CWStrategyKindStrategy, models.CWStrategyKindCloud} {
		t.Run(string(kind), func(t *testing.T) {
			yes := true
			template := int64(42)
			configID := "config-1"
			strategy := strategyForBusiness(2)
			strategy.Kind, strategy.IsDefault, strategy.MonitorTemplateID, strategy.ConfigID = kind, &yes, &template, &configID
			scope, err := enrich.NewScope(strategyTestAlert(t, 2), enrich.Sources{CWStrategy: strategyProcessorReader{strategy: strategy}})
			if err != nil {
				t.Fatal(err)
			}
			processor, err := NewStrategy(map[string]any{"web_saas_module_url": "https://old.example.test/app/"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := processor.Process(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := result.Value["url"]; exists {
				t.Fatalf("frontend URL still emitted: %s", result.Value["url"])
			}
			if string(result.Value["strategy_kind"]) != `"`+string(kind)+`"` || string(result.Value["is_default"]) != "true" {
				t.Fatalf("missing strategy classification facts: %#v", result.Value)
			}
			var got string
			if err := json.Unmarshal(result.Value["strategy_config_id"], &got); err != nil || got != "config1" {
				t.Fatalf("config ID=%q err=%v", got, err)
			}
		})
	}
}

func TestStrategyInstanceFactsUseTenantAndPreserveOpaqueIdentity(t *testing.T) {
	for _, test := range []struct {
		name         string
		found        bool
		fields       map[string]any
		err          error
		wantInstance bool
	}{
		{name: "opaque identity", found: true, fields: map[string]any{"bk_obj_id": "host", "object_model_group_id": 7}, wantInstance: true},
		{name: "missing classification", found: true, fields: map[string]any{"bk_obj_id": "host"}},
		{name: "missing instance"},
		{name: "dependency unavailable", err: errors.New("unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &strategyFactsInstanceReader{found: test.found, fields: test.fields, err: test.err}
			alert := strategyTestAlert(t, 2)
			alert.BKTenantID = "tenant-a"
			alert.Dimensions["bk_inst_id"] = domain.NewStringScalar("host|101")
			modelCode := "cw-Host"
			strategy := strategyForBusiness(2)
			strategy.Kind = models.CWStrategyKindStrategy
			isDefault := false
			strategy.IsDefault = &isDefault
			strategy.ObjectModelCode = &modelCode
			scope, err := enrich.NewScope(alert, enrich.Sources{CWStrategy: strategyProcessorReader{strategy: strategy}, OneModel: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (Strategy{}).Process(context.Background(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if reader.tenant != "tenant-a" || reader.query.InstanceID != "host|101" {
				t.Fatalf("lookup tenant=%q query=%#v", reader.tenant, reader.query)
			}
			instance, exists := result.Value["strategy_instance"]
			if exists != test.wantInstance {
				t.Fatalf("strategy facts=%s", result.Value)
			}
			if exists {
				var got models.StrategyInstance
				if err := json.Unmarshal(instance, &got); err != nil {
					t.Fatal(err)
				}
				want := models.StrategyInstance{ObjectModelCode: "cw-Host", BKObjID: "host", BKInstID: "host|101", ObjectModelGroupID: "7"}
				if got != want {
					t.Fatalf("instance facts=%#v", got)
				}
			}
			if _, exists := result.Value["url"]; exists {
				t.Fatal("unexpected frontend URL")
			}
		})
	}
}

func TestStrategyCancellationRemainsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scope, err := enrich.NewScope(strategyTestAlert(t, 2), enrich.Sources{CWStrategy: strategyProcessorReader{strategy: strategyForBusiness(2)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Strategy{}).Process(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

type strategyFactsInstanceReader struct {
	tenant string
	query  enrich.InstanceQuery
	found  bool
	fields map[string]any
	err    error
}

func (r *strategyFactsInstanceReader) FindInstance(_ context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.tenant, r.query = tenant, query
	return enrich.Instance{ModelCode: "cw-Host", InstanceID: "host|101", Attributes: r.fields}, r.found, r.err
}

func TestStrategyDoesNotInventMissingDefaultAttribute(t *testing.T) {
	strategy := strategyForBusiness(2)
	strategy.Kind = models.CWStrategyKindStrategy
	scope, err := enrich.NewScope(strategyTestAlert(t, 2), enrich.Sources{CWStrategy: strategyProcessorReader{strategy: strategy}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (Strategy{}).Process(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := result.Value["is_default"]; exists {
		t.Fatalf("invented default attribute: %s", result.Value)
	}
}
