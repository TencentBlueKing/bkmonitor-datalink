// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dynamicconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/runtimeconfig"
)

const initialLevels = `[{"name":"fatal","priority":0},{"name":"warning","priority":1},{"name":"remind","priority":2}]`

type fakeSource struct {
	raw   string
	err   error
	reads int
}

func (s *fakeSource) Read(context.Context) (json.RawMessage, error) {
	s.reads++
	return json.RawMessage(s.raw), s.err
}

func (*fakeSource) Close() error { return nil }

type memoryStore struct {
	records           map[string]Record
	versions          map[string]int
	writes            int
	readErr, writeErr error
}

func newMemoryStore() *memoryStore {
	return &memoryStore{records: map[string]Record{}, versions: map[string]int{}}
}

func (s *memoryStore) Load(_ context.Context, key string) (Record, string, error) {
	if s.readErr != nil {
		return Record{}, "", s.readErr
	}
	r, ok := s.records[key]
	if !ok {
		return r, "", ErrNotFound
	}
	return r, fmt.Sprint(s.versions[key]), nil
}

func (s *memoryStore) Save(_ context.Context, key, token string, r Record) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	expected := ""
	if s.versions[key] > 0 {
		expected = fmt.Sprint(s.versions[key])
	}
	if token != expected {
		return ErrConflict
	}
	s.records[key] = r
	s.versions[key]++
	s.writes++
	return nil
}

func testSettings() config.DynamicConfigConfig {
	return config.DynamicConfigConfig{Enabled: true, Sources: map[string]config.DynamicSourceConfig{"kingeye": {Type: config.DynamicSourceAlarmLevel, MySQL: &config.MySQLConfig{Address: "localhost:3306", Database: "kingeye", Username: "reader"}}}, Bindings: config.DynamicBindings{Severity: &config.DynamicBinding{Source: "kingeye"}}}
}

