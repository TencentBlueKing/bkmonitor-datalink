// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package maps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestClone(t *testing.T) {
	tests := []struct {
		input    map[string]string
		expected map[string]string
	}{
		{
			input:    nil,
			expected: nil,
		},
		{
			input: map[string]string{
				"aaa": "111",
				"bbb": "222",
			},
			expected: map[string]string{
				"aaa": "111",
				"bbb": "222",
			},
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, Clone(tt.input))
	}
}

func TestMerge(t *testing.T) {
	tests := []struct {
		input    map[string]string
		added    map[string]string
		expected map[string]string
	}{
		{
			input:    nil,
			added:    nil,
			expected: map[string]string{},
		},
		{
			input: map[string]string{
				"aaa": "111",
				"bbb": "222",
			},
			added: map[string]string{
				"aaa": "112",
				"ccc": "333",
			},
			expected: map[string]string{
				"aaa": "112",
				"bbb": "222",
				"ccc": "333",
			},
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, Merge(tt.input, tt.added))
	}
}

func TestMergeWith(t *testing.T) {
	tests := []struct {
		input    map[string]string
		added    []string
		expected map[string]string
	}{
		{
			input:    nil,
			added:    nil,
			expected: map[string]string{},
		},
		{
			input: map[string]string{
				"aaa": "111",
				"bbb": "222",
			},
			added: []string{"aaa", "112", "ccc", "333"},
			expected: map[string]string{
				"aaa": "112",
				"bbb": "222",
				"ccc": "333",
			},
		},
		{
			input: map[string]string{
				"aaa": "111",
				"bbb": "222",
			},
			added: []string{"foo", "121", "bar"},
			expected: map[string]string{
				"aaa": "111",
				"bbb": "222",
				"foo": "121",
			},
		},
		{
			input: map[string]string{
				"aaa": "111",
				"bbb": "222",
			},
			expected: map[string]string{
				"aaa": "111",
				"bbb": "222",
			},
		},
		{
			input: nil,
			added: []string{"foo", "121", "bar", "333"},
			expected: map[string]string{
				"foo": "121",
				"bar": "333",
			},
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, MergeWith(tt.input, tt.added...))
	}
}

func TestMergeReplaceAttributes(t *testing.T) {
	m1 := pcommon.NewMap()
	m1.InsertString("aaa", "111")
	m1.InsertString("bbb.x", "222")

	m2 := pcommon.NewMap()
	m2.InsertString("aaa", "112")
	m2.InsertString("ccc.x", "333")

	m3 := MergeReplaceAttributes(m1, m2)
	assert.Equal(t, map[string]string{
		"aaa":   "112",
		"bbb_x": "222",
		"ccc_x": "333",
	}, m3)
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
			MergeReplaceAttributes(m)
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
			MergeReplaceAttributes(m)
		}
	})
}

func BenchmarkMerge(b *testing.B) {
	m := map[string]string{
		"telemetry.sdk.name":     "telemetry_sdk_name",
		"telemetry.sdk.version":  "telemetry_sdk_version",
		"telemetry.sdk.language": "telemetry_sdk_language",
		"foo.bar.key.value":      "foo.bar.key.value",
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			Merge(m, map[string]string{"net.peer.ip": "127.0.0.1"})
		}
	})
}

func BenchmarkMergeWith(b *testing.B) {
	m := map[string]string{
		"telemetry.sdk.name":     "telemetry_sdk_name",
		"telemetry.sdk.version":  "telemetry_sdk_version",
		"telemetry.sdk.language": "telemetry_sdk_language",
		"foo.bar.key.value":      "foo.bar.key.value",
	}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			MergeWith(m, "net.peer.ip", "127.0.0.1")
		}
	})
}
