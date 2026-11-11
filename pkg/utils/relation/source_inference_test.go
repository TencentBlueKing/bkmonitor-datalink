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
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSourceInferenceOrderCoversEveryBuiltinResource(t *testing.T) {
	builtin := make([]string, 0, len(DefaultResourceDefinitions()))
	for _, definition := range DefaultResourceDefinitions() {
		builtin = append(builtin, definition.Name)
	}
	assert.ElementsMatch(t, builtin, sourceInferenceOrder)
}

func TestSourceInferenceLess(t *testing.T) {
	for _, tc := range []struct {
		name          string
		finer, coarse string
	}{
		{"pod before a Prometheus job label", "pod", "job"},
		{"pod before a Prometheus service label", "pod", "service"},
		{"pod before its owners", "pod", "deployment"},
		{"replica set before its deployment", "replicaset", "deployment"},
		{"machine before the CMDB tree", "system", "host"},
		{"module before its set", "module", "set"},
		{"set before its business", "set", "business"},
		{"business before a data source", "business", "datasource"},
		{"built-in before custom, against the name order", "system", "aaa_custom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, SourceInferenceLess(tc.finer, tc.coarse))
			assert.False(t, SourceInferenceLess(tc.coarse, tc.finer))
		})
	}
	t.Run("custom resources by name", func(t *testing.T) {
		assert.True(t, SourceInferenceLess("custom_a", "custom_b"))
		assert.False(t, SourceInferenceLess("custom_b", "custom_a"))
		assert.False(t, SourceInferenceLess("custom_a", "custom_a"))
	})
}

func TestSourceInferenceOrderIgnoresInputOrder(t *testing.T) {
	want := []string{"pod", "job", "deployment", "service", "custom_a", "custom_b"}
	for _, input := range [][]string{
		{"custom_b", "service", "job", "custom_a", "deployment", "pod"},
		{"pod", "custom_a", "deployment", "custom_b", "job", "service"},
	} {
		got := append([]string(nil), input...)
		sort.SliceStable(got, func(i, j int) bool { return SourceInferenceLess(got[i], got[j]) })
		assert.Equal(t, want, got)
	}
}
