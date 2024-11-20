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
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

type LogsGenerator struct {
	opts define.LogsOptions

	attributes pcommon.Map
	resources  pcommon.Map
}

func NewLogsGenerator(opts define.LogsOptions) *LogsGenerator {
	attributes := random.AttributeMap(opts.RandomAttributeKeys, opts.DimensionsValueType)
	resources := random.AttributeMap(opts.RandomResourceKeys, opts.DimensionsValueType)
	return &LogsGenerator{
		attributes: attributes,
		resources:  resources,
		opts:       opts,
	}
}

func (g *LogsGenerator) Generate() plog.Logs {
	pdLogs := plog.NewLogs()
	rs := pdLogs.ResourceLogs().AppendEmpty()
	rs.Resource().Attributes().UpsertString("service.name", "generator.service")
	rs.Resource().Attributes().UpsertString("bk.data.token", "generator.data.token")

	g.resources.CopyTo(rs.Resource().Attributes())
	for k, v := range g.opts.Resources {
		rs.Resource().Attributes().UpsertString(k, v)
	}

	now := time.Now()
	for i := 0; i < g.opts.LogCount; i++ {
		log := rs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
		log.SetSpanID(random.SpanID())
		log.SetTraceID(random.TraceID())
		log.SetTimestamp(pcommon.NewTimestampFromTime(now))
		log.Body().SetStringVal(random.String(g.opts.LogLength))
		g.attributes.CopyTo(log.Attributes())
		for k, v := range g.opts.Attributes {
			log.Attributes().UpsertString(k, v)
		}
	}

	return pdLogs
}
