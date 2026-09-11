// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventsource

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
)

type memoryDocs struct {
	mu          sync.Mutex
	data        map[string]json.RawMessage
	versions    map[string]int
	failRelease bool
}

func newDocs() *memoryDocs {
	return &memoryDocs{data: map[string]json.RawMessage{}, versions: map[string]int{}}
}

func (d *memoryDocs) Get(_ context.Context, k, id string) (json.RawMessage, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := k + "/" + id
	b, ok := d.data[key]
	if !ok {
		return nil, "", ErrNotFound
	}
	return append(json.RawMessage(nil), b...), fmt.Sprint(d.versions[key]), nil
}

func (d *memoryDocs) Put(_ context.Context, k, id, expected string, b json.RawMessage) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if k == "releases" && d.failRelease {
		d.failRelease = false
		return fmt.Errorf("injected failure")
	}
	key := k + "/" + id
	v := d.versions[key]
	if (v == 0 && expected != "") || (v > 0 && expected != fmt.Sprint(v)) {
		return ErrConflict
	}
	d.versions[key]++
	d.data[key] = append(json.RawMessage(nil), b...)
	return nil
}

func (d *memoryDocs) List(_ context.Context, k, after string, limit int) ([]json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var keys []string
	for key := range d.data {
		if len(key) > len(k)+1 && key[:len(k)+1] == k+"/" && key[len(k)+1:] > after {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var result []json.RawMessage
	for _, key := range keys[:min(limit, len(keys))] {
		result = append(result, append(json.RawMessage(nil), d.data[key]...))
	}
	return result, nil
}

func sample() config.EventSource {
	return config.EventSource{EventSourceID: "source-a", Enabled: true, Storage: config.EventSourceStorageConfig{Type: "kafka", Kafka: config.KafkaStorageConfig{Brokers: []string{"localhost:9092"}, Topic: "events", ConsumerGroup: "linkd"}}}.WithDefaults()
}

func TestPublicationRecoveryAndCAS(t *testing.T) {
	ctx := context.Background()
	d := newDocs()
	s := New(d, config.SeverityConfig{})
	d.failRelease = true
	if _, e := s.Apply(ctx, sample(), 0, false, "test"); e == nil {
		t.Fatal("failure not injected")
	}
	r, e := s.Get(ctx, "source-a")
	if e != nil || r.Pending == nil || r.Published != 0 {
		t.Fatal("pending state missing", e)
	}
	r, e = s.Recover(ctx, r.ID)
	if e != nil || r.Published != 1 || r.Pending != nil {
		t.Fatal("recovery failed", e)
	}
	rel, e := s.GetRelease(ctx, r.ID, 1)
	if e != nil || rel.Spec.Version != 1 {
		t.Fatal("release lost version")
	}
	r, e = s.Apply(ctx, sample(), 0, false, "retry")
	if e != nil || r.Published != 1 {
		t.Fatal("idempotent retry failed", e)
	}
	other := sample()
	other.Enabled = false
	if _, e = s.Apply(ctx, other, 0, false, "stale"); !errors.Is(e, ErrConflict) {
		t.Fatal("CAS lost", e)
	}
	r, e = s.Apply(ctx, other, 1, false, "provider")
	if e != nil || r.Published != 2 {
		t.Fatal("provider update failed", e)
	}
	r, e = s.Apply(ctx, other, 2, true, "api")
	if e != nil || !r.Deleted {
		t.Fatal("delete failed", e)
	}
	old, e := s.GetRelease(ctx, r.ID, 1)
	if e != nil || !old.Spec.Enabled {
		t.Fatal("old release mutated")
	}
}

func TestConcurrentPublishHasSingleWinner(t *testing.T) {
	s := New(newDocs(), config.SeverityConfig{})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			spec := sample()
			spec.DefaultSeverity = []string{"critical", "warning"}[i%2]
			_, e := s.Apply(context.Background(), spec, 0, false, "api")
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	success := 0
	for e := range errs {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	r, e := s.Get(context.Background(), "source-a")
	if e != nil || r.Revision != 1 || success == 0 {
		t.Fatalf("concurrent revision %+v %v", r, e)
	}
}

func TestConcurrentRecoveryReturnsCommittedPublication(t *testing.T) {
	ctx := context.Background()
	d := newDocs()
	d.failRelease = true
	s := New(d, config.SeverityConfig{})
	if _, err := s.Apply(ctx, sample(), 0, false, "api"); err == nil {
		t.Fatal("expected pending release")
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Recover(ctx, "source-a")
			if err == nil && r.Published != 1 {
				err = fmt.Errorf("wrong published version")
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal("same publication incorrectly conflicted", err)
		}
	}
}

func TestPublicationPreservesEnrichAcrossReleases(t *testing.T) {
	ctx := context.Background()
	service := New(newDocs(), config.SeverityConfig{})
	spec := sample()
	spec.Enrich.Processors = []config.EnrichProcessorConfig{{Type: "source"}}
	spec.Enrich.DataSources = &config.EnrichDataSources{MySQL: &config.EnrichMySQLDataSource{
		Address: "mysql.example.com:3306", Database: "kingeye", Username: "reader", Password: "secret",
	}}
	record, err := service.Apply(ctx, spec, 0, false, "test")
	if err != nil {
		t.Fatal(err)
	}
	spec.Enrich.Processors[0].Type = "metric"
	spec.Enrich.DataSources = &config.EnrichDataSources{MySQL: &config.EnrichMySQLDataSource{
		Address: "mysql.example.com:3306", Database: "kingeye", Username: "reader",
	}}
	if _, err := service.Apply(ctx, spec, record.Revision, false, "test"); err != nil {
		t.Fatal(err)
	}
	for version, want := range map[int64]string{1: "source", 2: "metric"} {
		release, err := service.GetRelease(ctx, spec.EventSourceID, version)
		if err != nil {
			t.Fatal(err)
		}
		if len(release.Spec.Enrich.Processors) != 1 || release.Spec.Enrich.Processors[0].Type != want {
			t.Fatalf("release %d lost enrich: %#v", version, release.Spec.Enrich)
		}
		encoded, err := json.Marshal(release.Spec)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &body); err != nil {
			t.Fatal(err)
		}
		if release.Spec.Enrich.DataSources == nil {
			t.Fatalf("release %d lost enrich datasources", version)
		}
		enrichJSON := string(body["enrich"])
		if strings.Contains(enrichJSON, `"Address"`) || !strings.Contains(enrichJSON, `"address"`) {
			t.Fatalf("invalid datasource JSON schema: %s", body["enrich"])
		}
		if !strings.Contains(enrichJSON, fmt.Sprintf(`"processors":[{"type":%q}]`, want)) {
			t.Fatalf("invalid API enrich schema: %s", body["enrich"])
		}
	}
}

func TestPublicationPreservesHookParametersAndIsolation(t *testing.T) {
	ctx := context.Background()
	service := New(newDocs(), config.SeverityConfig{})
	spec := sample()
	spec.Hooks = []config.HookConfig{{Name: "active", Type: config.HookTypeActiveAlertByStrategy, Config: config.HookParameters{Redis: &config.RedisConfig{Address: "redis:6379", Password: "private"}, KeyPrefix: "first"}}}
	record, err := service.Apply(ctx, spec, 0, false, "api")
	if err != nil {
		t.Fatal(err)
	}
	spec.Hooks[0].Config.KeyPrefix = "second"
	if _, err := service.Apply(ctx, spec, record.Revision, false, "provider"); err != nil {
		t.Fatal(err)
	}
	for version, want := range map[int64]string{1: "first", 2: "second"} {
		release, err := service.GetRelease(ctx, spec.EventSourceID, version)
		if err != nil {
			t.Fatal(err)
		}
		if len(release.Spec.Hooks) != 1 || release.Spec.Hooks[0].Config.KeyPrefix != want || *release.Spec.Hooks[0].Config.TimeoutMilliseconds != 1000 || release.Spec.Hooks[0].Config.Redis.Password != "private" {
			t.Fatal("release lost hook configuration")
		}
	}
	if record.Redacted().Spec.Hooks[0].Config.Redis.Password == "private" {
		t.Fatal("management record leaked hook password")
	}
	spec.Hooks[0].Type = "unknown"
	if _, err := service.Apply(ctx, spec, 2, false, "api"); err == nil {
		t.Fatal("published unknown hook")
	}
}
