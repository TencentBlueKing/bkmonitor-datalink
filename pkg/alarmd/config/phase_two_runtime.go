// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

const PhaseTwoWorkerIDEnvironment = "ALARMD_PHASE_TWO_WORKER_ID"

type PhaseTwoWorkerConfig struct {
	ID                        string   `yaml:"id"`
	RegistrationTTL           Duration `yaml:"-"`
	RegistrationRenewInterval Duration `yaml:"-"`
}

// DefaultTimezone is the platform's evaluation timezone, the constant Python
// runs under (TIME_ZONE); DefaultQuerySource is the product name unify-query
// sees on every request. Both are program facts a deployment need not state.
const (
	DefaultTimezone    = "Asia/Shanghai"
	DefaultQuerySource = "alarmd"
)

type PhaseTwoControlConfig struct {
	StrategyCachePrefix string `yaml:"strategy_cache_prefix"`
	ProviderRoute       string `yaml:"provider_route"`
	// Timezone defaults to DefaultTimezone.
	Timezone          string   `yaml:"timezone"`
	RefreshInterval   Duration `yaml:"-"`
	ReconcileInterval Duration `yaml:"-"`
	CatalogTTL        Duration `yaml:"-"`
}

// PhaseTwoPlatformSettingsConfig is alarmd's deployment layer of the
// platform's settings and where it reads the platform's own layer from.
//
// The four values are the platform's global settings; the platform
// distributes its database's word on them through Redis under its dynamic
// configuration protocol, and alarmd reads that distribution at run time.
// What is stated here is the layer beneath it: the protocol's own fallback
// reaches a deployment's YAML when the database says nothing or says the
// code default, and this group is alarmd's YAML. A deployment whose values
// differ from the platform's code defaults states them here exactly as it
// did before the distribution existed; the distribution then overrides them
// when the platform's page does.
//
// RedisKeyPrefix is the platform's common.redis_key_prefix, rendered by the
// chart from the platform's own setting; the connection the distribution is
// read from is platform_cache.dynamic_config, rendered the same way. Neither
// is a value an operator knows better than the platform does.
type PhaseTwoPlatformSettingsConfig struct {
	RedisKeyPrefix           string    `yaml:"redis_key_prefix"`
	HostDisableMonitorStates *[]string `yaml:"host_disable_monitor_states,omitempty"`
	IsAccessBKData           *bool     `yaml:"is_access_bk_data,omitempty"`
	BKDataCMDBLevelTables    *[]string `yaml:"bkdata_cmdb_level_tables,omitempty"`
	FileSystemTypeIgnore     *[]string `yaml:"file_system_type_ignore,omitempty"`
}

// Layer is the deployment layer as the platformsettings copy resolves it.
func (c PhaseTwoPlatformSettingsConfig) Layer() platformsettings.Layer {
	layer := platformsettings.Layer{IsAccessBKData: c.IsAccessBKData, Origin: platformsettings.HorizonSourceValues}
	if c.HostDisableMonitorStates != nil {
		values := append([]string{}, *c.HostDisableMonitorStates...)
		layer.HostDisableMonitorStates = &values
	}
	if c.BKDataCMDBLevelTables != nil {
		values := append([]string{}, *c.BKDataCMDBLevelTables...)
		layer.BKDataCMDBLevelTables = &values
	}
	if c.FileSystemTypeIgnore != nil {
		values := append([]string{}, *c.FileSystemTypeIgnore...)
		layer.FileSystemTypeIgnore = &values
	}
	return layer
}

// The two device filters the legacy query compiler applies. The disk
// filter's field is a constant of the platform and its values are the
// platform setting file_system_type_ignore; the network filter is a
// constant of the platform in both, and has never been a setting.
const (
	SystemDiskFilterField     = "device_type"
	SystemNetworkFilterField  = "device_name"
	systemNetworkFilterIgnore = "lo"
)

