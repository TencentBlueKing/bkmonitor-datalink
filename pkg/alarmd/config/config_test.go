// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

func TestRedisConnectionSupportsStandaloneAndSentinel(t *testing.T) {
	standalone := validGoAccessConfigObject()
	standalone.Redis.Mode = RedisModeStandalone
	if err := standalone.Validate(); err != nil {
		t.Fatalf("standalone Validate() error = %v", err)
	}

	sentinel := validGoAccessConfigObject()
	sentinel.Redis.Mode = RedisModeSentinel
	sentinel.Redis.Address = ""
	sentinel.Redis.SentinelAddress = []string{"sentinel-a:26379", "sentinel-b:26379"}
	sentinel.Redis.MasterName = "monitor-master"
	sentinel.Redis.SentinelUsername = "sentinel-user"
	sentinel.Redis.SentinelPassword = "sentinel-secret"
	if err := sentinel.Validate(); err != nil {
		t.Fatalf("sentinel Validate() error = %v", err)
	}

	for name, mutate := range map[string]func(*Config){
		"unknown mode": func(cfg *Config) { cfg.Redis.Mode = "cluster" },
		"standalone address": func(cfg *Config) {
			cfg.Redis.Mode = RedisModeStandalone
			cfg.Redis.Address = ""
		},
		"sentinel addresses": func(cfg *Config) {
			cfg.Redis.SentinelAddress = nil
		},
		"sentinel master": func(cfg *Config) { cfg.Redis.MasterName = "" },
		"duplicate sentinel": func(cfg *Config) {
			cfg.Redis.SentinelAddress = []string{"sentinel-a:26379", "sentinel-a:26379"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := sentinel
			mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "redis") {
				t.Fatalf("Validate() error = %v, want Redis connection rejection", err)
			}
		})
	}
}

// Stating where a platform cache lives moves that read and nothing else.
// alarmd's own store is the top-level connection, and the way to move it is to
// move the top-level connection.
func TestAPlatformCacheOverrideDoesNotMoveAlarmdsOwnStore(t *testing.T) {
	cfg := validGoAccessConfigObject()
	strategyCache := cfg.Redis.Connection()
	strategyCache.Address = "strategy-cache:6379"
	cfg.PlatformCache.Strategy = &strategyCache
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected a stated platform cache: %v", err)
	}
	if got := cfg.StrategySourceRedis(); got.Address != "strategy-cache:6379" {
		t.Fatalf("strategy source = %+v, want the stated platform cache", got)
	}
	if got := cfg.RuntimeStoreRedis(); got.Address != "redis.test:6379" {
		t.Fatalf("runtime store = %+v, want the top-level connection", got)
	}
	// An unstated host cache follows the top-level connection, not the cache
	// that happened to be stated next to it.
	if got := cfg.CMDBCacheRedis(); got.Address != "redis.test:6379" {
		t.Fatalf("cmdb cache = %+v, want the top-level connection", got)
	}
}

func TestPhaseTwoRuntimePrefixCannotOverlapCanonicalStrategyCache(t *testing.T) {
	for name, prefix := range map[string]string{
		"same":           "alarm-config",
		"source child":   "alarm-config:runtime:v2",
		"source parent":  "alarm",
		"redis hash tag": "alarmd:{phase-two}:v2",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validGoAccessConfigObject()
			cfg.Redis.StatePrefix = prefix
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "state_prefix") {
				t.Fatalf("Validate() error = %v, want prefix isolation rejection", err)
			}
		})
	}
}

func TestDefaultRequiresExplicitEnvironmentCoordinates(t *testing.T) {
	cfg := Default()

	if cfg.Input.Mode != InputModeGoAccess {
		t.Fatalf("default input = %+v, want Go Access without compatibility coordinates", cfg.Input)
	}
	if cfg.HTTP.Listen == "" || cfg.ShutdownTimeout.Duration() <= 0 {
		t.Fatal("default local HTTP and shutdown budgets must be usable")
	}
	if cfg.Kafka.TriggerEvent.MaxMessageBytes <= 0 {
		t.Fatal("default output byte budgets must be positive")
	}
	if cfg.Redis.Address != "" || len(cfg.Kafka.Brokers) != 0 {
		t.Fatal("environment Kafka and Redis coordinates must not have defaults")
	}
	// The state prefix is not an environment coordinate: it is the program's
	// own key space and schema generation, so it has the program's default.
	if cfg.Redis.StatePrefix != DefaultStatePrefix {
		t.Fatalf("default state prefix = %q, want the program's %q", cfg.Redis.StatePrefix, DefaultStatePrefix)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "broker") {
		t.Fatalf("default configuration error = %v, want missing Kafka broker", err)
	}
}

