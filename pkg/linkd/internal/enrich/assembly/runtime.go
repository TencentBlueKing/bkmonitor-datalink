// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package assembly

import (
	"context"
	"errors"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	onemodelassembly "linkd/internal/onemodel/assembly"
	"linkd/internal/redisclient"
	elasticsearchstore "linkd/internal/store/elasticsearch"
	"linkd/internal/telemetry"
)

// Runtime 持有来源发布所需的只读外部连接。
type Runtime struct {
	display     *redis.Client
	dataSources *datasources.Runtime
	transport   *elasticsearchstore.HTTPTransport
	sources     enrich.Sources
}

// Close 释放本运行时的 MySQL、Redis 和 HTTP 连接。
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	var result error
	if r.display != nil {
		result = errors.Join(result, r.display.Close())
	}
	if r.dataSources != nil {
		result = errors.Join(result, r.dataSources.Close())
	}
	if r.transport != nil {
		r.transport.Close()
	}
	return result
}

// Open 依据当前 EventSource 的 Processor Chain 打开并绑定所需数据源。
// Runtime 与来源任务生命周期一致，Release 更新会关闭旧连接并按新配置重新建立。
func Open(
	ctx context.Context,
	source config.EventSource,
	resources config.ResourcesConfig,
	maxConnections int,
	timeout time.Duration,
	telemetryRuntime *telemetry.Runtime,
) (*Runtime, error) {
	dataSources, err := source.Enrich.SelectResources(resources)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{}
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
	if dataSources.OneModel != nil {
		oneModelConfig := dataSources.OneModel
		client, transport, err := onemodelassembly.Open(oneModelConfig, maxConnections, timeout)
		if err != nil {
			_ = runtime.Close()
			return nil, err
		}
		runtime.transport = transport
		sources.OneModel = client
		sources.CMDB = client
		k8sReader, err := datasources.NewOneModelK8sReader(client)
		if err != nil {
			_ = runtime.Close()
			return nil, fmt.Errorf("create k8s reader: %w", err)
		}
		sources.K8s = k8sReader
		sources.CollectTopology = client
	}
	if dataSources.KingeyeDisplay != nil {
		options := dataSources.KingeyeDisplay.Redis.ClientOptions()
		options.PoolSize = maxConnections
		options.ContextTimeoutEnabled = true
		client, err := redisclient.New(options)
		if err != nil {
			_ = runtime.Close()
			return nil, err
		}
		runtime.display = client
		sources.Display = datasources.NewDisplayClient(client, sources.Model, dataSources.KingeyeDisplay.KeyPrefix)
	}
	for _, processor := range source.Enrich.Processors {
		if processor.Type == "test" {
			sources.Test = datasources.TestClient{}
		}
	}
	if telemetryRuntime != nil {
		sources = telemetryRuntime.ObserveEnrichSources(sources)
	}
	runtime.sources = sources
	return runtime, nil
}

// Router 为当前来源 Release 构建固定的 Processor Chain。
func (r *Runtime) Router(source config.EventSource, telemetryRuntime *telemetry.Runtime) (*Router, error) {
	if telemetryRuntime == nil {
		return NewRouter([]config.EventSource{source}, r.sources)
	}
	return NewRouter([]config.EventSource{source}, r.sources, WithEnrichObserver(telemetryRuntime.EnrichProcessorObserver()))
}

func mysqlDataSourceConfig(value *config.MySQLResource) *datasources.MySQLConfig {
	if value == nil {
		return nil
	}
	return &datasources.MySQLConfig{
		Address: value.Address, Database: value.Database,
		Username: value.Username, Password: value.Password,
	}
}
