// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

const content = `
logger:
 level: info

dry_run: false
default_period: 

service_name: bkmonitor-operator-stack-operator
monitor_namespace: bkmonitor-operator
deny_target_namespaces:
 - "thanos"
 - "ieg-blueking-monitor-prod"

target_label_selector: 

enable_probe: false
enable_service_monitor: true
enable_pod_monitor: true
enable_prometheus_rule: false
enable_statefulset_worker: true
enable_daemonset_worker: true

statefulset_worker_hpa: true
statefulset_dispatch_type: hash
statefulset_match_rules:
monitor_blacklist_match_rules:
 - kind: ServiceMonitor
   name: kube-state-metrics
   namespace: kube-system
 - kind: ServiceMonitor
   name: node-exporter
   namespace: kube-system
kubelet:
 enable: true
 name: bkmonitor-operator-stack-kubelet
 namespace: bkmonitor-operator
`

func TestConfig(t *testing.T) {
	f, err := os.CreateTemp("", "operator-configs.yaml")
	assert.NoError(t, err)
	defer os.Remove(f.Name())

	_, err = f.Write([]byte(content))
	assert.NoError(t, err)
	assert.NoError(t, Load(f.Name()))

	t.Logf("configs: %#v", G())

	assert.Equal(t, 1, G().StatefulSetReplicas)
	assert.Equal(t, 10, G().StatefulSetMaxReplicas)
	assert.Equal(t, float64(600), G().StatefulSetWorkerFactor)
	assert.Equal(t, "60s", G().DefaultPeriod)
}
