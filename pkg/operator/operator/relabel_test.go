// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"testing"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/config"
)

func TestServiceMonitorRelabel(t *testing.T) {
	_ = config.InitConfig()
	m := &promv1.ServiceMonitor{
		TypeMeta:   metav1.TypeMeta{Kind: "serviceMonitor"},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "testnamespace"},
		Spec: promv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"testa": "a",
					"testb": "b",
				},
			},
			TargetLabels:    []string{"a", "b", "c"},
			PodTargetLabels: []string{"e", "f", "g"},
			JobLabel:        "job",
		},
	}
	ep := &promv1.Endpoint{
		Port: "http",
		RelabelConfigs: []*promv1.RelabelConfig{
			{
				SourceLabels: []promv1.LabelName{"from"},
				TargetLabel:  "to",
			},
		},
	}

	content := "- source_labels:\n  - job\n  target_label: monitor_type\n  regex: (.+?)/.*\n  replacement: ${1}\n- action: keep\n  source_labels:\n  - __meta_kubernetes_service_label_testa\n  regex: a\n- action: keep\n  source_labels:\n  - __meta_kubernetes_service_label_testb\n  regex: b\n- action: keep\n  source_labels:\n  - __meta_kubernetes_endpoint_port_name\n  regex: http\n- source_labels:\n  - __meta_kubernetes_endpoint_address_target_kind\n  - __meta_kubernetes_endpoint_address_target_name\n  separator: ;\n  regex: Node;(.*)\n  replacement: ${1}\n  target_label: node\n- source_labels:\n  - __meta_kubernetes_endpoint_address_target_kind\n  - __meta_kubernetes_endpoint_address_target_name\n  separator: ;\n  regex: Pod;(.*)\n  replacement: ${1}\n  target_label: pod\n- source_labels:\n  - __meta_kubernetes_namespace\n  target_label: namespace\n- source_labels:\n  - __meta_kubernetes_service_name\n  target_label: service\n- source_labels:\n  - __meta_kubernetes_pod_name\n  target_label: pod\n- source_labels:\n  - __meta_kubernetes_pod_container_name\n  target_label: container\n- source_labels:\n  - __meta_kubernetes_service_label_a\n  target_label: a\n  regex: (.+)\n  replacement: ${1}\n- source_labels:\n  - __meta_kubernetes_service_label_b\n  target_label: b\n  regex: (.+)\n  replacement: ${1}\n- source_labels:\n  - __meta_kubernetes_service_label_c\n  target_label: c\n  regex: (.+)\n  replacement: ${1}\n- source_labels:\n  - __meta_kubernetes_pod_label_e\n  target_label: e\n  regex: (.+)\n  replacement: ${1}\n- source_labels:\n  - __meta_kubernetes_pod_label_f\n  target_label: f\n  regex: (.+)\n  replacement: ${1}\n- source_labels:\n  - __meta_kubernetes_pod_label_g\n  target_label: g\n  regex: (.+)\n  replacement: ${1}\n- source_labels:\n  - __meta_kubernetes_service_name\n  target_label: job\n  replacement: ${1}\n- source_labels:\n  - __meta_kubernetes_service_label_job\n  target_label: job\n  regex: (.+)\n  replacement: ${1}\n- target_label: endpoint\n  replacement: http\n- source_labels:\n  - from\n  target_label: to\n"
	yamlSlice := getServiceMonitorRelabels(m, ep)
	data, err := yaml.Marshal(yamlSlice)
	assert.Nil(t, err)
	assert.Equal(t, content, string(data))

	_, err = yamlToRelabels(yamlSlice)
	assert.Nil(t, err)
}

func TestPodMonitorRelabel(t *testing.T) {
	m := &promv1.PodMonitor{
		TypeMeta:   metav1.TypeMeta{Kind: "podMonitor"},
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "testnamespace"},
		Spec: promv1.PodMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"testa": "a",
					"testb": "b",
				},
			},
			PodTargetLabels: []string{"e", "f", "g"},
			JobLabel:        "job",
		},
	}
	ep := &promv1.PodMetricsEndpoint{
		Port: "http",
	}

	content := "- source_labels:\n  - job\n  target_label: monitor_type\n  regex: (.+?)/.*\n  replacement: ${1}\n- action: keep\n  source_labels:\n  - __meta_kubernetes_pod_label_testa\n  regex: a\n- action: keep\n  source_labels:\n  - __meta_kubernetes_pod_label_testb\n  regex: b\n- action: keep\n  source_labels:\n  - __meta_kubernetes_pod_container_port_name\n  regex: http\n- source_labels:\n  - __meta_kubernetes_namespace\n  target_label: namespace\n- source_labels:\n  - __meta_kubernetes_pod_container_name\n  target_label: container\n- source_labels:\n  - __meta_kubernetes_pod_name\n  target_label: pod\n- source_labels:\n  - __meta_kubernetes_pod_label_e\n  target_label: e\n  regex: (.+)\n  replacement: ${1}\n- source_labels:\n  - __meta_kubernetes_pod_label_f\n  target_label: f\n  regex: (.+)\n  replacement: ${1}\n- source_labels:\n  - __meta_kubernetes_pod_label_g\n  target_label: g\n  regex: (.+)\n  replacement: ${1}\n- target_label: job\n  replacement: testnamespace/test\n- source_labels:\n  - __meta_kubernetes_pod_label_job\n  target_label: job\n  regex: (.+)\n  replacement: ${1}\n- target_label: endpoint\n  replacement: http\n"
	yamlSlice := getPodMonitorRelabels(m, ep)
	data, err := yaml.Marshal(yamlSlice)
	assert.Nil(t, err)
	assert.Equal(t, content, string(data))

	_, err = yamlToRelabels(yamlSlice)
	assert.Nil(t, err)
}
