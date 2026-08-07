// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package podterminating

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

const (
	StateVersion      = 2
	StateDataKey      = "state.json"
	HardMaxStateBytes = 900_000
)

type Dimension struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Node      string `json:"node"`
}

type Observation struct {
	UID               types.UID
	Dimension         Dimension
	DeletionStartedAt time.Time
}

type RecoveryDimension struct {
	Dimension
	ExpiresAt            float64 `json:"expires_at"`
	RestartExtensionUsed bool    `json:"restart_extension_used"`
}

type Snapshot struct {
	Version  int                 `json:"version"`
	Active   []Dimension         `json:"active"`
	Recovery []RecoveryDimension `json:"recovery"`
}

type MetricRow struct {
	Dimension
	Seconds int64
}

func lessDimension(left, right Dimension) bool {
	if left.Namespace != right.Namespace {
		return left.Namespace < right.Namespace
	}
	if left.Pod != right.Pod {
		return left.Pod < right.Pod
	}
	return left.Node < right.Node
}

func sortedDimensions(values map[Dimension]Observation) []Dimension {
	dimensions := make([]Dimension, 0, len(values))
	for dimension := range values {
		dimensions = append(dimensions, dimension)
	}
	sort.Slice(dimensions, func(i, j int) bool {
		return lessDimension(dimensions[i], dimensions[j])
	})
	return dimensions
}

func sortedRecovery(values map[Dimension]RecoveryDimension) []RecoveryDimension {
	recovery := make([]RecoveryDimension, 0, len(values))
	for _, value := range values {
		recovery = append(recovery, value)
	}
	sort.Slice(recovery, func(i, j int) bool {
		return lessDimension(recovery[i].Dimension, recovery[j].Dimension)
	})
	return recovery
}

func MarshalSnapshot(snapshot Snapshot) ([]byte, error) {
	if err := validateSnapshot(snapshot); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal state snapshot: %w", err)
	}
	if len(raw) > HardMaxStateBytes {
		return nil, fmt.Errorf("persisted state size %d exceeds hard limit %d", len(raw), HardMaxStateBytes)
	}
	return raw, nil
}

func UnmarshalSnapshot(raw []byte, maxBytes int) (Snapshot, error) {
	if len(raw) == 0 {
		return Snapshot{}, fmt.Errorf("state snapshot must not be empty")
	}
	if maxBytes <= 0 || maxBytes > HardMaxStateBytes {
		return Snapshot{}, fmt.Errorf("state max bytes %d is outside (0,%d]", maxBytes, HardMaxStateBytes)
	}
	if len(raw) > maxBytes {
		return Snapshot{}, fmt.Errorf("persisted state size %d exceeds limit %d", len(raw), maxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var snapshot Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode state snapshot: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Snapshot{}, fmt.Errorf("state snapshot contains trailing JSON values")
		}
		return Snapshot{}, fmt.Errorf("decode trailing state snapshot data: %w", err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func validateSnapshot(snapshot Snapshot) error {
	if snapshot.Version != StateVersion {
		return fmt.Errorf("unsupported state version %d, want %d", snapshot.Version, StateVersion)
	}
	active := make(map[Dimension]struct{}, len(snapshot.Active))
	for _, dimension := range snapshot.Active {
		if err := validateDimension(dimension); err != nil {
			return fmt.Errorf("invalid active dimension: %w", err)
		}
		if _, exists := active[dimension]; exists {
			return fmt.Errorf("duplicate active dimension %#v", dimension)
		}
		active[dimension] = struct{}{}
	}
	recovery := make(map[Dimension]struct{}, len(snapshot.Recovery))
	for _, value := range snapshot.Recovery {
		if err := validateDimension(value.Dimension); err != nil {
			return fmt.Errorf("invalid recovery dimension: %w", err)
		}
		if value.ExpiresAt <= 0 {
			return fmt.Errorf("recovery expiry must be positive")
		}
		if _, exists := active[value.Dimension]; exists {
			return fmt.Errorf("dimension %#v is both active and recovery", value.Dimension)
		}
		if _, exists := recovery[value.Dimension]; exists {
			return fmt.Errorf("duplicate recovery dimension %#v", value.Dimension)
		}
		recovery[value.Dimension] = struct{}{}
	}
	return nil
}

func validateDimension(dimension Dimension) error {
	if dimension.Namespace == "" {
		return fmt.Errorf("namespace must not be empty")
	}
	if dimension.Pod == "" {
		return fmt.Errorf("pod must not be empty")
	}
	return nil
}
