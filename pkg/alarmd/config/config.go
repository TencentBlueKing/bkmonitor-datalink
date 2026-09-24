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
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

type Duration time.Duration

func (d *Duration) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("parse duration: %w", err)
	}
	*d = Duration(value)
	return nil
}

func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

type HTTPConfig struct {
	// Listen carries health, metrics and the observability API. It is the only
	// surface a host platform may route to.
	Listen string `yaml:"listen"`
	// DiagnosticsListen carries pprof on its own listener, defaulting to
	// loopback so routing the query surface cannot expose it. An empty value
	// serves no diagnostics at all; pprof is never folded back into Listen.
	DiagnosticsListen string `yaml:"diagnostics_listen"`
	// InternalListen carries /metrics, /healthz and /readyz for the cluster
	// alone. A restricted public surface (PublicSurfaceRestrictionRequested,
	// once the CLI is up) stops serving /metrics on Listen, so the scrape
	// needs this port; without it the process still runs and reports that
	// its metrics have no way out. Empty otherwise serves nothing extra: an
	// unrestricted Listen still carries all three.
	InternalListen string `yaml:"internal_listen"`
}

// CLIConfig enables the deployment-operator evidence channel. AdminKey authorizes
// grant issuance directly in alarmd; it is not a CLI session token or user identity.
// The key must never be included in runtime configuration evidence.
type CLIConfig struct {
	Enabled         bool   `yaml:"enabled"`
	EnvironmentID   string `yaml:"environment_id"`
	EnvironmentName string `yaml:"environment_name"`
	PublicBaseURL   string `yaml:"public_base_url"`
	AdminKey        string `yaml:"admin_key" json:"-"`
}

// CLIAdminKeyEnvironment carries the administrator key when the deployment
// keeps it in a Secret of its own rather than in the rendered configuration:
// a chart then references the Secret and the key never appears in values.
const CLIAdminKeyEnvironment = "ALARMD_CLI_ADMIN_KEY"

// resolveAdminKeyFromEnvironment fills the administrator key from the
// environment. A key stated in both places is refused: two sources for one
// secret is a deployment that can rotate one and keep using the other.
func (c *CLIConfig) resolveAdminKeyFromEnvironment() error {
	env, ok := os.LookupEnv(CLIAdminKeyEnvironment)
	if !ok || env == "" {
		return nil
	}
	if c.AdminKey != "" {
		return errors.New("cli admin_key is set both in the file and in " + CLIAdminKeyEnvironment)
	}
	c.AdminKey = env
	return nil
}

// DiagnosticsFact renders the diagnostics surface for startup logging. Every
// runtime reports it through this one helper so the three of them cannot drift
// into disagreeing about what an unset address means.
func (c HTTPConfig) DiagnosticsFact() string {
	if c.DiagnosticsListen == "" {
		return "disabled"
	}
	return c.DiagnosticsListen
}

type KafkaOutputConfig struct {
	Topic           string `yaml:"topic"`
	MaxMessageBytes int    `yaml:"max_message_bytes"`
}

type KafkaConfig struct {
	LegacyAdapter LegacyAdapterConfig `yaml:"legacy_adapter"`
	Brokers       []string            `yaml:"brokers"`
	InputTopic    string              `yaml:"input_topic"`
	TriggerEvent  KafkaOutputConfig   `yaml:"trigger_event"`
	// Deprecated: accepted and ignored. It required every output topic to be
	// repeated in a list, which protected nothing the topics themselves did not
	// already state, and turned "add an output topic" into a startup failure
	// when the second place was forgotten. The field stays only so a rendered
	// configuration that still carries it keeps loading; it is removed once no
	// deployment states it.
	AllowedOutputTopics []string `yaml:"allowed_output_topics"`
	GroupID             string   `yaml:"group_id"`
	// ClientID and BrokerVersion identify this producer to the broker and fix
	// the protocol it speaks. Neither is something a deployment knows better
	// than the product: the identity is the product's name and the version is
	// the oldest protocol that carries what the program sends
	// (enginekafka.MinimumBrokerVersion: record headers, for the tenant on
	// the standard RawEvent).
	ClientID      string `yaml:"-"`
	BrokerVersion string `yaml:"-"`
	InitialOffset string `yaml:"initial_offset"`
}

