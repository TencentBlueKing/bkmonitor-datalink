// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"bytes"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/logging"
)

func TestK8sDATAFallbackSuccessStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		change       func(*k8sContextReader, *dataSliceReader)
		groups       bool
		monitor, k8s domain.EnrichStatus
		unresolved   string
		warnings     int
	}{
		{name: "complete fallback", monitor: domain.EnrichStatusSucceeded, k8s: domain.EnrichStatusSucceeded},
		{name: "namespace missing", change: func(k *k8sContextReader, _ *dataSliceReader) { delete(k.instances, "cw-K8s_Namespace") }, monitor: domain.EnrichStatusSucceeded, k8s: domain.EnrichStatusPartial, unresolved: "instance_not_found", warnings: 1},
		{name: "cluster error", change: func(k *k8sContextReader, _ *dataSliceReader) { k.failed = "cw-K8s_Cluster" }, monitor: domain.EnrichStatusSucceeded, k8s: domain.EnrichStatusPartial, unresolved: "query_error", warnings: 1},
		{name: "model metadata missing", change: func(_ *k8sContextReader, r *dataSliceReader) { r.modelMissing = true }, monitor: domain.EnrichStatusPartial, k8s: domain.EnrichStatusSucceeded, unresolved: "model_not_found", warnings: 1},
		{name: "dynamic group error", groups: true, monitor: domain.EnrichStatusPartial, k8s: domain.EnrichStatusSucceeded, unresolved: "dynamic_group", warnings: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := newK8sContextReader()
			delete(k.instances, "cw-K8s_Container")
			r := &dataSliceReader{model: "cw-K8s_Container"}
			if tc.change != nil {
				tc.change(k, r)
			}
			event := processorBaseTargetAlert(t, k8sPodDimensions())
			event.Dimensions["container_name"] = domain.NewStringScalar("app")
			original := event.Clone()
			sources := enrich.Sources{CWStrategy: r, Model: r, Metric: r, OneModel: r, K8s: k}
			if tc.groups {
				sources.DynamicGroup = failingGroups{}
			}
			var logs bytes.Buffer
			logger, err := logging.New(logging.DefaultConfig(), &logs)
			if err != nil {
				t.Fatal(err)
			}
			chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, K8s{}}, sources, enrich.WithLogger(logger))
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
			unresolved := false
			for _, entry := range payload.Processors {
				for name, step := range entry {
					want := tc.monitor
					if name == "k8s" {
						want = tc.k8s
					}
					if step.Status != want {
						t.Errorf("%s status=%s want=%s", name, step.Status, want)
					}
					successes := 0
					for _, d := range step.Diagnostics {
						if string(d.Code) == "fallback_succeeded" {
							successes++
							if d.ReasonCode != "instance_not_found" || d.ModelID != "cw-K8s_Container" {
								t.Errorf("fallback=%+v", d)
							}
						}
						if string(d.Code) != "fallback_succeeded" && (d.ReasonCode == tc.unresolved || d.Dependency == tc.unresolved) {
							unresolved = true
						}
					}
					if successes != 1 {
						t.Errorf("%s expected one fallback success, got=%+v", name, step.Diagnostics)
					}
				}
			}
			if tc.unresolved != "" && !unresolved {
				t.Fatal("unresolved dependency was hidden")
			}
			if bytes.Count(logs.Bytes(), []byte("\n")) != tc.warnings {
				t.Errorf("unexpected WARN count: %s", logs.String())
			}
			if !reflect.DeepEqual(event, original) {
				t.Fatal("source Event changed")
			}
		})
	}
}

func TestK8sDATAFallbackSuccessReusesSnapshot(t *testing.T) {
	t.Parallel()
	for _, reuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "reuse disabled", true: "reuse enabled"}[reuse], func(t *testing.T) {
			k := newK8sContextReader()
			delete(k.instances, "cw-K8s_Container")
			r := &dataSliceReader{model: "cw-K8s_Container"}
			options := []enrich.ChainOption{}
			if reuse {
				options = append(options, enrich.WithReuseOnActive("monitor-resource", "k8s"))
			}
			chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, K8s{}}, enrich.Sources{CWStrategy: r, Model: r, Metric: r, OneModel: r, K8s: k}, options...)
			if err != nil {
				t.Fatal(err)
			}
			event := processorBaseTargetAlert(t, k8sPodDimensions())
			event.Dimensions["container_name"] = domain.NewStringScalar("app")
			first, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
			if err != nil {
				t.Fatal(err)
			}
			if first.Status != domain.EnrichStatusSucceeded {
				t.Fatalf("first status=%s", first.Status)
			}
			active := domain.Alert{AlertID: "fallback-opening", BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID, Fingerprint: event.Fingerprint, Status: domain.AlertStatusActive, EnrichSeverity: "warning", Enrich: first.Data.Evaluations[0].Data.Clone()}
			// 开启复用后，即使后端随后补齐实例，本生命周期仍沿用已成功的自然键与诊断。
			k.instances["cw-K8s_Container"] = enrich.Instance{TenantID: event.BKTenantID, ModelCode: "cw-K8s_Container", InstanceID: "new-backend-id"}
			k.models = nil
			event.EventID = "next-fallback-event"
			second, err := chain.Enrich(t.Context(), enrich.Input{Event: event, ActiveAlert: &active})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(second.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range payload.Processors {
				for name, step := range entry {
					if step.Status != domain.EnrichStatusSucceeded {
						t.Errorf("%s=%+v", name, step)
					}
					if reuse {
						if step.ReusedFromAlertID != active.AlertID || len(step.Diagnostics) != 1 || string(step.Diagnostics[0].Code) != "fallback_succeeded" {
							t.Errorf("reused %s=%+v", name, step)
						}
					} else if step.ReusedFromAlertID != "" || len(step.Diagnostics) != 0 {
						t.Errorf("fresh %s=%+v", name, step)
					}
				}
			}
			if reuse && len(k.models) != 0 {
				t.Fatalf("reuse queried backend: %v", k.models)
			}
			if !reuse && len(k.models) == 0 {
				t.Fatal("disabled reuse did not query backend")
			}
			if _, err := chain.Enrich(t.Context(), enrich.Input{Event: event, ActiveAlert: &active, Preview: true}); err != nil {
				t.Fatal(err)
			}
			if len(k.models) == 0 {
				t.Fatal("preview unexpectedly reused snapshot")
			}
		})
	}
}
