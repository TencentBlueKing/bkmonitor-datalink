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
	UQEndpoint  string `yaml:"uq_endpoint"`
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
	// This is a transcription with a stated exit: once the control plane syncs
	// platform settings periodically, the value comes from there and this key
	// is removed.
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
	// OutputProtocolAuto keeps the split the frozen revision already decides:
	// a strategy with a revision publishes the native event, one without it
	// publishes the Python-compatible event. It is the default because it is
	// what the process already did.
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
// exists because the replacement of that encoder has to be rolled out in
// stages that a deployment chooses, not that the program can decide for
// itself: only the operator of a given cluster knows whether its digests have
// already been proven to match on that cluster's own traffic.
//
// Everything else about the encoder stays derived. There is no tuning here,
// only a position in the rollout and how much of the traffic the comparison
// covers.
//
// Retirement, stated here because a temporary switch with no written exit
// condition is a permanent one. Both keys come out, together with the
// established encoder and the mode machinery behind them, once all four hold:
//
//	every deployment has run stream_shadow and reported zero divergence
//	  in all three classes over a window that covered its own call sites;
//	the covered call-site count has stopped rising on each of them;
//	the branches that production never sends have been written down as a
//	  conclusion, so that a coverage figure short of the offline corpus is
//	  known to be "will never arrive" rather than "has not arrived yet";
//	the terminal mode has been the default for one release without a rollback;
//	and, checked at that moment rather than remembered from this one, no
//	  deployment is still relying on the derived default for something other
//	  than the terminal rate -- removing these keys is what makes that default
//	  live everywhere, so the blast radius has to be read the day it changes.
//
// The terminal mode is stream_shadow at the derived stride, not stream. The
// single-pass form answers and the established one keeps checking a sparse
// sample of it, forever. Stopping at stream would trade a guard that costs
// about a thousandth of the canonical path for the sentence "it was verified
// once" -- and the thing it guards is every future change to this package,
// not the one change that has already been proven.
//
// Until then the answer to "does the operator know better than the program"
// is still no for what the encoder should do, and yes only for when a given
// cluster is ready to move -- which is the whole and only reason these exist.
type PhaseTwoCanonicalConfig struct {
	// Mode is one of established, shadow, stream_shadow, stream. Empty means
	// established, which is what every deployment runs until its own shadow
	// evidence says otherwise.
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
// A transient window that wants dense sampling says so explicitly; this is the
// value a deployment gets when it says nothing, which after the two rollout
// keys retire is every deployment.
const defaultCanonicalShadowStride = 1024

func (c PhaseTwoCanonicalConfig) mode() string {
	if c.Mode == "" {
		return contract.CanonicalModeEstablished
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
	// Empty disables final Shadow evidence; the file is the frozen Epoch manifest.
	ShadowManifestPath string                    `yaml:"shadow_manifest_path,omitempty"`
	Worker             PhaseTwoWorkerConfig      `yaml:"worker"`
	Control            PhaseTwoControlConfig     `yaml:"control"`
	Output             PhaseTwoOutputConfig      `yaml:"output"`
	Ownership          PhaseTwoOwnershipConfig   `yaml:"-"`
	Scheduler          PhaseTwoSchedulerConfig   `yaml:"scheduler"`
	Access             PhaseTwoAccessConfig      `yaml:"access"`
	Coordinator        PhaseTwoCoordinatorConfig `yaml:"-"`
	Canonical          PhaseTwoCanonicalConfig   `yaml:"canonical"`
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
			TickInterval:        Duration(time.Second),
			MaxQueuedItemsPerQG: 16, MaxReplaySlots: 3, MaxReplayAge: Duration(10 * time.Minute),
			RetryMinDelay: Duration(time.Second), RetryMaxDelay: Duration(30 * time.Second),
			QueryUnavailableCooldown: true,
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
