// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdb

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListBizHostsTopoDataInfoHost_ToResourceMapUnmarshal 测试 ToResourceMap 字段的反序列化
func TestListBizHostsTopoDataInfoHost_ToResourceMapUnmarshal(t *testing.T) {
	tests := []struct {
		name      string
		jsonInput string
		want      map[string]map[string]map[string]any
		wantErr   bool
	}{
		{
			name: "basic app_version mapping",
			jsonInput: `{
				"bk_host_id": 123,
				"bk_host_innerip": "192.168.1.1",
				"to_resource_map": {
					"set": {
						"app_version": {
							"app_name": "myapp",
							"version": "1.0"
						}
					}
				}
			}`,
			want: map[string]map[string]map[string]any{
				"set": {
					"app_version": {
						"app_name": "myapp",
						"version":  "1.0",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "multiple resource types",
			jsonInput: `{
				"bk_host_id": 456,
				"bk_host_innerip": "192.168.1.2",
				"to_resource_map": {
					"set": {
						"app_version": {
							"app_name": "app1",
							"version": "2.0"
						},
						"service_name": {
							"service": "api",
							"port": 8080
						}
					},
					"module": {
						"component": {
							"name": "redis",
							"cluster": "main"
						}
					}
				}
			}`,
			want: map[string]map[string]map[string]any{
				"set": {
					"app_version": {
						"app_name": "app1",
						"version":  "2.0",
					},
					"service_name": {
						"service": "api",
						"port":    float64(8080),
					},
				},
				"module": {
					"component": {
						"name":    "redis",
						"cluster": "main",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "empty to_resource_map",
			jsonInput: `{
				"bk_host_id": 789,
				"bk_host_innerip": "192.168.1.3",
				"to_resource_map": {}
			}`,
			want:    map[string]map[string]map[string]any{},
			wantErr: false,
		},
		{
			name: "missing to_resource_map",
			jsonInput: `{
				"bk_host_id": 999,
				"bk_host_innerip": "192.168.1.4"
			}`,
			want:    nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var host ListBizHostsTopoDataInfoHost
			err := json.Unmarshal([]byte(tt.jsonInput), &host)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, host.ToResourceMap)
		})
	}
}
