// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package flow

import (
	"context"
	"fmt"

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
)

const (
	ProcessorName     = "bk_networkflow"
	FlowTypeSFlow     = "SFLOW_5"
	FlowTypeNetFlowV5 = "NETFLOW_V5"
	FlowTypeNetFlowV9 = "NETFLOW_V9"
	FlowTypeIPFIX     = "IPFIX"
)

var allowedFlowTypes = map[string]struct{}{
	FlowTypeSFlow:     {},
	FlowTypeNetFlowV5: {},
	FlowTypeNetFlowV9: {},
	FlowTypeIPFIX:     {},
}

// The Flow raw ETL intentionally keeps a minimal field set.
// Additional collector/GSE passthrough fields should be introduced only when
// they are required by a confirmed query or troubleshooting scenario.

func newRawDimensionFields() []etl.Field {
	return []etl.Field{
		etl.NewSimpleField("dataid", etl.ExtractByJMESPath("dataid"), etl.TransformNilInt64),
		etl.NewSimpleField("sampler_address", etl.ExtractByJMESPath("sampler_address"), etl.TransformNilString),
		etl.NewSimpleField("src_addr", etl.ExtractByJMESPath("src_addr"), etl.TransformNilString),
		etl.NewSimpleField("dst_addr", etl.ExtractByJMESPath("dst_addr"), etl.TransformNilString),
		etl.NewSimpleField("src_port", etl.ExtractByJMESPath("src_port"), etl.TransformNilInt64),
		etl.NewSimpleField("dst_port", etl.ExtractByJMESPath("dst_port"), etl.TransformNilInt64),
		etl.NewSimpleField("proto", etl.ExtractByJMESPath("proto"), etl.TransformNilString),
		etl.NewSimpleField("in_if", etl.ExtractByJMESPath("in_if"), etl.TransformNilInt64),
		etl.NewSimpleField("out_if", etl.ExtractByJMESPath("out_if"), etl.TransformNilInt64),
		etl.NewSimpleField("etype", etl.ExtractByJMESPath("etype"), etl.TransformNilString),
		etl.NewSimpleField("type", etl.ExtractByJMESPath("type"), etl.TransformNilString),
	}
}

func newRawMetricFields() []etl.Field {
	return []etl.Field{
		etl.NewSimpleField("time_flow_start_ms", etl.ExtractByJMESPath("time_flow_start_ns"), transformMilliTimeStamp),
		etl.NewSimpleField("time_flow_end_ms", etl.ExtractByJMESPath("time_flow_end_ns"), transformMilliTimeStamp),
		etl.NewSimpleField("time_received_ms", etl.ExtractByJMESPath("time_received_ns"), transformMilliTimeStamp),
		etl.NewSimpleField("bytes", etl.ExtractByJMESPath("bytes"), etl.TransformNilInt64),
		etl.NewSimpleField("packets", etl.ExtractByJMESPath("packets"), etl.TransformNilInt64),
		etl.NewSimpleField("sampling_rate", etl.ExtractByJMESPath("sampling_rate"), etl.TransformNilInt64),
		etl.NewFutureField("stat_time", func(name string, from etl.Container, to etl.Container) error {
			statTime, err := deriveStatTimeFromSource(from)
			if err != nil {
				return err
			}
			return to.Put(name, statTime)
		}),
		etl.NewFutureField("@timestamp", func(name string, from etl.Container, to etl.Container) error {
			val, err := to.Get("stat_time")
			if err != nil {
				return err
			}
			return to.Put(name, val)
		}),
		etl.NewFutureField("flow_bytes", func(name string, from etl.Container, to etl.Container) error {
			val, err := deriveAmplifiedFieldFromSource(from, "bytes")
			if err != nil {
				return err
			}
			return to.Put(name, val)
		}),
		etl.NewFutureField("flow_packets", func(name string, from etl.Container, to etl.Container) error {
			val, err := deriveAmplifiedFieldFromSource(from, "packets")
			if err != nil {
				return err
			}
			return to.Put(name, val)
		}),
	}
}

// NewNetworkFlowProcessor builds the raw-only Flow ETL schema.
func NewNetworkFlowProcessor(ctx context.Context, name string) *template.RecordProcessor {
	return template.NewRecordProcessorWithContext(
		ctx,
		name,
		config.PipelineConfigFromContext(ctx),
		etl.NewTSSchemaRecord(name).
			AddTime(etl.NewFunctionField("time", func(name string, from etl.Container, to etl.Container) error {
				statTime, err := deriveStatTimeFromSource(from)
				if err != nil {
					return err
				}
				return to.Put(name, statTime)
			})).
			AddDimensions(newRawDimensionFields()...).
			AddMetrics(newRawMetricFields()...),
	)
}

func deriveStatTimeFromSource(from etl.Container) (interface{}, error) {
	endRaw, err := from.Get("time_flow_end_ns")
	if err == nil {
		endNanos, convErr := conv.DefaultConv.Int64(endRaw)
		if convErr == nil && endNanos > 0 {
			return endNanos / 1e6, nil
		}
	}

	receivedRaw, err := from.Get("time_received_ns")
	if err != nil {
		return nil, err
	}
	receivedNanos, convErr := conv.DefaultConv.Int64(receivedRaw)
	if convErr != nil {
		return nil, convErr
	}
	return receivedNanos / 1e6, nil
}

func transformMilliTimeStamp(value interface{}) (interface{}, error) {
	nanos, err := conv.DefaultConv.Int64(value)
	if err != nil {
		return nil, err
	}
	return nanos / 1e6, nil
}

func deriveAmplifiedFieldFromSource(from etl.Container, name string) (interface{}, error) {
	value, err := getInt64Field(from, name)
	if err != nil {
		return nil, err
	}

	flowType, err := getStringField(from, "type")
	if err != nil {
		return nil, err
	}

	if err = validateFlowType(flowType); err != nil {
		return nil, err
	}

	if flowType != FlowTypeSFlow {
		return value, nil
	}

	samplingRate, err := getInt64Field(from, "sampling_rate")
	if err != nil {
		return nil, err
	}

	return value * samplingRate, nil
}

func getInt64Field(container etl.Container, name string) (int64, error) {
	value, err := container.Get(name)
	if err != nil {
		return 0, err
	}

	return conv.DefaultConv.Int64(value)
}

func getStringField(container etl.Container, name string) (string, error) {
	value, err := container.Get(name)
	if err != nil {
		return "", err
	}

	return conv.DefaultConv.String(value)
}

func validateFlowType(flowType string) error {
	if _, ok := allowedFlowTypes[flowType]; ok {
		return nil
	}

	return fmt.Errorf("unsupported flow type %q", flowType)
}

func init() {
	define.RegisterDataProcessor(ProcessorName, func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}

		return NewNetworkFlowProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