func (c KafkaConfig) ConsumerCoordinates() enginekafka.Config {
	return enginekafka.Config{
		Brokers:       append([]string(nil), c.Brokers...),
		Topic:         c.InputTopic,
		GroupID:       c.GroupID,
		ClientID:      c.ClientID,
		BrokerVersion: c.BrokerVersion,
		InitialOffset: c.InitialOffset,
	}
}

func (c KafkaConfig) TriggerEventCoordinates() enginekafka.DecisionSinkConfig {
	return c.outputCoordinates(c.TriggerEvent)
}

func (c KafkaConfig) outputCoordinates(output KafkaOutputConfig) enginekafka.DecisionSinkConfig {
	return enginekafka.DecisionSinkConfig{
		Brokers:         append([]string(nil), c.Brokers...),
		InputTopic:      c.InputTopic,
		OutputTopic:     output.Topic,
		ClientID:        c.ClientID,
		BrokerVersion:   c.BrokerVersion,
		MaxMessageBytes: output.MaxMessageBytes,
	}
}

// DefaultStatePrefix is alarmd's own key space for its runtime state (catalog,
// ownership, state, fleet, progress). The g2 and v1 segments are the program's
// schema generation, which rises with the code when the persisted shape
// changes; a deployment has no reason to state it, and one that does must
// state this value.
const DefaultStatePrefix = "alarmd:phase2:g2:runtime:v1"

type RedisConfig struct {
	RedisConnectionConfig `yaml:",inline"`
	// StatePrefix defaults to DefaultStatePrefix; it is a program fact, not
	// an environment choice.
	StatePrefix   string   `yaml:"state_prefix"`
	MinTTL        Duration `yaml:"min_ttl"`
	MaxTTL        Duration `yaml:"max_ttl"`
	RestartMargin Duration `yaml:"restart_margin"`
}

// PlatformCacheConfig names the platform's own caches that alarmd reads. They
// are separate connections because the platform routes them separately: its
// cache backend can be redirected per module, so the strategy cache and the
// CMDB cache may each live somewhere other than the instance the rest of the
// deployment points at. Reading them off one connection is correct only where
// a deployment happens not to use that routing, and where it does, the reads
// land on an instance nothing writes - which reads back as "no strategies" and
// "no hosts" rather than as an error.
//
// Neither is alarmd's own storage. Top-level redis is, and these stay out of
// it so that one key does not have to mean two different things.
type PlatformCacheConfig struct {
	// Strategy is where the platform writes the strategy cache alarmd reads.
	Strategy *RedisConnectionConfig `yaml:"strategy,omitempty"`
	// CMDB is where the platform writes the host cache the target filter and
	// the host status filter decide on.
	CMDB *RedisConnectionConfig `yaml:"cmdb,omitempty"`
	// DynamicConfig is where the platform distributes its dynamic
	// configuration: the instance and logical database of the platform's
	// default Redis, rendered by the chart from the platform's own setting.
	// Unlike the two above it has no fallback: absent means the deployment
	// renders no distribution (the publisher is not deployed), and the
	// platform settings copy says not_configured rather than reading the
	// wrong instance as "nothing published".
	DynamicConfig *RedisConnectionConfig `yaml:"dynamic_config,omitempty"`
	// TargetGroup locates the dynamic target group cache. Prefix-only legacy
	// configurations retain their historical CMDB connection.
	TargetGroup *RedisConnectionConfig `yaml:"target_group,omitempty"`
	// DynamicGroupKeyPrefix is the fork's own Redis key prefix, under which
	// its dynamic group module writes "<prefix>dynamic_group:<id>" on the
	// instance selected by TargetGroup (historically CMDB). It is a deployment
	// coordinate rendered from the fork's setting, spelled exactly as the writer spells
	// it, separator included; alarmd derives nothing from it and has no
	// default for it. Absent means the deployment has no such writer and no
	// group is read (every dynamic group selector resolves unavailable by
	// name); present and empty is a rendering that went wrong and is
	// refused rather than read as a prefix.
	DynamicGroupKeyPrefix *string `yaml:"dynamic_group_key_prefix,omitempty"`
}

