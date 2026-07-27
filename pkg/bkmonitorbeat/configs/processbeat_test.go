// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package configs

import "testing"

func TestProcessbeatConfigGetTaskConfigList(t *testing.T) {
	tests := []struct {
		name   string
		config ProcessbeatConfig
	}{
		{name: "port data ID", config: ProcessbeatConfig{PortDataId: 1}},
		{name: "performance data ID", config: ProcessbeatConfig{PerfDataId: 1}},
		{name: "top data ID", config: ProcessbeatConfig{TopDataId: 1}},
	}
	for _, tt := range tests {
		t.Run("skip config without processes for "+tt.name, func(t *testing.T) {
			if got := len(tt.config.GetTaskConfigList()); got != 0 {
				t.Fatalf("expected no task without process configuration, got %d", got)
			}
		})
	}

	t.Run("keep config with processes", func(t *testing.T) {
		config := &ProcessbeatConfig{
			PortDataId: 1,
			Processes: []ProcessbeatPortConfig{
				{Name: "example"},
			},
		}

		if got := len(config.GetTaskConfigList()); got != 1 {
			t.Fatalf("expected one task with process configuration, got %d", got)
		}
	})
}
