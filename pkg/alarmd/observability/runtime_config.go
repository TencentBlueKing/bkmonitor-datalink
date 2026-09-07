// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

// RuntimeConfigFacts is a fixed, credential-free startup evidence surface.
// It is not a WorkerCompatibility or business identity contract.
type RuntimeConfigFacts struct {
	Profile    string               `json:"profile"`
	Source     string               `json:"source"`
	CPUSource  string               `json:"cpu_source"`
	GOMAXPROCS int                  `json:"gomaxprocs"`
	Capacity   RuntimeCapacityFacts `json:"capacity"`
	Digest     string               `json:"runtime_config_digest"`
}

type RuntimeCapacityFacts struct {
	ExpiredRangeEnabled        bool   `json:"expired_range_enabled"`
	ActiveExecutions           int    `json:"active_executions"`
	ConfiguredActiveExecutions int    `json:"configured_active_executions"`
	QueryPermits               int    `json:"query_permits"`
	RecoveryQueryPermits       int    `json:"recovery_query_permits"`
	ReadyQueue                 int    `json:"ready_queue"`
	RecoveryQueue              int    `json:"recovery_queue"`
	QueuedPerQG                int    `json:"queued_per_qg"`
	TickNS                     int64  `json:"tick_ns"`
	ReplaySlots                uint32 `json:"replay_slots"`
	ReplayAgeNS                int64  `json:"replay_age_ns"`
	RetryMinNS                 int64  `json:"retry_min_ns"`
	RetryMaxNS                 int64  `json:"retry_max_ns"`
	SequencerReservations      int    `json:"sequencer_reservations"`
	Series                     uint64 `json:"series"`
	RetainedBytes              uint64 `json:"retained_bytes"`
	StateMutations             uint64 `json:"state_mutations"`
	Events                     uint64 `json:"events"`
	GapMutations               uint64 `json:"gap_mutations"`
	GapFacts                   uint64 `json:"gap_facts"`
	// SlotStateMutations and SlotGapMutations are the derived per-Slot caps:
	// the smaller of the process budget and what StateApplyChunks Store calls
	// of StoreMaxItems can carry. A Slot above its cap completes UNAVAILABLE.
	SlotStateMutations  uint64 `json:"slot_state_mutations"`
	SlotGapMutations    uint64 `json:"slot_gap_mutations"`
	StateApplyChunks    int    `json:"state_apply_chunks"`
	StoreMaxValueBytes  int    `json:"store_max_value_bytes"`
	StoreMaxItems       int    `json:"store_max_items"`
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
