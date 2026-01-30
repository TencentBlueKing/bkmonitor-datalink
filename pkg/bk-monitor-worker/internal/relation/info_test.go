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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInfo_ToResourceMap(t *testing.T) {
	// 构造测试数据
	info := Info{
		ID:       "test-id",
		Resource: "service",
		Label: map[string]string{
			"app": "my-service",
		},
		Expands: map[string]map[string]string{
			"service": {
				"version": "1.0.0",
			},
		},
		Links: []Link{
			{
				{ID: "pod-1", Resource: "pod"},
			},
		},
		ToResourceMap: map[string]map[string]map[string]any{
			"container": {
				"pod": {
					"name": "test-pod",
					"ip":   "192.168.1.1",
				},
			},
		},
	}

	// 测试 JSON marshaling
	data, err := json.Marshal(info)
	assert.NoError(t, err)
	assert.NotEmpty(t, data)

	// 测试 JSON unmarshaling
	var unmarshaled Info
	err = json.Unmarshal(data, &unmarshaled)
	assert.NoError(t, err)

	// 验证基本字段
	assert.Equal(t, info.ID, unmarshaled.ID)
	assert.Equal(t, info.Resource, unmarshaled.Resource)
	assert.Equal(t, info.Label, unmarshaled.Label)
	assert.Equal(t, info.Expands, unmarshaled.Expands)

	// 验证 ToResourceMap 字段
	assert.NotNil(t, unmarshaled.ToResourceMap)
	assert.Contains(t, unmarshaled.ToResourceMap, "container")
	assert.Contains(t, unmarshaled.ToResourceMap["container"], "pod")
	assert.Equal(t, "test-pod", unmarshaled.ToResourceMap["container"]["pod"]["name"])
	assert.Equal(t, "192.168.1.1", unmarshaled.ToResourceMap["container"]["pod"]["ip"])
}