// DynamicGroupKeyPrefix is the fork's key prefix for its dynamic group
// cache, and whether the deployment renders one.
func (c Config) DynamicGroupKeyPrefix() (string, bool) {
	if c.PlatformCache.DynamicGroupKeyPrefix == nil {
		return "", false
	}
	return *c.PlatformCache.DynamicGroupKeyPrefix, true
}

type Config struct {
	Input           PhaseTwoInputConfig   `yaml:"input"`
	HTTP            HTTPConfig            `yaml:"http"`
	CLI             CLIConfig             `yaml:"cli"`
	Kafka           KafkaConfig           `yaml:"kafka"`
	Redis           RedisConfig           `yaml:"redis"`
	PlatformCache   PlatformCacheConfig   `yaml:"platform_cache"`
	Limits          LimitsConfig          `yaml:"limits"`
	PhaseTwo        PhaseTwoRuntimeConfig `yaml:"phase_two"`
	ShutdownTimeout Duration              `yaml:"shutdown_timeout"`
}

// Default is the product configuration, and it is the same on every machine:
// the capacity budgets describe ReferenceContainer, not whatever the process
// happens to be running on. Load is where they follow the real container.
// Reading the machine here would make a build agent's core count part of the
// product default and every test's expectations a property of its host.
func Default() Config {
	cfg := Config{
		Input: DefaultPhaseTwoInput(),
		HTTP: HTTPConfig{
			Listen: "127.0.0.1:8080",
			// The pprof convention; kept off the query port so the two
			// surfaces never share a mux.
			DiagnosticsListen: "127.0.0.1:6060",
		},
		Kafka: KafkaConfig{
			ClientID: "alarmd", BrokerVersion: enginekafka.MinimumBrokerVersion,
			TriggerEvent:  KafkaOutputConfig{Topic: "alarmd_event", MaxMessageBytes: defaultOutputMaxMessageBytes},
			LegacyAdapter: LegacyAdapterConfig{Topic: "alarmd_0bkmonitor_backend_event"},
		},
		Redis: RedisConfig{
			RedisConnectionConfig: RedisConnectionConfig{Mode: RedisModeStandalone,
				DialTimeout: Duration(3 * time.Second), ReadTimeout: Duration(3 * time.Second),
				WriteTimeout: Duration(3 * time.Second), PoolSize: 0},
			StatePrefix: DefaultStatePrefix,
			MinTTL:      Duration(time.Minute), MaxTTL: Duration(30 * 24 * time.Hour), RestartMargin: Duration(10 * time.Minute),
		},
		Limits:          defaultLimits(),
		PhaseTwo:        defaultPhaseTwoRuntime(),
		ShutdownTimeout: Duration(10 * time.Second),
	}
	return cfg.withDerivedCapacity(ReferenceContainer())
}

// withDerivedCapacity sizes admission, queueing and the Coordinator budgets
// from one container's CPU and memory.
func (c Config) withDerivedCapacity(inputs CapacityInputs) Config {
	derived := DeriveScheduler(inputs)
	c.PhaseTwo.Scheduler.ActiveExecutionLimit = derived.ActiveExecutions
	c.PhaseTwo.Scheduler.ProcessQueryPermits = derived.ProcessQueryPermits
	c.PhaseTwo.Scheduler.RecoveryQueryPermits = derived.RecoveryQueryPermits
	c.PhaseTwo.Scheduler.ReadyQueueCapacity = derived.ReadyQueueCapacity
	c.PhaseTwo.Scheduler.RecoveryQueueCapacity = derived.RecoveryQueueCapacity
	c.PhaseTwo.Coordinator = DeriveCoordinator(
		inputs, uint64(c.Limits.Store.MaxKeysPerBatch), c.chunkedStateApplyBudget(),
	)
	return c
}

// WithContainerCapacity sizes the budgets for the container this process was
// given. Load applies it; Default deliberately does not.
func (c Config) WithContainerCapacity() Config {
	return c.withDerivedCapacity(DetectCapacityInputs())
}

// chunkedStateApplyBudget is the most one Slot's State or Gap mutations can
// carry: StateApplyMaxChunks successive Store calls of max_keys_per_batch
// items each.
func (c Config) chunkedStateApplyBudget() uint64 {
	return uint64(c.Limits.Store.MaxKeysPerBatch) * execution.StateApplyMaxChunks
}

