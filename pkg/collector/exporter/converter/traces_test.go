// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
)

var attributeKeys = []string{
	"http.method",
	"http.status_code",
	"http.response_content_length",
	"net.peer.name",
	"net.peer.ip",
	"net.peer.port",
}

var resourceKeys = []string{
	"bk.instance.id",
	"bk.data.token",
}

// cmd: go test -bench=. -cpu 1,2,4,8,16 -benchmem

func makeTracesGenerator(n int) *generator.TracesGenerator {
	opts := define.TracesOptions{
		SpanCount:  n,
		EventCount: n,
		LinkCount:  n,
	}
	opts.RandomAttributeKeys = attributeKeys
	opts.RandomResourceKeys = resourceKeys
	return generator.NewTracesGenerator(opts)
}

func TestTracesRandom(t *testing.T) {
	g := makeTracesGenerator(2)
	traces := g.Generate()

	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       traces,
	}

	events := make([]define.Event, 0)
	gather := func(evts ...define.Event) {
		for i := 0; i < len(evts); i++ {
			evt := evts[i]
			assert.Equal(t, define.RecordTraces, evt.RecordType())
			events = append(events, evt)
		}
	}
	TestConverter.Convert(&record, gather)
	assert.Equal(t, len(events), 2)
	assert.NotEqual(t, events[0].Data()["trace_id"], events[1].Data()["trace_id"])
	assert.NotEqual(t, events[0].Data()["span_id"], events[1].Data()["span_id"])
}

func BenchmarkTracesConvert_10_Span(b *testing.B) {
	g := makeTracesGenerator(10)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		TracesConverter.Convert(&record, gather)
	}
}

func BenchmarkTracesConvert_100_Span(b *testing.B) {
	g := makeTracesGenerator(100)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		TracesConverter.Convert(&record, gather)
	}
}

func BenchmarkTracesConvert_1000_Span(b *testing.B) {
	g := makeTracesGenerator(1000)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		TracesConverter.Convert(&record, gather)
	}
}

func BenchmarkTracesConvert_10000_Span(b *testing.B) {
	g := makeTracesGenerator(10000)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordTraces,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		TracesConverter.Convert(&record, gather)
	}
}
