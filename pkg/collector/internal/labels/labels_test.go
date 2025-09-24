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

func TestHashFromMap(t *testing.T) {
	m1 := map[string]string{
		"a": "1",
		"b": "2",
		"c": "3",
	}
	h1 := HashFromMap(m1)
	assert.Equal(t, h1, HashFromMap(m1))

	m2 := map[string]string{
		"a": "1",
		"b": "2",
		"c": "4",
	}
	h2 := HashFromMap(m2)
	assert.NotEqual(t, h1, h2)
}

func BenchmarkLabelsHashPool(b *testing.B) {
	b.ReportAllocs()
	m := random.Dimensions(6)
	for i := 0; i < b.N; i++ {
		HashFromMap(m)
	}
}
