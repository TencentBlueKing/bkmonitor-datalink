// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package controlplaneprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

type limitDocuments struct {
	mu       sync.Mutex
	data     map[string]json.RawMessage
	versions map[string]int
}

func newLimitDocuments() *limitDocuments {
	return &limitDocuments{data: map[string]json.RawMessage{}, versions: map[string]int{}}
}

func (d *limitDocuments) Get(_ context.Context, kind, id string) (json.RawMessage, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	key := kind + "/" + id
	b, ok := d.data[key]
	if !ok {
		return nil, "", eventsource.ErrNotFound
	}
	return append(json.RawMessage(nil), b...), fmt.Sprint(d.versions[key]), nil
}

func (d *limitDocuments) Put(ctx context.Context, kind, id, expected string, raw json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := kind + "/" + id
	v := d.versions[key]
	if (v == 0 && expected != "") || (v > 0 && expected != fmt.Sprint(v)) {
		return eventsource.ErrConflict
	}
	d.data[key] = append(json.RawMessage(nil), raw...)
	d.versions[key]++
	return nil
}

func (d *limitDocuments) List(_ context.Context, kind, after string, n int) ([]json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	keys := []string{}
	for key := range d.data {
		if id, ok := strings.CutPrefix(key, kind+"/"); ok && id > after {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := []json.RawMessage{}
	for _, key := range keys[:min(n, len(keys))] {
		out = append(out, d.data[key])
	}
	return out, nil
}

func defaultSourceSpec() config.EventSource {
	return config.EventSource{EventSourceID: "built_in_bk", Enabled: true, Storage: config.EventSourceStorageConfig{Type: config.StorageTypeKafka, Kafka: config.KafkaStorageConfig{Brokers: []string{"kafka:9092"}, Topic: "raw", ConsumerGroup: "cleaner"}}}
}

func TestSourceLimitDefaultOwnership(t *testing.T) {
	source := defaultSourceSpec()
	got := sourceLimitDefaults(source)
	if source.ActiveAlertLimits != nil || got.ActiveAlertLimits.Rules[0].MaxActive != 5000 || got.ActiveAlertLimits.Rules[0].Notify.Fields["strategy_id"] != "labels.strategy_id" {
		t.Fatal(got)
	}
	explicit := got
	explicit.ActiveAlertLimits = &config.ActiveAlertLimits{Rules: []config.ActiveAlertLimitRule{{Name: "my-strategy", GroupBy: []string{"labels.strategy_id"}, MaxActive: 200}}}
	if !reflect.DeepEqual(sourceLimitDefaults(explicit), explicit) {
		t.Fatal("overwrote explicit strategy rule")
	}
	disabled := false
	explicit.ActiveAlertLimits = &config.ActiveAlertLimits{Enabled: &disabled}
	if len(sourceLimitDefaults(explicit).ActiveAlertLimits.Rules) != 0 {
		t.Fatal("overrode disabled config")
	}
	source.Enabled = false
	if sourceLimitDefaults(source).ActiveAlertLimits != nil {
		t.Fatal("enabled stopped source")
	}
	source.Enabled = true
	source.EventSourceID = "other"
	if sourceLimitDefaults(source).ActiveAlertLimits != nil {
		t.Fatal("injected another source")
	}
}

func TestSourceLimitPublicationIsIdempotentAndVisible(t *testing.T) {
	docs := newLimitDocuments()
	service := eventsource.New(docs, config.DefaultSeverityConfig(), eventsource.Options{SourceDefaults: sourceLimitDefaults})
	spec := defaultSourceSpec()
	first, err := service.Apply(t.Context(), spec, 0, false, "api")
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"provider", "import", "api"} {
		again, err := service.Apply(t.Context(), spec, first.Revision, false, actor)
		if err != nil || again.Revision != first.Revision {
			t.Fatal("duplicate publication", again, err)
		}
	}
	published, err := service.GetRelease(t.Context(), spec.EventSourceID, first.Published)
	if err != nil || published.Spec.ActiveAlertLimits.Rules[0].MaxActive != 5000 {
		t.Fatal(published, err)
	}
	changed := published.Spec
	changed.ActiveAlertLimits.Rules[0].MaxActive = 200
	second, err := service.Apply(t.Context(), changed, first.Revision, false, "kac")
	if err != nil {
		t.Fatal(err)
	}
	if second.Spec.ActiveAlertLimits.Rules[0].MaxActive != 200 {
		t.Fatal("KAC threshold overwritten")
	}
	historical, _ := service.GetRelease(t.Context(), spec.EventSourceID, first.Published)
	if historical.Spec.ActiveAlertLimits.Rules[0].MaxActive != 5000 {
		t.Fatal("rewrote immutable release")
	}
}

type limitProvider struct{ spec config.EventSource }

func (p limitProvider) Pull(context.Context, eventsource.Reader) ([]eventsource.Change, error) {
	return []eventsource.Change{{Spec: p.spec}}, nil
}

type cancelLimitRound struct{ cancel context.CancelFunc }

func (cancelLimitRound) RoundStarted() {}

func (o cancelLimitRound) RoundFinished(context.Context, time.Duration, int, bool) { o.cancel() }

func TestProviderAndExistingSourceBackfill(t *testing.T) {
	docs := newLimitDocuments()
	old := eventsource.New(docs, config.DefaultSeverityConfig())
	spec := defaultSourceSpec()
	first, err := old.Apply(t.Context(), spec, 0, false, "existing")
	if err != nil {
		t.Fatal(err)
	}
	service := eventsource.New(docs, config.DefaultSeverityConfig(), eventsource.Options{SourceDefaults: sourceLimitDefaults})
	backfilled, err := service.Apply(t.Context(), first.Spec, first.Revision, false, "linkd-defaults")
	if err != nil || backfilled.Revision != first.Revision+1 {
		t.Fatal(backfilled, err)
	}
	emptyDocs := newLimitDocuments()
	empty := eventsource.New(emptyDocs, config.DefaultSeverityConfig(), eventsource.Options{SourceDefaults: sourceLimitDefaults})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runSourceLimitDefaults(ctx, empty, slog.New(slog.NewTextHandler(io.Discard, nil)), nil); err != nil {
		t.Fatal(err)
	}
	if len(emptyDocs.data) != 0 {
		t.Fatal("created absent built_in_bk")
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	if err := empty.RunProvider(ctx, limitProvider{spec}, time.Second, nil, cancelLimitRound{cancel}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	record, err := empty.Get(t.Context(), spec.EventSourceID)
	if err != nil || record.Spec.ActiveAlertLimits.Rules[0].MaxActive != 5000 {
		t.Fatal(record, err)
	}
}

func TestRemovedHookRecordCanBeReplacedWithoutRewritingHistory(t *testing.T) {
	docs := newLimitDocuments()
	spec := defaultSourceSpec().WithDefaults()
	rawSpec, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(rawSpec, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy["hooks"] = []any{map[string]any{"name": "old-index", "type": "active-alert-by-strategy", "config": map[string]any{"redis": map[string]any{"address": "old-redis:6379", "password": "historical-secret"}, "key_prefix": "old-active", "timeout_milliseconds": 1000, "pool_size": 16}}}
	recordBytes, err := json.Marshal(map[string]any{"id": "built_in_bk", "revision": 1, "published": 1, "spec": legacy})
	if err != nil {
		t.Fatal(err)
	}
	releaseBytes, err := json.Marshal(map[string]any{"id": "built_in_bk", "version": 1, "spec": legacy})
	if err != nil {
		t.Fatal(err)
	}
	docs.data["records/built_in_bk"] = recordBytes
	docs.versions["records/built_in_bk"] = 1
	docs.data["releases/built_in_bk:1"] = releaseBytes
	docs.versions["releases/built_in_bk:1"] = 1
	service := eventsource.New(docs, config.DefaultSeverityConfig(), eventsource.Options{SourceDefaults: sourceLimitDefaults})
	record, err := service.Get(t.Context(), "built_in_bk")
	if err != nil {
		t.Fatal("upgrade cannot read old record", err)
	}
	redacted, err := json.Marshal(record.Redacted())
	if err != nil || strings.Contains(string(redacted), "historical-secret") {
		t.Fatal("historical credential leaked", err)
	}
	if _, err := service.Apply(t.Context(), record.Spec, record.Revision, false, "api"); err == nil {
		t.Fatal("old hook republished")
	}
	record.Spec.Hooks = nil
	updated, err := service.Apply(t.Context(), record.Spec, record.Revision, false, "api")
	if err != nil || updated.Spec.ActiveAlertLimits.Rules[0].MaxActive != 5000 {
		t.Fatal(updated, err)
	}
	historical, err := service.GetRelease(t.Context(), "built_in_bk", 1)
	if err != nil || historical.Spec.Hooks[0].Type != "active-alert-by-strategy" {
		t.Fatal("history unreadable", err)
	}
	if string(docs.data["releases/built_in_bk:1"]) != string(releaseBytes) {
		t.Fatal("rewrote historical release")
	}
}
