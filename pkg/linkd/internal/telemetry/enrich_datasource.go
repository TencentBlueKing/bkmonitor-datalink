// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

const (
	enrichDataSourceCWStrategy      = "cw_strategy"
	enrichDataSourceBusiness        = "business"
	enrichDataSourceMetricLibrary   = "metric_library"
	enrichDataSourceModel           = "onemodel_model"
	enrichDataSourceAlarmSource     = "alarm_source"
	enrichDataSourceCollectConfig   = "collect_config"
	enrichDataSourceCollectTopology = "collect_topology"
	enrichDataSourceUptime          = "uptime"
	enrichDataSourceOneModel        = "onemodel"
)

// ObserveEnrichSources 为全部已配置 Reader 增加调用结果和耗时指标。
func (r *Runtime) ObserveEnrichSources(sources enrich.Sources) enrich.Sources {
	if r == nil || r.metrics == nil {
		return sources
	}
	if sources.CWStrategy != nil {
		sources.CWStrategy = &observedCWStrategyReader{next: sources.CWStrategy, metrics: r.metrics}
	}
	if sources.Business != nil {
		sources.Business = &observedBusinessReader{next: sources.Business, metrics: r.metrics}
	}
	if sources.Metric != nil {
		sources.Metric = &observedMetricReader{next: sources.Metric, metrics: r.metrics}
	}
	if sources.Model != nil {
		sources.Model = &observedModelReader{next: sources.Model, metrics: r.metrics}
	}
	if sources.AlarmSource != nil {
		sources.AlarmSource = &observedAlarmSourceReader{next: sources.AlarmSource, metrics: r.metrics}
	}
	if sources.OneModel != nil {
		sources.OneModel = &observedOneModelReader{next: sources.OneModel, metrics: r.metrics}
	}
	if sources.CollectConfig != nil {
		sources.CollectConfig = &observedCollectConfigReader{next: sources.CollectConfig, metrics: r.metrics}
	}
	if sources.CollectTopology != nil {
		sources.CollectTopology = &observedCollectTopologyReader{next: sources.CollectTopology, metrics: r.metrics}
	}
	if sources.Uptime != nil {
		sources.Uptime = &observedUptimeReader{next: sources.Uptime, metrics: r.metrics}
	}
	if sources.UptimeNode != nil {
		sources.UptimeNode = &observedUptimeNodeReader{next: sources.UptimeNode, metrics: r.metrics}
	}
	if sources.Test != nil {
		sources.Test = &observedTestSource{next: sources.Test, metrics: r.metrics}
	}
	return sources
}

type observedTestSource struct {
	next    enrich.TestSource
	metrics *instruments
}

func (r *observedTestSource) Call(ctx context.Context, request enrich.TestRequest) error {
	startedAt := time.Now()
	err := r.next.Call(ctx, request)
	enrichDataSourceRecorder{r.metrics}.record(ctx, "test", "call", startedAt, err == nil, err)
	return err
}

type enrichDataSourceRecorder struct{ metrics *instruments }

func (r enrichDataSourceRecorder) record(ctx context.Context, source, operation string, startedAt time.Time, found bool, err error) {
	attributes := metric.WithAttributes(
		attribute.String("linkd.datasource", source),
		attribute.String("linkd.operation", operation),
		attribute.String("linkd.outcome", enrichDataSourceOutcome(found, err)),
	)
	r.metrics.enrichDatasourceOperations.Add(ctx, 1, attributes)
	r.metrics.enrichDatasourceDuration.Record(ctx, time.Since(startedAt).Seconds(), attributes)
}

func enrichDataSourceOutcome(found bool, err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case errors.Is(err, enrich.ErrInvalidDataSourceResponse):
		return "invalid_response"
	case err != nil:
		return "failed"
	case found:
		return "found"
	default:
		return "not_found"
	}
}

type observedCWStrategyReader struct {
	next    enrich.CWStrategyReader
	metrics *instruments
}

func (r *observedCWStrategyReader) GetByBKStrategyID(ctx context.Context, tenantID string, strategyID int64) (models.CWStrategy, bool, error) {
	startedAt := time.Now()
	value, found, err := r.next.GetByBKStrategyID(ctx, tenantID, strategyID)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceCWStrategy, "get_by_bk_strategy_id", startedAt, found, err)
	return value, found, err
}

type observedBusinessReader struct {
	next    enrich.BusinessReader
	metrics *instruments
}