// deploymentProfile is the Worker's Ownership compatibility identity: Workers
// registered under different profiles never take over one another's Query
// Groups. It is persisted in every Worker registration, so the literal stays
// what deployed Workers already wrote (it was the value of the retired
// top-level mode key). It is not configurable: a deployment cannot opt out of
// the takeover fence, and changing the value is the production-ownership
// switch, which is a rollout of its own.
const deploymentProfile = "shadow"

// DeploymentProfile reports the persisted Ownership compatibility identity.
func (c Config) DeploymentProfile() string {
	return deploymentProfile
}

// AdmittedQueryConcurrency is the number of queries the scheduler may have in
// flight at once. It bounds the query stage only; it is not the bound on Redis
// concurrency, because the Slot source reads the control plane before a Slot
// becomes eligible to query and holds no permit while doing so.
func (c Config) AdmittedQueryConcurrency() int {
	s := c.PhaseTwo.Scheduler
	return s.ProcessQueryPermits + s.RecoveryQueryPermits
}

// redisPoolCPUBudget is the CPU budget the pool derives from. automaxprocs has
// already resolved GOMAXPROCS from the container's cgroup quota by the time the
// configuration is read, so this reports what the container was actually given
// rather than the host's core count.
func redisPoolCPUBudget() int {
	return runtime.GOMAXPROCS(0)
}

// WithResolvedRedisPoolSize returns a copy whose Redis pool sizes are concrete
// positive numbers, so the resolved value can be reported as a startup fact
// rather than staying implicit in the client.
func (c Config) WithResolvedRedisPoolSize() Config {
	cpuBudget := redisPoolCPUBudget()
	c.Redis.PoolSize = c.Redis.Connection().EffectivePoolSize(cpuBudget)
	for _, platform := range []**RedisConnectionConfig{&c.PlatformCache.Strategy, &c.PlatformCache.CMDB, &c.PlatformCache.DynamicConfig, &c.PlatformCache.TargetGroup} {
		if *platform == nil {
			continue
		}
		resolved := (*platform).clone()
		resolved.PoolSize = resolved.EffectivePoolSize(cpuBudget)
		*platform = &resolved
	}
	return c
}

func (c Config) RedisBackendOptions() state.RedisBackendOptions {
	return state.RedisBackendOptions{
		Address: c.Redis.Address, Username: c.Redis.Username, Password: c.Redis.Password, DB: c.Redis.DB,
		DialTimeout: c.Redis.DialTimeout.Duration(), ReadTimeout: c.Redis.ReadTimeout.Duration(),
		WriteTimeout: c.Redis.WriteTimeout.Duration(),
		// Resolve here as well as in the runtime path: WithResolvedRedisPoolSize
		// carries the authoritative value into the startup facts, but options can
		// also be built by paths that never ran it, and a zero must never reach a
		// client. Both paths derive from the CPU budget so they cannot disagree;
		// passing the admitted concurrency here used to produce a different pool
		// than the one the process reported.
		PoolSize: c.Redis.Connection().EffectivePoolSize(redisPoolCPUBudget()),
	}
}

func (c RedisConfig) Connection() RedisConnectionConfig {
	return c.RedisConnectionConfig.clone()
}

// StrategySourceRedis is where the platform's strategy cache is read from.
func (c Config) StrategySourceRedis() RedisConnectionConfig {
	if c.PlatformCache.Strategy != nil {
		return c.PlatformCache.Strategy.clone()
	}
	return c.Redis.Connection()
}

// PlatformKeyPrefix is the platform's own key prefix - the root every cache it
// writes hangs off. Both the host cache this process reads and the strategy
// snapshot the compatibility output writes are keyed under it.
//
// It is stated once, under the compatibility adapter, because that is where the
// platform's key space was first needed. The field is misnamed for this second
// use and the name is worth moving, but not by stating the same value twice:
// two keys for one platform fact drift, and the failure of a drifted prefix is
// a read that returns nothing rather than an error. This accessor exists so the
// read sites say which fact they want instead of reaching into a neighbouring
// feature's configuration.
func (c Config) PlatformKeyPrefix() string {
	return c.Kafka.LegacyAdapter.SnapshotPrefix
}