// The consumer-side Kafka fields survive the input runtime that filled them.
// This pins that a valid configuration leaves every one of them empty and
// validates with no receipt budget at all, so the fields cannot quietly come
// back into use without an assertion noticing.
func TestValidConfigurationLeavesConsumerKafkaFieldsEmpty(t *testing.T) {
	cfg := validGoAccessConfigObject()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Kafka.InputTopic != "" || cfg.Kafka.GroupID != "" || cfg.Kafka.InitialOffset != "" {
		t.Fatalf("a valid configuration carries consumer-side Kafka assets: %+v", cfg.Kafka)
	}
}

func TestGoAccessValidatesOnlyTriggerEventKafkaTopology(t *testing.T) {
	tests := map[string]func(*Config){
		"missing broker":        func(cfg *Config) { cfg.Kafka.Brokers = nil },
		"invalid broker":        func(cfg *Config) { cfg.Kafka.Brokers = []string{"missing-port"} },
		"duplicate broker":      func(cfg *Config) { cfg.Kafka.Brokers = append(cfg.Kafka.Brokers, cfg.Kafka.Brokers[0]) },
		"invalid trigger topic": func(cfg *Config) { cfg.Kafka.TriggerEvent.Topic = "invalid topic" },
		// The two output topics carry different wire formats, so one topic
		// named twice is a stream whose consumer can only read half of it.
		"native and compatibility topics are the same": func(cfg *Config) {
			cfg.Kafka.LegacyAdapter.Topic = cfg.Kafka.TriggerEvent.Topic
		},
		"missing client":    func(cfg *Config) { cfg.Kafka.ClientID = "" },
		"invalid version":   func(cfg *Config) { cfg.Kafka.BrokerVersion = "not-a-version" },
		"zero output bytes": func(cfg *Config) { cfg.Kafka.TriggerEvent.MaxMessageBytes = 0 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := validGoAccessConfigObject()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() accepted %s", name)
			}
		})
	}
}

func TestLoadRejectsLegacyAndPhaseTwoFields(t *testing.T) {
	for name, testCase := range map[string]struct {
		contents string
		field    string
	}{
		"legacy output": {
			contents: strings.Replace(validGoAccessRuntimeConfigYAML("worker-a"), "  trigger_event:\n", "  output_topic: legacy-output\n  trigger_event:\n", 1),
			field:    "output_topic",
		},
		"worker shards": {contents: "worker_shards: 4\n", field: "worker_shards"},
		"owner epoch":   {contents: "owner_epoch: 1\n", field: "owner_epoch"},
		"state CAS":     {contents: "state_cas: true\n", field: "state_cas"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, testCase.contents))
			if err == nil || !strings.Contains(err.Error(), testCase.field) {
				t.Fatalf("Load() error = %v, want strict rejection of %q", err, testCase.field)
			}
		})
	}
}

func TestLoadRejectsUnsafeKafkaTopicTopology(t *testing.T) {
	tests := map[string]func(*Config){
		"zero trigger event bytes": func(cfg *Config) { cfg.Kafka.TriggerEvent.MaxMessageBytes = 0 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := validGoAccessConfigObject()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() accepted %s", name)
			}
		})
	}
}

