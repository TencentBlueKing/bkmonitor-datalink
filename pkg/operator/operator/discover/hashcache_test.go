// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package discover

import (
	"sort"
	"testing"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
)

func benchmarkHash(b *testing.B, sorted bool) {
	gettlset := func() model.LabelSet {
		return model.LabelSet{
			"__address__":                                   "127.0.0.1:9101",
			"__meta_kubernetes_pod_container_image":         "mirrors.tencent.com/prometheus/node-exporter:v1.0.1",
			"__meta_kubernetes_pod_container_init":          "false",
			"__meta_kubernetes_pod_container_name":          "node-exporter",
			"__meta_kubernetes_pod_container_port_name":     "metrics",
			"__meta_kubernetes_pod_container_port_number":   "9101",
			"__meta_kubernetes_pod_container_port_protocol": "TCP",
		}
	}

	gettglset := func() model.LabelSet {
		return model.LabelSet{
			"__meta_kubernetes_namespace":                                       "bkmonitor-operator",
			"__meta_kubernetes_pod_controller_kind":                             "DaemonSet",
			"__meta_kubernetes_pod_controller_name":                             "bkm-prometheus-node-exporter",
			"__meta_kubernetes_pod_host_ip":                                     "127.0.0.2",
			"__meta_kubernetes_pod_ip":                                          "127.0.0.2",
			"__meta_kubernetes_pod_label_app":                                   "prometheus-node-exporter",
			"__meta_kubernetes_pod_label_chart":                                 "prometheus-node-exporter-1.12.0",
			"__meta_kubernetes_pod_label_controller_revision_hash":              "7c6dfc4b45",
			"__meta_kubernetes_pod_label_heritage":                              "Helm",
			"__meta_kubernetes_pod_label_io_tencent_bcs_clusterid":              "BCS-K8S-00000",
			"__meta_kubernetes_pod_label_io_tencent_bcs_controller_name":        "bkm-prometheus-node-exporter",
			"__meta_kubernetes_pod_label_io_tencent_bcs_controller_type":        "DaemonSet",
			"__meta_kubernetes_pod_label_io_tencent_bcs_namespace":              "bkmonitor-operator",
			"__meta_kubernetes_pod_label_io_tencent_paas_projectid":             "269e76f1b26d4d789044cb162b0a2b70",
			"__meta_kubernetes_pod_label_io_tencent_paas_source_type":           "helm",
			"__meta_kubernetes_pod_label_jobLabel":                              "node-exporter",
			"__meta_kubernetes_pod_label_pod_template_generation":               "11",
			"__meta_kubernetes_pod_label_release":                               "bkmonitor-operator-stack",
			"__meta_kubernetes_pod_labelpresent_app":                            "true",
			"__meta_kubernetes_pod_labelpresent_chart":                          "true",
			"__meta_kubernetes_pod_labelpresent_controller_revision_hash":       "true",
			"__meta_kubernetes_pod_labelpresent_heritage":                       "true",
			"__meta_kubernetes_pod_labelpresent_io_tencent_bcs_clusterid":       "true",
			"__meta_kubernetes_pod_labelpresent_io_tencent_bcs_controller_name": "true",
			"__meta_kubernetes_pod_labelpresent_io_tencent_bcs_controller_type": "true",
			"__meta_kubernetes_pod_labelpresent_io_tencent_bcs_namespace":       "true",
			"__meta_kubernetes_pod_labelpresent_io_tencent_paas_projectid":      "true",
			"__meta_kubernetes_pod_labelpresent_io_tencent_paas_source_type":    "true",
			"__meta_kubernetes_pod_labelpresent_jobLabel":                       "true",
			"__meta_kubernetes_pod_labelpresent_pod_template_generation":        "true",
			"__meta_kubernetes_pod_labelpresent_release":                        "true",
			"__meta_kubernetes_pod_name":                                        "bkm-prometheus-node-exporter-pkxg8",
			"__meta_kubernetes_pod_node_name":                                   "127.0.0.2",
			"__meta_kubernetes_pod_phase":                                       "Running",
			"__meta_kubernetes_pod_ready":                                       "true",
			"__meta_kubernetes_pod_uid":                                         "5fb783ea-8046-4cb9-a76a-481763ba69e3",
		}
	}

	toLabels := func(ls model.LabelSet) labels.Labels {
		lbs := make(labels.Labels, 0, len(ls))
		for k, v := range ls {
			lbs = append(lbs, labels.Label{
				Name:  string(k),
				Value: string(v),
			})
		}
		if sorted {
			sort.Sort(lbs)
		}
		return lbs
	}

	c := &hashCache{}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.hash("blueking", toLabels(gettlset()), toLabels(gettglset()))
		}
	})
}

func BenchmarkHashWithSortedLabels(b *testing.B) {
	benchmarkHash(b, true)
}

func BenchmarkHashWithoutSortedLabels(b *testing.B) {
	benchmarkHash(b, false)
}