// OutputProtocol is the deployment's choice of wire format for published
// events. It is resolved per strategy when a Plan is built, and frozen there.
func (c Config) OutputProtocol() string {
	return c.PhaseTwo.Output.protocol()
}

// NoDataTrackingHorizonSeconds is the horizon the deployment's values state,
// and whether they state one at all.
//
// It is the values layer and nothing more. The effective platform horizon is
// resolved by the platform settings copy: a dynamic value, else this one,
// else the contract's one day. Absent here therefore no longer means "track
// indefinitely" - that reading is withdrawn; the approved contract gives every
// group a finite horizon by default. A strategy stating its own overrides all
// three.
func (c Config) NoDataTrackingHorizonSeconds() (int64, bool) {
	if c.PhaseTwo.NoData.TrackingHorizonSeconds == nil {
		return 0, false
	}
	return *c.PhaseTwo.NoData.TrackingHorizonSeconds, true
}

// PlatformSettingsLayer is the deployment's layer of the platform settings:
// the platform_settings group, and the no-data horizon from its own leaf.
func (c Config) PlatformSettingsLayer() platformsettings.Layer {
	layer := c.PhaseTwo.PlatformSettings.Layer()
	if horizon, stated := c.NoDataTrackingHorizonSeconds(); stated {
		layer.NoDataTrackingHorizonSeconds = &horizon
	}
	return layer
}

// CMDBCacheRedis is where the platform's host cache is read from.
func (c Config) CMDBCacheRedis() RedisConnectionConfig {
	if c.PlatformCache.CMDB != nil {
		return c.PlatformCache.CMDB.clone()
	}
	return c.Redis.Connection()
}

// DynamicConfigRedis is where the platform distributes its dynamic
// configuration, and whether the deployment renders it at all.
func (c Config) DynamicConfigRedis() (RedisConnectionConfig, bool) {
	if c.PlatformCache.DynamicConfig == nil {
		return RedisConnectionConfig{}, false
	}
	return c.PlatformCache.DynamicConfig.clone(), true
}

// TargetGroupRedis returns the explicit location, or the historical CMDB
// location for prefix-only configurations. Without a prefix no groups are read.
func (c Config) TargetGroupRedis() (RedisConnectionConfig, bool) {
	if c.PlatformCache.DynamicGroupKeyPrefix == nil {
		return RedisConnectionConfig{}, false
	}
	if c.PlatformCache.TargetGroup != nil {
		return c.PlatformCache.TargetGroup.clone(), true
	}
	return c.CMDBCacheRedis(), true
}

// resolvePlatformCacheRedis writes down which connection each platform cache
// actually resolved to, rather than leaving it to be worked out again at every
// call site. The resolved configuration is what a release check reads and what
// an incident is reconstructed from, and "inherited" is not an answer to the
// question of where a read went.
func (c *Config) resolvePlatformCacheRedis() {
	if c == nil || c.Input.Mode != InputModeGoAccess {
		return
	}
	for _, cache := range []**RedisConnectionConfig{&c.PlatformCache.Strategy, &c.PlatformCache.CMDB, &c.PlatformCache.DynamicConfig, &c.PlatformCache.TargetGroup} {
		if *cache == nil {
			if cache == &c.PlatformCache.DynamicConfig || cache == &c.PlatformCache.TargetGroup {
				continue
			}
			resolved := c.Redis.Connection()
			*cache = &resolved
			continue
		}
		// A stated cache says where to read, not how long to wait for it.
		// Timeouts and pool size are one operational setting for this process,
		// so they are inherited rather than restated per location - the same
		// choice the compatibility service Redis already makes, and for the
		// same reason: a deployment that has to repeat them will eventually
		// repeat them differently.
		resolved := (*cache).clone()
		if resolved.DialTimeout == 0 {
			resolved.DialTimeout = c.Redis.DialTimeout
		}
		if resolved.ReadTimeout == 0 {
			resolved.ReadTimeout = c.Redis.ReadTimeout
		}
		if resolved.WriteTimeout == 0 {
			resolved.WriteTimeout = c.Redis.WriteTimeout
		}
		if resolved.PoolSize == 0 {
			resolved.PoolSize = c.Redis.PoolSize
		}
		*cache = &resolved
	}
}