func TestValidateRejectsInvalidRedisAndRuntimeBudgets(t *testing.T) {
	tests := map[string]func(*Config){
		"missing Redis address": func(cfg *Config) { cfg.Redis.Address = "" },
		"negative Redis DB":     func(cfg *Config) { cfg.Redis.DB = -1 },
		"negative Redis pool":   func(cfg *Config) { cfg.Redis.PoolSize = -1 },
		"blank state prefix":    func(cfg *Config) { cfg.Redis.StatePrefix = " " },
		"reversed state TTL":    func(cfg *Config) { cfg.Redis.MaxTTL = cfg.Redis.MinTTL - 1 },
		"negative restart margin": func(cfg *Config) {
			cfg.Redis.RestartMargin = Duration(-time.Second)
		},
		"zero reader budget":   func(cfg *Config) { cfg.Limits.Reader.MaxEnvelopeBytes = 0 },
		"zero compiler budget": func(cfg *Config) { cfg.Limits.Compiler.MaxPlanBytes = 0 },
		"zero detect budget":   func(cfg *Config) { cfg.Limits.Detect.MaxPlans = 0 },
		"zero trigger budget":  func(cfg *Config) { cfg.Limits.Trigger.MaxLevels = 0 },
		"compiler levels exceed trigger event": func(cfg *Config) {
			cfg.Limits.Trigger.MaxLevelResultsPerEvent = uint32(cfg.Limits.Compiler.MaxLevelsPerPlan - 1)
		},
		"zero codec budget": func(cfg *Config) { cfg.Limits.Codec.MaxLevels = 0 },
		"zero store budget": func(cfg *Config) { cfg.Limits.Store.MaxKeysPerBatch = 0 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := validGoAccessConfigObject()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() accepted %s", name)
			}
		})
	}
}

// The top-level mode key is retired: shadow was its only value and the
// process never behaved differently for it. A deployment that still writes it
// is told so at load rather than having the key silently ignored.
func TestLoadRejectsRetiredModeKey(t *testing.T) {
	for _, mode := range []string{"shadow", "owner"} {
		t.Run(mode, func(t *testing.T) {
			_, err := Load(writeConfig(t, "mode: "+mode+"\n"))
			if err == nil || !strings.Contains(err.Error(), "mode") {
				t.Fatalf("Load() error = %v, want retired key rejection", err)
			}
		})
	}
}

func TestLoadRejectsInvalidHTTPAndTimeout(t *testing.T) {
	tests := map[string]string{
		"listen":         "http:\n  listen: invalid\n",
		"empty host":     "http:\n  listen: :8080\n",
		"zero port":      "http:\n  listen: 127.0.0.1:0\n",
		"port too large": "http:\n  listen: 127.0.0.1:65536\n",
		"timeout":        "shutdown_timeout: 0s\n",
	}

	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, contents)); err == nil {
				t.Fatalf("Load() accepted invalid %s", name)
			}
		})
	}
}

func TestDurationRejectsInvalidText(t *testing.T) {
	var duration Duration
	if err := duration.UnmarshalText([]byte("forever")); err == nil {
		t.Fatal("UnmarshalText() accepted an invalid duration")
	}
	if err := duration.UnmarshalText([]byte("3s")); err != nil {
		t.Fatalf("UnmarshalText() rejected a valid duration: %v", err)
	}
	if got := duration.Duration(); got != 3*time.Second {
		t.Fatalf("Duration() = %s, want 3s", got)
	}
}

type staticRouter struct{}

func (staticRouter) Route(_, _ string) (state.StorageTarget, error) {
	return state.StorageTarget{Name: "primary", Backend: nil}, nil
}

// withCompatibilityServiceRedis gives a configuration the service Redis the
// built-in Python-compatible protocol needs. Every deployment needs it, because
// a strategy without a frozen revision selects that protocol and its snapshot
// is written before the event is published.
func withCompatibilityServiceRedis(cfg *Config, address string) {
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = "alarmd-test"
	// Load resolves the timeouts from the runtime Redis; a configuration built
	// in Go and validated directly has to state them.
	cfg.Kafka.LegacyAdapter.ServiceRedis = RedisConnectionConfig{
		Mode: RedisModeStandalone, Address: address,
		DialTimeout:  cfg.Redis.DialTimeout,
		ReadTimeout:  cfg.Redis.ReadTimeout,
		WriteTimeout: cfg.Redis.WriteTimeout,
	}
}

