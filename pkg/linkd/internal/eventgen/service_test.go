// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

type memDocuments struct {
	mu       sync.Mutex
	data     map[string]json.RawMessage
	versions map[string]int
}

func memoryDocuments() *memDocuments {
	return &memDocuments{data: map[string]json.RawMessage{}, versions: map[string]int{}}
}

func (m *memDocuments) Get(_ context.Context, k, id string) (json.RawMessage, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := k + "/" + id
	b, ok := m.data[key]
	if !ok {
		return nil, "", ErrNotFound
	}
	return append([]byte(nil), b...), fmt.Sprint(m.versions[key]), nil
}

func (m *memDocuments) Put(_ context.Context, k, id, v string, b json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := k + "/" + id
	version := m.versions[key]
	if version == 0 && v != "" || version != 0 && v != fmt.Sprint(version) {
		return ErrConflict
	}
	m.data[key] = append([]byte(nil), b...)
	m.versions[key]++
	return nil
}

func (m *memDocuments) List(_ context.Context, k, prefix, after string, n int) ([]json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := []string{}
	for key := range m.data {
		if strings.HasPrefix(key, k+"/"+prefix) && key > k+"/"+after {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	result := []json.RawMessage{}
	for _, key := range keys[:min(n, len(keys))] {
		result = append(result, append([]byte(nil), m.data[key]...))
	}
	return result, nil
}

type fakeSources struct{}

func (fakeSources) Get(_ context.Context, id string) (eventsource.Record, error) {
	return eventsource.Record{ID: id, Published: 1}, nil
}

func (fakeSources) GetRelease(_ context.Context, id string, _ int64) (eventsource.Release, error) {
	return eventsource.Release{Spec: config.EventSource{EventSourceID: id, Enabled: true, Storage: config.EventSourceStorageConfig{Type: config.StorageTypeKafka}}}, nil
}

func TestTenantScopeCASAndRestart(t *testing.T) {
	ctx := t.Context()
	store := memoryDocuments()
	s := NewService(store, fakeSources{})
	c := fixture()
	p, e := s.ApplyPlan(ctx, c.TenantID, "test", 0, c)
	if e != nil {
		t.Fatal(e)
	}
	if p.Revision != 1 {
		t.Fatal(p)
	}
	if _, e = s.ApplyPlan(ctx, c.TenantID, "test", 0, c); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.GetPlan(ctx, "other", "test"); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.Start(ctx, c.TenantID, "test", Command{OperationID: "run1"}); e == nil {
		t.Fatal("offline runner accepted")
	}
	if _, e = s.Poll(ctx, Heartbeat{SessionID: "session1", NewSession: true}); e != nil {
		t.Fatal(e)
	}
	run, e := s.Start(ctx, c.TenantID, "test", Command{OperationID: "run1"})
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Start(ctx, c.TenantID, "test", Command{OperationID: "run1"})
	if e != nil || again.ID != run.ID {
		t.Fatal("start duplicated")
	}
	a, e := s.Poll(ctx, Heartbeat{SessionID: "session1"})
	if e != nil || len(a) != 1 {
		t.Fatal(a, e)
	}
	if _, e = s.Poll(ctx, Heartbeat{SessionID: "session2", NewSession: true}); e != nil {
		t.Fatal(e)
	}
	run, e = s.GetRun(ctx, c.TenantID, run.ID)
	if e != nil || run.Status.State != "interrupted" {
		t.Fatal(run, e)
	}
	if _, e = s.Poll(ctx, Heartbeat{SessionID: "session1"}); !errors.Is(e, ErrConflict) {
		t.Fatal("old session accepted")
	}
}
