// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// hostCache places a record on the host its bk_host_id names and answers
// that host's business. Which host admission's fullers place a record on is
// tested where they run, in cmdbcache.
type hostCache map[string]string

func (cache hostCache) PlaceHostBusiness(dimensions map[string]json.RawMessage) (string, bool) {
	business, found := cache[dimensionText(dimensions, "bk_host_id")]
	return business, found
}

func attributionDimensions(values map[string]any) map[string]json.RawMessage {
	raw := make(map[string]json.RawMessage, len(values))
	for name, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		raw[name] = encoded
	}
	return raw
}

func hostTarget() *contract.TargetPlanV1 {
	return &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{"101"}}
}

const planBusiness = "524"

// The three sources, each taken only when the one before it is not
// configured or does not answer for this record.
func TestAttributionFallsThroughTargetThenDimensionThenGlobal(t *testing.T) {
	hosts := hostCache{"101": "11"}
	byBusiness := []string{"bk_biz_id", "bk_host_id"}
	for name, test := range map[string]struct {
		target     *contract.TargetPlanV1
		dimensions []string
		record     map[string]any
		want       BusinessAttribution
	}{
		"the target answers": {
			target: hostTarget(), dimensions: byBusiness,
			record: map[string]any{"bk_host_id": "101", "bk_biz_id": "22"},
			want:   BusinessAttribution{BusinessID: "11", Source: contract.BusinessAttributionTarget},
		},
		"the host is not in the cache, so the dimension answers": {
			target: hostTarget(), dimensions: byBusiness,
			record: map[string]any{"bk_host_id": "999", "bk_biz_id": "22"},
			want:   BusinessAttribution{BusinessID: "22", Source: contract.BusinessAttributionDimension},
		},
		"neither answers, so the global business": {
			target: hostTarget(), dimensions: []string{"bk_host_id"},
			record: map[string]any{"bk_host_id": "999"},
			want:   BusinessAttribution{BusinessID: planBusiness, Source: contract.BusinessAttributionGlobal},
		},
		"no target, grouped by business": {
			dimensions: []string{"bk_biz_id"}, record: map[string]any{"bk_biz_id": 33},
			want: BusinessAttribution{BusinessID: "33", Source: contract.BusinessAttributionDimension},
		},
		"no target and not grouped by business: aggregated across businesses": {
			dimensions: []string{"region"}, record: map[string]any{"region": "south", "bk_biz_id": "33"},
			want: BusinessAttribution{BusinessID: planBusiness, Source: contract.BusinessAttributionGlobal},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := AttributeBusiness(test.target, test.dimensions, planBusiness, attributionDimensions(test.record), BusinessLookups{Hosts: hosts})
			if got != test.want {
				t.Fatalf("AttributeBusiness() = %+v, want %+v", got, test.want)
			}
		})
	}
}

// A strategy that configures both a target and a business dimension files
// the event under the target's business, whatever the dimension says.
func TestTheTargetIsTakenBeforeTheBusinessDimension(t *testing.T) {
	got := AttributeBusiness(hostTarget(), []string{"bk_biz_id", "bk_host_id"}, planBusiness,
		attributionDimensions(map[string]any{"bk_host_id": 101, "bk_biz_id": 22}), BusinessLookups{Hosts: hostCache{"101": "11"}})
	if got.BusinessID != "11" || got.Source != contract.BusinessAttributionTarget {
		t.Fatalf("AttributeBusiness() = %+v, want the host's business 11 from the target", got)
	}
}

// A model_inst_id plan over hosts reads the record's host exactly as a
// host_id plan does.
func TestAHostMemberPlanIsAttributedByTheHost(t *testing.T) {
	target := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
		Identity:      contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true},
		StaticKeys:    []string{},
		StaticMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "101"}}}
	got := AttributeBusiness(target, nil, planBusiness, attributionDimensions(map[string]any{"bk_host_id": "101"}), BusinessLookups{Hosts: hostCache{"101": "11"}})
	if got.BusinessID != "11" || got.Source != contract.BusinessAttributionTarget {
		t.Fatalf("AttributeBusiness() = %+v, want the host's business 11", got)
	}
}

