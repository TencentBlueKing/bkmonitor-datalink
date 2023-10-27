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
	"github.com/elastic/beats/libbeat/common"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type logsEvent struct {
	define.CommonEvent
}

func (e logsEvent) RecordType() define.RecordType {
	return define.RecordLogs
}

var LogsConverter EventConverter = logsConverter{}

type logsConverter struct{}

func (c logsConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return logsEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c logsConverter) ToDataID(record *define.Record) int32 {
	return record.Token.LogsDataId
}

func (c logsConverter) Convert(record *define.Record, f define.GatherFunc) {
	pdLogs := record.Data.(plog.Logs)
	resourceLogsSlice := pdLogs.ResourceLogs()
	if resourceLogsSlice.Len() == 0 {
		return
	}
	dataId := c.ToDataID(record)

	for i := 0; i < resourceLogsSlice.Len(); i++ {
		resourceLogs := resourceLogsSlice.At(i)
		rsAttrs := resourceLogs.Resource().Attributes().AsRaw()
		scopeLogsSlice := resourceLogs.ScopeLogs()
		events := make([]define.Event, 0)
		for j := 0; j < scopeLogsSlice.Len(); j++ {
			logRecordSlice := scopeLogsSlice.At(j).LogRecords()
			for k := 0; k < logRecordSlice.Len(); k++ {
				content, err := c.Extract(record.RequestClient.IP, logRecordSlice.At(k), rsAttrs)
				if err != nil {
					DefaultMetricMonitor.IncConverterFailedCounter(define.RecordLogs, dataId)
					logger.Warnf("failed to extract content: %v", err)
					continue
				}
				events = append(events, c.ToEvent(record.Token, dataId, content))
			}
		}
		if len(events) > 0 {
			f(events...)
		}
	}
}

func (c logsConverter) Extract(ip string, logRecord plog.LogRecord, rsAttrs common.MapStr) (common.MapStr, error) {
	m := common.MapStr{
		"time_unix":       logRecord.Timestamp() / 1000,
		"span_id":         logRecord.SpanID().HexString(),
		"trace_id":        logRecord.TraceID().HexString(),
		"attributes":      CleanAttributesMap(logRecord.Attributes().AsRaw()),
		"body":            logRecord.Body().AsString(),
		"flags":           logRecord.Flags(),
		"severity_number": logRecord.SeverityNumber(),
		"severity_text":   logRecord.SeverityText(),
		"resource":        rsAttrs,
	}
	content, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

	return common.MapStr{
		"data":   string(content),
		"source": ip,
	}, nil
}
