// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

// Where the process's CPU budget (GOMAXPROCS) was resolved from: what
// RuntimeConfigFacts.CPUSource and container_cpu_cores{source} say. The
// resolver writes these and the metric's closed label set is built from
// them, so the two cannot name a source differently.
const (
	// CPUSourceQuota is the container's CPU quota.
	CPUSourceQuota = "cpu_quota"
	// CPUSourceQuotaMinimum is a quota below one CPU, raised to the minimum.
	CPUSourceQuotaMinimum = "cpu_quota_minimum"
	// CPUSourceEnvironmentOverride is GOMAXPROCS set in the environment.
	CPUSourceEnvironmentOverride = "environment_override"
	// CPUSourceRuntimeDefault is no quota at all: the Go runtime's own
	// default, the host's CPUs rather than the container's.
	CPUSourceRuntimeDefault = "runtime_default"
)

// CPUSources is every source the CPU budget can be resolved from.
var CPUSources = []string{CPUSourceQuota, CPUSourceQuotaMinimum, CPUSourceEnvironmentOverride, CPUSourceRuntimeDefault}

// RuntimeConfigFacts is a fixed, credential-free startup evidence surface.
// It is not a WorkerCompatibility or business identity contract.
type RuntimeConfigFacts struct {
	Profile    string `json:"profile"`
	Source     string `json:"source"`
	CPUSource  string `json:"cpu_source"`
	GOMAXPROCS int    `json:"gomaxprocs"`
	// The memory limit and where it was read from travel with the budgets
	// derived from it: a table produced outside the Pod says so itself
	// instead of passing a fallback off as the container's limit.
	MemorySource     string               `json:"memory_source"`
	MemoryLimitBytes uint64               `json:"memory_limit_bytes"`
	Capacity         RuntimeCapacityFacts `json:"capacity"`
	// Storage says where each class of read and write goes. It is the question
	// an incident actually asks - which instance did this replica read
	// strategies from - and once a location may be inherited, the configuration
	// file no longer answers it. Addresses and databases only: this surface is
	// credential-free by contract.
	Storage RuntimeStorageFacts `json:"storage"`
	// Linkd is the alert link's settings that decide what this process does,
	// credential-free: whether the Console is configured, which is what
	// turns the alert closes on, is otherwise readable only from the
	// configuration file it was started with.
	Linkd   RuntimeLinkdFacts   `json:"linkd"`
	UQCodec RuntimeUQCodecFacts `json:"uq_codec"`
	// Retention is how long this deployment keeps what a Slot reads again,
	// beside every input each length is derived from. Whether a sixty-hour
	// strategy's content outlives its period was a question for the
	// configuration file and four functions; the Slot source's retention and
	// admission's once came from two different formulas, and nothing said so.
	Retention RuntimeRetentionFacts `json:"retention"`
	Digest    string                `json:"runtime_config_digest"`
}

// This is the startup negotiation scope, not a server capability claim.
// Ordinary production Execute negotiates by default; other entry points stay legacy.
type RuntimeUQCodecFacts struct {
	Mode  string `json:"mode"`
	Scope string `json:"scope"`
}

