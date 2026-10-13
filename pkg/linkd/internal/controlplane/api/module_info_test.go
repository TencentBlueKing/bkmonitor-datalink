// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/onemodel/queryservice"
)

func moduleConfig() config.Config {
	return config.Config{
		Dispatch:  config.DispatchConfig{Deployment: "test-deployment", JWT: config.JWTConfig{SecretKey: "management-secret"}, WorkerToken: "worker-secret"},
		Blueking:  config.BluekingConfig{AppSecret: "blueking-secret"},
		Resources: config.ResourcesConfig{MySQL: &config.MySQLResource{Password: "resource-secret"}},
		Storage: &config.StorageConfig{Repository: config.RepositoryTypeElasticsearch,
			Elasticsearch: &config.ElasticsearchConfig{Addresses: []string{"https://es-a:9200", "https://es-b:9200"}, IndexPrefix: "test-deployment",
				APIKey: "es-secret", TimePartition: config.ElasticsearchTimePartitionConfig{EventBucketDays: 3, AlertHistoryBucketDays: 5, AlertLogBucketDays: 9}},
			Redis: &config.RedisConfig{Mode: config.RedisModeSentinel, Username: "redis-user", Password: "redis-secret", Database: 2,
				Sentinel: &config.RedisSentinelConfig{MasterName: "master", Addresses: []string{"sentinel:26379"}, Username: "sentinel-user", Password: "sentinel-secret"}}},
		Lifecycle: &config.LifecycleConfig{},
		Plugins: config.PluginsConfig{KAC: &config.KACPluginConfig{Enabled: true, AlarmEventIndex: "alarm_event",
			ActionEndpoint: "https://kac.example/actions", JWT: config.JWTConfig{SecretKey: "kac-jwt-secret"}, //nolint:gosec // G101: 固定假密钥用于断言敏感接口也不得导出 KAC JWT。
			Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{"https://kac-es:9200"}, APIKey: "kac-es-secret"}}}, //nolint:gosec // G101: 固定假凭据用于验证两种响应的隔离，不是真实连接密钥。
	}
}

func newModuleAPI(t *testing.T, cfg config.Config, listen string) *API {
	t.Helper()
	info, err := NewModuleInfo(cfg, "1.2.3", listen)
	if err != nil {
		t.Fatal(err)
	}
	return &API{ModuleInfo: info, Config: cfg.Dispatch, Sources: eventsource.New(configurationDocuments{}, config.DefaultSeverityConfig())}
}

