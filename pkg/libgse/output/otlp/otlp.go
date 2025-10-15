// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package otlp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/elastic/beats/libbeat/outputs"
	"github.com/elastic/beats/libbeat/publisher"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
)

const (
	DefaultDataType      string = "log_v2"
	DefaultOutputType    string = "otlp_trace"
	BkDataTokenFieldName string = "bk.data.token"
)

type Output struct {
	exporter    *otlptrace.Exporter
	bkDataToken string
	dataType    string // log_v1, log_v2
	outputType  string // otlp_trace, otlp_metric
}

func init() {
	outputs.RegisterType("otlp", MakeOutput)
}

func MakeOutput(_ outputs.IndexManager, _ beat.Info, _ outputs.Observer, cfg *common.Config) (outputs.Group, error) {
	c := defaultConfig
	err := cfg.Unpack(&c)
	if err != nil {
		logp.Err("unpack config error:%v", err)
		return outputs.Fail(err)
	}
	output, err := NewOutput(c)
	if err != nil {
		return outputs.Fail(err)
	}
	return outputs.Success(int(c.EventBufferMax), 0, output)
}

func (c *Output) Publish(batch publisher.Batch) error {
	if c.dataType == DefaultDataType && c.outputType == DefaultOutputType {
		snapshots := c.parseTraceData(batch)
		err := pushData(c, snapshots)
		if err != nil {
			return err
		}
		batch.ACK()
		return nil
	}
	batch.ACK()
	return nil
}

func (c *Output) String() string {
	return "otlp"
}

func (c *Output) Close() error {
	return c.exporter.Shutdown(context.Background())
}

func NewExporter(grpcHost string) (*otlptrace.Exporter, error) {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(grpcHost),
		otlptracegrpc.WithReconnectionPeriod(50 * time.Millisecond),
	}
	client := otlptracegrpc.NewClient(opts...)

	return otlptrace.New(context.Background(), client)
}

func NewOutput(c Config) (*Output, error) {
	exp, err := NewExporter(c.GrpcHost)
	if err != nil {
		return nil, err
	}
	output := Output{
		exporter:    exp,
		bkDataToken: c.BkDataToken,
		dataType:    c.DataType,
		outputType:  c.OutputType,
	}
	return &output, nil
}

func pushData(c *Output, snapshots []tracesdk.ReadOnlySpan) error {
	err := c.exporter.ExportSpans(context.Background(), snapshots)
	if err != nil {
		logp.Err("push data err: %v", err)
		return err
	}
	return nil
}

func (c *Output) parseTraceData(batch publisher.Batch) []tracesdk.ReadOnlySpan {
	events := batch.Events()
	roSpans := make([]TraceData, 0)
	for i := range events {
		data := events[i].Content.Fields
		eventItems, err := data.GetValue("items")
		if err != nil {
			logp.Err("parse log data items error: %v", err)
			continue
		}

		items, ok := eventItems.([]common.MapStr)
		if !ok {
			logp.Err("parse log data items to error items:%v", eventItems)
			continue
		}
		for _, item := range items {
			itemData, err := item.GetValue("data")
			if err != nil {
				logp.Err("Failed to get the data in the item. error:%v, item:%v", err, item)
				continue
			}
			log, ok := itemData.(string)
			if !ok {
				logp.Err("Failed to log data to string. log:%v", itemData)
				continue
			}

			var traceData TraceData
			err = json.Unmarshal([]byte(log), &traceData)
			if err != nil {
				logp.Err("parse log to TraceData error. error:%v log:%v", err, log)
				continue
			}
			traceData.Resource[BkDataTokenFieldName] = c.bkDataToken
			roSpans = append(roSpans, traceData)
		}
	}
	spanStubs := SpanStubs(roSpans)
	snapshots := spanStubs.Snapshots()
	return snapshots
}