// SystemNetworkFilterValues is the network filter's constant value list.
func SystemNetworkFilterValues() []string { return []string{systemNetworkFilterIgnore} }

type PhaseTwoOwnershipConfig struct {
	ControlLeaderTTL           Duration
	ControlLeaderRenewInterval Duration
	LeaseTTL                   Duration
	LeaseRenewInterval         Duration
}

type PhaseTwoSchedulerConfig struct {
	// ActiveExecutionLimit bounds outstanding Runner invocations. It is derived
	// from the container's CPU budget alongside the permits below, and there is
	// no unlimited setting: zero was one until production showed it produced
	// parked executions rather than query throughput.
	ActiveExecutionLimit int      `yaml:"-"`
	TickInterval         Duration `yaml:"-"`
	// Admission and queue depth are derived from the container's CPU budget.
	ProcessQueryPermits   int      `yaml:"-"`
	RecoveryQueryPermits  int      `yaml:"-"`
	ReadyQueueCapacity    int      `yaml:"-"`
	RecoveryQueueCapacity int      `yaml:"-"`
	MaxQueuedItemsPerQG   int      `yaml:"-"`
	MaxReplaySlots        uint32   `yaml:"-"`
	MaxReplayAge          Duration `yaml:"-"`
	RetryMinDelay         Duration `yaml:"-"`
	RetryMaxDelay         Duration `yaml:"-"`
}

func (config PhaseTwoSchedulerConfig) RecoveryLimits() scheduler.RecoveryLimits {
	return scheduler.RecoveryLimits{
		ProcessQueryPermits: config.ProcessQueryPermits, RecoveryQueryPermits: config.RecoveryQueryPermits,
		ReadyQueueCapacity: config.ReadyQueueCapacity, RecoveryQueueCapacity: config.RecoveryQueueCapacity,
		MaxQueuedItemsPerQG: config.MaxQueuedItemsPerQG,
		MaxReplaySlots:      config.MaxReplaySlots, MaxReplayAge: config.MaxReplayAge.Duration(),
		RetryMinDelay: config.RetryMinDelay.Duration(), RetryMaxDelay: config.RetryMaxDelay.Duration(),
	}
}

type PhaseTwoAccessConfig struct {
	UQEndpoint string `yaml:"uq_endpoint"`
	// QuerySource defaults to DefaultQuerySource.
	QuerySource string `yaml:"query_source"`
	// SelfMetricsSpaceUID is where this deployment's own metrics can be read
	// back from. alarmd cannot derive it: which space its scraped metrics land
	// in is decided outside the process, by whoever wired the collection.
	//
	// It is optional and buys exactly one thing -- the trend curves on the
	// object page. Leaving it empty costs the curves and nothing else, so a new
	// environment still gets the judgment and the object list with no
	// configuration at all.
	SelfMetricsSpaceUID string `yaml:"self_metrics_space_uid"`
	// MonitorWebBaseURL is where this environment's monitor SaaS is served, and
	// it turns the object page's strategy references into links.
	//
	// alarmd cannot derive it for the same reason it cannot derive the space
	// above: which host serves the console is decided outside this process. It
	// is the origin only -- the path a strategy lives at is the product's own
	// route and is built in code, so an environment configures one value and
	// nothing about the page's structure.
	//
	// Optional, and it buys exactly one thing: a reader who has found the
	// strategy causing an anomaly can open it instead of copying an id into a
	// search box. Empty means the references render as they did before, as
	// plain labels, so a new environment still gets every other part of the page
	// with no configuration at all.
	MonitorWebBaseURL          string   `yaml:"monitor_web_base_url"`
	MinReadyDelay              Duration `yaml:"-"`
	DownstreamExecutionReserve Duration `yaml:"-"`
}

type PhaseTwoCoordinatorConfig struct {
	MaxSequencerReservations int
	MaxSeries                uint64
	MaxRetainedBytes         uint64
	MaxStateMutations        uint64
	MaxEvents                uint64
	MaxGapMutations          uint64
}