func testManager(t *testing.T, source Source, store Store, settings config.DynamicConfigConfig) (*Manager, *runtimeconfig.Severity) {
	t.Helper()
	state := runtimeconfig.NewSeverity(config.DefaultSeverityConfig())
	m, err := New(settings, "test-deployment", config.DefaultSeverityConfig(), state, source, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return m, state
}

func TestPersistBeforePublishAndRestoreWithoutUpstream(t *testing.T) {
	ctx := context.Background()
	source := &fakeSource{raw: initialLevels}
	store := newMemoryStore()
	m, state := testManager(t, source, store, testSettings())
	m.Bootstrap(ctx)
	if !state.SeveritySnapshot().Severity.Has("fatal") || store.writes != 1 {
		t.Fatal("snapshot not persisted and installed")
	}
	initial := state.SeveritySnapshot().Digest
	source.raw = `[{"name":"custom","priority":0}]`
	store.writeErr = errors.New("disk unavailable")
	m.Sync(ctx)
	if state.SeveritySnapshot().Digest != initial || m.Status().SyncState != "persistence_error" {
		t.Fatal("unpersisted configuration published")
	}
	store.writeErr = nil
	source2 := &fakeSource{err: errors.New("upstream unavailable")}
	restored, next := testManager(t, source2, store, testSettings())
	restored.Bootstrap(ctx)
	if source2.reads != 0 || next.SeveritySnapshot().Digest != initial || restored.Status().Origin != "persisted" {
		t.Fatal("startup failed to prefer persisted snapshot")
	}
	restored.Sync(ctx)
	if next.SeveritySnapshot().Digest != initial || restored.Status().SyncState != "source_error" {
		t.Fatal("source failure replaced cached configuration")
	}
}

func TestBootstrapFallbackAndEventualRecovery(t *testing.T) {
	source := &fakeSource{err: errors.New("offline")}
	store := newMemoryStore()
	m, state := testManager(t, source, store, testSettings())
	m.Bootstrap(context.Background())
	if m.Status().Origin != "yaml" || !state.SeveritySnapshot().Severity.Has("critical") {
		t.Fatal("missing YAML fallback")
	}
	source.err = nil
	source.raw = initialLevels
	m.Sync(context.Background())
	if m.Status().Origin != "upstream" || !state.SeveritySnapshot().Severity.Has("fatal") {
		t.Fatal("recovery did not apply online")
	}
}

func TestInvalidUpstreamPreservesLastGood(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{}`, `[{"name":"warning"}]`, `[{"name":"warning","priority":null}]`, `[{"name":"warning","priority":1.1}]`, `[{"name":"warning","priority":1},{"name":"warning","priority":2}]`, `[{"name":"warning","priority":1},{"name":"other","priority":1}]`} {
		t.Run(raw, func(t *testing.T) {
			source := &fakeSource{raw: initialLevels}
			store := newMemoryStore()
			m, state := testManager(t, source, store, testSettings())
			m.Bootstrap(context.Background())
			digest := state.SeveritySnapshot().Digest
			source.raw = raw
			m.Sync(context.Background())
			if state.SeveritySnapshot().Digest != digest || store.writes != 1 || m.Status().SyncState != "invalid_config" {
				t.Fatal("invalid configuration replaced last good snapshot")
			}
		})
	}
}

func TestSnapshotIdentityAndCrashRecovery(t *testing.T) {
	settings := testSettings()
	source := &fakeSource{raw: initialLevels}
	store := newMemoryStore()
	m, state := testManager(t, source, store, settings)
	// 模拟保存成功后、发布前进程退出。
	snapshot, err := decodeLevels(json.RawMessage(initialLevels), "warning")
	if err != nil {
		t.Fatal(err)
	}
	r := Record{SchemaVersion: 1, Deployment: m.deployment, Item: "severity", SourceIdentity: m.identity, PersistedAt: time.Now(), Snapshot: snapshot}
	if err = store.Save(context.Background(), m.key, "", r); err != nil {
		t.Fatal(err)
	}
	m.Bootstrap(context.Background())
	if state.SeveritySnapshot().Digest != snapshot.Digest || source.reads != 0 {
		t.Fatal("did not recover committed snapshot")
	}
	changed := settings.Clone()
	s := changed.Sources["kingeye"]
	s.MySQL.Password = "rotated"
	changed.Sources["kingeye"] = s
	rotated, _ := testManager(t, source, store, changed)
	if rotated.key != m.key {
		t.Fatal("credential rotation invalidated cache")
	}
	s.BKTenantID = "another"
	changed.Sources["kingeye"] = s
	offline := &fakeSource{err: errors.New("offline")}
	other, next := testManager(t, offline, store, changed)
	other.Bootstrap(context.Background())
	if other.key == m.key || next.SeveritySnapshot().NativeNames || other.Status().Origin != "yaml" {
		t.Fatal("cross-tenant snapshot restored")
	}
}

func TestContentOrderDoesNotPublishNewRevision(t *testing.T) {
	source := &fakeSource{raw: initialLevels}
	store := newMemoryStore()
	m, state := testManager(t, source, store, testSettings())
	m.Bootstrap(context.Background())
	digest := state.SeveritySnapshot().Digest
	source.raw = `[{"name":"remind","priority":2},{"name":"fatal","priority":0},{"name":"warning","priority":1}]`
	m.Sync(context.Background())
	if state.SeveritySnapshot().Digest != digest || store.writes != 1 {
		t.Fatal("ordering changed digest")
	}
}

type watchSource struct {
	started chan struct{}
	closed  chan struct{}
}

func (s *watchSource) Read(context.Context) (json.RawMessage, error) {
	return json.RawMessage(initialLevels), nil
}

func (*watchSource) Close() error { return nil }

func (s *watchSource) Watch(ctx context.Context, notify func()) error {
	close(s.started)
	notify()
	<-ctx.Done()
	close(s.closed)
	return nil
}

func TestRunCancellationAndConcurrentSnapshotReaders(t *testing.T) {
	source := &watchSource{started: make(chan struct{}), closed: make(chan struct{})}
	m, state := testManager(t, source, newMemoryStore(), testSettings())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	<-source.started
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 100 {
				s := m.Status()
				_ = state.SeveritySnapshot()
				if s.Current.Severity.Levels != nil {
					s.Current.Severity.Levels[0].Name = "caller mutation"
				}
			}
		})
	}
	readers.Wait()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not stop")
	}
	select {
	case <-source.closed:
	default:
		t.Fatal("watch leaked")
	}
	if state.SeveritySnapshot().Severity.Levels[0].Name == "caller mutation" {
		t.Fatal("snapshot not isolated")
	}
}