// Each Kubernetes rule takes the business configured on the static target
// the record's key matches; a matched target that carries none falls
// through.
func TestEachKubernetesRuleTakesTheMatchedStaticTargetsBusiness(t *testing.T) {
	for name, test := range map[string]struct {
		target *contract.TargetPlanV1
		record map[string]any
	}{
		"k8s cluster": {
			target: &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-K8s_Cluster", Rule: contract.TargetPlanRuleK8sCluster,
				Identity:         contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id"}},
				StaticKeys:       []string{"cluster-a", "cluster-b"},
				StaticBusinesses: map[string]string{"cluster-a": "41"}},
			record: map[string]any{"bcs_cluster_id": "cluster-a", "pod": "web-1"},
		},
		"k8s node": {
			target: &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-K8s_Node", Rule: contract.TargetPlanRuleK8sNode,
				Identity:         contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id", "node"}},
				StaticKeys:       []string{"cluster-a|node-01", "cluster-b|node-01"},
				StaticBusinesses: map[string]string{"cluster-a|node-01": "41"}},
			record: map[string]any{"bcs_cluster_id": "cluster-a", "node": "node-01"},
		},
		"k8s workload": {
			target: &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-K8s_Workload", Rule: contract.TargetPlanRuleK8sWorkload,
				Identity:         contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id", "namespace", "workload_kind", "workload_name"}},
				StaticKeys:       []string{"cluster-a|prod|Deployment|web", "cluster-b|prod|Deployment|web"},
				StaticBusinesses: map[string]string{"cluster-a|prod|Deployment|web": "41"}},
			record: map[string]any{"bcs_cluster_id": "cluster-a", "namespace": "prod", "workload_kind": "Deployment", "workload_name": "web"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := AttributeBusiness(test.target, []string{"bk_biz_id"}, planBusiness, attributionDimensions(test.record), BusinessLookups{})
			if got.BusinessID != "41" || got.Source != contract.BusinessAttributionTarget {
				t.Fatalf("AttributeBusiness() = %+v, want the matched static target's business 41", got)
			}
			other := map[string]any{"bk_biz_id": "22"}
			for dimension, value := range test.record {
				other[dimension] = value
			}
			other["bcs_cluster_id"] = "cluster-b"
			got = AttributeBusiness(test.target, []string{"bk_biz_id"}, planBusiness, attributionDimensions(other), BusinessLookups{})
			if got.BusinessID != "22" || got.Source != contract.BusinessAttributionDimension {
				t.Fatalf("a target with no business configured: AttributeBusiness() = %+v, want the dimension's 22", got)
			}
		})
	}
}

// A no-data event on a Kubernetes roster group is built from the group's
// dimensions and the no-data tag. It is attributed through the same static
// target, so an absence and a threshold on one target file under one
// business.
func TestAKubernetesNoDataGroupTakesItsStaticTargetsBusiness(t *testing.T) {
	target := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-K8s_Workload", Rule: contract.TargetPlanRuleK8sWorkload,
		Identity:         contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id", "namespace", "workload_kind", "workload_name"}},
		StaticKeys:       []string{"cluster-a|prod|Deployment|web"},
		StaticBusinesses: map[string]string{"cluster-a|prod|Deployment|web": "41"}}
	group, ok := target.Identity.Group("cluster-a|prod|Deployment|web")
	if !ok {
		t.Fatal("the roster key does not split into its group")
	}
	record := map[string]any{contract.NoDataDimensionTag: "1"}
	for dimension, value := range group {
		record[dimension] = value
	}
	got := AttributeBusiness(target, target.Identity.RosterDimensions(), planBusiness, attributionDimensions(record), BusinessLookups{})
	if got.BusinessID != "41" || got.Source != contract.BusinessAttributionTarget {
		t.Fatalf("AttributeBusiness() = %+v, want the static target's business 41", got)
	}
}

// Zero is the platform's "no business" and a negative id is a space that is
// not a CMDB business; neither becomes an alert's business label, from the
// data or from the host cache.
func TestABusinessThatIsNotPositiveFallsThrough(t *testing.T) {
	for _, value := range []any{0, "0", -3, "biz", ""} {
		got := AttributeBusiness(nil, []string{"bk_biz_id"}, planBusiness, attributionDimensions(map[string]any{"bk_biz_id": value}), BusinessLookups{})
		if got.Source != contract.BusinessAttributionGlobal || got.BusinessID != planBusiness {
			t.Fatalf("bk_biz_id=%v: AttributeBusiness() = %+v, want the global business", value, got)
		}
	}
	got := AttributeBusiness(hostTarget(), nil, planBusiness, attributionDimensions(map[string]any{"bk_host_id": "101"}), BusinessLookups{Hosts: hostCache{"101": "0"}})
	if got.Source != contract.BusinessAttributionGlobal {
		t.Fatalf("a host held under business 0: AttributeBusiness() = %+v, want the global business", got)
	}
}

