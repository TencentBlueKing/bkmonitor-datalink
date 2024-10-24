// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package target

import (
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/feature"
)

func TestMetricsTarget(t *testing.T) {
	target := MetricTarget{
		Meta: define.MonitorMeta{
			Name:      "monitor-name",
			Namespace: "blueking",
			Index:     0,
		},
		Address:         "http://localhost:8080",
		NodeName:        "node-127-0-1-1",
		DataID:          12345,
		Namespace:       "blueking",
		Period:          "10s",
		Timeout:         "10s",
		Path:            "/metrics",
		BearerTokenFile: "/path/to/token",
		ProxyURL:        "http://localhost:8081/metrics",
		Labels: []labels.Label{
			{Name: "label1", Value: "value1"},
			{Name: "label2", Value: "value2"},
			{Name: "job", Value: "my-job"},
		},
		ExtraLabels: map[string]string{
			"label3": "value3",
		},
	}

	b, err := target.YamlBytes()
	assert.NoError(t, err)

	expected := `type: metricbeat
name: http://localhost:8080/metrics
version: "1"
dataid: 12345
max_timeout: 100s
min_period: 3s
tasks:
- task_id: 715974526
  bk_biz_id: 2
  period: 10s
  timeout: 10s
  custom_report: true
  labels:
  - label1: value1
    label2: value2
    job: my-job
    bk_endpoint_url: http://localhost:8080/metrics
    bk_endpoint_index: "0"
    bk_monitor_name: monitor-name
    bk_monitor_namespace: blueking
    label3: value3
  module:
    module: prometheus
    metricsets:
    - collector
    enabled: true
    period: 10s
    proxy_url: http://localhost:8081/metrics
    timeout: 10s
    disable_custom_timestamp: false
    normalize_metric_name: false
    hosts:
    - http://localhost:8080
    namespace: blueking
    metrics_path: /metrics
    bearer_file: /path/to/token
`
	assert.Equal(t, expected, string(b))
}

func TestRemoteRelabelConfig(t *testing.T) {
	cases := []struct {
		Name   string
		Input  *MetricTarget
		Output *yaml.MapItem
	}{
		{
			Name: "NoRules",
			Input: &MetricTarget{
				NodeName:     "worker1",
				RelabelIndex: "0",
				RelabelRule:  "",
			},
			Output: nil,
		},
		{
			Name: "v1/workload",
			Input: &MetricTarget{
				NodeName:     "worker1",
				RelabelIndex: "0",
				RelabelRule:  "v1/workload",
			},
			Output: &yaml.MapItem{
				Key:   "metric_relabel_remote",
				Value: "http://:0/workload/node/worker1",
			},
		},
		{
			Name: "v2/workload",
			Input: &MetricTarget{
				NodeName:     "worker1",
				RelabelIndex: "0",
				RelabelRule:  "v2/workload",
				Labels:       labels.Labels{{Name: "pod_name", Value: "pod1"}},
			},
			Output: &yaml.MapItem{
				Key:   "metric_relabel_remote",
				Value: "http://:0/workload/node/worker1?q=cG9kTmFtZT1wb2Qx",
			},
		},
		{
			Name: "v2/workload,labeljoin",
			Input: &MetricTarget{
				NodeName:     "worker1",
				RelabelIndex: "0",
				RelabelRule:  "v2/workload,v1/labeljoin",
			},
			Output: &yaml.MapItem{
				Key:   "metric_relabel_remote",
				Value: "http://:0/labeljoin",
			},
		},
		{
			Name: "v1/workload,v1/labeljoin",
			Input: &MetricTarget{
				NodeName:     "worker1",
				RelabelIndex: "0",
				RelabelRule:  "v1/workload,v1/labeljoin",
				LabelJoinMatcher: &feature.LabelJoinMatcherSpec{
					Kind:        "Pod",
					Annotations: []string{"annotations1"},
					Labels:      []string{"label1"},
				},
			},
			Output: &yaml.MapItem{
				Key:   "metric_relabel_remote",
				Value: "http://:0/workload/node/worker1?q=YW5ub3RhdGlvbnM9YW5ub3RhdGlvbnMxJmtpbmQ9UG9kJmxhYmVscz1sYWJlbDEmcnVsZXM9bGFiZWxqb2lu",
			},
		},
		{
			Name: "v2/workload,v1/labeljoin",
			Input: &MetricTarget{
				NodeName:     "worker1",
				RelabelIndex: "0",
				RelabelRule:  "v2/workload,v1/labeljoin",
				LabelJoinMatcher: &feature.LabelJoinMatcherSpec{
					Kind:        "Pod",
					Annotations: []string{"annotations1"},
					Labels:      []string{"label1"},
				},
				Labels: labels.Labels{
					{Name: "pod_name", Value: "pod1"},
				},
			},
			Output: &yaml.MapItem{
				Key:   "metric_relabel_remote",
				Value: "http://:0/workload/node/worker1?q=YW5ub3RhdGlvbnM9YW5ub3RhdGlvbnMxJmtpbmQ9UG9kJmxhYmVscz1sYWJlbDEmcG9kTmFtZT1wb2QxJnJ1bGVzPWxhYmVsam9pbg",
			},
		},
		{
			Name: "v1/workload,v1/labeljoin",
			Input: &MetricTarget{
				NodeName:     "worker1",
				RelabelIndex: "0",
				RelabelRule:  "v1/workload,v1/labeljoin",
				LabelJoinMatcher: &feature.LabelJoinMatcherSpec{
					Kind:        "Pod",
					Annotations: []string{"annotations1"},
					Labels:      []string{"label1"},
				},
			},
			Output: &yaml.MapItem{
				Key:   "metric_relabel_remote",
				Value: "http://:0/workload/node/worker1?q=YW5ub3RhdGlvbnM9YW5ub3RhdGlvbnMxJmtpbmQ9UG9kJmxhYmVscz1sYWJlbDEmcnVsZXM9bGFiZWxqb2lu",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			assert.Equal(t, c.Output, c.Input.RemoteRelabelConfig())
		})
	}
}
