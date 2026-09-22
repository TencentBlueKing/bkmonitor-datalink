// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

type indexSourceStub struct {
	records  []eventsource.Record
	releases map[string]eventsource.Release
	err      error
}

func (s indexSourceStub) List(context.Context, string, int) ([]eventsource.Record, error) {
	return s.records, s.err
}

func (s indexSourceStub) GetRelease(_ context.Context, id string, _ int64) (eventsource.Release, error) {
	r, ok := s.releases[id]
	if !ok {
		return r, errors.New("release missing")
	}
	return r, nil
}

func indexSpec(source, password string) config.EventSource {
	return config.EventSource{EventSourceID: source, Hooks: []config.HookConfig{{Name: "active", Type: config.HookTypeActiveAlertByStrategy, Config: config.HookParameters{Redis: &config.RedisConfig{Address: "redis:6379", Password: password, Database: 8}, KeyPrefix: "active"}}}}
}

func TestIndexTargetsUsePublishedUnion(t *testing.T) {
	s := indexSourceStub{records: []eventsource.Record{{ID: "a", Published: 1, Spec: indexSpec("a", "draft")}, {ID: "b", Published: 2, Deleted: true}}, releases: map[string]eventsource.Release{"a": {Spec: indexSpec("a", "one")}, "b": {Spec: indexSpec("b", "two"), Deleted: true}}}
	targets, err := loadIndexTargets(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets=%d", len(targets))
	}
	for _, target := range targets {
		if len(target.Sources) != 2 || target.Redis.Password != "one" {
			t.Fatal("did not use published shared scope")
		}
	}
	s.releases["a"] = eventsource.Release{Spec: indexSpec("a", "rotated")}
	after, err := loadIndexTargets(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	for id := range targets {
		if _, ok := after[id]; !ok {
			t.Fatal("credential rotation changed target identity")
		}
	}
	delete(s.releases, "b")
	if _, err := loadIndexTargets(t.Context(), s); err == nil {
		t.Fatal("accepted incomplete shared scope")
	}
}

func TestNoIndexHooksDoesNotOpenStorage(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runActiveIndexes(ctx, config.Config{}, indexSourceStub{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("task did not stop")
	}
}

func TestIndexTargetsRejectNestedPrefixes(t *testing.T) {
	a, b := indexSpec("a", "one"), indexSpec("b", "two")
	b.Hooks[0].Config.KeyPrefix = "active:source-b"
	s := indexSourceStub{records: []eventsource.Record{{ID: "a", Published: 1}, {ID: "b", Published: 1}}, releases: map[string]eventsource.Release{"a": {Spec: a}, "b": {Spec: b}}}
	if _, err := loadIndexTargets(t.Context(), s); err == nil {
		t.Fatal("parent prefix could delete nested target")
	}
	b.Hooks[0].Config.Redis.Database = 9
	s.releases["b"] = eventsource.Release{Spec: b}
	if _, err := loadIndexTargets(t.Context(), s); err != nil {
		t.Fatal("different databases do not overlap")
	}
}