func validGoAccessConfigObject() Config {
	cfg := Default()
	accessBKData := false
	cfg.Kafka.Brokers = []string{"127.0.0.1:9092"}
	cfg.Kafka.TriggerEvent.Topic = "alarmd-trigger-event"
	cfg.Kafka.AllowedOutputTopics = []string{"alarmd-trigger-event", cfg.Kafka.LegacyAdapter.Topic}
	withCompatibilityServiceRedis(&cfg, "redis.test:6379")
	cfg.Kafka.ClientID = "alarmd"
	cfg.Kafka.BrokerVersion = "2.6.0"
	cfg.Redis.Address = "redis.test:6379"
	cfg.Redis.StatePrefix = "alarmd-phase-two"
	cfg.PhaseTwo.Worker.ID = "alarmd-worker-0"
	cfg.PhaseTwo.Control.StrategyCachePrefix = "alarm-config"
	cfg.PhaseTwo.Control.ProviderRoute = "unify-query-primary"
	cfg.PhaseTwo.Control.Timezone = "Asia/Shanghai"
	cfg.PhaseTwo.Control.LegacyQueryRuntime.AccessBKData = &accessBKData
	cfg.PhaseTwo.Control.LegacyQueryRuntime.BKDataCMDBLevelTables = []string{}
	cfg.PhaseTwo.Control.LegacyQueryRuntime.SystemDiskFilter = PhaseTwoRuntimeFilterConfig{FieldName: "device_type", Values: []string{}}
	cfg.PhaseTwo.Control.LegacyQueryRuntime.SystemNetworkFilter = PhaseTwoRuntimeFilterConfig{FieldName: "device_name", Values: []string{}}
	cfg.PhaseTwo.Access.UQEndpoint = "http://unify-query.service"
	cfg.PhaseTwo.Access.QuerySource = "alarmd"
	return cfg
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// Zero no longer means "unset and invalid": it means the pool follows the CPU
// budget the container was given. The cases below pin the floor a one-core
// deployment keeps, the production sizes, and the ceiling that stops one Worker
// from opening an unreasonable number of connections to a shared Redis.
func TestDeriveRedisPoolSizeFollowsCPUBudget(t *testing.T) {
	tests := []struct {
		name      string
		cpuBudget int
		want      int
	}{
		{"a single core keeps the floor", 1, 64},
		{"the floor still covers four cores", 4, 64},
		{"the production budget scales past the floor", 8, 128},
		{"a larger budget keeps scaling", 16, 256},
		{"the ceiling bounds the connection count", 64, 512},
		{"an unreported budget still yields a usable pool", 0, 64},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := DeriveRedisPoolSize(test.cpuBudget); got != test.want {
				t.Fatalf("DeriveRedisPoolSize(%d) = %d, want %d", test.cpuBudget, got, test.want)
			}
		})
	}
}

func TestEffectivePoolSizeKeepsExplicitOverride(t *testing.T) {
	connection := RedisConnectionConfig{PoolSize: 3}
	if got := connection.EffectivePoolSize(8); got != 3 {
		t.Fatalf("explicit pool_size was not honoured: got %d", got)
	}
	connection.PoolSize = 0
	if got := connection.EffectivePoolSize(8); got != 128 {
		t.Fatalf("zero pool_size did not derive: got %d", got)
	}
}

// The pool exists so that queueing for a connection never becomes the process's
// execution gate. The Slot source reads the control plane for every ready
// runner without holding a query permit, so covering the permits is necessary
// but nowhere near sufficient; the derived pool must clear them with room left
// for that fan-out at every budget a container can be given.
func TestDerivedRedisPoolClearsAdmittedConcurrencyWithRoom(t *testing.T) {
	cfg := Default()
	admitted := cfg.AdmittedQueryConcurrency()
	for cpuBudget := 0; cpuBudget <= 64; cpuBudget++ {
		if got := DeriveRedisPoolSize(cpuBudget); got <= admitted {
			t.Fatalf("pool %d at %d CPU does not exceed admitted concurrency %d", got, cpuBudget, admitted)
		}
	}
}

