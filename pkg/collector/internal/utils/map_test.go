// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestCloneMap(t *testing.T) {
	t.Run("NilMap", func(t *testing.T) {
		assert.Nil(t, CloneMap(nil))
	})

	t.Run("Clone", func(t *testing.T) {
		m1 := map[string]string{
			"aaa": "111",
			"bbb": "222",
		}
		m2 := CloneMap(m1)
		assert.True(t, reflect.DeepEqual(m1, m2))
	})
}

func TestMergeMap(t *testing.T) {
	m1 := map[string]string{
		"aaa": "111",
		"bbb": "222",
	}
	m2 := map[string]string{
		"aaa": "112",
		"ccc": "333",
	}

	m3 := MergeMaps(m1, m2)
	assert.True(t, reflect.DeepEqual(map[string]string{
		"aaa": "112",
		"bbb": "222",
		"ccc": "333",
	}, m3))
}

func TestMergeReplaceMaps(t *testing.T) {
	m1 := map[string]string{
		"aaa":   "111",
		"bbb.x": "222",
	}
	m2 := map[string]string{
		"aaa":   "112",
		"ccc.x": "333",
	}

	m3 := MergeReplaceMaps(m1, m2)
	assert.True(t, reflect.DeepEqual(map[string]string{
		"aaa":   "112",
		"bbb_x": "222",
		"ccc_x": "333",
	}, m3))
}

func TestMergeReplaceAttributeMaps(t *testing.T) {
	m1 := pcommon.NewMap()
	m1.InsertString("aaa", "111")
	m1.InsertString("bbb.x", "222")

	m2 := pcommon.NewMap()
	m2.InsertString("aaa", "112")
	m2.InsertString("ccc.x", "333")

	m3 := MergeReplaceAttributeMaps(m1, m2)
	assert.True(t, reflect.DeepEqual(map[string]string{
		"aaa":   "112",
		"bbb_x": "222",
		"ccc_x": "333",
	}, m3))
}

func BenchmarkMergeReplaceCache(b *testing.B) {
	m := pcommon.NewMap()
	m.InsertString("telemetry.sdk.name", "telemetry_sdk_name")
	m.InsertString("telemetry.sdk.version", "telemetry_sdk_version")
	m.InsertString("telemetry.sdk.language", "telemetry_sdk_language")
	m.InsertString("foo.bar.key.value", "foo.bar.key.value")

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			MergeReplaceAttributeMaps(m)
		}
	})
}

func BenchmarkMergeReplaceWithoutCache(b *testing.B) {
	m := pcommon.NewMap()
	m.InsertString("telemetry.sdk.namex", "telemetry_sdk_name")
	m.InsertString("telemetry.sdk.versionx", "telemetry_sdk_version")
	m.InsertString("telemetry.sdk.languagex", "telemetry_sdk_language")
	m.InsertString("foo.bar.key.value", "foo.bar.key.value")

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			MergeReplaceAttributeMaps(m)
		}
	})
}

func TestAnyMap(t *testing.T) {
	tests := []struct {
		input    string
		expected map[string]int
	}{
		{
			input: `foo=1,bar=2`,
			expected: map[string]int{
				"foo": 1,
				"bar": 2,
			},
		},
		{
			input: `foo=1, bar=2`,
			expected: map[string]int{
				"foo": 1,
				"bar": 2,
			},
		},
		{
			input: `foo = 1, bar = 2`,
			expected: map[string]int{
				"foo": 1,
				"bar": 2,
			},
		},
		{
			input: `foo=1`,
			expected: map[string]int{
				"foo": 1,
			},
		},
		{
			input: `foo=1,`,
			expected: map[string]int{
				"foo": 1,
			},
		},
	}

	for _, tt := range tests {
		om := NewOptMap(tt.input)
		for k, v := range tt.expected {
			i, ok := om.GetInt(k)
			assert.True(t, ok)
			assert.Equal(t, v, i)
		}
	}
}

func TestNameOpts(t *testing.T) {
	tests := []struct {
		nameOpts string
		name     string
		opts     string
	}{
		{
			nameOpts: "foo1",
			name:     "foo1",
		},
		{
			nameOpts: "foo1;",
			name:     "foo1",
		},
		{
			nameOpts: "foo1;k1=v1",
			name:     "foo1",
			opts:     "k1=v1",
		},
		{
			nameOpts: "foo1;k1=v1,k2=v2",
			name:     "foo1",
			opts:     "k1=v1,k2=v2",
		},
	}

	for _, tt := range tests {
		name, opts := NameOpts(tt.nameOpts)
		assert.Equal(t, tt.name, name)
		assert.Equal(t, tt.opts, opts)
	}
}