func (r *observedBusinessReader) IsGlobalBusiness(ctx context.Context, tenantID string, bizID int64) (bool, bool, error) {
	startedAt := time.Now()
	isGlobal, found, err := r.next.IsGlobalBusiness(ctx, tenantID, bizID)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceBusiness, "is_global_business", startedAt, found, err)
	return isGlobal, found, err
}

type observedMetricReader struct {
	next    enrich.MetricReader
	metrics *instruments
}

func (r *observedMetricReader) FindMetricLibrary(ctx context.Context, query models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	startedAt := time.Now()
	value, found, err := r.next.FindMetricLibrary(ctx, query)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceMetricLibrary, "find_metric_library", startedAt, found, err)
	return value, found, err
}

type observedModelReader struct {
	next    enrich.ModelReader
	metrics *instruments
}

func (r *observedModelReader) GetModelByCode(ctx context.Context, tenantID, modelCode string) (enrich.Model, bool, error) {
	startedAt := time.Now()
	value, found, err := r.next.GetModelByCode(ctx, tenantID, modelCode)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceModel, "get_model_by_code", startedAt, found, err)
	return value, found, err
}

type observedAlarmSourceReader struct {
	next    enrich.AlarmSourceReader
	metrics *instruments
}

func (r *observedAlarmSourceReader) GetAlarmSourceName(ctx context.Context, tenantID, sourceID string) (string, bool, error) {
	startedAt := time.Now()
	value, found, err := r.next.GetAlarmSourceName(ctx, tenantID, sourceID)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceAlarmSource, "get_alarm_source_name", startedAt, found, err)
	return value, found, err
}

type observedOneModelReader struct {
	next    enrich.OneModelReader
	metrics *instruments
}

func (r *observedOneModelReader) FindInstance(ctx context.Context, tenantID string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	startedAt := time.Now()
	value, found, err := r.next.FindInstance(ctx, tenantID, query)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceOneModel, "find_instance", startedAt, found, err)
	return value, found, err
}

type observedCollectConfigReader struct {
	next    enrich.CollectConfigReader
	metrics *instruments
}

func (r *observedCollectConfigReader) GetCollectConfig(ctx context.Context, tenantID, taskID string) (models.CollectConfig, bool, error) {
	startedAt := time.Now()
	value, found, err := r.next.GetCollectConfig(ctx, tenantID, taskID)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceCollectConfig, "get_collect_config", startedAt, found, err)
	return value, found, err
}

type observedCollectTopologyReader struct {
	next    enrich.CollectTopologyReader
	metrics *instruments
}

func (r *observedCollectTopologyReader) FindRelatedHost(ctx context.Context, tenantID, modelCode, instanceID, relation string) (enrich.Instance, bool, error) {
	startedAt := time.Now()
	value, found, err := r.next.FindRelatedHost(ctx, tenantID, modelCode, instanceID, relation)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceCollectTopology, "find_related_host", startedAt, found, err)
	return value, found, err
}

func (r *observedCollectTopologyReader) FindHostTopology(ctx context.Context, tenantID, hostID string) (models.ResourceTopology, bool, error) {
	startedAt := time.Now()
	value, found, err := r.next.FindHostTopology(ctx, tenantID, hostID)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceCollectTopology, "find_host_topology", startedAt, found, err)
	return value, found, err
}

type observedUptimeReader struct {
	next    enrich.UptimeReader
	metrics *instruments
}

func (r *observedUptimeReader) GetUptimeTask(ctx context.Context, tenantID, taskID string) (models.UptimeTask, bool, error) {
	startedAt := time.Now()
	value, found, err := r.next.GetUptimeTask(ctx, tenantID, taskID)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceUptime, "get_uptime_task", startedAt, found, err)
	return value, found, err
}

type observedUptimeNodeReader struct {
	next    enrich.UptimeNodeReader
	metrics *instruments
}

func (r *observedUptimeNodeReader) GetUptimeNode(ctx context.Context, tenantID, nodeID string) (models.UptimeNode, bool, error) {
	startedAt := time.Now()
	value, found, err := r.next.GetUptimeNode(ctx, tenantID, nodeID)
	enrichDataSourceRecorder{r.metrics}.record(ctx, enrichDataSourceUptime, "get_uptime_node", startedAt, found, err)
	return value, found, err
}