// Output protocols. A strategy's protocol is decided when its Plan is built and
// frozen with it, so a Slot that is retried cannot change wire format between
// attempts.
const (
	// OutputProtocolAuto selects the standard raw event for a strategy with a
	// frozen revision and the Python-compatible event for one without it.
	// TriggerEvent remains an internal evaluation result, never a wire format.
	OutputProtocolAuto = "auto"
	// OutputProtocolLegacy publishes every strategy through the
	// Python-compatible protocol, including strategies that have a revision.
	OutputProtocolLegacy = "legacy"
	// OutputProtocolNative publishes the standard raw event the alert pipeline
	// consumes. A strategy with no frozen revision has no alert identity there,
	// so it is refused activation rather than quietly sent the other way.
	OutputProtocolNative = "native"
)

type PhaseTwoOutputConfig struct {
	// Protocol is the deployment's choice of wire format. Empty means auto.
	Protocol string `yaml:"protocol,omitempty"`
}

func (c PhaseTwoOutputConfig) protocol() string {
	if c.Protocol == "" {
		return OutputProtocolAuto
	}
	return c.Protocol
}

type PhaseTwoRuntimeConfig struct {
	Linkd            LinkdConfig                    `yaml:"linkd"`
	Worker           PhaseTwoWorkerConfig           `yaml:"worker"`
	Control          PhaseTwoControlConfig          `yaml:"control"`
	Output           PhaseTwoOutputConfig           `yaml:"output"`
	Ownership        PhaseTwoOwnershipConfig        `yaml:"-"`
	Scheduler        PhaseTwoSchedulerConfig        `yaml:"scheduler"`
	Access           PhaseTwoAccessConfig           `yaml:"access"`
	Coordinator      PhaseTwoCoordinatorConfig      `yaml:"-"`
	PlatformSettings PhaseTwoPlatformSettingsConfig `yaml:"platform_settings"`
	NoData           PhaseTwoNoDataConfig           `yaml:"no_data"`
}

// PhaseTwoNoDataConfig is the deployment's say over how long one absent group
// goes on being reported before detection stops tracking it.
//
// It is here rather than derived because nothing in the process knows the
// answer. The horizon is a statement about how long a group that stopped
// reporting stays interesting to the people carrying the pager - a host
// decommissioned on purpose and one that fell over look identical to
// detection, and only the deployment knows which its population is mostly
// made of. Everything the horizon then costs is derived from it.
type PhaseTwoNoDataConfig struct {
	// TrackingHorizonSeconds is the platform default every Plan that does not
	// state its own inherits. Positive seconds; there is no value meaning
	// "track forever", so the leaf is read by presence and not by its value.
	//
	// A pointer for that reason. An absent leaf is a deployment that has not
	// set a platform horizon, and absence is the only way to say so: with a
	// plain integer, "not written" and "written as zero" are the same value,
	// and the zero would be carrying a second meaning nobody wrote - which is
	// how this feature spent three batches looking configured while never
	// running. Present, it must be at least one second, and both zero and a
	// negative are refused by name rather than read as an intention.
	//
	// Absent, the platform settings copy resolves the horizon: a dynamic
	// value published under base_config.domains.strategy, else the approved
	// contract's one day. An earlier ruling had absence mean "track
	// indefinitely"; it is withdrawn in favour of the contract, which gives
	// every group a finite horizon by default.
	TrackingHorizonSeconds *int64 `yaml:"tracking_horizon_seconds,omitempty"`
}

