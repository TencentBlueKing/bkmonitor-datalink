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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
)

func TestMonitorBlacklistMatchWithoutTypeMeta(t *testing.T) {
	originalRules := configs.G().MonitorBlacklistMatchRules
	t.Cleanup(func() {
		configs.G().MonitorBlacklistMatchRules = originalRules
	})
	configs.G().MonitorBlacklistMatchRules = []configs.MonitorBlacklistMatchRule{
		{Kind: monitorKindServiceMonitor, Namespace: "default", Name: "service"},
		{Kind: monitorKindPodMonitor, Namespace: "default", Name: "pod"},
	}

	serviceMonitor := &promv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "service"},
	}
	podMonitor := &promv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "pod"},
	}

	assert.Empty(t, serviceMonitor.Kind)
	assert.Empty(t, podMonitor.Kind)
	assert.True(t, ifRejectServiceMonitor(serviceMonitor))
	assert.True(t, ifRejectPodMonitor(podMonitor))
}
