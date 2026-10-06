// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

// GapExtensionFacts explains a same-Slot protection extension. These are log
// facts, never metric labels. Existing progress is not counted again.
type GapExtensionFacts struct {
	StrategyID     string          `json:"strategy_id"`
	MarkerRevision uint64          `json:"marker_revision"`
	Persisted      []GapScopeFacts `json:"persisted"`
	Proposed       []GapScopeFacts `json:"proposed"`
}
type GapScopeFacts struct {
	LevelID  uint32 `json:"level_id,omitempty"`
	HasLevel bool   `json:"has_level"`
	Kind     string `json:"kind,omitempty"`
	Status   string `json:"status,omitempty"`
	Reason   string `json:"reason"`
	Required uint32 `json:"required"`
	Observed uint32 `json:"observed"`
}
