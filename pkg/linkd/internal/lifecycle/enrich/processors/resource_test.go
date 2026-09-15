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
	"encoding/json"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

func TestAppendAdditionalDisplayDimensions(t *testing.T) {
	t.Parallel()
	value, _ := domain.NewNumberScalar(101)
	entries := appendAdditionalDisplayDimensions(nil, domain.DimensionMap{"bk_host_id": value}, nil, models.ResourceValues{})
	entries = appendAdditionalDisplayDimensions(entries, domain.DimensionMap{"bk_host_id": value}, nil, models.ResourceValues{})
	if len(entries) != 1 {
		t.Fatalf("duplicate entries=%#v", entries)
	}
	text := buildDimensionText(entries, rules.DisplayBase, models.ResourceValues{}, "system.cpu", domain.DimensionMap{"bk_host_id": value})
	if text != "bk_host_id(101)" {
		t.Fatalf("dimension text=%q", text)
	}
}

func TestCombinedDimensions(t *testing.T) {
	t.Parallel()
	host, _ := domain.NewNumberScalar(101)
	alert := domain.Alert{
		Dimensions: domain.DimensionMap{"bk_target_ip": domain.NewStringScalar("10.0.0.1")},
		ExtraData:  domain.JSONObject{"additional_dimensions": json.RawMessage(`{"bk_host_id":101}`)},
	}
	combined, err := combinedDimensions(alert)
	if err != nil {
		t.Fatal(err)
	}
	if combined["bk_host_id"] != host || len(combined) != 2 {
		t.Fatalf("combined=%#v", combined)
	}
	alert.ExtraData["additional_dimensions"] = json.RawMessage(`{"bk_target_ip":"other"}`)
	if _, err := combinedDimensions(alert); err == nil {
		t.Fatal("duplicate additional dimension accepted")
	}
}

func TestResourceInstanceQueryUsesHostIDPriority(t *testing.T) {
	t.Parallel()
	instID, _ := domain.NewNumberScalar(101)
	hostID, _ := domain.NewNumberScalar(102)
	targetID, _ := domain.NewNumberScalar(103)
	query, diagnostics := resourceInstanceQuery(rules.HostModelCode, domain.DimensionMap{
		"bk_inst_id": instID, "bk_host_id": hostID, "bk_target_host_id": targetID,
	})
	if len(diagnostics) != 0 || query.ModelCode != rules.HostModelCode || query.InstanceID != "101" {
		t.Fatalf("query=%#v diagnostics=%#v", query, diagnostics)
	}
}

func TestResourceInstanceQueryFallsBackToHostAddress(t *testing.T) {
	t.Parallel()
	cloudID, _ := domain.NewNumberScalar(0)
	query, diagnostics := resourceInstanceQuery(rules.HostModelCode, domain.DimensionMap{
		"bk_target_ip": domain.NewStringScalar("10.0.0.1"), "bk_target_cloud_id": cloudID,
	})
	if len(diagnostics) != 1 || len(query.AttributeFilters) != 2 ||
		query.AttributeFilters[0] != (enrich.InstanceAttributeFilter{Field: rules.FieldBKHostInnerIP, Type: enrich.InstanceAttributeKeyword, Value: "10.0.0.1"}) ||
		query.AttributeFilters[1] != (enrich.InstanceAttributeFilter{Field: rules.FieldBKCloudID, Type: enrich.InstanceAttributeLong, Value: float64(0)}) {
		t.Fatalf("query=%#v diagnostics=%#v", query, diagnostics)
	}
}

func TestResourceInstanceQueryKeepsNonHostBoundary(t *testing.T) {
	t.Parallel()
	hostID, _ := domain.NewNumberScalar(102)
	query, diagnostics := resourceInstanceQuery("cw-Disk", domain.DimensionMap{"bk_host_id": hostID})
	if query.InstanceID != "" || len(query.AttributeFilters) != 0 || len(diagnostics) != 1 || diagnostics[0].Fields[0] != "dimensions.bk_inst_id" {
		t.Fatalf("query=%#v diagnostics=%#v", query, diagnostics)
	}
}

func TestResourceValuesMergeUnifiedInstanceAttributes(t *testing.T) {
	t.Parallel()
	values := resourceValues(enrich.Instance{
		ModelCode: "cw-Host", InstanceID: "167",
		Fields: map[string]any{
			"bk_tenant_id": "system", "model_id": "cw-Host", "model_inst_id": "167",
			"entity_uid": "cw-Host|167", "bk_biz_ids": []any{json.Number("2")},
		},
		Attributes: map[string]any{
			"bk_obj_id": "host", "bk_host_id": json.Number("167"), "bk_biz_id": json.Number("2"),
			"bk_biz_name": "蓝鲸-修改后2", "bk_cloud_id": json.Number("0"), "bk_cloud_name": "Default Area",
			"model_name": "主机",
		},
	}, 99)
	if values.ModelID != "cw-Host" || values.ModelInstID != "167" || values.BKObjID != "host" ||
		values.BKInstID != json.Number("167") || values.BKBizID != json.Number("2") ||
		values.BKBizName != "蓝鲸-修改后2" || values.ModelName != "主机" || values.BKCloudID != json.Number("0") {
		t.Fatalf("resource values=%#v", values)
	}
}

func TestResourceValuesUsesRootBusinessIDs(t *testing.T) {
	t.Parallel()
	values := resourceValues(enrich.Instance{
		ModelCode: "cw-Host", InstanceID: "167",
		Fields:     map[string]any{"bk_biz_ids": []any{json.Number("2")}},
		Attributes: map[string]any{"bk_host_id": json.Number("167")},
	}, 99)
	if values.BKBizID != json.Number("2") {
		t.Fatalf("bk_biz_id=%#v", values.BKBizID)
	}
	multiple := resourceValues(enrich.Instance{
		ModelCode: "cw-Host", InstanceID: "167", Fields: map[string]any{"bk_biz_ids": []any{json.Number("2"), json.Number("3")}},
	}, 99)
	if multiple.BKBizID != int64(99) {
		t.Fatalf("multiple business fallback=%#v", multiple.BKBizID)
	}
}
