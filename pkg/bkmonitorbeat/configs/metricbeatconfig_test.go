// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs_test

import (
	"testing"

	"github.com/elastic/beats/libbeat/common"
	ucfgyaml "github.com/elastic/go-ucfg/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

// MetricbeatConfigSuite :
type MetricbeatConfigSuite struct {
	suite.Suite
}

// TestPingConfig :
func TestMetricConfig(t *testing.T) {
	suite.Run(t, &MetricbeatConfigSuite{})
}

// TestConfig :
func (s *MetricbeatConfigSuite) TestConfigClean() {
	metaConf := configs.NewMetricBeatMetaConfig(configs.NewConfig())
	taskConf := configs.NewMetricBeatConfig()
	taskConf.Module = common.NewConfig()
	metaConf.Tasks = append(metaConf.Tasks, taskConf)

	s.NoError(metaConf.Clean(), "clean error")

	s.Equal(define.DefaultTimeout, metaConf.MaxTimeout)
	s.Equal(define.DefaultPeriod, metaConf.MinPeriod)
	s.Equal(define.DefaultPeriod, taskConf.Period)
	s.Equal(define.DefaultTimeout, taskConf.Timeout)
}

func TestMetricBeatMetaConfigKeepLatestByTarget(t *testing.T) {
	metaConf := configs.NewMetricBeatMetaConfig(configs.NewConfig())

	oldTask := configs.NewMetricBeatConfig()
	oldTask.TaskID = 100
	oldTask.PodUID = "pod-uid-old"
	oldTask.ConfigRevision = 1
	oldTask.Module = common.MustNewConfigFrom(map[string]interface{}{
		"hosts":        []string{"http://127.0.0.1:8080"},
		"metrics_path": "/metrics",
	})

	newTask := configs.NewMetricBeatConfig()
	newTask.TaskID = 101
	newTask.PodUID = "pod-uid-new"
	newTask.ConfigRevision = 2
	newTask.Module = common.MustNewConfigFrom(map[string]interface{}{
		"hosts":        []string{"http://127.0.0.1:8080"},
		"metrics_path": "/metrics",
	})

	metaConf.Tasks = append(metaConf.Tasks, oldTask, newTask)
	if err := metaConf.Clean(); err != nil {
		t.Fatalf("clean error: %v", err)
	}

	if len(metaConf.Tasks) != 1 {
		t.Fatalf("expected only latest task to remain, got %d", len(metaConf.Tasks))
	}
	assert.Equal(t, int32(101), metaConf.Tasks[0].TaskID)
	assert.Equal(t, "pod-uid-new", metaConf.Tasks[0].PodUID)
}

func TestMetricBeatMetaConfigYamlCompatibility(t *testing.T) {
	tests := []struct {
		name               string
		content            string
		wantPodUID         string
		wantConfigRevision uint64
	}{
		{
			name: "old config without internal fields",
			content: `
dataid: 12345
max_timeout: 100s
min_period: 3s
tasks:
  - task_id: 1
    bk_biz_id: 2
    period: 10s
    timeout: 10s
    custom_report: true
    labels:
      - pod: gate-pod
        bk_endpoint_url: http://127.0.0.1:8080/metrics
    module:
      module: prometheus
      metricsets: ["collector"]
      enabled: true
      period: 10s
      timeout: 10s
      hosts: ["http://127.0.0.1:8080"]
      namespace: blueking
      metrics_path: /metrics
`,
		},
		{
			name: "new config with internal fields",
			content: `
dataid: 12345
max_timeout: 100s
min_period: 3s
tasks:
  - task_id: 2
    bk_biz_id: 2
    period: 10s
    timeout: 10s
    custom_report: true
    pod_uid: pod-uid-a
    config_revision: 9
    labels:
      - pod: gate-pod
        bk_endpoint_url: http://127.0.0.1:8080/metrics
    module:
      module: prometheus
      metricsets: ["collector"]
      enabled: true
      period: 10s
      timeout: 10s
      hosts: ["http://127.0.0.1:8080"]
      namespace: blueking
      metrics_path: /metrics
`,
			wantPodUID:         "pod-uid-a",
			wantConfigRevision: 9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ucfgyaml.NewConfig([]byte(tt.content))
			if err != nil {
				t.Fatalf("parse yaml: %v", err)
			}

			metaConf := configs.NewMetricBeatMetaConfig(configs.NewConfig())
			if err := cfg.Unpack(metaConf); err != nil {
				t.Fatalf("unpack config: %v", err)
			}
			if err := metaConf.Clean(); err != nil {
				t.Fatalf("clean config: %v", err)
			}
			if len(metaConf.Tasks) != 1 {
				t.Fatalf("expected 1 task, got %d", len(metaConf.Tasks))
			}

			task := metaConf.Tasks[0]
			assert.Equal(t, tt.wantPodUID, task.PodUID)
			assert.Equal(t, tt.wantConfigRevision, task.ConfigRevision)
			for _, item := range task.Labels {
				_, hasPodUID := item["pod_uid"]
				_, hasConfigRevision := item["config_revision"]
				assert.False(t, hasPodUID)
				assert.False(t, hasConfigRevision)
			}
		})
	}
}
