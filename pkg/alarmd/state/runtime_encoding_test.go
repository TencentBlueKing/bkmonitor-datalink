// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"encoding/json"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The encoders below build runtime state in the representations the store
// reads, for tests that prepare stored records directly.

// executionStateSchemaV2 and runtimeEnvelope are the JSON envelope every
// binary before the framed record wrote under the older runtime key. Nothing
// writes or reads it any more; the shape stays here only so the baselines that
// compare its size with the framed record's keep measuring the real thing.
const executionStateSchemaV2 = "alarmd-runtime-state-v2"

type runtimeEnvelope struct {
	Schema         string                                `json:"schema"`
	Identity       execution.StateKeyIdentity            `json:"identity"`
	BlobRevision   uint64                                `json:"blob_revision"`
	ApplyVersion   execution.ApplyVersion                `json:"apply_version"`
	MutationDigest execution.MutationDigest              `json:"mutation_digest"`
	LastEventTime  int64                                 `json:"last_event_time"`
	SeriesGuard    *execution.StateGuardFact             `json:"series_guard,omitempty"`
	Levels         []execution.RuntimeLevelStateMutation `json:"levels"`
	History        []execution.StateHistoryPoint         `json:"history"`
}

// encodeRuntime writes the JSON envelope, for the size baselines only.
func encodeRuntime(mutation execution.StateMutation, revision uint64) ([]byte, error) {
	levels := append([]execution.RuntimeLevelStateMutation(nil), mutation.Levels...)
	sort.Slice(levels, func(i, j int) bool { return levels[i].LevelID < levels[j].LevelID })
	last := int64(0)
	for _, level := range levels {
		if level.LastProcessedEventTime > last {
			last = level.LastProcessedEventTime
		}
	}
	return json.Marshal(runtimeEnvelope{executionStateSchemaV2, mutation.Identity, revision, mutation.ApplyVersion,
		mutation.MutationDigest, last, mutation.SeriesGuard, levels, mutation.Points})
}

// encodeRuntimePacked writes the framed record.
func encodeRuntimePacked(mutation execution.StateMutation, revision uint64) ([]byte, error) {
	encoded, _, err := encodeRuntimePackedCounted(mutation, revision)
	return encoded, err
}
