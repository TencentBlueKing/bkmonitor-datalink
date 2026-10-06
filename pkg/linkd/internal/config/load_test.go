// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/kafkaclient"
	"linkd/internal/logging"
)

func TestLoadLayers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		env       map[string]string
		overrides Overrides
		want      Config
		wantError string
	}{
		{
			name:    "minimal config uses logging defaults",
			content: "{}\n",
			want: Config{
				Logging:      logging.DefaultConfig(),
				EventSources: []EventSource{},
			},
		},
		{
			name: "yaml overrides defaults",
			content: `logging:
  level: debug
  format: text
`,
			want: Config{
				Logging:      logging.Config{Level: logging.LevelDebug, Format: logging.FormatText},
				EventSources: []EventSource{},
			},
		},
		{
			name: "environment overrides yaml",
			content: `logging:
  level: debug
  format: text
`,
			env: map[string]string{
				envLogLevel:  logging.LevelWarn,
				envLogFormat: logging.FormatJSON,
			},
			want: Config{
				Logging:      logging.Config{Level: logging.LevelWarn, Format: logging.FormatJSON},
				EventSources: []EventSource{},
			},
		},
		{
			name: "explicit cli overrides environment",
			content: `logging:
  level: debug
  format: text
`,
			env: map[string]string{
				envLogLevel:  logging.LevelWarn,
				envLogFormat: logging.FormatJSON,
			},
			overrides: Overrides{
				LogLevel:  stringPointer(logging.LevelError),
				LogFormat: stringPointer(logging.FormatText),
			},
			want: Config{
				Logging:      logging.Config{Level: logging.LevelError, Format: logging.FormatText},
				EventSources: []EventSource{},
			},
		},
		{
			name:      "empty environment value is explicit",
			content:   "{}\n",
			env:       map[string]string{envLogLevel: ""},
			wantError: "logging.level",
		},
		{
			name:      "empty cli value is explicit",
			content:   "{}\n",
			overrides: Overrides{LogFormat: stringPointer("")},
			wantError: "logging.format",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := writeConfig(t, test.content)
			cfg, err := load(path, test.overrides, mapLookup(test.env))
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("load() error = %v, want containing %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("load() error = %v", err)
			}
			test.want.Severity = DefaultSeverityConfig()
			test.want.Cleaner = DefaultCleanerRuntimeConfig()
			if !reflect.DeepEqual(cfg, test.want) {
				t.Fatalf("load() = %#v, want %#v", cfg, test.want)
			}
		})
	}
}

func TestLoadEventSources(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, `event_sources:
  - event_source_id: source-a
    enabled: false
    storage:
      type: kafka
      kafka:
        brokers:
          - kafka-1.example.com:9092
          - kafka-2.example.com:9092
        topic: cw_kingeye_linkd_raw_event_source-a
        consumer_group: linkd-source-a
        security:
          protocol: sasl_ssl
          sasl:
            mechanism: scram_sha_256
            username: linkd
            password: secret
  - event_source_id: zabbix
    enabled: true
    cleaner:
      type: standard
    fingerprint_mode: fields
    fingerprint_fields: [subject_id, dimensions.port]
    severity_mapping:
      P1: critical
    storage:
      type: kafka
      kafka:
        brokers:
          - kafka.example.com:9092
        topic: cw_kingeye_linkd_raw_event_zabbix
        consumer_group: linkd-zabbix
`)

	cfg, err := load(path, Overrides{}, mapLookup(nil))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if len(cfg.EventSources) != 2 {
		t.Fatalf("load() event sources = %#v", cfg.EventSources)
	}
	if cfg.EventSources[0].Enabled {
		t.Fatal("load() did not preserve enabled: false")
	}
	if cfg.EventSources[0].Storage.Kafka.Security.SASL.Password != "secret" {
		t.Fatal("load() did not preserve the runtime password")
	}
	if cfg.EventSources[1].Storage.Kafka.Security.Protocol != kafkaclient.SecurityProtocolPlaintext {
		t.Fatalf("load() default protocol = %q", cfg.EventSources[1].Storage.Kafka.Security.Protocol)
	}
	if !reflect.DeepEqual(
		cfg.EventSources[1].FingerprintFields,
		[]string{"subject_id", "dimensions.port"},
	) || cfg.EventSources[1].SeverityMapping["P1"] != "critical" {
		t.Fatalf("load() source config = %#v", cfg.EventSources[1])
	}
}

