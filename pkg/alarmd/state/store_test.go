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
	"context"
	"errors"
	"testing"
	"time"
)

func TestStateTTLRejectsUnsatisfiedHardMaximum(t *testing.T) {
	requirement := requirement(1, "1", 2, 4)
	if _, err := StateTTL([]LevelRequirement{requirement}, time.Minute, time.Minute, 4*time.Minute); !errors.Is(err, ErrStateBudget) {
		t.Fatalf("StateTTL() error = %v, want budget error", err)
	}
}

type fakeBackend struct {
	values      map[string][]byte
	mgetBatches [][]string
	setBatches  [][]BackendWrite
	readErr     error
	writeErr    error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{values: make(map[string][]byte)}
}

func (backend *fakeBackend) MGet(_ context.Context, keys []string) ([][]byte, error) {
	backend.mgetBatches = append(backend.mgetBatches, append([]string(nil), keys...))
	if backend.readErr != nil {
		return nil, backend.readErr
	}
	values := make([][]byte, len(keys))
	for index, key := range keys {
		if value, exists := backend.values[key]; exists {
			values[index] = append([]byte(nil), value...)
		}
	}
	return values, nil
}

func (backend *fakeBackend) SetMany(_ context.Context, writes []BackendWrite) error {
	batch := append([]BackendWrite(nil), writes...)
	backend.setBatches = append(backend.setBatches, batch)
	if backend.writeErr != nil {
		return backend.writeErr
	}
	for _, write := range writes {
		backend.values[write.Key] = append([]byte(nil), write.Value...)
	}
	return nil
}
