// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"go.yaml.in/yaml/v3"
	"linkd/internal/config"
	storeassembly "linkd/internal/store/assembly"
	elasticsearchstore "linkd/internal/store/elasticsearch"
	mysqlstore "linkd/internal/store/mysql"
)

type moduleAvailability struct {
	Status string `json:"status"`           // Status 区分配置存在、能力已装配和不可用。
	Reason string `json:"reason,omitempty"` // Reason 仅使用固定原因码，不携带原始错误。
}

type moduleAuth struct {
	Type     string `json:"type"`               // Type 为 none、api_key 或 basic。
	APIKey   string `json:"api_key,omitempty"`  // APIKey 只出现在显式敏感响应中。
	Username string `json:"username,omitempty"` // Username 只出现在显式敏感响应中。
	Password string `json:"password,omitempty"` // Password 只出现在显式敏感响应中。
}

type moduleElasticsearch struct {
	moduleAvailability
	*elasticsearchstore.QueryLayout
	Addresses                         []string    `json:"addresses,omitempty"`                             // Addresses 只包含已经校验的 origin。
	Auth                              *moduleAuth `json:"auth,omitempty"`                                  // Auth 默认只描述认证类型。
	IndexPrefix                       string      `json:"index_prefix,omitempty"`                          // IndexPrefix 是有效部署前缀。
	RefreshIntervalSeconds            int         `json:"refresh_interval_seconds,omitempty"`              // RefreshIntervalSeconds 对应普通时间桶。
	ActiveAlertRefreshIntervalSeconds int         `json:"active_alert_refresh_interval_seconds,omitempty"` // ActiveAlertRefreshIntervalSeconds 对应活动索引。
}

type moduleMySQL struct {
	moduleAvailability
	*mysqlstore.QueryLayout
	Address  string `json:"address,omitempty"`  // Address 是 host:port，不是 DSN。
	Database string `json:"database,omitempty"` // Database 是实体数据所在库。
	Username string `json:"username,omitempty"` // Username 只出现在显式敏感响应中。
	Password string `json:"password,omitempty"` // Password 只出现在显式敏感响应中。
}

type moduleRedis struct {
	moduleAvailability
	Connection *config.RedisConfig `json:"connection,omitempty"` // Connection 使用深拷贝，默认省略两级认证。
}

type moduleStorage struct {
	moduleAvailability
	Repository    string              `json:"repository,omitempty"` // Repository 选择实体查询后端，不根据连接存在性推断。
	TenantField   string              `json:"tenant_field"`         // TenantField 必须参与业务查询范围。
	Elasticsearch moduleElasticsearch `json:"elasticsearch"`        // Elasticsearch 不冒充未选择的实体后端。
	MySQL         moduleMySQL         `json:"mysql"`                // MySQL 不暴露辅助资源连接。
	Redis         moduleRedis         `json:"redis"`                // Redis 独立于实体 Repository 选择。
}

type moduleMetrics struct {
	moduleAvailability
	ListenAddress string `json:"listen_address,omitempty"` // ListenAddress 是本进程真实监听地址，不是公共访问 URL。
	Path          string `json:"path,omitempty"`           // Path 是 exporter 路径，不提供 Prometheus 查询 API。
}

type moduleKafka struct {
	moduleAvailability
	SourcesEndpoint string `json:"sources_endpoint,omitempty"` // SourcesEndpoint 通过发布配置发现来源级连接。
}

type moduleKAC struct {
	moduleAvailability
	Addresses    []string    `json:"addresses,omitempty"`     // Addresses 来自独立 KAC ES，不继承 Repository。
	Auth         *moduleAuth `json:"auth,omitempty"`          // Auth 不包含 KAC JWT 或动作端点凭据。
	AlertTargets []string    `json:"alert_targets,omitempty"` // AlertTargets 保留 KAC 配置中的真实 alias。
}

type moduleCapability struct {
	moduleAvailability
	Endpoint string `json:"endpoint"` // Endpoint 描述当前二进制实际注册的管理入口。
}

type moduleInfoResponse struct {
	SchemaVersion   int                         `json:"schema_version"`    // SchemaVersion 是发现协议版本。
	Module          string                      `json:"module"`            // Module 固定为 linkd。
	Deployment      string                      `json:"deployment"`        // Deployment 来自有效控制面配置。
	Version         string                      `json:"version,omitempty"` // Version 仅使用启动方掌握的构建版本。
	SecretsIncluded bool                        `json:"secrets_included"`  // SecretsIncluded 反映显式敏感模式。
	Storage         moduleStorage               `json:"storage"`           // Storage 不包含完整原始配置。
	Metrics         moduleMetrics               `json:"metrics"`           // Metrics 只描述当前进程 exporter。
	Kafka           moduleKafka                 `json:"kafka"`             // Kafka 不虚构全局连接。
	KAC             moduleKAC                   `json:"kac"`               // KAC 只描述已启用的兼容读取。
	RuntimeSettings map[string]json.RawMessage  `json:"runtime_settings"`  // RuntimeSettings 只允许显式无凭据白名单。
	Capabilities    map[string]moduleCapability `json:"capabilities"`      // Capabilities 由实际 API 依赖填充。
}