func moduleRequest(t *testing.T, handler http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/module-info"+query, nil)
	r.Header.Set("Internal-Token", testJWT(t, "management-secret"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestModuleInfoConnectionDiscoveryAndSecrets(t *testing.T) {
	cfg := moduleConfig()
	a := newModuleAPI(t, cfg, "[::]:9464")
	// 控制面会装配占位 OneModel 服务；没有实际后端不能因此宣称查询可用。
	a.OneModel = queryservice.New(nil, nil)
	// 构造后修改原始配置不得污染当前生效快照或其他并发请求。
	cfg.Storage.Elasticsearch.Addresses[0] = "https://changed:9200"
	cfg.Storage.Elasticsearch.APIKey = "changed-secret"
	cfg.Storage.Redis.Sentinel.Password = "changed-secret"
	cfg.Plugins.KAC.Elasticsearch.Addresses[0] = "https://changed:9200"
	for _, query := range []string{"", "?include_secrets=false", "?include_secrets=true"} {
		w := moduleRequest(t, a.Handler(), query)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("status=%d headers=%v", w.Code, w.Header())
		}
		body := w.Body.String()
		for _, forbidden := range []string{"management-secret", "worker-secret", "blueking-secret", "resource-secret", "kac-jwt-secret", "changed-secret", "https://changed"} {
			if strings.Contains(body, forbidden) {
				t.Fatal("non-allowlisted credential or mutated config leaked")
			}
		}
		secrets := query == "?include_secrets=true"
		for _, secret := range []string{"es-secret", "redis-user", "redis-secret", "sentinel-user", "sentinel-secret", "kac-es-secret"} {
			if strings.Contains(body, secret) != secrets {
				t.Fatal("incorrect secret mode")
			}
		}
		var info moduleInfoResponse
		if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		if info.SchemaVersion != 1 || info.Module != "linkd" || info.Deployment != "test-deployment" || info.Version != "1.2.3" || info.SecretsIncluded != secrets {
			t.Fatal("incorrect module identity")
		}
		es := info.Storage.Elasticsearch
		if es.Status != "configured" || es.Auth.Type != "api_key" || len(es.Addresses) != 2 || es.EventTargets[0] != "test-deployment-events" || es.AlertTargets[0] != "test-deployment-alerts" || es.AlertLogTargets[0] != "test-deployment-alert-logs" || es.ActiveAlertTargets[0] != "test-deployment-alerts-active" || es.AlertHistoryTargets[0] != "test-deployment-alert-history" {
			t.Fatal("incorrect production query targets")
		}
		if es.TimePartition.Timezone != "UTC" || es.TimePartition.Epoch != "1970-01-05T00:00:00Z" || es.TimePartition.EventBucketDays != 3 || es.TimePartition.AlertHistoryBucketDays != 5 || es.TimePartition.AlertLogBucketDays != 9 || es.RefreshIntervalSeconds != 5 {
			t.Fatal("incorrect normalized time partition")
		}
		if info.Storage.MySQL.Status != "unavailable" || info.Storage.Redis.Connection.Database != 2 || info.KAC.AlertTargets[0] != "alarm_event" || info.KAC.Addresses[0] != "https://kac-es:9200" {
			t.Fatal("incorrect backend scoping")
		}
		if info.Metrics.Status != "available" || info.Metrics.ListenAddress != "[::]:9464" || info.Metrics.Path != "/metrics" || info.Kafka.Status != "source_scoped" || info.Capabilities["policies"].Status != "unavailable" || info.Capabilities["onemodel"].Status != "unavailable" || info.Capabilities["event_sources"].Status != "available" {
			t.Fatal("incorrect actual capability state")
		}
		for _, section := range []string{"cleaner", "lifecycle", "elasticsearch_manager", "redis_stream_manager"} {
			if !json.Valid(info.RuntimeSettings[section]) {
				t.Fatalf("missing normalized settings %s", section)
			}
		}
		if !strings.Contains(string(info.RuntimeSettings["lifecycle"]), `"process_timeout_seconds":30`) {
			t.Fatal("runtime setting did not use Linkd defaults")
		}
	}
}

func TestModuleInfoBackendModes(t *testing.T) {
	for _, backend := range []string{"none", "mysql", "basic", "unauthenticated"} {
		t.Run(backend, func(t *testing.T) {
			cfg := moduleConfig()
			cfg.Plugins = config.PluginsConfig{}
			cfg.Lifecycle = nil
			switch backend {
			case "none":
				cfg.Storage = nil
			case "mysql":
				cfg.Storage.Repository = config.RepositoryTypeMySQL
				cfg.Storage.MySQL = &config.MySQLConfig{Address: "mysql:3306", Database: "linkd", Username: "mysql-user", Password: "mysql-secret"}
			case "basic":
				cfg.Storage.Elasticsearch.APIKey = ""
				cfg.Storage.Elasticsearch.BasicAuth = &config.BasicAuthConfig{Username: "basic-user", Password: "basic-secret"}
			case "unauthenticated":
				cfg.Storage.Elasticsearch.APIKey = ""
			}
			a := newModuleAPI(t, cfg, "")
			for _, secrets := range []bool{false, true} {
				query := ""
				if secrets {
					query = "?include_secrets=true"
				}
				w := moduleRequest(t, a.Handler(), query)
				var info moduleInfoResponse
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &info) != nil {
					t.Fatal("backend discovery failed")
				}
				if info.Metrics.Status != "unavailable" || info.KAC.Status != "unavailable" {
					t.Fatal("unconfigured service advertised")
				}
				switch backend {
				case "none":
					if info.Storage.Status != "unavailable" || info.Storage.Redis.Status != "unavailable" || info.Storage.Repository != "" {
						t.Fatal("unconfigured storage was invented")
					}
				case "mysql":
					m := info.Storage.MySQL
					if m.Status != "configured" || m.Address != "mysql:3306" || m.Database != "linkd" || m.EventTable != "linkd_events" || m.AlertTable != "linkd_alerts" || m.AlertLogTable != "linkd_alert_logs" || info.Storage.Elasticsearch.Status != "unavailable" || info.Storage.Elasticsearch.QueryLayout != nil {
						t.Fatal("incorrect MySQL entity layout")
					}
					if (m.Username == "mysql-user") != secrets || (m.Password == "mysql-secret") != secrets {
						t.Fatal("incorrect MySQL secret mode")
					}
				case "basic":
					auth := info.Storage.Elasticsearch.Auth
					if auth.Type != "basic" || (auth.Username == "basic-user") != secrets || (auth.Password == "basic-secret") != secrets {
						t.Fatal("incorrect Basic Auth discovery")
					}
				case "unauthenticated":
					if info.Storage.Elasticsearch.Auth.Type != "none" {
						t.Fatal("authentication was invented")
					}
				}
			}
		})
	}
}

func TestModuleInfoAuthenticationAndQueryBoundary(t *testing.T) {
	a := newModuleAPI(t, moduleConfig(), "")
	for _, tc := range []struct {
		query, auth string
		status      int
	}{
		{"?include_secrets=true", "", 401},
		{"?include_secrets=true", "worker", 401},
		{"?include_secrets=true", "wrong-jwt", 401},
		{"?include_secrets=true&include_secrets=false", "jwt", 400},
		{"?include_secrets=", "jwt", 400},
		{"?include_secrets", "jwt", 400},
		{"?include_secrets=1", "jwt", 400},
		{"?include_secrets=true&url=http://arbitrary", "jwt", 400},
		{"?config=/arbitrary", "jwt", 400},
		{"?%zz", "jwt", 400},
		{"?foo=bar", "jwt", 400},
		{"?include_secrets=true;foo=bar", "jwt", 400},
	} {
		t.Run(tc.auth+tc.query, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/module-info"+tc.query, nil)
			switch tc.auth {
			case "worker":
				r.Header.Set("Authorization", "Bearer worker-secret")
			case "jwt":
				r.Header.Set("Internal-Token", testJWT(t, "management-secret"))
			case "wrong-jwt":
				r.Header.Set("Internal-Token", testJWT(t, "wrong-secret"))
			}
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "es-secret") {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
	a.ModuleInfo = nil
	if w := moduleRequest(t, a.Handler(), "?include_secrets=true"); w.Code != 503 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing module service did not report unavailable")
	}
}

func TestModuleInfoConcurrentSecretModes(t *testing.T) {
	handler := newModuleAPI(t, moduleConfig(), "").Handler()
	var requests sync.WaitGroup
	for i := range 32 {
		requests.Go(func() {
			query := ""
			if i%2 == 0 {
				query = "?include_secrets=true"
			}
			w := moduleRequest(t, handler, query)
			if w.Code != 200 || strings.Contains(w.Body.String(), "es-secret") != (i%2 == 0) {
				t.Error("concurrent request leaked or lost credentials")
			}
		})
	}
	requests.Wait()
}

func TestModuleInfoRejectsUnsafeConfigurationWithoutLeak(t *testing.T) {
	for _, address := range []string{"http://user:embedded-secret@es:9200", "http://es:9200?token=embedded-secret", "http://es:9200/embedded-secret", "file:///embedded-secret"} {
		cfg := moduleConfig()
		cfg.Storage.Elasticsearch.Addresses = []string{address}
		if _, err := NewModuleInfo(cfg, "", ""); err == nil || strings.Contains(err.Error(), "embedded-secret") {
			t.Fatal("unsafe connection configuration accepted or leaked")
		}
	}
}