func (c PhaseTwoNoDataConfig) validate() error {
	// Refused here as well as in the contract because this is where an
	// operator's typo is still a startup failure they can read. Reaching the
	// contract means it is already inside a compiled Plan, where the same
	// mistake is a refused strategy rather than a refused deployment.
	//
	// Zero is refused rather than read as "no horizon" because a deployment
	// with no horizon says so by not writing the leaf. Accepting zero here
	// would give the value a second meaning, and the operator who wrote it
	// meant something - most likely "off", which is what deleting the leaf
	// already says, and conceivably "immediately", which the horizon has no
	// way to mean.
	if c.TrackingHorizonSeconds != nil && *c.TrackingHorizonSeconds < 1 {
		return fmt.Errorf("phase_two.no_data.tracking_horizon_seconds %d must be a positive number of "+
			"seconds; omit the key to inherit the platform's horizon (the published dynamic value, else one day)",
			*c.TrackingHorizonSeconds)
	}
	if c.TrackingHorizonSeconds != nil && *c.TrackingHorizonSeconds > contract.MaxNoDataTrackingHorizonSeconds {
		return fmt.Errorf("phase_two.no_data.tracking_horizon_seconds %d is past the largest horizon a lifetime "+
			"can hold, %d seconds", *c.TrackingHorizonSeconds, contract.MaxNoDataTrackingHorizonSeconds)
	}
	return nil
}

func defaultPhaseTwoRuntime() PhaseTwoRuntimeConfig {
	return PhaseTwoRuntimeConfig{
		Worker: PhaseTwoWorkerConfig{
			// A 60 second TTL survives several missed 10 second renewals
			// during a short Ownership Store outage before the Worker
			// drops out of the ready set.
			RegistrationTTL: Duration(60 * time.Second), RegistrationRenewInterval: Duration(10 * time.Second),
		},
		Control: PhaseTwoControlConfig{
			ProviderRoute: "unify-query-primary",
			// The platform evaluates in one timezone; Python's TIME_ZONE is
			// this constant, and the per-business timezone it can layer on
			// top is a design item alarmd does not carry yet.
			Timezone:        DefaultTimezone,
			RefreshInterval: Duration(30 * time.Second), ReconcileInterval: Duration(5 * time.Second),
			CatalogTTL: Duration(24 * time.Hour),
		},
		Ownership: PhaseTwoOwnershipConfig{
			ControlLeaderTTL: Duration(30 * time.Second), ControlLeaderRenewInterval: Duration(10 * time.Second),
			LeaseTTL: Duration(30 * time.Second), LeaseRenewInterval: Duration(10 * time.Second),
		},
		// Admission, queue depth and the Coordinator budgets are absent here on
		// purpose: they are sized from the container by Default, which is the
		// only place that knows the chunked Store apply budget they are held
		// against.
		Scheduler: PhaseTwoSchedulerConfig{
			TickInterval:        Duration(time.Second),
			MaxQueuedItemsPerQG: 16, MaxReplaySlots: 3, MaxReplayAge: Duration(10 * time.Minute),
			RetryMinDelay: Duration(time.Second), RetryMaxDelay: Duration(30 * time.Second),
		},
		Access: PhaseTwoAccessConfig{
			// The query source is the product's name on every unify-query
			// request; it is not something a deployment picks.
			QuerySource:   DefaultQuerySource,
			MinReadyDelay: Duration(30 * time.Second), DownstreamExecutionReserve: Duration(5 * time.Second),
		},
		PlatformSettings: PhaseTwoPlatformSettingsConfig{RedisKeyPrefix: platformsettings.DefaultKeyPrefix},
	}
}

func (c *Config) resolvePhaseTwoWorkerIDFromEnvironment() {
	if c == nil {
		return
	}
	if workerID, ok := os.LookupEnv(PhaseTwoWorkerIDEnvironment); ok {
		c.PhaseTwo.Worker.ID = workerID
	}
}