// ModuleInfo 保存启动时已收敛的连接发现快照；内部状态不可变，可被管理请求并发读取。
// 敏感和非敏感快照分别构造，避免请求切换模式时修改共享凭据或污染其他请求。
type ModuleInfo struct {
	public    moduleInfoResponse
	protected moduleInfoResponse
}

// NewModuleInfo 从进程有效配置构造白名单快照，不连接依赖、不读取文件、不导出 JWT 或 Worker Token。
// version 和 metricsListen 必须由实际进程提供，不能用配置监听地址冒充已绑定的 exporter。
func NewModuleInfo(cfg config.Config, version, metricsListen string) (*ModuleInfo, error) {
	if cfg.Storage != nil {
		if err := cfg.Storage.WithDefaults().Validate(); err != nil {
			return nil, errors.New("invalid module storage configuration")
		}
	}
	if err := cfg.Plugins.Validate(); err != nil {
		return nil, errors.New("invalid module plugin configuration")
	}
	public, err := buildModuleInfo(cfg, version, metricsListen, false)
	if err != nil {
		return nil, err
	}
	protected, err := buildModuleInfo(cfg, version, metricsListen, true)
	if err != nil {
		return nil, err
	}
	return &ModuleInfo{public: public, protected: protected}, nil
}

func unavailable(reason string) moduleAvailability {
	return moduleAvailability{Status: "unavailable", Reason: reason}
}

func connectionAuth(key, username, password string, secrets bool) *moduleAuth {
	auth := &moduleAuth{Type: "none"}
	if key != "" {
		auth.Type = "api_key"
		if secrets {
			auth.APIKey = key
		}
	} else if username != "" {
		auth.Type = "basic"
		if secrets {
			auth.Username, auth.Password = username, password
		}
	}
	return auth
}

func buildModuleInfo(cfg config.Config, version, listen string, secrets bool) (moduleInfoResponse, error) {
	info := moduleInfoResponse{SchemaVersion: 1, Module: "linkd", Deployment: cfg.Dispatch.WithDefaults().Deployment,
		Version: version, SecretsIncluded: secrets,
		Storage: moduleStorage{moduleAvailability: unavailable("not_configured"), TenantField: "bk_tenant_id",
			Elasticsearch: moduleElasticsearch{moduleAvailability: unavailable("repository_not_elasticsearch")},
			MySQL:         moduleMySQL{moduleAvailability: unavailable("repository_not_mysql")},
			Redis:         moduleRedis{moduleAvailability: unavailable("not_configured")}},
		Metrics:         moduleMetrics{moduleAvailability: unavailable("not_enabled")},
		Kafka:           moduleKafka{moduleAvailability: unavailable("not_configured")},
		KAC:             moduleKAC{moduleAvailability: unavailable("plugin_disabled")},
		RuntimeSettings: make(map[string]json.RawMessage)}
	if listen != "" {
		info.Metrics = moduleMetrics{moduleAvailability: moduleAvailability{Status: "available"}, ListenAddress: listen, Path: "/metrics"}
	}
	if cfg.Storage != nil {
		storage := cfg.Storage.WithDefaults()
		info.Storage.moduleAvailability = moduleAvailability{Status: "configured"}
		info.Storage.Repository = storage.Repository
		switch storage.Repository {
		case config.RepositoryTypeElasticsearch:
			es := storage.Elasticsearch
			layout, err := storeassembly.ElasticsearchQueryLayout(*es)
			if err != nil {
				return moduleInfoResponse{}, errors.New("invalid module query layout")
			}
			username, password := "", ""
			if es.BasicAuth != nil {
				username, password = es.BasicAuth.Username, es.BasicAuth.Password
			}
			info.Storage.Elasticsearch = moduleElasticsearch{moduleAvailability: moduleAvailability{Status: "configured"},
				QueryLayout: &layout, Addresses: append([]string(nil), es.Addresses...), IndexPrefix: es.IndexPrefix,
				Auth: connectionAuth(es.APIKey, username, password, secrets), RefreshIntervalSeconds: es.RefreshIntervalSeconds,
				ActiveAlertRefreshIntervalSeconds: es.ActiveAlertRefreshIntervalSeconds}
		case config.RepositoryTypeMySQL:
			mysql := storage.MySQL
			layout := mysqlstore.EntityQueryLayout()
			info.Storage.MySQL = moduleMySQL{moduleAvailability: moduleAvailability{Status: "configured"},
				QueryLayout: &layout, Address: mysql.Address, Database: mysql.Database}
			if secrets {
				info.Storage.MySQL.Username, info.Storage.MySQL.Password = mysql.Username, mysql.Password
			}
		}
		if storage.Redis != nil {
			r := storage.Redis.WithDefaults()
			if !secrets {
				r.Username, r.Password = "", ""
				if r.Sentinel != nil {
					r.Sentinel.Username, r.Sentinel.Password = "", ""
				}
			}
			info.Storage.Redis = moduleRedis{moduleAvailability: moduleAvailability{Status: "configured"}, Connection: &r}
		}
	}
	if cfg.Plugins.KACEnabled() {
		kac := cfg.Plugins.KAC
		username, password := "", ""
		if kac.Elasticsearch.BasicAuth != nil {
			username, password = kac.Elasticsearch.BasicAuth.Username, kac.Elasticsearch.BasicAuth.Password
		}
		info.KAC = moduleKAC{moduleAvailability: moduleAvailability{Status: "configured"},
			Addresses: append([]string(nil), kac.Elasticsearch.Addresses...), AlertTargets: []string{kac.AlarmEventIndex},
			Auth: connectionAuth(kac.Elasticsearch.APIKey, username, password, secrets)}
	}
	// 只编码已核对不含凭据的运行参数类型，不能将 cfg、resources、plugins 或 dispatch 传入此集合。
	settings := map[string]any{"cleaner": cfg.Cleaner.WithDefaults()}
	if cfg.Lifecycle != nil {
		settings["lifecycle"] = cfg.Lifecycle.WithDefaults()
	}
	if info.Storage.Repository == config.RepositoryTypeElasticsearch {
		manager := (config.ElasticsearchControlPlaneConfig{}).WithDefaults()
		if cfg.ControlPlane != nil && cfg.ControlPlane.Elasticsearch != nil {
			manager = cfg.ControlPlane.Elasticsearch.WithDefaults()
		}
		settings["elasticsearch_manager"] = manager
	}
	if stream := cfg.RedisStreamSettings(); stream != nil {
		settings["redis_stream_manager"] = *stream
	}
	for key, value := range settings {
		raw, err := yaml.Marshal(value)
		if err != nil {
			return moduleInfoResponse{}, errors.New("invalid module runtime settings")
		}
		var fields map[string]any
		if yaml.Unmarshal(raw, &fields) != nil {
			return moduleInfoResponse{}, errors.New("invalid module runtime settings")
		}
		encoded, err := json.Marshal(fields)
		if err != nil {
			return moduleInfoResponse{}, errors.New("invalid module runtime settings")
		}
		info.RuntimeSettings[key] = encoded
	}
	return info, nil
}

