// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

func TestK8sDoesNotOverwriteExplicitResourceIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, model, id  string
		conflictingModel bool
		want             domain.EnrichStatus
	}{
		{name: "matching identity", model: "cw-K8s_Namespace", id: "cluster-1|default", want: domain.EnrichStatusSucceeded},
		{name: "native dimensions identify another instance", model: "cw-K8s_Namespace", id: "other-namespace", want: domain.EnrichStatusFailed},
		{name: "conflicting model locations", model: "cw-K8s_Cluster", id: "cluster-1", conflictingModel: true, want: domain.EnrichStatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			event := processorBaseTargetAlert(t, domain.DimensionMap{"bcs_cluster_id": domain.NewStringScalar("cluster-1"), "namespace": domain.NewStringScalar("default")})
			event.Labels["model_id"] = domain.NewStringScalar(tc.model)
			event.Labels["model_inst_id"] = domain.NewStringScalar(tc.id)
			if tc.conflictingModel {
				event.Dimensions["model_id"] = domain.NewStringScalar("cw-K8s_Namespace")
				event.Dimensions["model_inst_id"] = domain.NewStringScalar("cluster-1|default")
			}
			before := event.Clone()
			scope, err := enrich.NewScope(event, enrich.Sources{K8s: &k8sTestReader{contexts: k8sTestContexts()}})
			if err != nil {
				t.Fatal(err)
			}
			matched, err := (K8s{}).Match(t.Context(), scope)
			if err != nil || !matched {
				t.Fatalf("matching=%v err=%v", matched, err)
			}
			result, err := (K8s{}).Process(t.Context(), scope)
			if err != nil || result.Status != tc.want {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if tc.want == domain.EnrichStatusFailed && (len(result.Value) != 0 || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "identity_conflict") {
				t.Fatalf("conflicting identity published: %+v", result)
			}
			if !reflect.DeepEqual(event, before) {
				t.Fatal("source event changed")
			}
		})
	}
}