func (c PhaseTwoRuntimeConfig) validate() error {
	// Neither half of the old check survives: the deployment profile is derived
	// from the run mode rather than configured, and the target flow selection is
	// no longer configuration at all.
	if !canonicalText(c.Worker.ID) {
		return errors.New("phase_two worker identity must be canonical text")
	}
	switch c.Output.protocol() {
	case OutputProtocolAuto, OutputProtocolLegacy, OutputProtocolNative:
	default:
		return fmt.Errorf(
			"phase_two.output.protocol %q must be one of %s, %s, %s",
			c.Output.Protocol, OutputProtocolAuto, OutputProtocolLegacy, OutputProtocolNative,
		)
	}
	if !ttlExceedsRenew(c.Worker.RegistrationTTL, c.Worker.RegistrationRenewInterval) {
		return errors.New("phase_two worker registration_ttl must exceed registration_renew_interval")
	}
	if !canonicalText(c.Control.StrategyCachePrefix) || !canonicalText(c.Control.ProviderRoute) ||
		!canonicalText(c.Control.Timezone) {
		return errors.New("phase_two control source, provider route and timezone must be canonical text")
	}
	if _, err := time.LoadLocation(c.Control.Timezone); err != nil {
		return errors.New("phase_two control timezone is invalid")
	}
	if err := platformsettings.ValidateKeyPrefix(c.PlatformSettings.RedisKeyPrefix); err != nil {
		return fmt.Errorf("phase_two platform_settings.redis_key_prefix: %w", err)
	}
	if err := c.NoData.validate(); err != nil {
		return err
	}
	for name, list := range map[string]*[]string{
		"host_disable_monitor_states": c.PlatformSettings.HostDisableMonitorStates,
		"bkdata_cmdb_level_tables":    c.PlatformSettings.BKDataCMDBLevelTables,
		"file_system_type_ignore":     c.PlatformSettings.FileSystemTypeIgnore,
	} {
		if list != nil && !canonicalTextList(*list) {
			return fmt.Errorf("phase_two platform_settings.%s must be canonical text", name)
		}
	}
	if c.Control.RefreshInterval.Duration() <= 0 || c.Control.ReconcileInterval.Duration() <= 0 ||
		c.Control.CatalogTTL.Duration() <= c.Control.RefreshInterval.Duration() {
		return errors.New("phase_two control refresh, reconcile and catalog TTL are invalid")
	}
	if !ttlExceedsRenew(c.Ownership.ControlLeaderTTL, c.Ownership.ControlLeaderRenewInterval) ||
		!ttlExceedsRenew(c.Ownership.LeaseTTL, c.Ownership.LeaseRenewInterval) {
		return errors.New("phase_two ownership TTL must exceed its renew interval")
	}
	// Zero is rejected rather than read as unlimited. An unbounded dispatcher
	// is not a configuration a Pod can be asked to run, so there is no value
	// here that turns the bound off.
	if c.Scheduler.ActiveExecutionLimit <= 0 || c.Scheduler.TickInterval.Duration() <= 0 || c.Scheduler.RecoveryLimits().Validate() != nil {
		return errors.New("phase_two scheduler cadence and recovery limits are invalid")
	}
	endpoint, err := url.Parse(c.Access.UQEndpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" ||
		!canonicalText(c.Access.UQEndpoint) || !canonicalText(c.Access.QuerySource) ||
		c.Access.MinReadyDelay.Duration() <= 0 || c.Access.DownstreamExecutionReserve.Duration() <= 0 {
		return errors.New("phase_two access UQ coordinates and min_ready_delay are invalid")
	}
	budget := c.Coordinator
	if budget.MaxSequencerReservations <= 0 || budget.MaxSeries == 0 || budget.MaxRetainedBytes == 0 ||
		budget.MaxStateMutations == 0 || budget.MaxEvents == 0 || budget.MaxGapMutations == 0 {
		return errors.New("phase_two Coordinator and Sequencer budgets must be positive")
	}
	return nil
}

func ttlExceedsRenew(ttl, renew Duration) bool {
	return renew.Duration() > 0 && ttl.Duration() > renew.Duration()
}

func canonicalText(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

func canonicalTextList(values []string) bool {
	if values == nil {
		return false
	}
	for _, value := range values {
		if !canonicalText(value) {
			return false
		}
	}
	return true
}
