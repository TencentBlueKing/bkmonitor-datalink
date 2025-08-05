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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
)

func makeLogsGenerator(count, length int) *generator.LogsGenerator {
	opts := define.LogsOptions{
		LogCount:  count,
		LogLength: length,
	}
	opts.RandomAttributeKeys = attributeKeys
	opts.RandomResourceKeys = resourceKeys
	return generator.NewLogsGenerator(opts)
}

func TestLogsRandom(t *testing.T) {
	g := makeLogsGenerator(2, 20)
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	events := make([]define.Event, 0)
	gather := func(evts ...define.Event) {
		for i := 0; i < len(evts); i++ {
			evt := evts[i]
			assert.Equal(t, define.RecordLogs, evt.RecordType())
			events = append(events, evt)
		}
	}

	TestConverter.Convert(&record, gather)
	assert.Len(t, events, 2)
}

func TestLogsTime(t *testing.T) {
	assertLogsTime := func(t *testing.T, data plog.Logs) {
		record := define.Record{
			RecordType: define.RecordLogs,
			Data:       data,
		}

		gather := func(evts ...define.Event) {
			s := evts[0].Data().String()
			assert.True(t, strings.Contains(s, "10000000"))
		}
		TestConverter.Convert(&record, gather)
	}

	g := makeLogsGenerator(1, 20)
	ts := pcommon.Timestamp(10000000000)

	t.Run("Timestamp", func(t *testing.T) {
		data := g.Generate()
		log := testkits.FirstLogRecord(data)
		log.SetTimestamp(ts)
		log.SetObservedTimestamp(0)
		assertLogsTime(t, data)
	})

	t.Run("ObservedTimestamp", func(t *testing.T) {
		data := g.Generate()
		log := testkits.FirstLogRecord(data)
		log.SetTimestamp(0)
		log.SetObservedTimestamp(ts)
		assertLogsTime(t, data)
	})
}

func TestLogsEscapeHTML(t *testing.T) {
	g := makeLogsGenerator(1, 20)
	data := g.Generate()

	const body = "<html>&<tag>"

	first := testkits.FirstLogRecord(data)
	first.Body().SetStringVal(body)

	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	gather := func(evts ...define.Event) {
		for i := 0; i < len(evts); i++ {
			evt := evts[i]
			assert.Equal(t, define.RecordLogs, evt.RecordType())
			assert.Contains(t, evt.Data()["data"], body)
		}
	}

	TestConverter.Convert(&record, gather)
}

func BenchmarkLogsConvert_10x1KB_LogRecords(b *testing.B) {
	g := makeLogsGenerator(10, 1024) // 1KB
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		LogsConverter.Convert(&record, gather)
	}
}

func BenchmarkLogsConvert_10x10KB_LogRecords(b *testing.B) {
	g := makeLogsGenerator(10, 10240) // 10KB
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		LogsConverter.Convert(&record, gather)
	}
}

func BenchmarkLogsConvert_10x100KB_LogRecords(b *testing.B) {
	g := makeLogsGenerator(10, 102400) // 100KB
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		LogsConverter.Convert(&record, gather)
	}
}

func BenchmarkLogsConvert_100x1KB_LogRecords(b *testing.B) {
	g := makeLogsGenerator(100, 1024) // 1KB
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		LogsConverter.Convert(&record, gather)
	}
}

func BenchmarkLogsConvert_100x10KB_LogRecords(b *testing.B) {
	g := makeLogsGenerator(100, 10240) // 10KB
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		LogsConverter.Convert(&record, gather)
	}
}

func BenchmarkLogsConvert_100x100KB_LogRecords(b *testing.B) {
	g := makeLogsGenerator(100, 102400) // 100KB
	data := g.Generate()
	record := define.Record{
		RecordType: define.RecordLogs,
		Data:       data,
	}

	gather := func(evts ...define.Event) {}
	for i := 0; i < b.N; i++ {
		LogsConverter.Convert(&record, gather)
	}
}
