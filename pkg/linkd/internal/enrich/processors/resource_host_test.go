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
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/rules"
	"linkd/internal/enrich/view"
)

func TestResourceHostAddressContext(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, model, table, ipKey, cloudKey string
		catalog                             bool
	}{
		{name: "host physical query", model: rules.HostModelCode, table: "system.cpu", ipKey: "bk_target_ip", cloudKey: "bk_target_cloud_id"},
		{name: "strategy 177 catalog without declared model or table", model: rules.HostModelCode, ipKey: "bk_target_ip", cloudKey: "bk_target_cloud_id", catalog: true},
		{name: "mysql keeps original object", model: "cw-MySQL", table: "mysql.status", ipKey: "ip", cloudKey: "bk_cloud_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := newResourceHostReader()
			reader.model = tc.model
			reader.strategy = reader.strategyForTest()
			reader.strategy.Spec.TableID = tc.table
			reader.strategy.Spec.StrategyItem.QueryConfigs[0].ResultTableID = tc.table
			if tc.catalog {
				reader.strategy.ObjectModelCode = nil
				reader.strategy.Spec.StrategyItem.QueryConfigs[0].MetricRefID = 9
				reader.strategy.Spec.StrategyItem.QueryConfigs[0].QueryConfigType = "catalog"
			}
			dims := domain.DimensionMap{tc.ipKey: domain.NewStringScalar("192.0.2.11"), tc.cloudKey: processorNumber(t, 0)}
			if tc.model != "cw-Host" {
				dims["model_id"] = domain.NewStringScalar(tc.model)
				dims["model_inst_id"] = domain.NewStringScalar("17")
			}
			event := processorBaseTargetAlert(t, dims)
			original := event.Clone()
			chain, err := enrich.NewChain([]enrich.Processor{Resource{}, Display{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, CollectTopology: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
			if err != nil {
				t.Fatal(err)
			}
			event.Enrich = result.Data
			effective, err := view.EnrichedEvent(event, "warning")
			if err != nil {
				t.Fatal(err)
			}
			wantInstance, wantSubject := "101", "主机展示名"
			if tc.model != "cw-Host" {
				wantInstance, wantSubject = "17", "数据库展示名"
			}
			if result.Status != domain.EnrichStatusSucceeded || rules.DimensionText(effective.Labels, "bk_host_id") != "101" || rules.DimensionText(effective.Labels, "bk_host_name") != "real-host-name" || rules.DimensionText(effective.Labels, "model_id") != tc.model || rules.DimensionText(effective.Labels, "model_inst_id") != wantInstance || rules.DimensionText(effective.Labels, "bk_inst_id") != wantInstance || effective.SubjectName != wantSubject {
				t.Fatalf("status=%s labels=%v subject=%s", result.Status, effective.Labels, effective.SubjectName)
			}
			event.Enrich = original.Enrich
			if !reflect.DeepEqual(event, original) {
				t.Fatal("source Event changed")
			}
			if reader.hostCalls != 1 || reader.hostTenant != "tenant-a" {
				t.Fatalf("host calls=%d tenant=%s", reader.hostCalls, reader.hostTenant)
			}
		})
	}
}

func TestResourceHostContextFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		change     func(*resourceHostReader, domain.DimensionMap)
		wantCode   enrich.DiagnosticCode
		wantHostID string
		wantCalls  int
	}{
		{name: "no host address is optional", change: func(_ *resourceHostReader, d domain.DimensionMap) { delete(d, "ip"); delete(d, "bk_cloud_id") }},
		{name: "cloud zero string", change: func(_ *resourceHostReader, d domain.DimensionMap) { d["bk_cloud_id"] = domain.NewStringScalar("0") }, wantHostID: "101", wantCalls: 1},
		{name: "invalid IP", change: func(_ *resourceHostReader, d domain.DimensionMap) { d["ip"] = domain.NewStringScalar("not-an-ip") }, wantCode: enrich.DiagnosticCodeInvalidField},
		{name: "negative cloud", change: func(_ *resourceHostReader, d domain.DimensionMap) { d["bk_cloud_id"] = processorNumber(t, -1) }, wantCode: enrich.DiagnosticCodeInvalidField},
		{name: "conflicting IP aliases", change: func(_ *resourceHostReader, d domain.DimensionMap) {
			d["bk_target_ip"] = domain.NewStringScalar("192.0.2.12")
		}, wantCode: enrich.DiagnosticCodeInvalidField},
		{name: "conflicting cloud aliases", change: func(_ *resourceHostReader, d domain.DimensionMap) {
			d["bk_target_cloud_id"] = processorNumber(t, 1)
		}, wantCode: enrich.DiagnosticCodeInvalidField},
		{name: "host missing", change: func(r *resourceHostReader, _ domain.DimensionMap) { r.hostFound = false }, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantCalls: 1},
		{name: "ambiguous hosts", change: func(r *resourceHostReader, _ domain.DimensionMap) { r.hostErr = enrich.ErrInvalidDataSourceResponse }, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantCalls: 1},
		{name: "dependency error", change: func(r *resourceHostReader, _ domain.DimensionMap) { r.hostErr = errors.New("unavailable") }, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantCalls: 1},
		{name: "wrong tenant", change: func(r *resourceHostReader, _ domain.DimensionMap) { r.host.TenantID = "tenant-b" }, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantCalls: 1},
		{name: "wrong model", change: func(r *resourceHostReader, _ domain.DimensionMap) { r.host.ModelCode = "cw-MySQL" }, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantCalls: 1},
		{name: "invalid host ID", change: func(r *resourceHostReader, _ domain.DimensionMap) { r.host.InstanceID = "invalid" }, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantCalls: 1},
		{name: "conflicting host ID attribute", change: func(r *resourceHostReader, _ domain.DimensionMap) { r.host.Attributes["bk_host_id"] = int64(102) }, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantCalls: 1},
		{name: "missing hostname never uses display name", change: func(r *resourceHostReader, _ domain.DimensionMap) { delete(r.host.Attributes, "bk_host_name") }, wantCode: enrich.DiagnosticCodeMissingField, wantHostID: "101", wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := newResourceHostReader()
			dims := domain.DimensionMap{"ip": domain.NewStringScalar("192.0.2.11"), "bk_cloud_id": processorNumber(t, 0), "model_id": domain.NewStringScalar("cw-MySQL"), "model_inst_id": domain.NewStringScalar("17")}
			tc.change(reader, dims)
			event := processorBaseTargetAlert(t, dims)
			original := event.Clone()
			scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (Resource{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := domain.EnrichStatusSucceeded
			if tc.wantCode != "" {
				wantStatus = domain.EnrichStatusPartial
			}
			var hostID any
			if raw, exists := result.Value["bk_host_id"]; exists {
				if err := json.Unmarshal(raw, &hostID); err != nil {
					t.Fatal(err)
				}
			}
			gotID := ""
			if hostID != nil {
				gotID = rules.FirstStringFieldValue(map[string]any{"id": hostID}, "id")
			}
			if result.Status != wantStatus || string(result.Value["model_id"]) != `"cw-MySQL"` || string(result.Value["model_inst_id"]) != `"17"` || gotID != tc.wantHostID || reader.hostCalls != tc.wantCalls || !reflect.DeepEqual(event, original) {
				t.Fatalf("result=%#v calls=%d hostID=%s", result, reader.hostCalls, gotID)
			}
			if tc.wantCode != "" && len(result.Value["bk_host_name"]) != 0 {
				t.Fatal("failed host lookup fabricated a hostname")
			}
			if tc.wantCode != "" {
				found := false
				for _, d := range result.Diagnostics {
					if d.Code == tc.wantCode && len(d.Fields) > 0 {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing host diagnostic: %#v", result.Diagnostics)
				}
			}
		})
	}
}

func TestResourceHostContextCancellation(t *testing.T) {
	reader := newResourceHostReader()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader.cancel = cancel
	event := processorBaseTargetAlert(t, domain.DimensionMap{"ip": domain.NewStringScalar("192.0.2.11"), "bk_cloud_id": processorNumber(t, 0), "model_id": domain.NewStringScalar("cw-MySQL"), "model_inst_id": domain.NewStringScalar("17")})
	scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (Resource{}).Process(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	reader.cancel = nil
	if result, err := (Resource{}).Process(t.Context(), scope); err != nil || result.Status != domain.EnrichStatusSucceeded || reader.hostCalls != 2 {
		t.Fatalf("retry=%#v err=%v calls=%d", result, err, reader.hostCalls)
	}
}

func TestResourceHostCanonicalIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, canonicalID, wantBinding, wantHostID string
		invalid                                    bool
		wantStatus                                 domain.EnrichStatus
	}{
		{name: "matching canonical host", canonicalID: "101", wantBinding: "101", wantHostID: "101", wantStatus: domain.EnrichStatusSucceeded},
		{name: "address cannot replace canonical host", canonicalID: "102", wantBinding: "102", wantStatus: domain.EnrichStatusPartial},
		{name: "invalid canonical identity cannot fall back", invalid: true, wantStatus: domain.EnrichStatusPartial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := newResourceHostReader()
			reader.model = rules.HostModelCode
			dims := domain.DimensionMap{"model_id": domain.NewStringScalar(rules.HostModelCode), "model_inst_id": domain.NewStringScalar(tc.canonicalID), "ip": domain.NewStringScalar("192.0.2.11"), "bk_cloud_id": processorNumber(t, 0)}
			if tc.invalid {
				dims["model_inst_id"] = processorNumber(t, 101)
			}
			event := processorBaseTargetAlert(t, dims)
			scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, CollectTopology: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (Resource{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tc.wantStatus || string(result.Value["model_inst_id"]) != `"`+tc.wantBinding+`"` || string(result.Value["bk_host_id"]) != tc.wantHostID {
				t.Fatalf("status=%s binding=%s host=%s diagnostics=%v", result.Status, result.Value["model_inst_id"], result.Value["bk_host_id"], result.Diagnostics)
			}
			if tc.invalid && reader.hostCalls != 0 {
				t.Fatal("invalid canonical identity queried by address")
			}
		})
	}
}

type resourceHostReader struct {
	*dataSliceReader
	host       enrich.Instance
	hostFound  bool
	hostErr    error
	hostCalls  int
	hostTenant string
	cancel     context.CancelFunc
}

func newResourceHostReader() *resourceHostReader {
	return &resourceHostReader{dataSliceReader: &dataSliceReader{model: "cw-MySQL", found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-MySQL", InstanceID: "17", Attributes: map[string]any{"bk_inst_id": int64(17), "bk_biz_id": int64(2), "bk_inst_display_name": "数据库展示名"}}}, hostFound: true, host: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-Host", InstanceID: "101", Attributes: map[string]any{"bk_host_id": int64(101), "bk_host_name": "real-host-name", "bk_inst_display_name": "主机展示名", "bk_host_innerip": "192.0.2.11", "bk_cloud_id": int64(0), "bk_biz_id": int64(2)}}}
}

func (r *resourceHostReader) FindInstance(ctx context.Context, tenant string, q enrich.InstanceQuery) (enrich.Instance, bool, error) {
	if q.ModelCode != rules.HostModelCode {
		return r.dataSliceReader.FindInstance(ctx, tenant, q)
	}
	r.hostCalls++
	r.hostTenant = tenant
	if r.cancel != nil {
		r.cancel()
		return enrich.Instance{}, false, ctx.Err()
	}
	if q.InstanceID == "101" || q.InstanceID == "102" {
		host := r.host
		host.InstanceID = q.InstanceID
		host.Attributes = mergeInstanceFields(nil, r.host.Attributes)
		id, err := strconv.ParseInt(q.InstanceID, 10, 64)
		if err != nil {
			return enrich.Instance{}, false, err
		}
		host.Attributes["bk_host_id"] = id
		return host, true, nil
	}
	want := enrich.InstanceQuery{ModelCode: rules.HostModelCode, AttributeFilters: []enrich.InstanceAttributeFilter{{Field: "bk_host_innerip", Type: enrich.InstanceAttributeKeyword, Value: "192.0.2.11"}, {Field: "bk_cloud_id", Type: enrich.InstanceAttributeLong, Value: int64(0)}}}
	if !reflect.DeepEqual(q, want) {
		return enrich.Instance{}, false, errors.New("unexpected host query")
	}
	return r.host, r.hostFound, r.hostErr
}

func hostFixtureMatchesAddress(query enrich.InstanceQuery, attributes map[string]any) bool {
	if query.ModelCode != rules.HostModelCode || query.InstanceID != "" || len(query.AttributeFilters) != 2 {
		return false
	}
	for _, filter := range query.AttributeFilters {
		value, exists := attributes[filter.Field]
		if !exists || fmt.Sprint(value) != fmt.Sprint(filter.Value) {
			return false
		}
	}
	return true
}
