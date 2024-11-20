// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package labels

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

func TestFromMap(t *testing.T) {
	excepted := Labels{
		{Name: "baz", Value: "qux"},
		{Name: "foo", Value: "bar"},
	}
	m := map[string]string{
		"foo": "bar",
		"baz": "qux",
	}
	lbs := FromMap(m)
	assert.Equal(t, excepted, lbs)
}

func TestLabelsHash(t *testing.T) {
	lbls := Labels{
		{Name: "foo", Value: "bar"},
		{Name: "baz", Value: "qux"},
	}
	assert.Equal(t, lbls.Hash(), lbls.Hash())
	assert.NotEqual(t, lbls.Hash(), Labels{lbls[1], lbls[0]}.Hash(), "unordered labels match.")
	assert.NotEqual(t, lbls.Hash(), Labels{lbls[0]}.Hash(), "different labels match.")
}

func TestLabelsMap(t *testing.T) {
	assert.Equal(t, map[string]string{
		"aaa": "111",
		"bbb": "222",
	}, Labels{
		{Name: "aaa", Value: "111"},
		{Name: "bbb", Value: "222"},
	}.Map())
}

func TestHashFromMap(t *testing.T) {
	m := map[string]string{
		"aaa": "111",
		"bbb": "222",
	}
	h1 := HashFromMap(m)
	h2 := HashFromMap(m)
	assert.Equal(t, h1, h2)
}

func BenchmarkLabelsHash(b *testing.B) {
	m := random.Dimensions(6)
	for i := 0; i < b.N; i++ {
		lbs := FromMap(m)
		lbs.Hash()
	}
}

func BenchmarkLabelsHashPool(b *testing.B) {
	m := random.Dimensions(6)
	for i := 0; i < b.N; i++ {
		HashFromMap(m)
	}
}