// RuntimeStoreRedis is where alarmd's own runtime state lives: catalog,
// ownership, state, fleet and progress. It is the top-level redis and nothing
// else - there was once a second key that could move this connection on its
// own, which left the prefix and the TTLs that parameterise these keys stated
// under a different key from the connection they applied to. The way to put
// alarmd's state somewhere of its own is externalRedis.alarmd, which moves
// this whole section together.
func (c Config) RuntimeStoreRedis() RedisConnectionConfig {
	return c.Redis.Connection()
}

// resolveCompatibilityServiceTimeouts lets the compatibility service Redis
// inherit the runtime Redis timeouts when it does not state its own. Timeouts
// say how long to wait, not where to write, so inheriting them keeps one
// operational setting instead of two; mode and address stay explicit because
// they decide the destination and a wrong guess there writes a snapshot nobody
// reads.
// resolveCompatibilityPodCache fills in the Django cache coordinates the
// deployment does not choose. Workload enrichment reads the same cache Python
// reads, whose keys carry Django's cache version; the platform leaves that at
// Django's default, so the version is the product's to know rather than one
// more line for a values file to get wrong.
func (c *Config) resolveCompatibilityPodCache() {
	if c == nil || c.Kafka.LegacyAdapter.PodCache == nil {
		return
	}
	cache := c.Kafka.LegacyAdapter.PodCache
	if cache.Version <= 0 {
		cache.Version = defaultDjangoCacheVersion
	}
	if cache.Connection.DialTimeout == 0 {
		cache.Connection.DialTimeout = c.Redis.DialTimeout
	}
	if cache.Connection.ReadTimeout == 0 {
		cache.Connection.ReadTimeout = c.Redis.ReadTimeout
	}
	if cache.Connection.WriteTimeout == 0 {
		cache.Connection.WriteTimeout = c.Redis.WriteTimeout
	}
}

func (c *Config) resolveCompatibilityServiceTimeouts() {
	if c == nil {
		return
	}
	runtimeRedis := c.Redis.Connection()
	service := &c.Kafka.LegacyAdapter.ServiceRedis
	if service.DialTimeout == 0 {
		service.DialTimeout = runtimeRedis.DialTimeout
	}
	if service.ReadTimeout == 0 {
		service.ReadTimeout = runtimeRedis.ReadTimeout
	}
	if service.WriteTimeout == 0 {
		service.WriteTimeout = runtimeRedis.WriteTimeout
	}
}

func (c Config) StateStoreOptions(codec *state.Codec, router state.StorageRouter, observer state.Observer) state.StoreOptions {
	return state.StoreOptions{
		Prefix: c.Redis.StatePrefix, Codec: codec, Router: router, Limits: c.StoreLimits(),
		MinTTL: c.Redis.MinTTL.Duration(), MaxTTL: c.Redis.MaxTTL.Duration(),
		RestartMargin: c.Redis.RestartMargin.Duration(), Observer: observer,
	}
}

func Load(path string) (Config, error) {
	cfg := Default().WithContainerCapacity()
	if path == "" {
		cfg.resolvePlatformCacheRedis()
		cfg.resolveCompatibilityServiceTimeouts()
		cfg.resolveCompatibilityPodCache()
		return cfg, cfg.Validate()
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode config: multiple YAML documents are not allowed")
		}
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	cfg.resolvePlatformCacheRedis()
	cfg.resolveCompatibilityServiceTimeouts()
	cfg.resolveCompatibilityPodCache()
	cfg.resolvePhaseTwoWorkerIDFromEnvironment()
	if err := cfg.PhaseTwo.Linkd.resolveCredentialsFromEnvironment(); err != nil {
		return Config{}, err
	}
	if err := cfg.CLI.resolveAdminKeyFromEnvironment(); err != nil {
		return Config{}, err
	}
	if err := cfg.PhaseTwo.migratePlatformSettings(); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// PublicSurfaceRestrictionRequested is the configuration's half of the one
// switch for the public surface: the process serves the CLI with a
// deployment administrator key. The key is what makes a CLI session
// available, and only then do the routes that carry deployment coordinates
// have somewhere else to be read from; a key on a process with the CLI
// switched off opens no session. The other half is the runtime's: the
// surface is restricted only once the CLI has actually come up, since
// restricting without it would leave no way in. No separate setting exists,
// so the two cannot disagree.
func (c Config) PublicSurfaceRestrictionRequested() bool {
	return c.CLI.Enabled && c.CLI.AdminKey != ""
}

func (c Config) Validate() error {
	if err := c.validateCommon(); err != nil {
		return err
	}
	if err := c.Input.Validate(); err != nil {
		return fmt.Errorf("input configuration: %w", err)
	}

	return c.validateGoAccessRuntime()
}

func validateListenAddress(field, address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%s %q: %w", field, address, err)
	}
	if host == "" {
		return fmt.Errorf("%s %q has empty host", field, address)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return fmt.Errorf("%s %q has invalid port", field, address)
	}
	return nil
}

