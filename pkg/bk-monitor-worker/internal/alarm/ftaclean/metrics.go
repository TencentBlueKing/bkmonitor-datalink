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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
)

var factory = promauto.With(metrics.Registry)

var inputs = factory.NewCounterVec(prometheus.CounterOpts{Name: "bmw_fta_clean_input_messages_total", Help: "Kafka input messages acknowledged for offset tracking."}, []string{"data_id"})

var outputs = factory.NewCounterVec(prometheus.CounterOpts{Name: "bmw_fta_clean_output_messages_total", Help: "Kafka output messages acknowledged by the broker."}, []string{"data_id"})

var writeFailures = factory.NewCounterVec(prometheus.CounterOpts{Name: "bmw_fta_clean_write_failures_total", Help: "Failed Kafka output batches, including partial failures."}, []string{"data_id"})

var latency = factory.NewHistogramVec(prometheus.HistogramOpts{Name: "bmw_fta_clean_batch_duration_seconds", Help: "Batch duration including output acknowledgements."}, []string{"data_id"})

var lag = factory.NewGaugeVec(prometheus.GaugeOpts{Name: "bmw_fta_clean_partition_lag", Help: "High water mark minus the next offset after an acknowledged batch."}, []string{"data_id", "partition"})

// Slot metrics are process-wide because every shard borrows from the same pool.
var slotsUsed = factory.NewGauge(prometheus.GaugeOpts{Name: "bmw_fta_clean_slots_in_use", Help: "Decode and transform chunks currently holding a process slot."})

var slotsLimit = factory.NewGauge(prometheus.GaugeOpts{Name: "bmw_fta_clean_slots_limit", Help: "Configured cleaning slots in this worker."})

var slotWait = factory.NewHistogramVec(prometheus.HistogramOpts{Name: "bmw_fta_clean_slot_wait_seconds", Help: "Time per successful slot acquisition, including uncontended acquisitions.", Buckets: []float64{.00001, .0001, .001, .005, .01, .05, .1, .5, 1, 5}}, []string{"data_id"})

var flushes = factory.NewCounterVec(prometheus.CounterOpts{Name: "bmw_fta_clean_batches_total", Help: "Input batches by flush trigger, before processing."}, []string{"reason"})

var batchSize = factory.NewHistogram(prometheus.HistogramOpts{Name: "bmw_fta_clean_batch_messages", Help: "Number of input Kafka messages per flushed batch.", Buckets: prometheus.ExponentialBuckets(1, 2, 12)})

var expanded = factory.NewCounterVec(prometheus.CounterOpts{Name: "bmw_fta_clean_expanded_events_total", Help: "Events after decoding and flat-batch expansion."}, []string{"data_id"})

var discarded = factory.NewCounterVec(prometheus.CounterOpts{Name: "bmw_fta_clean_discards_total", Help: "Decode errors count input messages, flatten errors count events, remaining reasons count result-table records."}, []string{"data_id", "reason"})