func (a *API) moduleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query) > 1 || len(query) == 1 && len(query["include_secrets"]) != 1 {
		http.Error(w, "invalid module info query", http.StatusBadRequest)
		return
	}
	if values := query["include_secrets"]; len(values) == 1 && values[0] != "true" && values[0] != "false" {
		http.Error(w, "invalid module info query", http.StatusBadRequest)
		return
	}
	secrets, err := includeSecrets(w, r)
	if err != nil {
		http.Error(w, "invalid module info query", http.StatusBadRequest)
		return
	}
	if a.ModuleInfo == nil {
		http.Error(w, "module info unavailable", http.StatusServiceUnavailable)
		return
	}
	info := a.ModuleInfo.public
	if secrets {
		info = a.ModuleInfo.protected
	}
	info.Capabilities = a.moduleCapabilities()
	if a.Sources != nil {
		info.Kafka = moduleKafka{moduleAvailability: moduleAvailability{Status: "source_scoped"}, SourcesEndpoint: "/api/v1/event-sources?published=true"}
	}
	output(w, info)
}

func (a *API) moduleCapabilities() map[string]moduleCapability {
	result := make(map[string]moduleCapability)
	for _, entry := range []struct {
		id, endpoint string
		enabled      bool
	}{
		{"eventgen", "/api/v1/eventgen/tasks", a.Eventgen != nil},
		{"event_sources", "/api/v1/event-sources", a.Sources != nil},
		{"tenants", "/api/v1/tenants", a.Tenants != nil},
		{"policies", "/api/v1/policies", a.Policies != nil},
		{"policy_statistics", "/api/v1/policies/statistics", a.PolicyStatistics != nil},
		{"policy_preview", "/api/v1/policies/preview", a.PolicyPreviewer != nil},
		{"policy_simulation", "/api/v1/policies/simulate", a.PolicySimulator != nil},
		{"merge_runtime", "/api/v1/policy-runtime/merge/windows", a.MergeRuntime != nil},
		{"shield_runtime", "/api/v1/policy-runtime/shield/alerts", a.ShieldRuntime != nil},
		{"suppression_runtime", "/api/v1/policy-runtime/suppression/clip", a.SuppressionRuntime != nil},
		{"onemodel", "/api/v1/onemodel/search", a.OneModel.Configured()},
		{"enrich_preview", "/api/v1/enrich/preview", a.Previewer != nil},
		{"ingest_preview", "/api/v1/ingest/preview", a.IngestPreviewer != nil},
		{"runtime", "/api/v1/runtime", a.Controller != nil},
		{"control_plane_tasks", "/api/v1/control-plane/tasks", a.Tasks != nil},
		{"metric_catalog", "/api/v1/metrics/catalog", true},
	} {
		status := unavailable("not_configured")
		if entry.enabled {
			status = moduleAvailability{Status: "available"}
		}
		result[entry.id] = moduleCapability{moduleAvailability: status, Endpoint: entry.endpoint}
	}
	return result
}
