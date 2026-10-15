// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package basetarget

import (
	"context"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestNoDataHostNativeIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		dimensions   domain.DimensionMap
		subjectID    string
		wantInstance string
		wantQueries  int
	}{
		{name: "alarmd host ID", dimensions: domain.DimensionMap{"bk_host_id": domain.NewStringScalar("101")}, subjectID: "101", wantInstance: "101", wantQueries: 1},
		{name: "legacy target host ID", dimensions: domain.DimensionMap{"bk_target_host_id": domain.NewStringScalar("101")}, wantInstance: "101", wantQueries: 1},
		{name: "subject fallback", subjectID: "101", wantInstance: "101", wantQueries: 1},
		{name: "legacy address aliases", dimensions: domain.DimensionMap{"ip": domain.NewStringScalar("10.0.0.1"), "bk_cloud_id": number(t, 0)}, wantInstance: "101", wantQueries: 1},
		{name: "conflicting subject", dimensions: domain.DimensionMap{"bk_host_id": domain.NewStringScalar("101")}, subjectID: "999"},
		{name: "partial canonical pair cannot fall back", dimensions: domain.DimensionMap{"model_id": domain.NewStringScalar(rules.HostModelCode), "bk_host_id": domain.NewStringScalar("101")}, subjectID: "101"},
		{name: "invalid canonical identity cannot fall back", dimensions: domain.DimensionMap{"model_id": domain.NewStringScalar(rules.HostModelCode), "model_inst_id": domain.NewStringScalar("0101"), "bk_host_id": domain.NewStringScalar("101")}, subjectID: "101"},
		{name: "canonical subject conflict", dimensions: domain.DimensionMap{"model_id": domain.NewStringScalar(rules.HostModelCode), "model_inst_id": domain.NewStringScalar("101")}, subjectID: "999"},
		{name: "canonical native conflict", dimensions: domain.DimensionMap{"model_id": domain.NewStringScalar(rules.HostModelCode), "model_inst_id": domain.NewStringScalar("101"), "bk_target_host_id": domain.NewStringScalar("999")}},
		{name: "explicit empty canonical identity", dimensions: domain.DimensionMap{"model_id": domain.NewStringScalar(rules.HostModelCode), "model_inst_id": domain.NewStringScalar(""), "bk_host_id": domain.NewStringScalar("101")}, subjectID: "101"},
		{name: "invalid native ID", dimensions: domain.DimensionMap{"bk_host_id": domain.NewStringScalar("0101")}, subjectID: "101"},
		{name: "unscoped address", dimensions: domain.DimensionMap{"ip": domain.NewStringScalar("10.0.0.1")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dims := test.dimensions.Clone()
			if dims == nil {
				dims = domain.DimensionMap{}
			}
			dims[rules.FieldNoDataDimension] = domain.NewBoolScalar(true)
			event := baseTargetAlert(t, dims)
			event.SubjectSystem, event.SubjectType, event.SubjectID = "cmdb", "host", test.subjectID
			original := event.Clone()
			reader := hostReader()
			scope, err := enrich.NewScope(event, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader})
			if err != nil {
				t.Fatal(err)
			}
			code := rules.HostModelCode
			result, err := Enrich(t.Context(), scope, rules.Classification{Main: rules.MainTarget, BaseTarget: rules.BaseTargetNoData}, models.CWStrategy{ObjectModelCode: &code}, dims, 2, event.SubjectName)
			if err != nil {
				t.Fatal(err)
			}
			if result.Resource.ModelInstID != test.wantInstance || reader.instanceCalls != test.wantQueries {
				t.Fatalf("result=%#v calls=%d", result, reader.instanceCalls)
			}
			if test.wantInstance != "" && (len(result.ResourceDiagnostics) != 0 || result.Object != "主机 101" || result.Resource.BKSetName != "集群") {
				t.Fatalf("result=%#v", result)
			}
			if test.wantInstance == "" && len(result.ResourceDiagnostics) == 0 {
				t.Fatal("invalid identity lost diagnostics")
			}
			if result.Resource.ModelName != "主机" || !reflect.DeepEqual(event, original) {
				t.Fatalf("model metadata lost or event mutated: %#v", result)
			}
		})
	}
}

func TestBaseTargetModelAndTopologyCompensation(t *testing.T) {
	t.Parallel()
	for _, branch := range []rules.BaseTargetBranch{rules.BaseTargetMonitorSource, rules.BaseTargetNoData, rules.BaseTargetSystemMetric} {
		t.Run(string(branch), func(t *testing.T) {
			reader := hostReader()
			reader.instanceFound = false
			dims := domain.DimensionMap{"bk_inst_id": number(t, 101), "model_id": domain.NewStringScalar(rules.HostModelCode), "model_inst_id": number(t, 101)}
			event := baseTargetAlert(t, dims)
			scope, err := enrich.NewScope(event, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader})
			if err != nil {
				t.Fatal(err)
			}
			code := rules.HostModelCode
			result, err := Enrich(context.Background(), scope, rules.Classification{Main: rules.MainTarget, BaseTarget: branch}, models.CWStrategy{ObjectModelCode: &code}, dims, 2, event.SubjectName)
			if err != nil {
				t.Fatal(err)
			}
			if result.Resource.ModelName != "主机" || result.Resource.BKBizName != "业务" || result.Resource.BKModuleName != "模块" || result.Resource.ModelInstID != "" || len(result.ResourceDiagnostics) == 0 {
				t.Fatalf("result=%#v", result)
			}
			for _, label := range result.Resource.CWLabels {
				if label == "cw-Host|101" {
					t.Fatal("unverified instance granted a label")
				}
			}
		})
	}
	// 明确的错误响应不能触发拓扑补偿，更不能生成实例权限标签。
	reader := hostReader()
	reader.instanceErr = enrich.ErrInvalidDataSourceResponse
	reader.instanceFound = false
	dims := domain.DimensionMap{"bk_host_id": number(t, 101)}
	event := baseTargetAlert(t, dims)
	scope, _ := enrich.NewScope(event, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader})
	code := rules.HostModelCode
	result, err := Enrich(t.Context(), scope, rules.Classification{Main: rules.MainTarget, BaseTarget: rules.BaseTargetSystemMetric}, models.CWStrategy{ObjectModelCode: &code}, dims, 2, event.SubjectName)
	if err != nil || reader.topologyCalls != 0 || result.Resource.ModelInstID != "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestHostSubjectFallbackRequiresCMDBHost(t *testing.T) {
	t.Parallel()
	for _, subject := range []struct{ system, typ, id string }{{"cmdb", "service", "101"}, {"other", "host", "101"}, {"cmdb", "host", "0101"}} {
		event := baseTargetAlert(t, domain.DimensionMap{})
		event.SubjectSystem, event.SubjectType, event.SubjectID = subject.system, subject.typ, subject.id
		reader := hostReader()
		scope, err := enrich.NewScope(event, enrich.Sources{Model: reader, OneModel: reader})
		if err != nil {
			t.Fatal(err)
		}
		code := rules.HostModelCode
		result, err := Enrich(t.Context(), scope, rules.Classification{Main: rules.MainTarget, BaseTarget: rules.BaseTargetSystemMetric}, models.CWStrategy{ObjectModelCode: &code}, event.Dimensions, 2, "")
		if err != nil || reader.instanceCalls != 0 || result.Resource.ModelInstID != "" {
			t.Fatalf("result=%#v calls=%d err=%v", result, reader.instanceCalls, err)
		}
	}
}
