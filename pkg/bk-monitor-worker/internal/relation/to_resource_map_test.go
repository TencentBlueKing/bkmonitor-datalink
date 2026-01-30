// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvertToResourceMapToMetrics(t *testing.T) {
	tests := []struct {
		name          string
		toResourceMap map[string]map[string]map[string]any
		topologyID    string
		topologyLevel string
		bizID         int
		wantMetrics   []string
		wantLabels    map[string]map[string]string
	}{
		{
			name: "app_version with host",
			toResourceMap: map[string]map[string]map[string]any{
				"host": {
					"app_version": {
						"app_name": "user-service",
						"version":  "2.0.1",
					},
				},
			},
			topologyID:    "135",
			topologyLevel: "host",
			bizID:         7,
			wantMetrics:   []string{"app_version_with_host_relation"},
			wantLabels: map[string]map[string]string{
				"app_version_with_host_relation": {
					"app_name":   "user-service",
					"version":    "2.0.1",
					"bk_biz_id":  "7",
					"bk_host_id": "135",
				},
			},
		},
		{
			name: "app_version with set",
			toResourceMap: map[string]map[string]map[string]any{
				"set": {
					"app_version": {
						"app_name": "api-gateway",
						"version":  "1.0.0",
					},
				},
			},
			topologyID:    "21",
			topologyLevel: "set",
			bizID:         7,
			wantMetrics:   []string{"app_version_with_set_relation"},
			wantLabels: map[string]map[string]string{
				"app_version_with_set_relation": {
					"app_name":  "api-gateway",
					"version":   "1.0.0",
					"bk_biz_id": "7",
					"bk_set_id": "21",
				},
			},
		},
		{
			name: "multiple resource types",
			toResourceMap: map[string]map[string]map[string]any{
				"host": {
					"app_version": {
						"app_name": "svc1",
						"version":  "1.0",
					},
					"service_name": {
						"name":      "svc1-name",
						"namespace": "prod",
					},
				},
			},
			topologyID:    "135",
			topologyLevel: "host",
			bizID:         7,
			wantMetrics:   []string{"app_version_with_host_relation", "service_name_with_host_relation"},
			wantLabels: map[string]map[string]string{
				"app_version_with_host_relation": {
					"app_name":   "svc1",
					"version":    "1.0",
					"bk_biz_id":  "7",
					"bk_host_id": "135",
				},
				"service_name_with_host_relation": {
					"name":       "svc1-name",
					"namespace":  "prod",
					"bk_biz_id":  "7",
					"bk_host_id": "135",
				},
			},
		},
		{
			name:          "empty map",
			toResourceMap: map[string]map[string]map[string]any{},
			topologyID:    "135",
			topologyLevel: "host",
			bizID:         7,
			wantMetrics:   []string{},
			wantLabels:    map[string]map[string]string{},
		},
		{
			name:          "nil map",
			toResourceMap: nil,
			topologyID:    "135",
			topologyLevel: "host",
			bizID:         7,
			wantMetrics:   []string{},
			wantLabels:    map[string]map[string]string{},
		},
		{
			name: "skip non-matching topology level",
			toResourceMap: map[string]map[string]map[string]any{
				"set": {
					"app_version": {
						"app_name": "api-gateway",
						"version":  "1.0.0",
					},
				},
			},
			topologyID:    "135",
			topologyLevel: "host",
			bizID:         7,
			wantMetrics:   []string{},
			wantLabels:    map[string]map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertToResourceMapToMetrics(tt.toResourceMap, tt.topologyID, tt.topologyLevel, tt.bizID)

			assert.Equal(t, len(tt.wantMetrics), len(got), "metric count mismatch")

			// Build a map of got metrics for easier lookup
			gotMetrics := make(map[string]map[string]string)
			for _, m := range got {
				labelMap := make(map[string]string)
				for _, l := range m.Labels {
					labelMap[l.Name] = l.Value
				}
				gotMetrics[m.Name] = labelMap
			}

			// Verify each expected metric exists with correct labels
			for _, metricName := range tt.wantMetrics {
				gotLabels, ok := gotMetrics[metricName]
				assert.True(t, ok, "expected metric %s not found", metricName)
				if ok {
					expectedLabels := tt.wantLabels[metricName]
					assert.Equal(t, expectedLabels, gotLabels, "labels mismatch for metric %s", metricName)
				}
			}
		})
	}
}
