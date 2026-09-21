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
	"slices"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

const PhaseTwoWorkerIDEnvironment = "ALARMD_PHASE_TWO_WORKER_ID"

type PhaseTwoWorkerConfig struct {
	ID                        string `yaml:"id"`
	RegistrationTTL           Duration
	RegistrationRenewInterval Duration
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
	Timezone                   string `yaml:"timezone"`
	RefreshInterval            Duration
	ReconcileInterval          Duration
	CatalogTTL                 Duration
	LegacyMigrationMaxScanKeys int
	LegacyMigrationTimeout     Duration
	LegacyQueryRuntime         PhaseTwoLegacyQueryRuntimeConfig `yaml:"legacy_query_runtime"`
}

type PhaseTwoRuntimeFilterConfig struct {
	FieldName string   `yaml:"field_name"`
	Values    []string `yaml:"values"`
}

type PhaseTwoLegacyQueryRuntimeConfig struct {
	FTAEventStorage *execution.QueryStorage `yaml:"fta_event_storage"`
	// Deprecated: the four keys below are the platform's own settings and
	// live under phase_two.platform_settings, which the platform's dynamic
	// configuration distribution overrides at run time. They are still
	// accepted here for one release: a value given here is carried into the
	// new group, a value given in both places must agree, and the network
	// filter, which the platform has never made a setting, must be the
	// constant it always was. Remove once no deployment states them.
	AccessBKData          *bool                       `yaml:"access_bk_data"`
	BKDataCMDBLevelTables []string                    `yaml:"bkdata_cmdb_level_tables"`
	SystemDiskFilter      PhaseTwoRuntimeFilterConfig `yaml:"system_disk_filter"`
	SystemNetworkFilter   PhaseTwoRuntimeFilterConfig `yaml:"system_network_filter"`
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
	layer := platformsettings.Layer{IsAccessBKData: c.IsAccessBKData}
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
	// Disabling creation never disables recovery of an existing pending range.
	ExpiredRangeEnabled bool `yaml:"expired_range_enabled"`
	// ActiveExecutionLimit bounds outstanding Runner invocations. It is derived
	// from the container's CPU budget alongside the permits below, and there is
	// no unlimited setting: zero was one until production showed it produced
	// parked executions rather than query throughput.
	ActiveExecutionLimit int
	TickInterval         Duration
	// Admission and queue depth are derived from the container's CPU budget.
	ProcessQueryPermits      int
	RecoveryQueryPermits     int
	ReadyQueueCapacity       int
	RecoveryQueueCapacity    int
	MaxQueuedItemsPerQG      int
	MaxReplaySlots           uint32
	MaxReplayAge             Duration
	RetryMinDelay            Duration
	RetryMaxDelay            Duration
	QueryUnavailableCooldown bool `yaml:"query_unavailable_cooldown"`
}

