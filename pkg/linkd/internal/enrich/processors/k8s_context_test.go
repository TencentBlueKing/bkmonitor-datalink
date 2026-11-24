// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package processors

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/view"
)

func TestK8sContextIndependentOfLeafIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		change       func(*k8sContextReader)
		wantIdentity string
		wantInvalid  []string
	}{
		{name: "valid", wantIdentity: "container-1"},
		{name: "container missing", change: func(r *k8sContextReader) { delete(r.instances, "cw-K8s_Container") }, wantInvalid: []string{"cw-K8s_Container"}},
		{name: "container error", change: func(r *k8sContextReader) { r.failed = "cw-K8s_Container" }, wantInvalid: []string{"cw-K8s_Container"}},
		{name: "container wrong tenant", change: func(r *k8sContextReader) {
			v := r.instances["cw-K8s_Container"]
			v.TenantID = "other"
			r.instances[v.ModelCode] = v
		}, wantInvalid: []string{"cw-K8s_Container"}},
		{name: "container wrong model", change: func(r *k8sContextReader) {
			v := r.instances["cw-K8s_Container"]
			v.ModelCode = "cw-Host"
			r.instances["cw-K8s_Container"] = v
		}, wantInvalid: []string{"cw-K8s_Container"}},
		{name: "container empty identity", change: func(r *k8sContextReader) {
			v := r.instances["cw-K8s_Container"]
			v.InstanceID = ""
			r.instances[v.ModelCode] = v
		}, wantInvalid: []string{"cw-K8s_Container"}},
		{name: "namespace wrong tenant", wantIdentity: "container-1", change: func(r *k8sContextReader) {
			v := r.instances["cw-K8s_Namespace"]
			v.TenantID = "other"
			v.Attributes = map[string]any{"bk_biz_id": "foreign", "cluster_name": "foreign"}
			r.instances[v.ModelCode] = v
		}, wantInvalid: []string{"cw-K8s_Namespace"}},
		{name: "cluster wrong model", wantIdentity: "container-1", change: func(r *k8sContextReader) {
			v := r.instances["cw-K8s_Cluster"]
			v.ModelCode = "cw-Host"
			v.Attributes = map[string]any{"bk_biz_id": "foreign", "cluster_name": "foreign"}
			r.instances["cw-K8s_Cluster"] = v
		}, wantInvalid: []string{"cw-K8s_Cluster"}},
		{name: "namespace missing", wantIdentity: "container-1", change: func(r *k8sContextReader) { delete(r.instances, "cw-K8s_Namespace") }, wantInvalid: []string{"cw-K8s_Namespace"}},
		{name: "cluster error", wantIdentity: "container-1", change: func(r *k8sContextReader) { r.failed = "cw-K8s_Cluster" }, wantInvalid: []string{"cw-K8s_Cluster"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := newK8sContextReader()
			if test.change != nil {
				test.change(reader)
			}
			event := processorBaseTargetAlert(t, k8sPodDimensions())
			event.Dimensions["container_name"] = domain.NewStringScalar("app")
			event.Labels["model_id"] = domain.NewStringScalar("cw-K8s_Container")
			event.Labels["model_inst_id"] = domain.NewStringScalar("")
			chain, err := enrich.NewChain([]enrich.Processor{K8s{}}, enrich.Sources{K8s: reader})
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
			for key, want := range map[string]string{"cluster_name": "集群一", "namespace": "default", "model_inst_id": test.wantIdentity} {
				got, _ := effective.Labels[key].StringValue()
				if got != want {
					t.Errorf("%s=%q want=%q", key, got, want)
				}
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			step := payload.Processors[0]["k8s"]
			if len(test.wantInvalid) == 0 {
				if step.Status != domain.EnrichStatusSucceeded || len(step.Diagnostics) != 0 {
					t.Fatalf("step=%+v", step)
				}
			} else if step.Status != domain.EnrichStatusPartial || len(step.Diagnostics) != 1 || step.Diagnostics[0].Code != enrich.DiagnosticCodeDependencyInvalid || !reflect.DeepEqual(step.Diagnostics[0].Fields, test.wantInvalid) {
				t.Fatalf("step=%+v", step)
			}
			if len(reader.models) != 3 {
				t.Errorf("independent lookups=%v", reader.models)
			}
			if got, _ := effective.Labels["bk_biz_id"].StringValue(); got != "2" {
				t.Errorf("business=%q", got)
			}
		})
	}
}

func TestK8sClusterNameDoesNotDependOnBusinessOrLeafName(t *testing.T) {
	t.Parallel()
	reader := newK8sContextReader()
	reader.instances["cw-K8s_Namespace"] = enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Namespace", InstanceID: "ns-1", Attributes: map[string]any{"bk_biz_id": "2", "name": "default"}}
	reader.instances["cw-K8s_Cluster"] = enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Cluster", InstanceID: "cluster-1", Attributes: map[string]any{"name": "集群一"}}
	event := processorBaseTargetAlert(t, k8sPodDimensions())
	event.Dimensions["container_name"] = domain.NewStringScalar("app")
	scope, err := enrich.NewScope(event, enrich.Sources{K8s: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (K8s{}).Process(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Value["cluster_name"]) != `"集群一"` || string(result.Value["bk_biz_id"]) != `"2"` {
		t.Errorf("values=%v", result.Value)
	}
}

func TestK8sContextCancellationPropagates(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"cw-K8s_Namespace", "cw-K8s_Cluster"} {
		t.Run(model, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reader := newK8sContextReader()
			reader.cancelModel = model
			reader.cancel = cancel
			event := processorBaseTargetAlert(t, k8sPodDimensions())
			event.Dimensions["container_name"] = domain.NewStringScalar("app")
			scope, err := enrich.NewScope(event, enrich.Sources{K8s: reader})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := (K8s{}).Process(ctx, scope); !errors.Is(err, context.Canceled) {
				t.Errorf("cancellation=%v", err)
			}
		})
	}
}

type k8sContextReader struct {
	instances   map[string]enrich.Instance
	models      []string
	failed      string
	cancelModel string
	cancel      context.CancelFunc
}

func newK8sContextReader() *k8sContextReader {
	r := &k8sContextReader{instances: map[string]enrich.Instance{}}
	for model, id := range map[string]string{"cw-K8s_Container": "container-1", "cw-K8s_Namespace": "namespace-1", "cw-K8s_Cluster": "cluster-1"} {
		r.instances[model] = enrich.Instance{TenantID: "tenant-a", ModelCode: model, InstanceID: id, Attributes: map[string]any{"bk_biz_id": "2", "cluster_name": "集群一", "name": "app"}}
	}
	return r
}
func (r *k8sContextReader) FindK8sInstance(_ context.Context, _ string, model string, _ domain.DimensionMap) (enrich.Instance, bool, error) {
	r.models = append(r.models, model)
	if model == r.cancelModel {
		r.cancel()
		return enrich.Instance{}, false, context.Canceled
	}
	if model == r.failed {
		return enrich.Instance{}, false, errors.New("OneModel unavailable")
	}
	v, found := r.instances[model]
	return v, found, nil
}
