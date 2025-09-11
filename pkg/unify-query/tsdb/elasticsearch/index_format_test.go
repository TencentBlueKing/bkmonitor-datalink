// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIndexFormatFieldMap(t *testing.T) {
	testCases := []struct {
		name     string
		settings map[string]any
		mappings map[string]any

		fieldMap map[string]map[string]any
	}{
		{
			name: "test",
			settings: map[string]any{
				"index": map[string]any{
					"lifecycle": map[string]any{
						"name": "test",
					},
				},
			},
			mappings: map[string]any{
				"properties": map[string]any{
					"timestamp": map[string]any{
						"type": "date",
					},
					"value": map[string]any{
						"type": "double",
					},
				},
			},
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			iof := &IndexOptionFormat{}
			iof.Parse(c.settings, c.mappings)

			fieldMap := iof.FieldMap()
			assert.Equal(t, c.fieldMap, fieldMap)
		})
	}
}
