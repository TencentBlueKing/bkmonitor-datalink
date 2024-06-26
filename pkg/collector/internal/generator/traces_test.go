// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package generator

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestTraces(t *testing.T) {
	g := NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{
			RandomAttributeKeys: []string{"attr1", "attr2"},
			RandomResourceKeys:  []string{"res1", "res2"},
			Resources:           map[string]string{"foo": "bar"},
			Attributes:          map[string]string{"hello": "mando"},
		},
		SpanCount:  10,
		SpanKind:   1,
		EventCount: 1,
		LinkCount:  1,
	})

	data := g.Generate()
	assert.NotNil(t, data)
}

func TestSplitEachSpansWithJson(t *testing.T) {
	b, err := os.ReadFile("../../example/fixtures/traces1.json")
	assert.NoError(t, err)
	traces, err := FromJsonToTraces(b)
	assert.NoError(t, err)
	assert.Equal(t, 15, traces.SpanCount())
}
