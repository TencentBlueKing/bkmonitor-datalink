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
)

const PhaseTwoWorkerIDEnvironment = "ALARMD_PHASE_TWO_WORKER_ID"

type PhaseTwoWorkerConfig struct {
	ID                        string   `yaml:"id"`
	DeploymentProfile         string   `yaml:"deployment_profile"`
	RegistrationTTL           Duration `yaml:"registration_ttl"`
	RegistrationRenewInterval Duration `yaml:"registration_renew_interval"`
}

type PhaseTwoControlConfig struct {
	StrategyCachePrefix string                           `yaml:"strategy_cache_prefix"`
	ProviderRoute       string                           `yaml:"provider_route"`
	Timezone            string                           `yaml:"timezone"`
	RefreshInterval     Duration                         `yaml:"refresh_interval"`
	ReconcileInterval   Duration                         `yaml:"reconcile_interval"`
	CatalogTTL          Duration                         `yaml:"catalog_ttl"`
	LegacyQueryRuntime  PhaseTwoLegacyQueryRuntimeConfig `yaml:"legacy_query_runtime"`
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
	ControlLeaderTTL           Duration `yaml:"control_leader_ttl"`
	ControlLeaderRenewInterval Duration `yaml:"control_leader_renew_interval"`
	LeaseTTL                   Duration `yaml:"lease_ttl"`
	LeaseRenewInterval         Duration `yaml:"lease_renew_interval"`
}

type PhaseTwoSchedulerConfig struct {
	TickInterval Duration `yaml:"tick_interval"`
}

type PhaseTwoAccessConfig struct {
	UQEndpoint                 string   `yaml:"uq_endpoint"`
	QuerySource                string   `yaml:"query_source"`
	MinReadyDelay              Duration `yaml:"min_ready_delay"`
	DownstreamExecutionReserve Duration `yaml:"downstream_execution_reserve"`
}

type PhaseTwoCoordinatorConfig struct {
	MaxSequencerReservations int    `yaml:"max_sequencer_reservations"`
	MaxSeries                uint64 `yaml:"max_series"`
	MaxRetainedBytes         uint64 `yaml:"max_retained_bytes"`
	MaxStateMutations        uint64 `yaml:"max_state_mutations"`
	MaxEvents                uint64 `yaml:"max_events"`
	MaxGapMutations          uint64 `yaml:"max_gap_mutations"`
}

type PhaseTwoRuntimeConfig struct {
	Worker       PhaseTwoWorkerConfig      `yaml:"worker"`
	Control      PhaseTwoControlConfig     `yaml:"control"`
	Ownership    PhaseTwoOwnershipConfig   `yaml:"ownership"`
	Scheduler    PhaseTwoSchedulerConfig   `yaml:"scheduler"`
	Access       PhaseTwoAccessConfig      `yaml:"access"`
	Coordinator  PhaseTwoCoordinatorConfig `yaml:"coordinator"`
	RuntimeRedis *RedisConnectionConfig    `yaml:"runtime_redis,omitempty"`
}

func defaultPhaseTwoRuntime() PhaseTwoRuntimeConfig {
	return PhaseTwoRuntimeConfig{
		Worker: PhaseTwoWorkerConfig{
			RegistrationTTL: Duration(30 * time.Second), RegistrationRenewInterval: Duration(10 * time.Second),
		},
		Control: PhaseTwoControlConfig{
			RefreshInterval: Duration(30 * time.Second), ReconcileInterval: Duration(5 * time.Second),
			CatalogTTL: Duration(24 * time.Hour),
		},
		Ownership: PhaseTwoOwnershipConfig{
			ControlLeaderTTL: Duration(30 * time.Second), ControlLeaderRenewInterval: Duration(10 * time.Second),
			LeaseTTL: Duration(30 * time.Second), LeaseRenewInterval: Duration(10 * time.Second),
		},
		Scheduler: PhaseTwoSchedulerConfig{TickInterval: Duration(time.Second)},
		Access: PhaseTwoAccessConfig{
			MinReadyDelay: Duration(30 * time.Second), DownstreamExecutionReserve: Duration(5 * time.Second),
		},
		Coordinator: PhaseTwoCoordinatorConfig{
			MaxSequencerReservations: 8192, MaxSeries: 100_000, MaxRetainedBytes: 64 << 20,
			MaxStateMutations: 8192, MaxEvents: 8192, MaxGapMutations: 8192,
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
	if !canonicalText(c.Worker.ID) || !canonicalText(c.Worker.DeploymentProfile) {
		return errors.New("phase_two worker identity and deployment_profile must be canonical text")
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
		c.Control.CatalogTTL.Duration() <= c.Control.RefreshInterval.Duration() {
		return errors.New("phase_two control refresh, reconcile and catalog TTL are invalid")
	}
	if !ttlExceedsRenew(c.Ownership.ControlLeaderTTL, c.Ownership.ControlLeaderRenewInterval) ||
		!ttlExceedsRenew(c.Ownership.LeaseTTL, c.Ownership.LeaseRenewInterval) {
		return errors.New("phase_two ownership TTL must exceed its renew interval")
	}
	if c.Scheduler.TickInterval.Duration() <= 0 {
		return errors.New("phase_two scheduler tick_interval must be positive")
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
