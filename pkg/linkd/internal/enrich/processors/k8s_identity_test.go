// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package processors

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/rules"
	"linkd/internal/enrich/view"
	"linkd/internal/jsonpath"
)

func TestResourceThenK8sPreservesHostIdentity(t *testing.T) {
	t.Parallel()
	for _, service := range []string{"", "unrelated-service"} {
		t.Run("service="+service, func(t *testing.T) {
			reader := processorHostReader()
			k8sReader := &k8sTestReader{}
			event := processorBaseTargetAlert(t, domain.DimensionMap{
				rules.FieldBKInstID: processorNumber(t, 101), rules.FieldService: domain.NewStringScalar(service),
			})
			chain, err := enrich.NewChain([]enrich.Processor{Resource{}, K8s{}}, enrich.Sources{
				CWStrategy: processorBaseTargetStrategy{modelCode: rules.HostModelCode},
				Model:      reader, OneModel: reader, CollectTopology: reader, K8s: k8sReader,
			})
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
			if dimensionText(effective.Labels, rules.FieldModelID) != "cw-Host" || dimensionText(effective.Labels, rules.FieldModelInstID) != "101" {
				t.Fatalf("host identity overwritten: %#v", effective.Labels)
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			if step := payload.Processors[1][rules.K8sProcessor]; step.Status != domain.EnrichStatusSkipped || len(step.Patches) != 0 || len(k8sReader.models) != 0 {
				t.Fatalf("non-K8s resource was processed: %#v queries=%v", step, k8sReader.models)
			}
		})
	}
}

func TestK8sMatchUsesEffectiveModel(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, root, model string
		wantMatch         bool
	}{
		{name: "labels host overrides pod dimensions", root: "labels", model: "cw-Host"},
		{name: "extra data host overrides pod dimensions", root: "extra_data", model: "cw-Host"},
		{name: "dimension model overrides pod dimensions", root: "dimensions", model: "cw-Host"},
		{name: "unknown model prefix is not K8s", root: "labels", model: "cw-K8s_Unknown"},
		{name: "known cluster", root: "labels", model: "cw-K8s_Cluster", wantMatch: true},
		{name: "known namespace", root: "labels", model: "cw-K8s_Namespace", wantMatch: true},
		{name: "known service", root: "labels", model: "cw-K8s_Service", wantMatch: true},
		{name: "known workload", root: "labels", model: "cw-K8s_Workload", wantMatch: true},
		{name: "known pod", root: "labels", model: "cw-K8s_Pod", wantMatch: true},
		{name: "known container", root: "labels", model: "cw-K8s_Container", wantMatch: true},
		{name: "known node", root: "labels", model: "cw-K8s_Node", wantMatch: true},
		{name: "unknown model uses dimensions", wantMatch: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := processorBaseTargetAlert(t, k8sPodDimensions())
			switch test.root {
			case "labels":
				event.Labels[rules.FieldModelID] = domain.NewStringScalar(test.model)
			case "extra_data":
				raw, _ := json.Marshal(test.model)
				event.ExtraData[rules.FieldModelID] = raw
			case "dimensions":
				event.Dimensions[rules.FieldModelID] = domain.NewStringScalar(test.model)
			}
			scope, err := enrich.NewScope(event, enrich.Sources{})
			if err != nil {
				t.Fatal(err)
			}
			matched, err := (K8s{}).Match(t.Context(), scope)
			if err != nil || matched != test.wantMatch {
				t.Fatalf("match=%t want=%t error=%v", matched, test.wantMatch, err)
			}
		})
	}
}

func TestK8sFailuresPreserveExistingIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		model      string
		dimensions domain.DimensionMap
		reader     *k8sTestReader
		wantStatus domain.EnrichStatus
	}{
		{name: "no dimensions", dimensions: domain.DimensionMap{}, reader: &k8sTestReader{}, wantStatus: domain.EnrichStatusFailed},
		{name: "cluster without cluster id", model: "cw-K8s_Cluster", dimensions: domain.DimensionMap{}, reader: &k8sTestReader{}, wantStatus: domain.EnrichStatusFailed},
		{name: "missing namespace", dimensions: domain.DimensionMap{rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"), rules.FieldPodName: domain.NewStringScalar("web-1")}, reader: &k8sTestReader{}, wantStatus: domain.EnrichStatusFailed},
		{name: "reader unavailable", dimensions: k8sPodDimensions(), reader: &k8sTestReader{err: errors.New("k8s reader is unavailable")}, wantStatus: domain.EnrichStatusPartial},
		{name: "query failed", dimensions: k8sPodDimensions(), reader: &k8sTestReader{err: errors.New("query failed")}, wantStatus: domain.EnrichStatusPartial},
		{name: "instance missing", dimensions: k8sPodDimensions(), reader: &k8sTestReader{}, wantStatus: domain.EnrichStatusPartial},
		{name: "tenant mismatch", dimensions: k8sPodDimensions(), reader: &k8sTestReader{found: true, instance: enrich.Instance{TenantID: "other", ModelCode: "cw-K8s_Pod", InstanceID: "new"}}, wantStatus: domain.EnrichStatusPartial},
		{name: "model mismatch", dimensions: k8sPodDimensions(), reader: &k8sTestReader{found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Node", InstanceID: "new"}}, wantStatus: domain.EnrichStatusPartial},
		{name: "empty instance", dimensions: k8sPodDimensions(), reader: &k8sTestReader{found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Pod"}}, wantStatus: domain.EnrichStatusPartial},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := processorBaseTargetAlert(t, test.dimensions)
			model := test.model
			if model == "" {
				model = "cw-K8s_Pod"
			}
			event.Labels[rules.FieldModelID] = domain.NewStringScalar(model)
			event.Labels[rules.FieldModelInstID] = domain.NewStringScalar("existing-pod")
			chain, err := enrich.NewChain([]enrich.Processor{K8s{}}, enrich.Sources{K8s: test.reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			step := payload.Processors[0][rules.K8sProcessor]
			if step.Status != test.wantStatus || len(step.Diagnostics) == 0 {
				t.Fatalf("step=%#v", step)
			}
			for _, patch := range step.Patches {
				target, err := jsonpath.ParseTarget(patch.Path)
				if err != nil {
					t.Fatal(err)
				}
				parts := target.Parts()
				if len(parts) == 2 && parts[0] == "labels" && (parts[1] == rules.FieldModelID || parts[1] == rules.FieldModelInstID) {
					t.Fatalf("failed lookup emitted identity patch: %#v", patch)
				}
			}
			event.Enrich = result.Data
			effective, err := view.EnrichedEvent(event, "warning")
			if err != nil {
				t.Fatal(err)
			}
			if dimensionText(effective.Labels, rules.FieldModelID) != model || dimensionText(effective.Labels, rules.FieldModelInstID) != "existing-pod" {
				t.Fatalf("identity overwritten: %#v", effective.Labels)
			}
		})
	}
}

func TestK8sKnownModelOverridesDimensionGuess(t *testing.T) {
	t.Parallel()
	event := processorBaseTargetAlert(t, k8sPodDimensions())
	// 前序规则覆盖原始模型，K8s 必须读取当前模型而不是原始事实或 Resource 缓存。
	event.Labels[rules.FieldModelID] = domain.NewStringScalar("cw-Host")
	prior, err := NewRules("fields", map[string]any{"rules": []any{map[string]any{
		"id": "cluster", "operations": []any{map[string]any{
			"id": "model", "type": "assign", "assignments": []any{map[string]any{"target": "$.labels.model_id", "value": map[string]any{"literal": "cw-K8s_Cluster"}}},
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	reader := &k8sTestReader{found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Cluster", InstanceID: "cluster-instance"}}
	chain, err := enrich.NewChain([]enrich.Processor{prior, K8s{}}, enrich.Sources{K8s: reader})
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
	if dimensionText(effective.Labels, rules.FieldModelID) != "cw-K8s_Cluster" || dimensionText(effective.Labels, rules.FieldModelInstID) != "cluster-instance" || reader.models[0] != "cw-K8s_Cluster" {
		t.Fatalf("effective=%#v queries=%v", effective.Labels, reader.models)
	}
}

func TestK8sNoModelOrDimensionsSkips(t *testing.T) {
	t.Parallel()
	scope, err := enrich.NewScope(processorBaseTargetAlert(t, domain.DimensionMap{}), enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (K8s{}).Process(t.Context(), scope)
	if err != nil || result.Status != domain.EnrichStatusSkipped || len(result.Value) != 0 || len(result.Patches) != 0 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestK8sQueryCancellationPropagates(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	scope, err := enrich.NewScope(processorBaseTargetAlert(t, k8sPodDimensions()), enrich.Sources{K8s: cancelK8sReader{cancel: cancel}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (K8s{}).Process(ctx, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("query cancellation was hidden: %v", err)
	}
	if matched, err := (K8s{}).Match(ctx, scope); matched || !errors.Is(err, context.Canceled) {
		t.Fatalf("match ignored cancellation: match=%t error=%v", matched, err)
	}
}

func k8sPodDimensions() domain.DimensionMap {
	return domain.DimensionMap{
		rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"),
		rules.FieldNamespace:    domain.NewStringScalar("default"),
		rules.FieldPodName:      domain.NewStringScalar("web-1"),
	}
}

type cancelK8sReader struct{ cancel context.CancelFunc }

func (r cancelK8sReader) FindK8sInstance(context.Context, string, string, domain.DimensionMap) (enrich.Instance, bool, error) {
	r.cancel()
	return enrich.Instance{}, false, context.Canceled
}
