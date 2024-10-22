// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package forwarder

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pkg/generator"
)

func TestPicker(t *testing.T) {
	picker := NewPicker()
	picker.AddMember(":1001")
	picker.AddMember(":1002")

	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
	})

	for i := 0; i < 10; i++ {
		traces := g.Generate()
		ep, err := picker.PickTraces(traces)
		assert.NoError(t, err)
		t.Logf("pick member: %v", ep)
	}

	picker.RemoveMember(":1001")
	for i := 0; i < 10; i++ {
		traces := g.Generate()
		ep, err := picker.PickTraces(traces)
		assert.NoError(t, err)
		assert.Equal(t, ":1002", ep)
	}
}

func TestPickerFailed(t *testing.T) {
	t.Run("NoMembers", func(t *testing.T) {
		picker := NewPicker()
		g := generator.NewTracesGenerator(define.TracesOptions{
			SpanCount: 10,
		})

		traces := g.Generate()
		ep, err := picker.PickTraces(traces)
		assert.Empty(t, ep)
		assert.Equal(t, "no member found", err.Error())
	})

	t.Run("NoScopeSpans", func(t *testing.T) {
		picker := NewPicker()
		g := generator.NewTracesGenerator(define.TracesOptions{
			SpanCount: 0,
		})

		traces := g.Generate()
		ep, err := picker.PickTraces(traces)
		assert.Empty(t, ep)
		assert.Equal(t, "empty scope spans", err.Error())
	})
}