// listenAddressesCollide reports whether two listen addresses would contend for
// the same socket. Comparing the strings is not enough: the deployed query
// surface binds a wildcard, so a diagnostics address on the same port with any
// host still fails to bind. Catching it here turns a CrashLoop into a config
// error. Host names beyond localhost are left to the bind, which fails loudly.
func listenAddressesCollide(left, right string) bool {
	leftHost, leftPort, err := net.SplitHostPort(left)
	if err != nil {
		return false
	}
	rightHost, rightPort, err := net.SplitHostPort(right)
	if err != nil {
		return false
	}
	if leftPort != rightPort {
		return false
	}
	return isWildcardHost(leftHost) || isWildcardHost(rightHost) ||
		normalizeListenHost(leftHost) == normalizeListenHost(rightHost)
}

func isWildcardHost(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

func normalizeListenHost(host string) string {
	if host == "localhost" {
		return "127.0.0.1"
	}
	return host
}

func (c Config) validateCommon() error {
	if err := validateListenAddress("http listen", c.HTTP.Listen); err != nil {
		return err
	}
	// An empty diagnostics address serves no pprof, which is a supported
	// choice; a set one must be a real address and must not collide with the
	// query surface, or the split it exists to create would not happen.
	if c.HTTP.DiagnosticsListen != "" {
		if err := validateListenAddress("http diagnostics_listen", c.HTTP.DiagnosticsListen); err != nil {
			return err
		}
		if listenAddressesCollide(c.HTTP.Listen, c.HTTP.DiagnosticsListen) {
			return fmt.Errorf(
				"http diagnostics_listen %q must differ from http listen %q",
				c.HTTP.DiagnosticsListen, c.HTTP.Listen,
			)
		}
	}

	if c.HTTP.InternalListen != "" {
		if err := validateListenAddress("http internal_listen", c.HTTP.InternalListen); err != nil {
			return err
		}
		for _, other := range []struct{ field, address string }{{"http listen", c.HTTP.Listen}, {"http diagnostics_listen", c.HTTP.DiagnosticsListen}} {
			if other.address != "" && listenAddressesCollide(c.HTTP.InternalListen, other.address) {
				return fmt.Errorf("http internal_listen %q must differ from %s %q", c.HTTP.InternalListen, other.field, other.address)
			}
		}
	}

	if c.ShutdownTimeout.Duration() <= 0 {
		return errors.New("shutdown_timeout must be positive")
	}
	return nil
}

func (c Config) validateGoAccessRuntime() error {
	if err := c.PhaseTwo.Linkd.Validate(); err != nil {
		return err
	}
	if c.Kafka.InputTopic != "" || c.Kafka.GroupID != "" || c.Kafka.InitialOffset != "" {
		return errors.New("phase-two Go Access must not configure phase-one Kafka input coordinates")
	}
	if err := validatePhaseTwoKafkaOutput(c.Kafka); err != nil {
		return fmt.Errorf("trigger event configuration: %w", err)
	}
	if err := c.Kafka.validateCompatibilityOutput(); err != nil {
		return fmt.Errorf("compatibility output configuration: %w", err)
	}
	if err := c.validateSharedRuntime(); err != nil {
		return err
	}
	if err := c.PhaseTwo.validate(); err != nil {
		return err
	}
	if err := c.StrategySourceRedis().validate("platform_cache.strategy"); err != nil {
		return err
	}
	if err := c.CMDBCacheRedis().validate("platform_cache.cmdb"); err != nil {
		return err
	}
	if prefix, rendered := c.DynamicGroupKeyPrefix(); rendered && strings.TrimSpace(prefix) == "" {
		return errors.New("platform_cache.dynamic_group_key_prefix is rendered but empty; leave it out where no dynamic group cache is written")
	}
	if c.PlatformCache.TargetGroup != nil && c.PlatformCache.DynamicGroupKeyPrefix == nil {
		return errors.New("platform_cache.target_group requires platform_cache.dynamic_group_key_prefix")
	}
	if connection, configured := c.TargetGroupRedis(); configured {
		if err := connection.validate("platform_cache.target_group"); err != nil {
			return err
		}
	}
	if err := validateRuntimePrefixIsolation(c.Redis.StatePrefix, c.PhaseTwo.Control.StrategyCachePrefix); err != nil {
		return err
	}
	// A replayed Slot recognises its own earlier write and reports it as
	// already applied instead of emitting the same events twice. That only
	// works while the key it wrote still exists. Runtime State TTLs are derived
	// from Plan retention plus the restart margin, so the shortest one any Plan
	// can produce is the margin plus one evaluation interval: a margin below
	// max_replay_age would let the shortest-retention Plans lose that proof
	// inside the replay window, silently and only for them.
	if c.Redis.RestartMargin.Duration() < c.PhaseTwo.Scheduler.MaxReplayAge.Duration() {
		// The only reader of this message is whoever is holding the deployment
		// that will not start, and the two halves are not symmetric: the margin
		// is theirs to set - how long a restart takes is a property of their
		// cluster - while max_replay_age is a fixed property of the replay
		// algorithm that a values file cannot set. Naming both values and
		// saying which half moves is the difference between a message that can
		// be acted on and one that invites editing the half that is not
		// editable.
		return fmt.Errorf(
			"redis.restart_margin (%s) must cover phase_two.scheduler.max_replay_age (%s): "+
				"max_replay_age is a fixed replay window that a values file cannot set, "+
				"so raise restart_margin to at least %s",
			c.Redis.RestartMargin.Duration(),
			c.PhaseTwo.Scheduler.MaxReplayAge.Duration(),
			c.PhaseTwo.Scheduler.MaxReplayAge.Duration(),
		)
	}
	budget := c.PhaseTwo.Coordinator
	// One Slot's State or Gap mutations are applied in successive Store calls
	// of at most max_keys_per_batch items, up to StateApplyMaxChunks calls.
	// A process budget above that product could admit a Slot no apply can
	// ever carry, so it is rejected here rather than at runtime.
	chunkedApplyBudget := c.chunkedStateApplyBudget()
	if budget.MaxStateMutations > chunkedApplyBudget || budget.MaxGapMutations > chunkedApplyBudget {
		return errors.New("phase_two mutation budgets exceed the chunked state store apply budget")
	}
	if budget.MaxRetainedBytes > math.MaxInt64 ||
		budget.MaxSeries > math.MaxUint64/c.Limits.Detect.MaxRecordsPerSeries {
		return errors.New("phase_two provider budgets overflow production limits")
	}
	if c.Limits.Trigger.MaxEvidenceBytesPerEvent > c.Kafka.TriggerEvent.MaxMessageBytes {
		return errors.New("trigger_event max_message_bytes cannot admit maximum trigger evidence")
	}
	return nil
}

func (c Config) validateSharedRuntime() error {
	if err := c.validateRedis(); err != nil {
		return err
	}
	if err := c.Limits.validate(); err != nil {
		return err
	}
	return nil
}

func (c Config) validateRedis() error {
	if err := c.Redis.Connection().validate("redis"); err != nil {
		return err
	}
	if !canonicalRedisPrefix(c.Redis.StatePrefix) {
		return errors.New("redis state_prefix must be non-empty canonical text")
	}
	if c.Redis.MinTTL.Duration() <= 0 || c.Redis.MaxTTL.Duration() < c.Redis.MinTTL.Duration() || c.Redis.RestartMargin.Duration() < 0 {
		return errors.New("redis state TTL range is invalid")
	}
	return nil
}
