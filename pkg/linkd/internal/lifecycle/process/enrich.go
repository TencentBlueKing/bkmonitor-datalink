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

func validateEnricherConfig(source config.EventSource) error {
	if _, err := source.Enrich.SelectDataSources(); err != nil {
		return err
	}
	_, err := assembly.NewRouter([]config.EventSource{source}, enrich.Sources{})
	return err
}

// openEnrichRuntime 依据当前 EventSource 的 Processor Chain 打开并绑定所需数据源。
// Runtime 与来源任务生命周期一致，Release 更新会关闭旧连接并按新配置重新建立。
func openEnrichRuntime(
	ctx context.Context,
	source config.EventSource,
	maxConnections int,
	timeout time.Duration,
	telemetryRuntime *telemetry.Runtime,
) (*enrichRuntime, error) {
	dataSources, err := source.Enrich.SelectDataSources()
	if err != nil {
		return nil, err
	}
	runtime := &enrichRuntime{}
	sources := enrich.Sources{}
	if dataSources.MySQL != nil {
		dataSourceConfig := datasources.Config{MySQL: mysqlDataSourceConfig(dataSources.MySQL)}
		dataSourceRuntime, err := datasources.Open(ctx, dataSourceConfig, maxConnections)
		if err != nil {
			return nil, fmt.Errorf("open enrich mysql datasources: %w", err)
		}
		runtime.dataSources = dataSourceRuntime
		sources = dataSourceRuntime.Sources()
	}
	if dataSources.Elasticsearch != nil {
		oneModelConfig := dataSources.Elasticsearch
		transport, err := newOneModelTransport(
			oneModelConfig,
			maxConnections,
			timeout,
		)
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

// router 为当前来源 Release 构建固定的 Processor Chain。
func (r *enrichRuntime) router(source config.EventSource, telemetryRuntime *telemetry.Runtime) (lifecycle.AlertEnricher, error) {
	if telemetryRuntime == nil {
		return assembly.NewRouter([]config.EventSource{source}, r.sources)
	}
	return assembly.NewRouter([]config.EventSource{source}, r.sources, assembly.WithEnrichObserver(telemetryRuntime.EnrichProcessorObserver()))
}

func newOneModelTransport(
	dataSource *config.EnrichElasticsearchDataSource,
	maxConnections int,
	timeout time.Duration,
) (*elasticsearchstore.HTTPTransport, error) {
	transportConfig := elasticsearchstore.HTTPTransportConfig{
		Addresses:             append([]string(nil), dataSource.Addresses...),
		APIKey:                dataSource.APIKey,
		Timeout:               timeout,
		MaxConnectionsPerHost: maxConnections,
	}
	if dataSource.BasicAuth != nil {
		transportConfig.BasicUsername = dataSource.BasicAuth.Username
		transportConfig.BasicPassword = dataSource.BasicAuth.Password
	}
	return elasticsearchstore.NewHTTPTransport(transportConfig)
}

func mysqlDataSourceConfig(value *config.EnrichMySQLDataSource) *datasources.MySQLConfig {
	if value == nil {
		return nil
	}
	return &datasources.MySQLConfig{
		Address: value.Address, Database: value.Database,
		Username: value.Username, Password: value.Password,
	}
}