func TestLoadStorage(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, `storage:
  repository: elasticsearch
  mysql:
    address: 127.0.0.1:13306
    database: test
    username: test
    password: mysql-secret
  elasticsearch:
    addresses: [http://127.0.0.1:9200]
    number_of_replicas: 0
    basic_auth:
      username: elastic
      password: elastic-secret
  redis:
    address: 127.0.0.1:16379
    username: linkd
    password: redis-secret
    database: 2
`)

	cfg, err := load(path, Overrides{}, mapLookup(nil))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.Storage == nil || cfg.Storage.MySQL == nil || cfg.Storage.Elasticsearch == nil ||
		cfg.Storage.Redis == nil {
		t.Fatalf("load() storage = %#v", cfg.Storage)
	}
	if cfg.Storage.Repository != RepositoryTypeElasticsearch {
		t.Fatalf("load() storage repository = %q", cfg.Storage.Repository)
	}
	if cfg.Storage.MySQL.Password != "mysql-secret" ||
		cfg.Storage.Elasticsearch.BasicAuth.Password != "elastic-secret" ||
		cfg.Storage.Redis.Password != "redis-secret" {
		t.Fatalf("load() did not preserve runtime storage secrets: %#v", cfg.Storage)
	}
	if cfg.Storage.Elasticsearch.IndexPrefix != defaultElasticsearchIndexPrefix {
		t.Fatalf("load() elasticsearch index prefix = %q", cfg.Storage.Elasticsearch.IndexPrefix)
	}
	if cfg.Storage.Elasticsearch.NumberOfReplicas == nil || *cfg.Storage.Elasticsearch.NumberOfReplicas != 0 {
		t.Fatalf("load() elasticsearch number of replicas = %v", cfg.Storage.Elasticsearch.NumberOfReplicas)
	}
	partition := cfg.Storage.Elasticsearch.TimePartition
	if partition.EventBucketDays != 7 || partition.AlertHistoryBucketDays != 7 ||
		partition.AlertLogBucketDays != 7 || partition.PrecreatePastBuckets != 1 ||
		partition.PrecreateFutureBuckets != 1 || partition.MaxBucketsPerEntity != 512 {
		t.Fatalf("load() elasticsearch time partition = %#v", partition)
	}
}

func TestLoadLifecycleWithDefaults(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, `lifecycle:
  concurrency: 4
  output:
    kafka:
      brokers: [kafka.example.com:9092]
      topic: linkd-alerts
      client_id: linkd-lifecycle
      security:
        protocol: sasl_ssl
        sasl:
          mechanism: scram_sha_256
          username: linkd
          password: lifecycle-secret
`)

	cfg, err := load(path, Overrides{}, mapLookup(nil))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.Lifecycle == nil {
		t.Fatal("load() lifecycle is nil")
	}
	lifecycle := cfg.Lifecycle
	if lifecycle.Concurrency != 4 || lifecycle.Signal.Stream != defaultSignalStream ||
		lifecycle.Signal.Group != defaultSignalGroup || lifecycle.Lock.TTLSeconds != defaultLockTTLSeconds ||
		lifecycle.Mailbox.KeyPrefix != defaultMailboxKeyPrefix ||
		lifecycle.Mailbox.MaxPending != defaultMailboxMaxPending ||
		lifecycle.Mailbox.MaxDrainEvents != defaultMailboxMaxDrainEvents ||
		lifecycle.Mailbox.Backpressure.CacheTTLSeconds != defaultMailboxBackpressureCacheTTL ||
		lifecycle.Mailbox.Backpressure.QueryTimeoutSeconds != defaultMailboxBackpressureQueryTimeout ||
		lifecycle.Mailbox.Backpressure.HighWatermark != defaultMailboxBackpressureHighWatermark ||
		lifecycle.Mailbox.Backpressure.LowWatermark != defaultMailboxBackpressureLowWatermark ||
		lifecycle.Signal.CreateGroup == nil || !*lifecycle.Signal.CreateGroup {
		t.Fatalf("load() lifecycle defaults = %#v", lifecycle)
	}
	if lifecycle.RuntimeConfig().WorkerCount != 4 || lifecycle.Output.Kafka.Security.SASL.Password != "lifecycle-secret" {
		t.Fatalf("load() lifecycle runtime = %#v", lifecycle)
	}
}

func TestLoadRejectsInvalidLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		override  string
		wantError string
	}{
		{name: "zero concurrency", override: "  concurrency: -1\n", wantError: "lifecycle.concurrency"},
		{
			name: "claim too early",
			override: `  process_timeout_seconds: 30
  retry_max_elapsed_seconds: 120
  signal:
    claim_min_idle_seconds: 150
`,
			wantError: "claim_min_idle_seconds must exceed",
		},
		{
			name: "batch memory bound",
			override: `  signal:
    max_batch_messages: 4096
    max_message_bytes: 1048576
`,
			wantError: "batch capacity",
		},
		{name: "mailbox backpressure watermarks", override: "  mailbox:\n    backpressure:\n      low_watermark: 100\n      high_watermark: 99\n", wantError: "watermarks"},
		{name: "mailbox backpressure ttl", override: "  mailbox:\n    backpressure:\n      cache_ttl_seconds: 61\n", wantError: "cache_ttl_seconds"},
		{name: "mailbox backpressure timeout", override: "  mailbox:\n    backpressure:\n      cache_ttl_seconds: 2\n      query_timeout_seconds: 3\n", wantError: "query_timeout_seconds"},
		{name: "removed mailbox total", override: "  mailbox:\n    max_pending_total: 1000000\n", wantError: "field max_pending_total not found"},
		{
			name: "renew too slow",
			override: `  lock:
    ttl_seconds: 10
    renew_interval_seconds: 5
`,
			wantError: "less than half",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			content := `lifecycle:
` + test.override + `  output:
    kafka:
      brokers: [kafka.example.com:9092]
      topic: linkd-alerts
`
			path := writeConfig(t, content)
			_, err := load(path, Overrides{}, mapLookup(nil))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("load() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestLoadRejectsInvalidStorage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		wantError string
	}{
		{name: "empty storage", content: "storage: {}\n", wantError: "at least one backend"},
		{
			name: "unsupported repository",
			content: `storage:
  repository: unknown
  redis:
    address: 127.0.0.1:16379
`,
			wantError: "storage.repository must be one of",
		},
		{
			name: "selected mysql missing",
			content: `storage:
  repository: mysql
  redis:
    address: 127.0.0.1:16379
`,
			wantError: "storage.mysql is required",
		},
		{
			name: "selected elasticsearch missing",
			content: `storage:
  repository: elasticsearch
  redis:
    address: 127.0.0.1:16379
`,
			wantError: "storage.elasticsearch is required",
		},
		{
			name: "mysql invalid address",
			content: `storage:
  mysql:
    address: localhost
    database: test
    username: test
    password: secret
`,
			wantError: "storage.mysql.address must be host:port",
		},
		{
			name: "elasticsearch invalid url",
			content: `storage:
  elasticsearch:
    addresses: [127.0.0.1:9200]
`,
			wantError: "http(s) origin URL",
		},
		{
			name: "elasticsearch conflicting auth",
			content: `storage:
  elasticsearch:
    addresses: [http://127.0.0.1:9200]
    api_key: secret-key
    basic_auth:
      username: elastic
      password: secret-password
`,
			wantError: "mutually exclusive",
		},
		{
			name: "elasticsearch invalid index prefix",
			content: `storage:
  elasticsearch:
    addresses: [http://127.0.0.1:9200]
    index_prefix: Linkd/Invalid
`,
			wantError: "index_prefix",
		},
		{
			name: "elasticsearch negative replica count",
			content: `storage:
  elasticsearch:
    addresses: [http://127.0.0.1:9200]
    number_of_replicas: -1
`,
			wantError: "number_of_replicas",
		},
		{
			name: "elasticsearch invalid bucket days",
			content: `storage:
  elasticsearch:
    addresses: [http://127.0.0.1:9200]
    time_partition:
      event_bucket_days: 366
`,
			wantError: "event_bucket_days",
		},
		{
			name: "redis negative database",
			content: `storage:
  redis:
    address: 127.0.0.1:16379
    database: -1
`,
			wantError: "database must not be negative",
		},
		{
			name: "unknown storage field",
			content: `storage:
  mysql:
    address: 127.0.0.1:13306
    database: test
    username: test
    password: secret
    dsn: forbidden
`,
			wantError: "field dsn not found",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, test.content)
			_, err := load(path, Overrides{}, mapLookup(nil))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("load() error = %v, want containing %q", err, test.wantError)
			}
			for _, secret := range []string{"secret-password", "secret-key"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("load() error leaked secret: %v", err)
				}
			}
		})
	}
}

func TestLoadRejectsInvalidEventSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		wantError string
	}{
		{
			name: "missing enabled",
			content: `event_sources:
  - event_source_id: source-a
    storage:
      type: kafka
      kafka:
        brokers: [kafka:9092]
        topic: raw-events
        consumer_group: linkd
`,
			wantError: "event_sources[0].enabled is required",
		},
		{
			name: "missing storage",
			content: `event_sources:
  - event_source_id: source-a
    enabled: true
`,
			wantError: "event_sources[0].storage is required",
		},
		{
			name: "missing kafka",
			content: `event_sources:
  - event_source_id: source-a
    enabled: true
    storage:
      type: kafka
`,
			wantError: "event_sources[0].storage.kafka is required",
		},
		{
			name: "invalid cleaning fingerprint",
			content: `event_sources:
  - event_source_id: source-a
    enabled: true
    fingerprint_mode: field
    fingerprint_field: event_id
    storage:
      type: kafka
      kafka:
        brokers: [kafka:9092]
        topic: raw-events
        consumer_group: linkd
`,
			wantError: "not a stable Event path",
		},
		{
			name: "unknown kafka field",
			content: `event_sources:
  - event_source_id: source-a
    enabled: true
    storage:
      type: kafka
      kafka:
        brokers: [kafka:9092]
        topic: raw-events
        consumer_group: linkd
        client_id: source-a
`,
			wantError: "field client_id not found",
		},
		{
			name: "empty password",
			content: `event_sources:
  - event_source_id: source-a
    enabled: true
    storage:
      type: kafka
      kafka:
        brokers: [kafka:9092]
        topic: raw-events
        consumer_group: linkd
        security:
          protocol: sasl_ssl
          sasl:
            mechanism: plain
            username: linkd
            password: ""
`,
			wantError: "sasl.password is required",
		},
		{
			name: "invalid mechanism does not leak password",
			content: `event_sources:
  - event_source_id: source-a
    enabled: true
    storage:
      type: kafka
      kafka:
        brokers: [kafka:9092]
        topic: raw-events
        consumer_group: linkd
        security:
          protocol: sasl_ssl
          sasl:
            mechanism: unknown
            username: linkd
            password: secret-password
`,
			wantError: "sasl.mechanism must be one of",
		},
		{
			name: "duplicate subscription",
			content: `event_sources:
  - event_source_id: source-a
    enabled: true
    storage:
      type: kafka
      kafka:
        brokers: [kafka-a:9092, kafka-b:9092]
        topic: raw-events
        consumer_group: linkd
  - event_source_id: source-b
    enabled: false
    storage:
      type: kafka
      kafka:
        brokers: [KAFKA-B:09092, KAFKA-A:9092]
        topic: raw-events
        consumer_group: linkd
`,
			wantError: "duplicates event_sources",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := writeConfig(t, test.content)
			_, err := load(path, Overrides{}, mapLookup(nil))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("load() error = %v, want containing %q", err, test.wantError)
			}
			if strings.Contains(err.Error(), "secret-password") {
				t.Fatalf("load() error leaked password: %v", err)
			}
		})
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	t.Parallel()
	legacySourcesKey := "alarm" + "_" + "sources"
	legacySourceIDKey := "alarm" + "_" + "source" + "_" + "id"

	tests := []struct {
		name      string
		content   string
		wantError string
	}{
		{name: "empty document", content: "", wantError: "decode config"},
		{name: "removed version field", content: "version: 1\n", wantError: "field version not found"},
		{name: "legacy source collection", content: legacySourcesKey + ": []\n", wantError: "field " + legacySourcesKey + " not found"},
		{name: "legacy source identity", content: "event_sources:\n  - " + legacySourceIDKey + ": source-a\n", wantError: "field " + legacySourceIDKey + " not found"},
		{name: "unknown top-level field", content: "server: {}\n", wantError: "field server not found"},
		{name: "unknown nested field", content: "logging:\n  output: stdout\n", wantError: "field output not found"},
		{name: "duplicate key", content: "logging: {}\nlogging: {}\n", wantError: "mapping key \"logging\" already defined"},
		{name: "multiple documents", content: "{}\n---\n{}\n", wantError: "multiple YAML documents"},
		{name: "invalid type", content: "logging:\n  level: [info]\n", wantError: "cannot unmarshal"},
		{name: "malformed yaml", content: "logging: [\n", wantError: "decode config"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			path := writeConfig(t, test.content)
			_, err := load(path, Overrides{}, mapLookup(nil))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("load() error = %v, want containing %q", err, test.wantError)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("load() error = %q, want config path %q", err, path)
			}
		})
	}
}

func TestLoadRejectsOversizedFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "linkd.yaml")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'#'}, MaxFileSize+1), 0o600); err != nil {
		t.Fatalf("write oversized config: %v", err)
	}

	_, err := load(path, Overrides{}, mapLookup(nil))
	if err == nil || !strings.Contains(err.Error(), "file exceeds 1048576 bytes") {
		t.Fatalf("load() error = %v, want oversized file error", err)
	}
}

func TestLoadReportsMissingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.yaml")
	_, err := load(path, Overrides{}, mapLookup(nil))
	if err == nil || !strings.Contains(err.Error(), "open config") || !strings.Contains(err.Error(), path) {
		t.Fatalf("load() error = %v, want missing file and path", err)
	}
}

func TestLoadUsesProcessEnvironment(t *testing.T) {
	path := writeConfig(t, "{}\n")
	t.Setenv(envLogLevel, logging.LevelDebug)
	t.Setenv(envLogFormat, logging.FormatText)

	cfg, err := Load(path, Overrides{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Logging != (logging.Config{Level: logging.LevelDebug, Format: logging.FormatText}) {
		t.Fatalf("Load() logging = %#v", cfg.Logging)
	}
}

func TestMarshalRedacted(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Logging:      logging.Config{Level: logging.LevelWarn, Format: logging.FormatText},
		EventSources: []EventSource{},
	}
	data, err := MarshalRedacted(cfg)
	if err != nil {
		t.Fatalf("MarshalRedacted() error = %v", err)
	}
	if !strings.Contains(string(data), "default_severity: warning") || !strings.Contains(string(data), "event_sources: []") {
		t.Fatalf("MarshalRedacted() = %q", data)
	}
}

func TestMarshalRedactedEventSourcePassword(t *testing.T) {
	t.Parallel()

	sasl := &kafkaclient.SASLConfig{
		Mechanism: kafkaclient.SASLMechanismPlain,
		Username:  "linkd",
		Password:  "secret-password",
	}
	cfg := Config{
		Logging: logging.DefaultConfig(),
		EventSources: []EventSource{
			{
				EventSourceID: "source-a",
				Enabled:       true,
				Storage: EventSourceStorageConfig{
					Type: StorageTypeKafka,
					Kafka: KafkaStorageConfig{
						Brokers:       []string{"kafka:9092"},
						Topic:         "raw-events",
						ConsumerGroup: "linkd",
						Security: kafkaclient.SecurityConfig{
							Protocol: kafkaclient.SecurityProtocolSASLSSL,
							SASL:     sasl,
						},
					},
				},
			},
		},
	}

	data, err := MarshalRedacted(cfg)
	if err != nil {
		t.Fatalf("MarshalRedacted() error = %v", err)
	}
	if strings.Contains(string(data), "secret-password") || !strings.Contains(string(data), "password: '******'") {
		t.Fatalf("MarshalRedacted() = %s", data)
	}
	if sasl.Password != "secret-password" || cfg.EventSources[0].Storage.Kafka.Brokers[0] != "kafka:9092" {
		t.Fatalf("MarshalRedacted() changed original config = %#v", cfg.EventSources[0])
	}
}

func TestMarshalRedactedStorageSecrets(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Logging: logging.DefaultConfig(),
		Storage: &StorageConfig{
			MySQL: &MySQLConfig{
				Address: "mysql:3306", Database: "linkd", Username: "linkd", Password: "mysql-secret",
			},
			Elasticsearch: &ElasticsearchConfig{
				Addresses: []string{"http://elasticsearch:9200"}, APIKey: "elastic-secret",
			},
			Redis: &RedisConfig{Address: "redis:6379", Password: "redis-secret"},
		},
		EventSources: []EventSource{},
	}
	data, err := MarshalRedacted(cfg)
	if err != nil {
		t.Fatalf("MarshalRedacted() error = %v", err)
	}
	output := string(data)
	for _, secret := range []string{"mysql-secret", "elastic-secret", "redis-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("MarshalRedacted() leaked %q: %s", secret, output)
		}
	}
	if strings.Count(output, "'******'") != 3 {
		t.Fatalf("MarshalRedacted() = %s", output)
	}
	if cfg.Storage.MySQL.Password != "mysql-secret" || cfg.Storage.Elasticsearch.APIKey != "elastic-secret" ||
		cfg.Storage.Redis.Password != "redis-secret" {
		t.Fatalf("MarshalRedacted() changed original storage config = %#v", cfg.Storage)
	}
}

