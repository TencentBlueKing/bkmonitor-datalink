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
	"reflect"
	"slices"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/rules"
	"linkd/internal/enrich/view"
)

func TestRemoteHostCanonicalIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		target bool
		noData bool
		change func(*domain.Event, *remoteHostReader)
		failed bool
	}{
		{name: "catalog host uses target instead of collector"},
		{name: "target host uses target instead of collector", target: true},
		{name: "catalog no data host", noData: true},
		{name: "target no data host", target: true, noData: true},
		{name: "canonical identity without target host ID", change: func(e *domain.Event, _ *remoteHostReader) {
			delete(e.Dimensions, "bk_target_host_id")
		}},
		{name: "canonical identity overrides native host ID", change: func(e *domain.Event, _ *remoteHostReader) {
			e.Dimensions["bk_target_host_id"] = domain.NewStringScalar("20934")
		}},
		{name: "canonical identity without address", change: func(e *domain.Event, _ *remoteHostReader) {
			delete(e.Dimensions, "ip")
			delete(e.Dimensions, "bk_target_ip")
		}},
		{name: "missing model does not bind collector", failed: true, change: func(e *domain.Event, _ *remoteHostReader) {
			delete(e.Dimensions, "model_id")
		}},
		{name: "missing instance does not bind collector", failed: true, change: func(e *domain.Event, _ *remoteHostReader) {
			delete(e.Dimensions, "model_inst_id")
		}},
		{name: "invalid instance does not bind collector", failed: true, change: func(e *domain.Event, _ *remoteHostReader) {
			e.Dimensions["model_inst_id"] = domain.NewStringScalar("021250")
		}},
		{name: "missing target does not bind collector", failed: true, change: func(_ *domain.Event, r *remoteHostReader) { r.found = false }},
		{name: "target query failure does not bind collector", failed: true, change: func(_ *domain.Event, r *remoteHostReader) {
			r.instanceErr = errors.New("OneModel unavailable")
		}},
		{name: "foreign target rejected", failed: true, change: func(_ *domain.Event, r *remoteHostReader) { r.instance.TenantID = "tenant-b" }},
		{name: "wrong target rejected", failed: true, change: func(_ *domain.Event, r *remoteHostReader) { r.instance.InstanceID = "20934" }},
		{name: "wrong model rejected", failed: true, change: func(_ *domain.Event, r *remoteHostReader) { r.instance.ModelCode = "cw-MySQL" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &remoteHostReader{dataSliceReader: &dataSliceReader{
				model: rules.HostModelCode, found: true,
				instance: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "21250", Attributes: map[string]any{
					"bk_host_id": int64(21250), "bk_host_name": "target-host", "bk_inst_display_name": "被采主机", "bk_host_innerip": "10.10.24.242", "bk_cloud_id": int64(0), "bk_biz_id": int64(2),
				}},
			}}
			reader.strategy = reader.strategyForTest()
			if tc.target {
				reader.strategy.Spec.ConfigType = "target"
			}
			event := processorBaseTargetAlert(t, domain.DimensionMap{
				"model_id": domain.NewStringScalar("cw-Host"), "model_inst_id": domain.NewStringScalar("21250"),
				"entity_uid": domain.NewStringScalar("cw-Host|21250"), "bk_collect_config_id": domain.NewStringScalar("30"),
				"bk_host_id": domain.NewStringScalar("20934"), "bk_target_host_id": domain.NewStringScalar("21250"),
				"ip": domain.NewStringScalar("10.10.28.36"), "bk_target_ip": domain.NewStringScalar("10.10.28.36"),
				"bk_cloud_id": domain.NewStringScalar("0"), "bk_target_cloud_id": domain.NewStringScalar("0"),
			})
			event.SubjectSystem, event.SubjectType, event.SubjectID, event.SubjectName = "cmdb", "host", "20934", "采集主机"
			if tc.noData {
				event.Dimensions[rules.FieldNoDataDimension] = domain.NewBoolScalar(true)
			}
			if tc.change != nil {
				tc.change(&event, reader)
			}
			original := event.Clone()
			chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, Display{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(event, original) {
				t.Fatal("enrichment changed the source event")
			}
			for _, query := range reader.queries {
				if query.ModelCode != rules.HostModelCode || query.InstanceID != "21250" || len(query.AttributeFilters) != 0 {
					t.Fatalf("queried collector instead of canonical target: %#v", query)
				}
			}
			event.Enrich = result.Data
			effective, err := view.EnrichedEvent(event, "warning")
			if err != nil {
				t.Fatal(err)
			}
			if tc.failed {
				if result.Status == domain.EnrichStatusSucceeded || rules.DimensionText(effective.Labels, "model_inst_id") != "" || rules.ScalarProvided(effective.Labels["bk_host_id"]) {
					t.Fatalf("invalid target was bound: status=%s labels=%v", result.Status, effective.Labels)
				}
				return
			}
			for key, want := range map[string]string{"model_id": "cw-Host", "model_inst_id": "21250", "bk_inst_id": "21250", "bk_host_id": "21250", "bk_host_name": "target-host"} {
				if got := rules.DimensionText(effective.Labels, key); got != want {
					t.Errorf("%s=%q, want %q", key, got, want)
				}
			}
			if result.Status != domain.EnrichStatusSucceeded || effective.SubjectName != "被采主机" || len(reader.queries) != 1 {
				t.Fatalf("status=%s object=%q title=%q queries=%v", result.Status, effective.SubjectName, effective.Title, reader.queries)
			}
			var labels []string
			if err := json.Unmarshal(effective.ExtraData["cw_labels"], &labels); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(labels, "cw-Host|21250") || slices.Contains(labels, "cw-Host|20934") {
				t.Fatalf("wrong resource labels: %v", labels)
			}
		})
	}
}

type remoteHostReader struct {
	*dataSliceReader
	queries []enrich.InstanceQuery
}

func (r *remoteHostReader) FindInstance(ctx context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	if query.ModelCode == "cw-biz" {
		return r.dataSliceReader.FindInstance(ctx, tenant, query)
	}
	r.queries = append(r.queries, query)
	if tenant != "tenant-a" || query.ModelCode != rules.HostModelCode || query.InstanceID != "21250" || len(query.AttributeFilters) != 0 {
		return enrich.Instance{}, false, errors.New("remote target must be queried by model_id/model_inst_id")
	}
	return r.dataSliceReader.FindInstance(ctx, tenant, query)
}
