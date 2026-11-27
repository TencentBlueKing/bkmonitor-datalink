// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ftaclean

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Shopify/sarama"
	"golang.org/x/time/rate"

	tc "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl/fta"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type table struct {
	record  *etl.FunctionalRecord
	targets []destination
}

type destination struct {
	cluster uint
	topic   string
}

type pipeline struct {
	recordErrors     *rate.Limiter
	restarts         failureWindow
	dropEmptyMetrics bool
	id               int
	decoder          *etl.PayloadDecoder
	flatten          *etl.FunctionalRecord
	tables           []table
	slots            slots
}

func compile(spec *tc.PipelineConfig, capacity slots) (*pipeline, error) {
	tc.InitFTAPipelineOptions(spec)
	field, ok := spec.Option[tc.PipelineConfigOptFlatBatchKey].(string)
	if !ok {
		return nil, fmt.Errorf("invalid flat batch field for DataID %d", spec.DataID)
	}
	decode, flat := etl.FlatBatch(field, true)
	p := &pipeline{id: spec.DataID, decoder: decode, flatten: flat, slots: capacity, recordErrors: rate.NewLimiter(rate.Every(time.Minute), 1)}
	p.dropEmptyMetrics, _ = utils.NewMapHelper(spec.Option).GetBool(tc.PipelineConfigDropEmptyMetrics)
	for _, rt := range spec.ResultTableList {
		record, err := fta.NewAlertRecord(rt.ResultTable, spec.Option, rt)
		if err != nil {
			return nil, err
		}
		t := table{record: record}
		for _, ship := range rt.ShipperList {
			if ship.ClusterType != "kafka" {
				return nil, fmt.Errorf("FTA result table %s requires a Kafka destination", rt.ResultTable)
			}
			target, err := readEndpoint(ship)
			if err != nil {
				return nil, err
			}
			t.targets = append(t.targets, destination{target.cluster.ClusterId, target.topic})
		}
		if len(t.targets) > 0 {
			p.tables = append(p.tables, t)
		}
	}
	if len(p.tables) == 0 {
		return nil, fmt.Errorf("no enabled Kafka result table for DataID %d", spec.DataID)
	}
	return p, nil
}

type cleanedEvent struct {
	table int
	value []byte
}

func (p *pipeline) clean(ctx context.Context, msgs []*sarama.ConsumerMessage) (map[uint][]*sarama.ProducerMessage, error) {
	id := strconv.Itoa(p.id)
	wait := slotWait.WithLabelValues(id)
	decoded := make([][]etl.Container, len(msgs))
	err := p.slots.run(ctx, len(msgs), 4, wait, func(start, end int) {
		for i := start; i < end; i++ {
			events, err := p.decoder.Decode(define.NewJSONPayloadFrom(msgs[i].Value, 0))
			if err != nil {
				discarded.WithLabelValues(id, "decode").Inc()
				if p.recordErrors.Allow() {
					logger.Warnf("FTA DataID %d decode failed at partition %d offset %d", p.id, msgs[i].Partition, msgs[i].Offset)
				}
				continue
			}
			decoded[i] = events
		}
	})
	if err != nil {
		return nil, err
	}
	var events []etl.Container
	for _, items := range decoded {
		events = append(events, items...)
	}
	expanded.WithLabelValues(id).Add(float64(len(events)))
	rows := make([][]cleanedEvent, len(events))
	err = p.slots.run(ctx, len(events), 16, wait, func(start, end int) {
		for i := start; i < end; i++ {
			flat := etl.NewMapContainer()
			if err := p.flatten.Transform(events[i], flat); err != nil {
				discarded.WithLabelValues(id, "flatten").Inc()
				continue
			}
			for j, t := range p.tables {
				result := etl.NewMapContainer()
				if err := t.record.Transform(flat, result); err != nil {
					discarded.WithLabelValues(id, "transform").Inc()
					continue
				}
				if skipOutput(result, p.dropEmptyMetrics) {
					discarded.WithLabelValues(id, "filtered").Inc()
					continue
				}
				value, err := json.Marshal(result)
				if err != nil {
					discarded.WithLabelValues(id, "encode").Inc()
					continue
				}
				rows[i] = append(rows[i], cleanedEvent{j, value})
			}
		}
	})
	if err != nil {
		return nil, err
	}
	outputs := make(map[uint][]*sarama.ProducerMessage)
	for _, row := range rows {
		for _, event := range row {
			for _, dest := range p.tables[event.table].targets {
				outputs[dest.cluster] = append(outputs[dest.cluster], &sarama.ProducerMessage{Topic: dest.topic, Key: sarama.StringEncoder(""), Value: sarama.ByteEncoder(event.value)})
			}
		}
	}
	return outputs, nil
}

// Kafka's Transfer backend skips records with exclusively nil metrics. FTA
// normally emits flat alerts without a metrics field.
func skipOutput(row etl.MapContainer, dropEmpty bool) bool {
	metrics, ok := row["metrics"].(map[string]any)
	if !ok || len(metrics) == 0 {
		return dropEmpty
	}
	for _, v := range metrics {
		if v != nil {
			return false
		}
	}
	return true
}