// RuntimeRetentionFacts are the retention lengths, in seconds, and their
// inputs.
//
// One Plan's need, which admission holds against ObjectLimitSeconds and
// withholds the Plan by name when it is past it, is the same sum as
// SnapshotMinimumSeconds with the Plan's own completion offset in place of the
// cadence: PublicationDelayAllowanceSeconds + (the Plan's completion offset -
// DownstreamExecutionReserveSeconds) + MaxReplayAgeSeconds +
// PostRecoveryTerminalDelaySeconds.
type RuntimeRetentionFacts struct {
	// CatalogSeconds is how long published Catalogs are kept: the manifests,
	// the schedule timelines, the active set. The larger of the configured
	// catalog TTL and SnapshotMinimumSeconds.
	CatalogSeconds int64 `json:"catalog_seconds"`
	// ObjectLimitSeconds is the longest a Plan's content objects are kept for
	// it, and the retention the Slot source holds a frozen Slot's Snapshot
	// to: the larger of the state store's maximum TTL and CatalogSeconds. A
	// Plan whose recovery contract needs its content longer is withheld by
	// name at admission.
	ObjectLimitSeconds int64 `json:"object_limit_seconds"`
	// SnapshotMinimumSeconds is what the recovery contract needs for the
	// cadence the catalog keys are kept for: the publication delay
	// allowance, that cadence less the downstream execution reserve, the
	// maximum replay age and the post-recovery terminal delay.
	SnapshotMinimumSeconds int64 `json:"snapshot_minimum_seconds"`
	// CatalogKeyCadenceSeconds is that cadence. It is not the longest period
	// a Plan may have: a longer Plan reads its content by digest, kept to
	// ObjectLimitSeconds.
	CatalogKeyCadenceSeconds int64 `json:"catalog_key_cadence_seconds"`
	// The inputs, as configured or derived at startup. RedisRestartMargin
	// feeds none of the lengths above: it is added to the lifetime of a
	// series' runtime state and of a Plan's generation-scoped keys, whose
	// ceiling is RedisMaxTTL.
	CatalogTTLSeconds                 int64 `json:"catalog_ttl_seconds"`
	RedisMaxTTLSeconds                int64 `json:"redis_max_ttl_seconds"`
	RedisRestartMarginSeconds         int64 `json:"redis_restart_margin_seconds"`
	DownstreamExecutionReserveSeconds int64 `json:"downstream_execution_reserve_seconds"`
	PublicationDelayAllowanceSeconds  int64 `json:"publication_delay_allowance_seconds"`
	MaxReplayAgeSeconds               int64 `json:"max_replay_age_seconds"`
	PostRecoveryTerminalDelaySeconds  int64 `json:"post_recovery_terminal_delay_seconds"`
}

// RuntimeLinkdFacts is what the alert link's configuration switches on: the
// Console configured or not, the event source and hook the link's target is
// narrowed to. With the Console configured, the closes of alerts of
// strategies that no longer exist and of targets that left a strategy's
// scope send. No address, username or password.
type RuntimeLinkdFacts struct {
	ConsoleConfigured bool `json:"console_configured"`
}

type RuntimeStorageFacts struct {
	// OwnStore is alarmd's own runtime state: catalog, ownership, state,
	// fleet, progress. The other three are the platform's own locations.
	OwnStore      string `json:"own_store"`
	StrategyCache string `json:"strategy_cache"`
	CMDBCache     string `json:"cmdb_cache"`
	LegacyService string `json:"legacy_service"`
	// The prefixes matter alongside the addresses: the right instance read
	// with the wrong prefix returns nothing, and looks exactly like the right
	// prefix read on the wrong instance.
	// OutputProtocol is the deployment's wire format choice. It decides what
	// bytes land on the output topic, which is the first thing a consumer that
	// reads nothing usable will be asked about.
	OutputProtocol      string `json:"output_protocol"`
	PlatformKeyPrefix   string `json:"platform_key_prefix"`
	StrategyCachePrefix string `json:"strategy_cache_prefix"`
	OwnStorePrefix      string `json:"own_store_prefix"`
	// DynamicConfig is where the platform's dynamic configuration is read
	// from, or not_configured when the deployment renders no distribution;
	// PlatformSettingsKeyPrefix is the platform prefix its keys hang off.
	// Preflight evidence: a deployment that meant to read the platform and
	// renders nothing reads as not_configured here before it runs.
	DynamicConfig             string `json:"dynamic_config"`
	TargetGroup               string `json:"target_group"`
	DynamicGroupKeyPrefix     string `json:"dynamic_group_key_prefix"`
	PlatformSettingsKeyPrefix string `json:"platform_settings_key_prefix"`
}

