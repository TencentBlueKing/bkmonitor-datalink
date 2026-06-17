// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package hpa

import (
	"bytes"
	"strings"
	"testing"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func i32(v int32) *int32 { return &v }

func TestWriteMetrics(t *testing.T) {
	minR := i32(2)
	hpas := []*autoscalingv2.HorizontalPodAutoscaler{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  "ns1",
				Name:       "h1",
				Generation: 3,
				Labels:     map[string]string{"app": "web", "app.kubernetes.io/name": "svc", "1tier": "x"},
			},
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				MaxReplicas: 5,
				MinReplicas: minR,
			},
			Status: autoscalingv2.HorizontalPodAutoscalerStatus{
				CurrentReplicas: 4,
				DesiredReplicas: 5,
				Conditions: []autoscalingv2.HorizontalPodAutoscalerCondition{
					{Type: "AbleToScale", Status: corev1.ConditionTrue},
				},
			},
		},
		{
			// No MinReplicas → kube_hpa_spec_min_replicas must be skipped for it.
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns0", Name: "h0"},
			Spec:       autoscalingv2.HorizontalPodAutoscalerSpec{MaxReplicas: 1},
		},
	}

	var buf bytes.Buffer
	if err := writeMetrics(&buf, hpas); err != nil {
		t.Fatalf("writeMetrics: %v", err)
	}
	out := buf.String()

	mustContain := []string{
		"# HELP kube_hpa_spec_max_replicas Upper limit",
		"# TYPE kube_hpa_spec_max_replicas gauge",
		`kube_hpa_metadata_generation{namespace="ns1",hpa="h1"} 3`,
		`kube_hpa_spec_max_replicas{namespace="ns1",hpa="h1"} 5`,
		`kube_hpa_spec_min_replicas{namespace="ns1",hpa="h1"} 2`,
		`kube_hpa_status_current_replicas{namespace="ns1",hpa="h1"} 4`,
		`kube_hpa_status_desired_replicas{namespace="ns1",hpa="h1"} 5`,
		`kube_hpa_labels{namespace="ns1",hpa="h1",label_1tier="x",label_app="web",label_app_kubernetes_io_name="svc"} 1`,
		`kube_hpa_status_condition{namespace="ns1",hpa="h1",condition="AbleToScale",status="true"} 1`,
		`kube_hpa_status_condition{namespace="ns1",hpa="h1",condition="AbleToScale",status="false"} 0`,
		`kube_hpa_status_condition{namespace="ns1",hpa="h1",condition="AbleToScale",status="unknown"} 0`,
		`kube_hpa_spec_max_replicas{namespace="ns0",hpa="h0"} 1`,
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("output missing line:\n%s\n--- full output ---\n%s", want, out)
		}
	}

	// h0 has no MinReplicas → its min_replicas series must be absent.
	if strings.Contains(out, `kube_hpa_spec_min_replicas{namespace="ns0",hpa="h0"}`) {
		t.Errorf("kube_hpa_spec_min_replicas should be skipped when MinReplicas is nil")
	}

	// ns0 sorts before ns1.
	if strings.Index(out, `hpa="h0"`) > strings.Index(out, `hpa="h1"`) {
		t.Errorf("expected ns0/h0 series to render before ns1/h1")
	}
}

func TestSanitizeLabelName(t *testing.T) {
	// Must match kube-state-metrics v1.9.7: regexp [^a-zA-Z0-9_] -> "_", with no
	// leading-digit special-casing (the "label_" prefix keeps the name valid).
	cases := map[string]string{
		"app":                    "app",
		"app.kubernetes.io/name": "app_kubernetes_io_name",
		"1bad":                   "1bad",
		"9":                      "9",
		"0_x":                    "0_x",
		"a-b.c":                  "a_b_c",
	}
	for in, want := range cases {
		if got := sanitizeLabelName(in); got != want {
			t.Errorf("sanitizeLabelName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestEscapeLabelValue(t *testing.T) {
	if got := escapeLabelValue(`a"b\c` + "\n"); got != `a\"b\\c\n` {
		t.Errorf("escapeLabelValue mismatch: %q", got)
	}
	if got := escapeLabelValue("plain"); got != "plain" {
		t.Errorf("escapeLabelValue should pass through plain value, got %q", got)
	}
}
