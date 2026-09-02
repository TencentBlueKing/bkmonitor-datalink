// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	receiptDeliveryQueued  = "queued"
	receiptDeliveryACKed   = "acked"
	receiptDeliveryDropped = "dropped"
)

var receiptBusinessFields = []string{
	"received", "selected", "processed", "normal", "abnormal", "recovery",
	"unavailable", "terminal", "events", "level_terminal_affected",
}

var receiptStatuses = []string{
	contract.ReceiptStatusCompleted,
	contract.ReceiptStatusCompletedWithTerminal,
	contract.ReceiptStatusRejected,
}

type receiptMetrics struct {
	status   *prometheus.CounterVec
	business *prometheus.CounterVec
	delivery *prometheus.CounterVec
}

func newReceiptMetrics() receiptMetrics {
	status := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "message_receipt_status_total",
			Help:      "Cumulative validated MessageReceipts by contract status.",
		},
		[]string{"status"},
	)
	business := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "message_receipt_business_total",
			Help: "Cumulative validated MessageReceipt business counts. received=Record; selected, processed, normal, " +
				"abnormal, recovery, unavailable, terminal and level_terminal_affected=Plan x Record; events=Event. " +
				"level_terminal_affected is orthogonal to count conservation.",
		},
		[]string{"field"},
	)
	delivery := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: metricNamespace,
			Subsystem: metricSubsystem,
			Name:      "message_receipt_delivery_total",
			Help: "Cumulative best-effort MessageReceipt delivery lifecycle events. Outcomes occur at different times " +
				"and do not form a strict cross-window conservation equation.",
		},
		[]string{"outcome"},
	)
	for _, value := range receiptStatuses {
		status.WithLabelValues(value).Add(0)
	}
	for _, field := range receiptBusinessFields {
		business.WithLabelValues(field).Add(0)
	}
	for _, outcome := range []string{receiptDeliveryQueued, receiptDeliveryACKed, receiptDeliveryDropped} {
		delivery.WithLabelValues(outcome).Add(0)
	}
	return receiptMetrics{status: status, business: business, delivery: delivery}
}

func (metrics receiptMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{metrics.status, metrics.business, metrics.delivery}
}

// RecordValidatedMessageReceipt requires a Receipt accepted by the official
// contract encoder or validator. Keeping validation at the publisher boundary
// avoids repeating it on the normal path.
func (r *Recorder) RecordValidatedMessageReceipt(receipt *contract.MessageReceiptV1) {
	if r == nil || r.receipts.status == nil || r.receipts.business == nil || receipt == nil ||
		!knownReceiptStatus(receipt.Status) {
		return
	}
	r.receipts.status.WithLabelValues(receipt.Status).Inc()
	var normal, abnormal, recovery uint64
	for _, plan := range receipt.PerPlan {
		normal += plan.Normal
		abnormal += plan.Abnormal
		recovery += plan.Recovery
	}
	values := [...]uint64{
		receipt.Counts.Received, receipt.Counts.Selected, receipt.Counts.Processed,
		normal, abnormal, recovery,
		receipt.Counts.Unavailable, receipt.Counts.Terminal, receipt.Counts.Events,
		receipt.Counts.LevelTerminalAffected,
	}
	for index, value := range values {
		if value > 0 {
			r.receipts.business.WithLabelValues(receiptBusinessFields[index]).Add(float64(value))
		}
	}
}

func (r *Recorder) RecordMessageReceiptQueued(count uint64) {
	r.recordMessageReceiptDelivery(receiptDeliveryQueued, count)
}

func (r *Recorder) RecordMessageReceiptACKed(count uint64) {
	r.recordMessageReceiptDelivery(receiptDeliveryACKed, count)
}

func (r *Recorder) RecordMessageReceiptDropped(count uint64) {
	r.recordMessageReceiptDelivery(receiptDeliveryDropped, count)
}

func (r *Recorder) recordMessageReceiptDelivery(outcome string, count uint64) {
	if r == nil || r.receipts.delivery == nil || count == 0 {
		return
	}
	r.receipts.delivery.WithLabelValues(outcome).Add(float64(count))
}

func receiptCustomSeries() int {
	return len(receiptStatuses) + len(receiptBusinessFields) + 3
}

func knownReceiptStatus(status string) bool {
	return status == contract.ReceiptStatusCompleted || status == contract.ReceiptStatusCompletedWithTerminal ||
		status == contract.ReceiptStatusRejected
}
