// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package alias

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
)

func TestGetAttributes(t *testing.T) {
	t.Run("FuncContact", func(t *testing.T) {
		g := generator.NewTracesGenerator(define.TracesOptions{
			GeneratorOptions: define.GeneratorOptions{
				Attributes: map[string]string{
					"client.address": "localhost",
					"client.port":    "8080",
				},
			},
			SpanCount: 2,
			SpanKind:  2,
		})
		data := g.Generate()

		mgr := New()
		mgr.Register(
			KF{K: serverKind("net.peer.name"), F: FuncContact("client.address", "client.port", ":")},
		)

		var n int
		foreach.Spans(data, func(span ptrace.Span) {
			v, _ := mgr.GetAttributes(span, "net.peer.name")
			assert.Equal(t, "localhost:8080", v)
			n++
		})
		assert.Equal(t, 2, n)
	})

	t.Run("FuncOr", func(t *testing.T) {
		g := generator.NewTracesGenerator(define.TracesOptions{
			GeneratorOptions: define.GeneratorOptions{
				Attributes: map[string]string{
					"network.peer.address": "localhost",
				},
			},
			SpanCount: 2,
			SpanKind:  3,
		})
		data := g.Generate()

		mgr := New()
		mgr.Register(
			KF{K: clientKind("net.peer.ip"), F: FuncOr("server.address", "network.peer.address")},
		)

		var n int
		foreach.Spans(data, func(span ptrace.Span) {
			v, _ := mgr.GetAttributes(span, "net.peer.ip")
			assert.Equal(t, "localhost", v)
			n++
		})
		assert.Equal(t, 2, n)
	})
}
