// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

const PhaseTwoWorkerIDEnvironment = "ALARMD_PHASE_TWO_WORKER_ID"

type PhaseTwoWorkerConfig struct {
	ID                        string `yaml:"id"`
	RegistrationTTL           Duration
	RegistrationRenewInterval Duration
}

type PhaseTwoControlConfig struct {
	StrategyCachePrefix        string `yaml:"strategy_cache_prefix"`
	ProviderRoute              string `yaml:"provider_route"`
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
	AccessBKData          *bool                       `yaml:"access_bk_data"`
	BKDataCMDBLevelTables []string                    `yaml:"bkdata_cmdb_level_tables"`
	SystemDiskFilter      PhaseTwoRuntimeFilterConfig `yaml:"system_disk_filter"`
	SystemNetworkFilter   PhaseTwoRuntimeFilterConfig `yaml:"system_network_filter"`
}

type PhaseTwoOwnershipConfig struct {
	ControlLeaderTTL           Duration
	ControlLeaderRenewInterval Duration
	LeaseTTL                   Duration
	LeaseRenewInterval         Duration
}

type PhaseTwoSchedulerConfig struct {
	// Disabling creation never disables recovery of an existing pending range.
	ExpiredRangeEnabled bool `yaml:"expired_range_enabled"`
	// ActiveExecutionLimit is zero (unlimited) by default; positive values
	// are an emergency complete-Runner guard, not normal Query admission.
	// It is part of the product capacity profile, not an environment tuning knob.
	ActiveExecutionLimit int
	TickInterval         Duration
	// Admission and queue depth are derived from the container's CPU budget.
	ProcessQueryPermits   int
	RecoveryQueryPermits  int
	ReadyQueueCapacity    int
	RecoveryQueueCapacity int
	MaxQueuedItemsPerQG   int
	MaxReplaySlots        uint32
	MaxReplayAge          Duration
	RetryMinDelay         Duration
	RetryMaxDelay         Duration
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
	UQEndpoint                 string `yaml:"uq_endpoint"`
	QuerySource                string `yaml:"query_source"`
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

type PhaseTwoRuntimeConfig struct {
	// Empty disables final Shadow evidence; the file is the frozen Epoch manifest.
	ShadowManifestPath string                    `yaml:"shadow_manifest_path,omitempty"`
	Worker             PhaseTwoWorkerConfig      `yaml:"worker"`
	Control            PhaseTwoControlConfig     `yaml:"control"`
	Ownership          PhaseTwoOwnershipConfig   `yaml:"-"`
	Scheduler          PhaseTwoSchedulerConfig   `yaml:"scheduler"`
	Access             PhaseTwoAccessConfig      `yaml:"access"`
	Coordinator        PhaseTwoCoordinatorConfig `yaml:"-"`
	RuntimeRedis       *RedisConnectionConfig    `yaml:"runtime_redis,omitempty"`
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
			ProviderRoute:   "unify-query-primary",
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
			ActiveExecutionLimit: 0,
			TickInterval:         Duration(time.Second),
			MaxQueuedItemsPerQG:  16, MaxReplaySlots: 3, MaxReplayAge: Duration(10 * time.Minute),
			RetryMinDelay: Duration(time.Second), RetryMaxDelay: Duration(30 * time.Second),
		},
		Access: PhaseTwoAccessConfig{
			MinReadyDelay: Duration(30 * time.Second), DownstreamExecutionReserve: Duration(5 * time.Second),
		},
	}
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
	legacy := c.Control.LegacyQueryRuntime
	if legacy.AccessBKData == nil || !canonicalTextList(legacy.BKDataCMDBLevelTables) ||
		!canonicalText(legacy.SystemDiskFilter.FieldName) || !canonicalTextList(legacy.SystemDiskFilter.Values) ||
		!canonicalText(legacy.SystemNetworkFilter.FieldName) || !canonicalTextList(legacy.SystemNetworkFilter.Values) {
		return errors.New("phase_two control legacy query runtime facts must be explicit")
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
	if c.Scheduler.ActiveExecutionLimit < 0 || c.Scheduler.TickInterval.Duration() <= 0 || c.Scheduler.RecoveryLimits().Validate() != nil {
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