func TestWithResolvedRedisPoolSizeResolvesEveryConnection(t *testing.T) {
	cfg := Default()
	cfg.Input.Mode = InputModeGoAccess
	platformCache := cfg.Redis.Connection()
	cfg.PlatformCache.Strategy = &platformCache
	cfg.PlatformCache.CMDB = &platformCache
	resolved := cfg.WithResolvedRedisPoolSize()
	want := DeriveRedisPoolSize(runtime.GOMAXPROCS(0))
	if resolved.Redis.PoolSize != want {
		t.Fatalf("own store pool size = %d, want %d", resolved.Redis.PoolSize, want)
	}
	for name, connection := range map[string]*RedisConnectionConfig{
		"strategy": resolved.PlatformCache.Strategy, "cmdb": resolved.PlatformCache.CMDB,
	} {
		if connection == nil || connection.PoolSize != want {
			t.Fatalf("%s pool size was not resolved: %+v", name, connection)
		}
	}
	if cfg.Redis.PoolSize != 0 || cfg.PlatformCache.Strategy.PoolSize != 0 {
		t.Fatal("WithResolvedRedisPoolSize mutated its receiver")
	}
}

// The platform routes its cache backend per module, so the strategy cache and
// the host cache may each be somewhere other than the instance the rest of the
// deployment points at. Reading either off the wrong connection returns no
// error - it returns nothing, which reads back as "no strategies" or "no
// hosts". Each therefore has its own stated location.
func TestEachPlatformCacheIsReadWhereThePlatformWritesIt(t *testing.T) {
	loaded, err := Load(writeConfig(t, platformCacheConfigContents(`
platform_cache:
  strategy:
    mode: standalone
    address: strategy-cache:6379
    db: 8
  cmdb:
    mode: standalone
    address: cmdb-cache:6379
    db: 8
`)))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.StrategySourceRedis().Address; got != "strategy-cache:6379" {
		t.Fatalf("strategy source = %q, want the stated strategy cache", got)
	}
	if got := loaded.CMDBCacheRedis().Address; got != "cmdb-cache:6379" {
		t.Fatalf("cmdb cache = %q, want the stated host cache", got)
	}
	// alarmd's own store is a third location and is untouched by either.
	if got := loaded.RuntimeStoreRedis().Address; got != "runtime-store:6379" {
		t.Fatalf("runtime = %q, want alarmd's own store", got)
	}
	// Stating where to read must not mean restating how long to wait: a
	// deployment that has to repeat the timeouts will eventually repeat them
	// differently, and a shorter one on the host cache alone reads back as a
	// CMDB gap rather than as a timeout.
	for name, connection := range map[string]RedisConnectionConfig{
		"strategy": loaded.StrategySourceRedis(), "cmdb": loaded.CMDBCacheRedis(),
	} {
		if connection.ReadTimeout != loaded.Redis.ReadTimeout || connection.DialTimeout != loaded.Redis.DialTimeout {
			t.Fatalf("%s cache timeouts = %+v, want the process-wide ones", name, connection)
		}
	}
}

// Unstated means "the same instance as the rest of the deployment", which is
// what a platform that does not use the per-module routing looks like. It is
// resolved at load rather than worked out again at each call site, so the
// resolved configuration a release check reads says where each read went
// instead of saying "inherited".
func TestAnUnstatedPlatformCacheResolvesToTheTopLevelConnection(t *testing.T) {
	loaded, err := Load(writeConfig(t, platformCacheConfigContents("")))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.PlatformCache.Strategy == nil || loaded.PlatformCache.CMDB == nil {
		t.Fatalf("platform cache = %+v, want both resolved rather than left unstated", loaded.PlatformCache)
	}
	for name, address := range map[string]string{
		"strategy": loaded.StrategySourceRedis().Address,
		"cmdb":     loaded.CMDBCacheRedis().Address,
	} {
		if address != "runtime-store:6379" {
			t.Fatalf("%s cache = %q, want the top-level connection", name, address)
		}
	}
}

func platformCacheConfigContents(platformCache string) string {
	return `input:
  mode: go_access
http:
  listen: 127.0.0.1:8080
kafka:
  brokers: [127.0.0.1:9092]
  trigger_event:
    topic: alarmd-trigger-event
  allowed_output_topics: [alarmd-trigger-event, alarmd_0bkmonitor_backend_event]
  legacy_adapter:
    topic: alarmd_0bkmonitor_backend_event
    snapshot_prefix: alarmd-test
    service_redis:
      mode: standalone
      address: redis.test:6379
redis:
  mode: standalone
  address: runtime-store:6379
  db: 8
  state_prefix: alarmd:phase-two:g1:v1` + platformCache + `
phase_two:
  worker:
    id: alarmd-worker-0
  control:
    strategy_cache_prefix: alarm-config
    timezone: Asia/Shanghai
    legacy_query_runtime:
      access_bk_data: false
      bkdata_cmdb_level_tables: []
      system_disk_filter:
        field_name: device_type
        values: []
  access:
    uq_endpoint: http://unify-query.service
    query_source: alarmd
`
}

