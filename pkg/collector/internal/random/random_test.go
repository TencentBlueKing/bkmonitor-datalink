// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package random

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandom(t *testing.T) {
	t.Run("String", func(t *testing.T) {
		s1 := String(10)
		s2 := String(10)
		assert.NotEqual(t, s1, s2)
	})

	t.Run("Dimensions", func(t *testing.T) {
		d1 := Dimensions(10)
		d2 := Dimensions(10)
		assert.NotEqual(t, d1, d2)
	})

	t.Run("FastDimensions", func(t *testing.T) {
		d1 := FastDimensions(10)
		d2 := FastDimensions(10)
		assert.NotEqual(t, d1, d2)
	})

	t.Run("TraceID", func(t *testing.T) {
		t1 := TraceID()
		t2 := TraceID()
		assert.NotEqual(t, t1, t2)
	})

	t.Run("SpanID", func(t *testing.T) {
		s1 := SpanID()
		s2 := SpanID()
		assert.NotEqual(t, s1, s2)
	})

	keys := []string{"key1", "key2"}
	t.Run("AttributeMap/Int", func(t *testing.T) {
		m1 := AttributeMap(keys, "int")
		m2 := AttributeMap(keys, "int")
		assert.NotEqual(t, m1, m2)
	})

	t.Run("AttributeMap/Float", func(t *testing.T) {
		m1 := AttributeMap(keys, "float")
		m2 := AttributeMap(keys, "float")
		assert.NotEqual(t, m1, m2)
	})

	t.Run("AttributeMap/X", func(t *testing.T) {
		m1 := AttributeMap(keys, "x")
		m2 := AttributeMap(keys, "x")
		assert.NotEqual(t, m1, m2)
	})
}

func BenchmarkRandomDimensions(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Dimensions(10)
	}
}

func BenchmarkFastRandomDimensions(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		FastDimensions(10)
	}
}