// An object-model target names no business, and a missing host cache
// answers for no host: both fall through rather than guess.
func TestATargetThatNamesNoBusinessFallsThrough(t *testing.T) {
	object := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-MySQL", Rule: contract.TargetPlanRuleModelInstID,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"cw_object_model_code", "cw_object_model_inst_id"}}, StaticKeys: []string{"cw-MySQL|mysql-01"}}
	got := AttributeBusiness(object, []string{"bk_biz_id"}, planBusiness,
		attributionDimensions(map[string]any{"cw_object_model_code": "cw-MySQL", "cw_object_model_inst_id": "mysql-01", "bk_biz_id": "22"}), BusinessLookups{Hosts: hostCache{}})
	if got.BusinessID != "22" || got.Source != contract.BusinessAttributionDimension {
		t.Fatalf("object target: AttributeBusiness() = %+v, want the dimension's 22", got)
	}
	got = AttributeBusiness(hostTarget(), nil, planBusiness, attributionDimensions(map[string]any{"bk_host_id": "101"}), BusinessLookups{})
	if got.Source != contract.BusinessAttributionGlobal {
		t.Fatalf("no host cache: AttributeBusiness() = %+v, want the global business", got)
	}
}

// clusterMapping is the published BCS cluster -> business mapping.
type clusterMapping map[string]string

func (mapping clusterMapping) LookupClusterBusiness(cluster string) (string, bool) {
	business, found := mapping[cluster]
	return business, found
}

// Kubernetes data that names no business is attributed through the business
// the platform published for its cluster, after the target and the business
// dimension and before the global business. A cluster the mapping does not
// hold is the global business, counted apart as unmapped.
func TestKubernetesDataIsAttributedThroughItsCluster(t *testing.T) {
	clusters := clusterMapping{"BCS-K8S-00001": "11", "BCS-K8S-00003": "0"}
	byCluster := []string{"bcs_cluster_id", "namespace"}
	for name, test := range map[string]struct {
		dimensions []string
		record     map[string]any
		lookups    BusinessLookups
		want       BusinessAttribution
	}{
		"a mapped cluster": {
			dimensions: byCluster, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "prod"},
			lookups: BusinessLookups{Clusters: clusters},
			want:    BusinessAttribution{BusinessID: "11", Source: contract.BusinessAttributionCluster},
		},
		"a cluster the mapping does not hold": {
			dimensions: byCluster, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00002", "namespace": "prod"},
			lookups: BusinessLookups{Clusters: clusters},
			want:    BusinessAttribution{BusinessID: planBusiness, Source: contract.BusinessAttributionUnmapped},
		},
		"a cluster published under no positive business": {
			dimensions: byCluster, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00003"},
			lookups: BusinessLookups{Clusters: clusters},
			want:    BusinessAttribution{BusinessID: planBusiness, Source: contract.BusinessAttributionUnmapped},
		},
		"no mapping at all": {
			dimensions: byCluster, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00001"},
			want: BusinessAttribution{BusinessID: planBusiness, Source: contract.BusinessAttributionUnmapped},
		},
		"the business dimension first": {
			dimensions: []string{"bcs_cluster_id", "bk_biz_id"}, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00001", "bk_biz_id": "22"},
			lookups: BusinessLookups{Clusters: clusters},
			want:    BusinessAttribution{BusinessID: "22", Source: contract.BusinessAttributionDimension},
		},
		"not grouped by cluster": {
			dimensions: []string{"namespace"}, record: map[string]any{"namespace": "prod", "bcs_cluster_id": "BCS-K8S-00001"},
			lookups: BusinessLookups{Clusters: clusters},
			want:    BusinessAttribution{BusinessID: planBusiness, Source: contract.BusinessAttributionGlobal},
		},
		"grouped by cluster, the record names none": {
			dimensions: byCluster, record: map[string]any{"namespace": "prod"},
			lookups: BusinessLookups{Clusters: clusters},
			want:    BusinessAttribution{BusinessID: planBusiness, Source: contract.BusinessAttributionGlobal},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := AttributeBusiness(nil, test.dimensions, planBusiness, attributionDimensions(test.record), test.lookups)
			if got != test.want {
				t.Fatalf("AttributeBusiness() = %+v, want %+v", got, test.want)
			}
		})
	}
}

