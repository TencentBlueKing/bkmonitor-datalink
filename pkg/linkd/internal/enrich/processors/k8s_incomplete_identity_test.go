// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"errors"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/rules"
	"linkd/internal/enrich/view"
	"linkd/internal/lifecycle/kachook"
)

func TestResourceThenK8sRequiresCompleteNodeIdentity(t *testing.T) {
	t.Parallel()
	for _, noData := range []bool{false, true} {
		for _, test := range []struct {
			name       string
			missing    string
			reader     k8sTestReader
			wantID     string
			wantCode   enrich.DiagnosticCode
			wantStatus domain.EnrichStatus
		}{
			{name: "node found", reader: k8sTestReader{found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.K8sNodeModelCode, InstanceID: "cluster-1|node-1"}}, wantID: "cluster-1|node-1", wantStatus: domain.EnrichStatusSucceeded},
			{name: "missing node", missing: "node", wantCode: enrich.DiagnosticCodeMissingField, wantStatus: domain.EnrichStatusPartial},
			{name: "missing cluster", missing: rules.FieldBCSClusterID, wantCode: enrich.DiagnosticCodeMissingField, wantStatus: domain.EnrichStatusPartial},
			{name: "node not found", wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
			{name: "query error", reader: k8sTestReader{err: errors.New("OneModel unavailable")}, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
			{name: "wrong tenant", reader: k8sTestReader{found: true, instance: enrich.Instance{TenantID: "tenant-b", ModelCode: rules.K8sNodeModelCode, InstanceID: "foreign"}}, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
			{name: "wrong model", reader: k8sTestReader{found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Pod", InstanceID: "pod-1"}}, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
			{name: "empty instance", reader: k8sTestReader{found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.K8sNodeModelCode}}, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantStatus: domain.EnrichStatusPartial},
		} {
			t.Run(map[bool]string{false: "metric/", true: "no data/"}[noData]+test.name, func(t *testing.T) {
				dims := domain.DimensionMap{rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"), "node": domain.NewStringScalar("node-1")}
				if noData {
					dims[rules.FieldNoDataDimension] = domain.NewBoolScalar(true)
				}
				delete(dims, test.missing)
				event := processorBaseTargetAlert(t, dims)
				original := event.Clone()
				reader := &processorBaseTargetReader{model: processorModel(event.BKTenantID, rules.K8sNodeModelCode, "K8s节点", ""), modelFound: true}
				sources := enrich.Sources{CWStrategy: processorBaseTargetStrategy{modelCode: rules.K8sNodeModelCode}, Model: reader, OneModel: reader, K8s: &test.reader}
				if !noData {
					data := &dataSliceReader{model: rules.K8sNodeModelCode}
					sources.CWStrategy, sources.Metric = data, data
				}
				chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, K8s{}}, sources)
				if err != nil {
					t.Fatal(err)
				}
				result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(event, original) {
					t.Fatal("enrichment modified original Event")
				}
				payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
				if err != nil {
					t.Fatal(err)
				}
				step := payload.Processors[1][rules.K8sProcessor]
				if step.Status != test.wantStatus {
					t.Fatalf("K8s status=%s want=%s diagnostics=%+v", step.Status, test.wantStatus, step.Diagnostics)
				}
				if test.wantCode != "" && (len(step.Diagnostics) != 1 || step.Diagnostics[0].Code != test.wantCode) {
					t.Fatalf("K8s diagnostics=%+v", step.Diagnostics)
				}
				if test.missing != "" && !reflect.DeepEqual(step.Diagnostics[0].Fields, []string{"dimensions." + test.missing}) {
					t.Fatalf("missing fields=%v", step.Diagnostics[0].Fields)
				}
				if test.wantCode == enrich.DiagnosticCodeDependencyInvalid && step.Diagnostics[0].Dependency != rules.DependencyOneModel {
					t.Fatalf("dependency=%s", step.Diagnostics[0].Dependency)
				}
				wantModel := ""
				if test.wantID != "" {
					wantModel = rules.K8sNodeModelCode
				}
				event.Enrich = result.Data
				effective, err := view.EnrichedEvent(event, "warning")
				if err != nil {
					t.Fatal(err)
				}
				if dimensionText(effective.Labels, rules.FieldModelID) != wantModel || dimensionText(effective.Labels, rules.FieldModelInstID) != test.wantID {
					t.Fatalf("effective identity=%v", effective.Labels)
				}
				alert := domain.Alert{Revision: 1, AlertID: "node-alert", BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, EventSourceVersion: event.EventSourceVersion, Fingerprint: event.Fingerprint, Title: event.Title, Content: event.Content, Severity: "warning", Dimensions: event.Dimensions.Clone(), Labels: event.Labels.Clone(), ExtraData: event.ExtraData.Clone(), Status: domain.AlertStatusActive, TriggerEventID: event.EventID, LatestEventID: event.EventID, BeginAt: event.OccurredAt, LastOccurredAt: event.OccurredAt, CreateAt: event.CreateAt, UpdateAt: event.CreateAt, EnrichStatus: result.Status, Enrich: result.Data.Evaluations[0].Data}
				message, err := kachook.CompatibilityMessage(alert, func(level string) (string, error) { return level, nil })
				if err != nil {
					t.Fatal(err)
				}
				if message.ModelID != wantModel || message.ModelInstID != test.wantID || message.BKBizID != "2" {
					t.Fatalf("KAC model=%q instance=%q business=%q", message.ModelID, message.ModelInstID, message.BKBizID)
				}
			})
		}
	}
}

func TestK8sFailureClearsIncompleteIdentityFromEffectiveState(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"labels", "extra_data", "dimensions"} {
		for _, identity := range []struct{ name, model, instance string }{
			{name: "model only", model: rules.K8sNodeModelCode},
			{name: "instance only", instance: "existing-node"},
			{name: "complete", model: rules.K8sNodeModelCode, instance: "existing-node"},
		} {
			t.Run(root+"/"+identity.name, func(t *testing.T) {
				event := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1")})
				chainProcessors := []enrich.Processor{K8s{}}
				if root == "dimensions" {
					event.Dimensions[rules.FieldModelID] = domain.NewStringScalar(identity.model)
					event.Dimensions[rules.FieldModelInstID] = domain.NewStringScalar(identity.instance)
				} else {
					prior, err := NewRules("fields", map[string]any{"rules": []any{map[string]any{
						"id": "resource-identity", "operations": []any{map[string]any{
							"id": "identity", "type": "assign", "assignments": []any{
								map[string]any{"target": "$." + root + ".model_id", "value": map[string]any{"literal": identity.model}},
								map[string]any{"target": "$." + root + ".model_inst_id", "value": map[string]any{"literal": identity.instance}},
							},
						}},
					}}})
					if err != nil {
						t.Fatal(err)
					}
					chainProcessors = append([]enrich.Processor{prior}, chainProcessors...)
				}
				chain, err := enrich.NewChain(chainProcessors, enrich.Sources{})
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
				if identity.model == "" || identity.instance == "" {
					if dimensionText(effective.Labels, rules.FieldModelID) != "" || dimensionText(effective.Labels, rules.FieldModelInstID) != "" {
						t.Fatalf("incomplete identity remains: %v", effective.Labels)
					}
					// 显式空 labels 必须遮蔽 extra_data 的同名字段，避免 KAC 再次读到残留模型。
					if _, exists := effective.Labels[rules.FieldModelID]; !exists {
						t.Fatal("cleanup did not emit an explicit model override")
					}
				} else {
					payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
					if err != nil {
						t.Fatal(err)
					}
					if len(payload.Processors[len(payload.Processors)-1][rules.K8sProcessor].Patches) != 0 {
						t.Fatal("missing dimensions overwrote complete existing identity")
					}
				}
			})
		}
	}
}