// The startup evidence surface answers "which instance did this replica read
// from", which is the question an incident asks once a location can be
// inherited. It is credential-free by contract, and the check is here rather
// than in review because the fields it renders sit next to two passwords.
func TestTheResolvedDestinationNamesCoordinatesAndNoCredentials(t *testing.T) {
	sentinel := RedisConnectionConfig{
		Mode: RedisModeSentinel, MasterName: "monitor", SentinelAddress: []string{"a:26379", "b:26379"},
		Password: "connection-password", SentinelPassword: "sentinel-password", DB: 8,
	}
	if got := sentinel.Destination(); got != "sentinel monitor [a:26379,b:26379]/8" {
		t.Fatalf("destination = %q", got)
	}
	standalone := RedisConnectionConfig{
		Mode: RedisModeStandalone, Address: "cache:6379", Password: "connection-password", DB: 10,
	}
	if got := standalone.Destination(); got != "standalone cache:6379/10" {
		t.Fatalf("destination = %q", got)
	}
	for _, connection := range []RedisConnectionConfig{sentinel, standalone} {
		for _, credential := range []string{"connection-password", "sentinel-password"} {
			if strings.Contains(connection.Destination(), credential) {
				t.Fatalf("destination %q carries a credential", connection.Destination())
			}
		}
	}
}

// The key that used to move alarmd's own state on its own is gone, and a
// configuration still carrying it is refused rather than silently ignored: it
// moved the connection while the prefix and the TTLs that parameterise those
// keys stayed under the key it moved away from, so accepting it quietly would
// write to the new instance with the old instance's shape.
func TestTheRemovedRuntimeRedisKeyIsRefusedRatherThanIgnored(t *testing.T) {
	_, err := Load(writeConfig(t, platformCacheConfigContents("")+`  runtime_redis:
    mode: standalone
    address: somewhere-else:6379
`))
	if err == nil || !strings.Contains(err.Error(), "runtime_redis") {
		t.Fatalf("Load() error = %v, want the removed key named", err)
	}
}

// The platform's key prefix is one fact with one home. The read sites ask for
// it by name rather than reaching into the compatibility adapter's
// configuration, and the accessor is the single place that knows where it is
// currently stated - which is what makes moving the field later a one-line
// change rather than a hunt.
func TestThePlatformKeyPrefixIsOneFactWithOneSpelling(t *testing.T) {
	cfg := validGoAccessConfigObject()
	if got := cfg.PlatformKeyPrefix(); got != cfg.Kafka.LegacyAdapter.SnapshotPrefix || got == "" {
		t.Fatalf("platform key prefix = %q, want the stated platform prefix", got)
	}
	// A valid configuration cannot leave it empty: an empty prefix reads the
	// wrong key space, and that returns nothing rather than failing.
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "snapshot_prefix") {
		t.Fatalf("Validate() error = %v, want the missing platform prefix rejected", err)
	}
}

// The wire format is a deployment choice with exactly three words, and an
// unrecognised one has to be refused at load: a typo that fell through to the
// default would publish a format the deployment did not ask for, and nothing
// downstream would say so.
func TestTheOutputProtocolIsOneOfThreeWords(t *testing.T) {
	for _, protocol := range []string{"", "auto", "legacy", "native"} {
		cfg := validGoAccessConfigObject()
		cfg.PhaseTwo.Output.Protocol = protocol
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() rejected protocol %q: %v", protocol, err)
		}
	}
	for _, protocol := range []string{"linkd", "Native", "python", "off"} {
		cfg := validGoAccessConfigObject()
		cfg.PhaseTwo.Output.Protocol = protocol
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "protocol") {
			t.Fatalf("Validate() error = %v, want protocol %q rejected", err, protocol)
		}
	}
	// Unset is auto, which is the behaviour that was already in force.
	cfg := validGoAccessConfigObject()
	if got := cfg.OutputProtocol(); got != "auto" {
		t.Fatalf("default protocol = %q, want auto", got)
	}
}
