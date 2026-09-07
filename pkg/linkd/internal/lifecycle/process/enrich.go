// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"errors"
	"fmt"
	"time"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/assembly"
	"linkd/internal/lifecycle/enrich/datasources"
	"linkd/internal/lifecycle/enrich/rules"
	elasticsearchstore "linkd/internal/store/elasticsearch"
	"linkd/internal/telemetry"
)

type enrichRuntime struct {
	dataSources *datasources.Runtime
	transport   *elasticsearchstore.HTTPTransport
	sources     enrich.Sources
}

func (r *enrichRuntime) Close() error {
	if r == nil {
		return nil
	}
	var result error
	if r.dataSources != nil {
		result = errors.Join(result, r.dataSources.Close())
	}
	if r.transport != nil {
		r.transport.Close()
	}
	return result
}

func validateEnricherConfig(eventSources []config.EventSource, lifecycleConfig config.LifecycleConfig) error {
	requirements := requiredEnrichDataSources(eventSources)
	if requirements.metric && lifecycleConfig.DataSources.Metric == nil {
		return fmt.Errorf("lifecycle.datasources.metric is required by configured metric processor")
	}
	if requirements.alarmSource && lifecycleConfig.DataSources.AlarmSource == nil {
		return fmt.Errorf("lifecycle.datasources.alarm_source is required by configured source processor")
	}
	if requirements.bkStrategy && lifecycleConfig.DataSources.BKStrategy == nil {
		return fmt.Errorf("lifecycle.datasources.bk_strategy is required by configured enrich processors")
	}
	if requirements.cwStrategy && lifecycleConfig.DataSources.CWStrategy == nil {
		return fmt.Errorf("lifecycle.datasources.cw_strategy is required by configured enrich processors")
	}
	if requirements.oneModel && lifecycleConfig.DataSources.OneModel == nil {
		return fmt.Errorf("lifecycle.datasources.onemodel is required by configured resource processor")
	}
	if _, err := assembly.NewRouter(eventSources, enrich.Sources{}); err != nil {
		return err
	}
	return nil
}

// openEnrichRuntime 打开进程配置的数据源，供各 Release 的独立路由共享。
// 来源可在启动后发布，因此连接范围由静态数据源配置决定；仅进程退出时关闭。
func openEnrichRuntime(
	ctx context.Context,
	lifecycleConfig config.LifecycleConfig,
	telemetryRuntime *telemetry.Runtime,
) (*enrichRuntime, error) {
	requirements := enrichDataSourceRequirements{
		metric:      lifecycleConfig.DataSources.Metric != nil,
		alarmSource: lifecycleConfig.DataSources.AlarmSource != nil,
		bkStrategy:  lifecycleConfig.DataSources.BKStrategy != nil,
		cwStrategy:  lifecycleConfig.DataSources.CWStrategy != nil,
		oneModel:    lifecycleConfig.DataSources.OneModel != nil,
	}
	runtime := &enrichRuntime{}
	sources := enrich.Sources{}
	if requirements.mysql() {
		dataSourceConfig := datasources.Config{}
		if requirements.metric {
			dataSourceConfig.Metric = mysqlDataSourceConfig(lifecycleConfig.DataSources.Metric)
		}
		if requirements.alarmSource {
			dataSourceConfig.AlarmSource = mysqlDataSourceConfig(lifecycleConfig.DataSources.AlarmSource)
		}
		if requirements.bkStrategy {
			dataSourceConfig.BKStrategy = mysqlDataSourceConfig(lifecycleConfig.DataSources.BKStrategy)
		}
		if requirements.cwStrategy {
			dataSourceConfig.CWStrategy = mysqlDataSourceConfig(lifecycleConfig.DataSources.CWStrategy)
		}
		dataSourceRuntime, err := datasources.Open(ctx, dataSourceConfig, lifecycleConfig.Concurrency+4)
		if err != nil {
			return nil, fmt.Errorf("open enrich mysql datasources: %w", err)
		}
		runtime.dataSources = dataSourceRuntime
		sources = dataSourceRuntime.Sources()
	}
	if requirements.oneModel {
		oneModelConfig := lifecycleConfig.DataSources.OneModel
		transportConfig := elasticsearchstore.HTTPTransportConfig{
			Addresses: append([]string(nil), oneModelConfig.Addresses...),
			APIKey:    oneModelConfig.APIKey,
			Timeout:   time.Duration(lifecycleConfig.ProcessTimeoutSeconds) * time.Second,
		}
		if oneModelConfig.BasicAuth != nil {
			transportConfig.BasicUsername = oneModelConfig.BasicAuth.Username
			transportConfig.BasicPassword = oneModelConfig.BasicAuth.Password
		}
		transport, err := elasticsearchstore.NewHTTPTransport(transportConfig)
		if err != nil {
			_ = runtime.Close()
			return nil, fmt.Errorf("create onemodel transport: %w", err)
		}
		runtime.transport = transport
		client, err := datasources.NewOneModelClient(datasources.OneModelClientConfig{
			Transport: transport,
			CMDBIndex: oneModelConfig.IndexPrefix + "cmdb_instance",
		})
		if err != nil {
			_ = runtime.Close()
			return nil, fmt.Errorf("create onemodel client: %w", err)
		}
		sources.OneModel = client
	}
	if telemetryRuntime != nil {
		sources = telemetryRuntime.ObserveEnrichSources(sources)
	}
	runtime.sources = sources
	return runtime, nil
}

// router 按任务固定的 Release 构建独立路由，后续发布不修改已经运行的链。
func (r *enrichRuntime) router(source config.EventSource, cfg config.LifecycleConfig, telemetryRuntime *telemetry.Runtime) (lifecycle.AlertEnricher, error) {
	sources := []config.EventSource{source}
	if err := validateEnricherConfig(sources, cfg); err != nil {
		return nil, err
	}
	if telemetryRuntime == nil {
		return assembly.NewRouter(sources, r.sources)
	}
	return assembly.NewRouter(sources, r.sources, assembly.WithEnrichObserver(telemetryRuntime.EnrichProcessorObserver()))
}

type enrichDataSourceRequirements struct {
	bkStrategy  bool
	cwStrategy  bool
	alarmSource bool
	metric      bool
	oneModel    bool
}

func (r enrichDataSourceRequirements) mysql() bool {
	return r.bkStrategy || r.cwStrategy || r.alarmSource || r.metric
}

func requiredEnrichDataSources(sources []config.EventSource) enrichDataSourceRequirements {
	var requirements enrichDataSourceRequirements
	for _, source := range sources {
		for _, processor := range source.Enrich.Processors {
			switch processor.Type {
			case rules.StrategyProcessor:
				requirements.bkStrategy = true
				requirements.cwStrategy = true
				requirements.oneModel = true
			case rules.ResourceProcessor:
				requirements.cwStrategy = true
				requirements.oneModel = true
			case rules.DisplayProcessor:
				requirements.cwStrategy = true
			case rules.SourceProcessor:
				requirements.alarmSource = true
			case rules.MetricProcessor:
				requirements.bkStrategy = true
				requirements.cwStrategy = true
				requirements.metric = true
			}
		}
	}
	return requirements
}

func mysqlDataSourceConfig(value *config.MySQLConfig) *datasources.MySQLConfig {
	if value == nil {
		return nil
	}
	return &datasources.MySQLConfig{
		Address: value.Address, Database: value.Database,
		Username: value.Username, Password: value.Password,
	}
}
