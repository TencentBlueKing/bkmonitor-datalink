// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package metrics

import "github.com/prometheus/client_golang/prometheus"

const metricMetadataNamespace = "bmw_metricmetadata"

var (
	MetricMetadataMessages        = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricMetadataNamespace, Name: "messages_total", Help: "Metadata messages by processing outcome."}, []string{"data_id", "result"})
	MetricMetadataRecords         = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricMetadataNamespace, Name: "records_total", Help: "Metadata records by outcome or rejection reason."}, []string{"data_id", "result"})
	MetricMetadataScopes          = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricMetadataNamespace, Name: "scope_changes_total", Help: "Committed reporting scope changes."}, []string{"data_id", "change"})
	MetricMetadataStorageErrors   = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricMetadataNamespace, Name: "storage_errors_total", Help: "Metadata database failures by operation."}, []string{"data_id", "operation"})
	MetricMetadataRefreshDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricMetadataNamespace, Name: "refresh_duration_seconds", Help: "Metadata state refresh duration by outcome.", Buckets: prometheus.DefBuckets}, []string{"data_id", "result"})
	MetricMetadataBatchDuration   = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricMetadataNamespace, Name: "batch_duration_seconds", Help: "Metadata batch persistence duration.", Buckets: prometheus.DefBuckets}, []string{"data_id"})
	MetricMetadataBatchRecords    = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: metricMetadataNamespace, Name: "batch_records", Help: "Records per metadata persistence batch.", Buckets: []float64{1, 10, 100, 1000, 5000, 10000, 30000}}, []string{"data_id"})
	MetricMetadataQueueMessages   = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: metricMetadataNamespace, Name: "queue_messages", Help: "Queued and processing metadata messages."}, []string{"data_id"})
	MetricMetadataQueueBytes      = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: metricMetadataNamespace, Name: "queue_bytes", Help: "Estimated queued and processing Kafka message memory bytes."}, []string{"data_id"})
	MetricMetadataCache           = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: metricMetadataNamespace, Name: "cache_size", Help: "Retained metadata cache scopes, members and estimated bytes."}, []string{"data_id", "kind"})
	MetricMetadataEvictions       = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: metricMetadataNamespace, Name: "cache_evictions_total", Help: "Metadata cache invalidations."}, []string{"data_id", "reason"})
)

func init() {
	Registry.MustRegister(MetricMetadataMessages, MetricMetadataRecords, MetricMetadataScopes, MetricMetadataStorageErrors, MetricMetadataRefreshDuration, MetricMetadataBatchDuration, MetricMetadataBatchRecords, MetricMetadataQueueMessages, MetricMetadataQueueBytes, MetricMetadataCache, MetricMetadataEvictions)
}