func TestMarshalRedactedLifecycleKafkaPassword(t *testing.T) {
	t.Parallel()

	lifecycle := validLifecycleConfigForTest()
	lifecycle.Output.Kafka.Security = kafkaclient.SecurityConfig{
		Protocol: "sasl_ssl",
		SASL: &kafkaclient.SASLConfig{
			Mechanism: "plain",
			Username:  "linkd",
			Password:  "lifecycle-secret",
		},
	}
	cfg := Config{
		Logging:      logging.DefaultConfig(),
		Lifecycle:    &lifecycle,
		EventSources: []EventSource{},
	}
	data, err := MarshalRedacted(cfg)
	if err != nil {
		t.Fatalf("MarshalRedacted() error = %v", err)
	}
	if strings.Contains(string(data), "lifecycle-secret") ||
		!strings.Contains(string(data), "password: '******'") {
		t.Fatalf("MarshalRedacted() = %s", data)
	}
	if cfg.Lifecycle.Output.Kafka.Security.SASL.Password != "lifecycle-secret" {
		t.Fatal("MarshalRedacted() changed original lifecycle password")
	}
}

func TestLoadResolvesAndValidatesKafkaTLSFiles(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	certDirectory := filepath.Join(directory, "certs")
	if err := os.Mkdir(certDirectory, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	caPath := filepath.Join(certDirectory, "ca.pem")
	if err := os.WriteFile(caPath, newTestCAPEM(t), 0o600); err != nil {
		t.Fatalf("WriteFile(CA) error = %v", err)
	}
	configPath := filepath.Join(directory, "linkd.yaml")
	content := `lifecycle:
  output:
    kafka:
      brokers: [kafka.example.com:9093]
      topic: alerts
      security:
        protocol: ssl
        tls:
          ca_file: ./certs/ca.pem
event_sources:
  - event_source_id: source-a
    enabled: true
    storage:
      type: kafka
      kafka:
        brokers: [kafka.example.com:9093]
        topic: raw-events
        consumer_group: linkd
        security:
          protocol: ssl
          tls:
            ca_file: ./certs/ca.pem
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cfg, err := load(configPath, Overrides{}, mapLookup(nil))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	gotPath := cfg.EventSources[0].Storage.Kafka.Security.TLS.CAFile
	if gotPath != caPath {
		t.Fatalf("load() ca_file = %q, want %q", gotPath, caPath)
	}
	if lifecyclePath := cfg.Lifecycle.Output.Kafka.Security.TLS.CAFile; lifecyclePath != caPath {
		t.Fatalf("load() lifecycle ca_file = %q, want %q", lifecyclePath, caPath)
	}
	printed, err := MarshalRedacted(cfg)
	if err != nil {
		t.Fatalf("MarshalRedacted() error = %v", err)
	}
	if strings.Count(string(printed), "ca_file: "+caPath) != 2 {
		t.Fatalf("MarshalRedacted() did not print resolved path: %s", printed)
	}

	if err := os.WriteFile(caPath, []byte("invalid CA"), 0o600); err != nil {
		t.Fatalf("WriteFile(invalid CA) error = %v", err)
	}
	_, err = load(configPath, Overrides{}, mapLookup(nil))
	if err == nil || !strings.Contains(err.Error(), caPath) || !strings.Contains(err.Error(), "valid certificate") {
		t.Fatalf("load(invalid CA) error = %v", err)
	}
}

func TestLoadRejectsUnknownKafkaTLSField(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, `event_sources:
  - event_source_id: source-a
    enabled: true
    storage:
      type: kafka
      kafka:
        brokers: [kafka.example.com:9093]
        topic: raw-events
        consumer_group: linkd
        security:
          protocol: ssl
          tls:
            ca_path: ./ca.pem
`)
	_, err := load(path, Overrides{}, mapLookup(nil))
	if err == nil || !strings.Contains(err.Error(), "field ca_path not found") {
		t.Fatalf("load() error = %v, want unknown TLS field", err)
	}
}

func TestMarshalRedactedKafkaInlinePrivateKey(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Logging: logging.DefaultConfig(),
		Lifecycle: &LifecycleConfig{Output: LifecycleOutputConfig{Kafka: &LifecycleKafkaConfig{
			Brokers: []string{"kafka:9093"},
			Topic:   "alerts",
			Security: kafkaclient.SecurityConfig{
				Protocol: kafkaclient.SecurityProtocolSSL,
				TLS: &kafkaclient.TLSConfig{
					CAPEM:         "public-ca",
					ClientCertPEM: "public-client-certificate",
					ClientKeyPEM:  "private-client-key",
				},
			},
		}}},
		EventSources: []EventSource{},
	}
	data, err := MarshalRedacted(cfg)
	if err != nil {
		t.Fatalf("MarshalRedacted() error = %v", err)
	}
	output := string(data)
	if strings.Contains(output, "private-client-key") || !strings.Contains(output, "client_key_pem: '******'") {
		t.Fatalf("MarshalRedacted() leaked private key: %s", output)
	}
	for _, publicValue := range []string{"public-ca", "public-client-certificate"} {
		if !strings.Contains(output, publicValue) {
			t.Fatalf("MarshalRedacted() hid %q: %s", publicValue, output)
		}
	}
	if cfg.Lifecycle.Output.Kafka.Security.TLS.ClientKeyPEM != "private-client-key" {
		t.Fatal("MarshalRedacted() changed original private key")
	}
}

func validLifecycleConfigForTest() LifecycleConfig {
	return LifecycleConfig{
		Output: LifecycleOutputConfig{
			Kafka: &LifecycleKafkaConfig{
				Brokers: []string{"kafka.example.com:9092"},
				Topic:   "linkd-alerts",
			},
		},
	}.WithDefaults()
}

func newTestCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Linkd Config Test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "linkd.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}

func stringPointer(value string) *string {
	return &value
}