func (config PhaseTwoSchedulerConfig) RecoveryLimits() scheduler.RecoveryLimits {
	return scheduler.RecoveryLimits{
		QueryUnavailableCooldown: config.QueryUnavailableCooldown,
		ProcessQueryPermits:      config.ProcessQueryPermits, RecoveryQueryPermits: config.RecoveryQueryPermits,
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
	MonitorWebBaseURL string `yaml:"monitor_web_base_url"`
	// HostDisableMonitorStates mirrors the platform's HOST_DISABLE_MONITOR_STATES
	// global config: a host whose CMDB bk_state contains any of these is not
	// monitored, and Python's access chain drops its records before they can
	// alert.
	//
	// It is stated here rather than derived because the program cannot derive
	// it: it is an operator-editable platform setting living in the platform's
	// own database, and this environment's value is not the shipped default.
	// Absent means the filter is not installed, which is the behaviour alarmd
	// had before it existed; it never falls back to the default, because a
	// wrong list silently changes which alerts are produced.
	//
	// Deprecated: the exit stated above has arrived. The value lives under
	// phase_two.platform_settings.host_disable_monitor_states and the
	// platform's distribution overrides it at run time; a value given here
	// is carried there for one release, and must agree with one given there.
	HostDisableMonitorStates   []string `yaml:"host_disable_monitor_states"`
	MinReadyDelay              Duration
	DownstreamExecutionReserve Duration
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

// PhaseTwoCanonicalConfig selects how the shared canonical encoder runs. It
// existed to roll the replacement of that encoder out in stages a deployment
// chose, because only the operator of a cluster knew whether the new form had
// been proven on that cluster's own traffic.
//
// That proof is in: both directions were compared on production traffic, over
// a hundred million calls, with zero divergence in all three classes and the
// covered call-site count flat. The comparison has done its job, so the
// default is the single-pass encoder with nothing comparing, and the keys
// below are the way back and the way to compare again, not a position a new
// deployment has to choose.
//
// Retirement: the keys, the established encoder and the mode machinery come
// out together once the single-pass default has been through one release
// without a rollback. A comparison a deployment wants after that is a
// one-off measurement with a dense stride, not a standing guard.
//
// Everything else about the encoder stays derived. There is no tuning here.
type PhaseTwoCanonicalConfig struct {
	// Mode is one of established, shadow, stream_shadow, stream. Empty means
	// stream: the proven single-pass encoder, nothing comparing.
	Mode string `yaml:"mode,omitempty"`
	// ShadowSampleStride compares one call in every stride. Running both forms
	// on all traffic doubles the work the replacement exists to remove, so a
	// mode that compares needs a stride, and the default is derived rather
	// than asked for.
	ShadowSampleStride uint64 `yaml:"shadow_sample_stride,omitempty"`
}

// defaultCanonicalShadowStride is derived from what the comparison costs, not
// chosen for feeling about right.
//
// The comparison runs the other form once every stride calls. Measured, the
// established form costs about five times the single-pass one, so with the
// single-pass form answering, the comparison adds 5/stride of one canonical
// call. Holding that under a thousandth of the canonical path gives
// stride > 5000/5 = 1000, and 1024 is the next power of two.
//
// Only the cost sets the bound; detection does not push back. A systematic
// divergence recurs, so at the observed 52,000 canonical calls a second, one
// affecting even a hundredth of one per cent of calls is seen within minutes.
// The thing sparse sampling cannot do is a census -- stride 64 missed six call
// site types that stride 1 found -- and a census is not what this is for.
//
// A window that wants dense sampling says so explicitly; this is the value a
// comparing mode gets when it names no stride.
const defaultCanonicalShadowStride = 1024

// The default is the single-pass encoder with nothing comparing. It was the
// established encoder until every deployment had proven the new form on its
// own traffic, and briefly the comparing form after that; the comparison is
// concluded, so a deployment that says nothing pays for one encoder. The
// established form stays selectable as the way back until the mechanism
// retires.
//
// Deployments are named by role rather than by environment: this file is
// public.
func (c PhaseTwoCanonicalConfig) mode() string {
	if c.Mode == "" {
		return contract.CanonicalModeStream
	}
	return c.Mode
}

// Stride is zero for a mode that does not compare, so that a leftover setting
// cannot quietly keep paying for a comparison nobody is reading.
func (c PhaseTwoCanonicalConfig) Stride() uint64 {
	switch c.mode() {
	case contract.CanonicalModeShadow, contract.CanonicalModeStreamShadow:
	default:
		return 0
	}
	if c.ShadowSampleStride == 0 {
		return defaultCanonicalShadowStride
	}
	return c.ShadowSampleStride
}

// Mode reports the rollout position this deployment asked for.
func (c PhaseTwoCanonicalConfig) SelectedMode() string { return c.mode() }

type PhaseTwoRuntimeConfig struct {
	Worker           PhaseTwoWorkerConfig           `yaml:"worker"`
	Control          PhaseTwoControlConfig          `yaml:"control"`
	Output           PhaseTwoOutputConfig           `yaml:"output"`
	Ownership        PhaseTwoOwnershipConfig        `yaml:"-"`
	Scheduler        PhaseTwoSchedulerConfig        `yaml:"scheduler"`
	Access           PhaseTwoAccessConfig           `yaml:"access"`
	Coordinator      PhaseTwoCoordinatorConfig      `yaml:"-"`
	Canonical        PhaseTwoCanonicalConfig        `yaml:"canonical"`
	PlatformSettings PhaseTwoPlatformSettingsConfig `yaml:"platform_settings"`
	Observation      PhaseTwoObservationConfig      `yaml:"observation"`
}

// PhaseTwoObservationConfig is the operator's allocation to the strategy
// directory, the cost candidates and the criterion samples: the diagnostics
// that read the control plane and write the diagnostic store on their own
// account, beyond what detection needs.
//
// MemoryPercent is the share of the container's memory limit they may hold,
// from which every other bound of theirs is derived (config.DeriveObservationCapacity).
// Zero -- the default -- leaves them off: the directory cold read of a
// ten-thousand-Plan catalogue and the per-tick cost summary were measured
// on synthetic populations only, and the ruling is that they are switched on
// by an operator who has been given the measured budget for that deployment,
// not by whichever container happens to know its limit. An operator turning
// them on says how much, and nothing here says "unlimited".
type PhaseTwoObservationConfig struct {
	MemoryPercent int `yaml:"memory_percent"`
}

// ObservationMemoryPercentMax bounds the allocation: a quarter of the
// container is the point past which the diagnostics are competing with the
// detection they are supposed to describe.
const ObservationMemoryPercentMax = 25

func (c PhaseTwoObservationConfig) validate() error {
	if c.MemoryPercent < 0 || c.MemoryPercent > ObservationMemoryPercentMax {
		return fmt.Errorf("phase_two.observation.memory_percent %d must be between 0 (off) and %d", c.MemoryPercent, ObservationMemoryPercentMax)
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
			CatalogTTL: Duration(24 * time.Hour), LegacyMigrationMaxScanKeys: 50000,
			LegacyMigrationTimeout: Duration(30 * time.Second),
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
			// Expired-range finalization is the product's behaviour; the key
			// stays as the rollback switch decision-002 keeps for a version
			// that cannot read the range proof.
			ExpiredRangeEnabled: true,
			TickInterval:        Duration(time.Second),
			MaxQueuedItemsPerQG: 16, MaxReplaySlots: 3, MaxReplayAge: Duration(10 * time.Minute),
			RetryMinDelay: Duration(time.Second), RetryMaxDelay: Duration(30 * time.Second),
			QueryUnavailableCooldown: true,
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

// migratePlatformSettings carries the deprecated keys into the new group.
// A value stated in both places must agree: a deployment that says two
// things about one setting is not one that can be read either way. The
// constants are checked rather than ignored, so that a deployment which
// changed one expecting an effect is told there is none.
func (c *PhaseTwoRuntimeConfig) migratePlatformSettings() error {
	group := &c.PlatformSettings
	legacy := &c.Control.LegacyQueryRuntime
	if states := c.Access.HostDisableMonitorStates; len(states) > 0 {
		if err := carryList("access.host_disable_monitor_states", states, &group.HostDisableMonitorStates); err != nil {
			return err
		}
	}
	if legacy.AccessBKData != nil {
		if group.IsAccessBKData != nil && *group.IsAccessBKData != *legacy.AccessBKData {
			return errors.New("phase_two control legacy_query_runtime.access_bk_data and platform_settings.is_access_bk_data disagree")
		}
		value := *legacy.AccessBKData
		group.IsAccessBKData = &value
	}
	if legacy.BKDataCMDBLevelTables != nil {
		if err := carryList("control.legacy_query_runtime.bkdata_cmdb_level_tables", legacy.BKDataCMDBLevelTables, &group.BKDataCMDBLevelTables); err != nil {
			return err
		}
	}
	if legacy.SystemDiskFilter.FieldName != "" && legacy.SystemDiskFilter.FieldName != SystemDiskFilterField {
		return fmt.Errorf("phase_two control legacy_query_runtime.system_disk_filter.field_name is the constant %q", SystemDiskFilterField)
	}
	if legacy.SystemDiskFilter.Values != nil {
		if err := carryList("control.legacy_query_runtime.system_disk_filter.values", legacy.SystemDiskFilter.Values, &group.FileSystemTypeIgnore); err != nil {
			return err
		}
	}
	network := legacy.SystemNetworkFilter
	if (network.FieldName != "" && network.FieldName != SystemNetworkFilterField) ||
		(network.Values != nil && !equalStringLists(network.Values, SystemNetworkFilterValues())) {
		return fmt.Errorf("phase_two control legacy_query_runtime.system_network_filter is the constant %s=%v and not a setting",
			SystemNetworkFilterField, SystemNetworkFilterValues())
	}
	// One source from here on: the deprecated keys are read, carried, and
	// then hold nothing a later reader could take for the value in force.
	c.Access.HostDisableMonitorStates = nil
	legacy.AccessBKData = nil
	legacy.BKDataCMDBLevelTables = nil
	legacy.SystemDiskFilter = PhaseTwoRuntimeFilterConfig{}
	legacy.SystemNetworkFilter = PhaseTwoRuntimeFilterConfig{}
	return nil
}

func carryList(oldKey string, values []string, into **[]string) error {
	if *into != nil {
		if !equalStringLists(**into, values) {
			return fmt.Errorf("phase_two %s and its platform_settings key disagree", oldKey)
		}
		return nil
	}
	copied := append([]string{}, values...)
	*into = &copied
	return nil
}

func equalStringLists(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (c *Config) resolvePhaseTwoWorkerIDFromEnvironment() {
	if c == nil || c.Input.Mode != InputModeGoAccess {
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
	if !slices.Contains(contract.CanonicalModeNames(), c.Canonical.mode()) {
		return fmt.Errorf("phase_two.canonical.mode %q must be one of %s",
			c.Canonical.Mode, strings.Join(contract.CanonicalModeNames(), ", "))
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
	if err := c.Observation.validate(); err != nil {
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
		c.Control.CatalogTTL.Duration() <= c.Control.RefreshInterval.Duration() ||
		c.Control.LegacyMigrationMaxScanKeys <= 0 || c.Control.LegacyMigrationTimeout.Duration() <= 0 {
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