// A Kubernetes static target that carries a business is taken before the
// cluster mapping; one that carries none falls through to it.
func TestAKubernetesTargetIsTakenBeforeTheClusterMapping(t *testing.T) {
	target := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-K8s_Cluster", Rule: contract.TargetPlanRuleK8sCluster,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id"}}, StaticKeys: []string{"BCS-K8S-00001", "BCS-K8S-00002"},
		StaticBusinesses: map[string]string{"BCS-K8S-00001": "41"}}
	lookups := BusinessLookups{Clusters: clusterMapping{"BCS-K8S-00001": "11", "BCS-K8S-00002": "12"}}
	got := AttributeBusiness(target, []string{"bcs_cluster_id"}, planBusiness, attributionDimensions(map[string]any{"bcs_cluster_id": "BCS-K8S-00001"}), lookups)
	if got.BusinessID != "41" || got.Source != contract.BusinessAttributionTarget {
		t.Fatalf("AttributeBusiness() = %+v, want the static target's 41", got)
	}
	got = AttributeBusiness(target, []string{"bcs_cluster_id"}, planBusiness, attributionDimensions(map[string]any{"bcs_cluster_id": "BCS-K8S-00002"}), lookups)
	if got.BusinessID != "12" || got.Source != contract.BusinessAttributionCluster {
		t.Fatalf("AttributeBusiness() = %+v, want the mapped cluster's 12", got)
	}
}

// namespaceMapping is the published BCS cluster + namespace -> business
// mapping, keyed "cluster|namespace".
type namespaceMapping map[string]string

func (mapping namespaceMapping) LookupNamespaceBusiness(cluster, namespace string) (string, bool) {
	business, found := mapping[cluster+"|"+namespace]
	return business, found
}

// A namespace's business comes before its cluster's, as the alert pipeline
// orders them, when the strategy groups by both; a namespace the mapping
// does not hold falls back to its cluster, and a cluster neither mapping
// holds is unmapped.
func TestANamespaceIsAttributedBeforeItsCluster(t *testing.T) {
	lookups := BusinessLookups{
		Clusters:   clusterMapping{"BCS-K8S-00001": "11"},
		Namespaces: namespaceMapping{"BCS-K8S-00001|prod": "21", "BCS-K8S-00001|zero": "0"},
	}
	byNamespace := []string{"bcs_cluster_id", "namespace"}
	for name, test := range map[string]struct {
		dimensions []string
		record     map[string]any
		want       BusinessAttribution
	}{
		"a mapped namespace": {
			dimensions: byNamespace, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "prod"},
			want: BusinessAttribution{BusinessID: "21", Source: contract.BusinessAttributionNamespace},
		},
		"a namespace the mapping does not hold falls back to its cluster": {
			dimensions: byNamespace, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "dev"},
			want: BusinessAttribution{BusinessID: "11", Source: contract.BusinessAttributionCluster},
		},
		"a namespace published under no positive business falls back to its cluster": {
			dimensions: byNamespace, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "zero"},
			want: BusinessAttribution{BusinessID: "11", Source: contract.BusinessAttributionCluster},
		},
		"neither mapping holds it": {
			dimensions: byNamespace, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00009", "namespace": "prod"},
			want: BusinessAttribution{BusinessID: planBusiness, Source: contract.BusinessAttributionUnmapped},
		},
		"not grouped by namespace: the cluster alone": {
			dimensions: []string{"bcs_cluster_id"}, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "prod"},
			want: BusinessAttribution{BusinessID: "11", Source: contract.BusinessAttributionCluster},
		},
		"grouped by namespace, the record names none: the cluster": {
			dimensions: byNamespace, record: map[string]any{"bcs_cluster_id": "BCS-K8S-00001"},
			want: BusinessAttribution{BusinessID: "11", Source: contract.BusinessAttributionCluster},
		},
		"the business dimension first": {
			dimensions: []string{"bcs_cluster_id", "namespace", "bk_biz_id"},
			record:     map[string]any{"bcs_cluster_id": "BCS-K8S-00001", "namespace": "prod", "bk_biz_id": "31"},
			want:       BusinessAttribution{BusinessID: "31", Source: contract.BusinessAttributionDimension},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := AttributeBusiness(nil, test.dimensions, planBusiness, attributionDimensions(test.record), lookups)
			if got != test.want {
				t.Fatalf("AttributeBusiness() = %+v, want %+v", got, test.want)
			}
		})
	}
}
