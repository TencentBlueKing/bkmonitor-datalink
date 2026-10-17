// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type mapperSnapshotter struct {
	level       func(string) (string, error)
	defaultUsed *bool
}

func (s mapperSnapshotter) WithLevelMapper(level func(string) (string, error)) PolicySnapshotter {
	s.level = level
	return s
}

func (s mapperSnapshotter) Snapshot(_ context.Context, _ domain.Event, at time.Time) (*store.PolicyContext, error) {
	*s.defaultUsed = s.level == nil
	return &store.PolicyContext{EvaluatedAt: at}, nil
}

func TestPolicySnapshotDefaultMapperMatchesEvaluator(t *testing.T) {
	processor := newTestProcessor(t, memory.New(), &recordingHook{})
	defaultUsed := false
	shared := mapperSnapshotter{level: func(name string) (string, error) { return name, nil }, defaultUsed: &defaultUsed}
	processor.policies = shared
	if _, err := processor.readPolicies(t.Context(), testEvent("default-mapper", "critical")); err != nil {
		t.Fatal(err)
	}
	if !defaultUsed || shared.level == nil {
		t.Fatalf("candidate mapper differs from evaluator or shared mapper changed: default=%v shared=%v", defaultUsed, shared.level == nil)
	}
}
