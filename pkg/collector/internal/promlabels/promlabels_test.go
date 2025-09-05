// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promlabels

import (
	"testing"

	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"
)

func TestLabelsGet(t *testing.T) {
	tests := []struct {
		name     string
		labels   Labels
		target   string
		expected prompb.Label
		found    bool
	}{
		{
			name:   "nil origin",
			target: "any",
			found:  false,
		},
		{
			name:   "empty origin",
			labels: Labels{},
			target: "key",
			found:  false,
		},
		{
			name:     "label found",
			labels:   Labels{{Name: "status", Value: "200"}},
			target:   "status",
			expected: prompb.Label{Name: "status", Value: "200"},
			found:    true,
		},
		{
			name:     "label not found",
			labels:   Labels{{Name: "status", Value: "200"}},
			target:   "status1",
			expected: prompb.Label{},
			found:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, found := tt.labels.Get(tt.target)
			assert.Equal(t, tt.found, found)
			assert.Equal(t, tt.expected, label)
		})
	}
}

func TestLabelsUpsert(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		value    string
		origin   Labels
		expected Labels
	}{
		{
			name:     "insert empty origin",
			origin:   Labels{},
			key:      "service",
			value:    "order",
			expected: Labels{{Name: "service", Value: "order"}},
		},
		{
			name:   "insert new label",
			origin: Labels{{Name: "method", Value: "POST"}},
			key:    "path",
			value:  "/users",
			expected: Labels{
				{Name: "method", Value: "POST"},
				{Name: "path", Value: "/users"},
			},
		},
		{
			name:     "insert same value",
			origin:   Labels{{Name: "key", Value: "value"}},
			key:      "key",
			value:    "value",
			expected: Labels{{Name: "key", Value: "value"}},
		},
		{
			name:     "update existing label",
			origin:   Labels{{Name: "status", Value: "200"}},
			key:      "status",
			value:    "500",
			expected: Labels{{Name: "status", Value: "500"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.origin.Upsert(tt.key, tt.value)
			assert.Equal(t, tt.expected, tt.origin)
		})
	}
}