type RuntimeCapacityFacts struct {
	// The two execution-limit fields are the derivation and what the dispatcher
	// actually runs with, and they differ only when the ready queue clamps the
	// derived value down. Both are here so that a clamp which really did take
	// effect is visible during an incident rather than inferred.
	//
	// Neither is "configured", and the earlier name that said so was worse than
	// imprecise: this table is a release preflight and an incident record, so a
	// field named for a setting sends whoever is reading it to look for a
	// configuration that does not exist, at the moment they can least afford it.
	// Nothing outside the process can set either value.
	DerivedActiveExecutions   int    `json:"derived_active_executions"`
	EffectiveActiveExecutions int    `json:"effective_active_executions"`
	QueryPermits              int    `json:"query_permits"`
	RecoveryQueryPermits      int    `json:"recovery_query_permits"`
	RedisPoolSize             int    `json:"redis_pool_size"`
	ReadyQueue                int    `json:"ready_queue"`
	RecoveryQueue             int    `json:"recovery_queue"`
	QueuedPerQG               int    `json:"queued_per_qg"`
	TickNS                    int64  `json:"tick_ns"`
	ReplaySlots               uint32 `json:"replay_slots"`
	ReplayAgeNS               int64  `json:"replay_age_ns"`
	RetryMinNS                int64  `json:"retry_min_ns"`
	RetryMaxNS                int64  `json:"retry_max_ns"`
	SequencerReservations     int    `json:"sequencer_reservations"`
	Series                    uint64 `json:"series"`
	RetainedBytes             uint64 `json:"retained_bytes"`
	StateMutations            uint64 `json:"state_mutations"`
	Events                    uint64 `json:"events"`
	GapMutations              uint64 `json:"gap_mutations"`
	GapFacts                  uint64 `json:"gap_facts"`
	// SlotStateMutations and SlotGapMutations are the derived per-Slot caps:
	// the smaller of the process budget and what StateApplyChunks Store calls
	// of StoreMaxItems can carry. A Slot above its cap completes UNAVAILABLE.
	SlotStateMutations uint64 `json:"slot_state_mutations"`
	SlotGapMutations   uint64 `json:"slot_gap_mutations"`
	StateApplyChunks   int    `json:"state_apply_chunks"`
	StoreMaxValueBytes int    `json:"store_max_value_bytes"`
	StoreMaxItems      int    `json:"store_max_items"`
	// ControlTimelineCacheBytes and ControlTimelineCacheEntries bound the
	// control plane's decoded Schedule timeline cache. They are derived from
	// the same memory limit as the budgets above, so they belong in the same
	// table: a preflight has to be able to state what this container's cache
	// will hold before the Pod exists, which the running process's occupancy
	// metrics cannot answer.
	ControlTimelineCacheBytes   int `json:"control_timeline_cache_bytes"`
	ControlTimelineCacheEntries int `json:"control_timeline_cache_entries"`
	// GoMemoryLimitBytes and GoGCPercent are the collector's budget, derived
	// from the same memory limit as the ceilings above. They belong in this
	// table for the reason the rest of it exists: they are what the process
	// runs under, no file can state them, and before they were derived the
	// container's memory limit and the collector's behaviour had nothing to do
	// with one another. Zero means no container stated a limit, so the process
	// left the Go defaults alone rather than enforcing a guess.
	GoMemoryLimitBytes  int64  `json:"go_memory_limit_bytes"`
	GoGCPercent         int    `json:"go_gc_percent"`
	EvaluatorMaxPlans   uint64 `json:"evaluator_max_plans"`
	EvaluatorMaxRecords uint64 `json:"evaluator_max_records"`
	EvaluatorMaxLevels  uint64 `json:"evaluator_max_levels"`
	EvidenceBytes       int    `json:"evidence_bytes"`
	OutputMessageBytes  int    `json:"output_message_bytes"`
	UQBodyBytes         int64  `json:"uq_body_bytes"`
	UQSeriesBytes       int64  `json:"uq_series_bytes"`
	UQSeries            uint64 `json:"uq_series"`
	UQRecords           uint64 `json:"uq_records"`
}
