// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/pluginruntime"
)

type releaseStub struct {
	record     eventsource.Record
	release    eventsource.Release
	err        error
	version    int64
	releaseErr error
}

func (s *releaseStub) Get(context.Context, string) (eventsource.Record, error) {
	return s.record, s.err
}

func (s *releaseStub) GetRelease(_ context.Context, _ string, version int64) (eventsource.Release, error) {
	s.version = version
	return s.release, s.releaseErr
}

func TestPublishedSourcesUseRelease(t *testing.T) {
	stub := &releaseStub{record: eventsource.Record{Published: 3, Spec: config.EventSource{RelatedTenantID: "draft-tenant"}}, release: eventsource.Release{Version: 3, Spec: config.EventSource{Enabled: true, EventSourceID: "source", RelatedTenantID: "published-tenant", Collection: &config.SourceCollection{Mode: "push", Plugin: "rest_api_push", PluginSourceID: "original-kac-source", SourceName: "原来源名称", Timezone: "UTC", InitialState: map[string]any{"meta_info": "initial"}, Secret: "source-secret"}}}}
	assembled := 0
	sources := NewPublishedSources(stub, func(_ context.Context, r eventsource.Release) (*Service, error) { assembled++; return &Service{}, nil })
	bindings, err := sources.Lookup(t.Context(), "source")
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].Request.TenantID != "published-tenant" || bindings[0].Request.SourceVersion != 3 || bindings[0].Request.PluginSourceID != "original-kac-source" || bindings[0].Request.SourceName != "原来源名称" || bindings[0].Request.Timezone != "UTC" || bindings[0].Request.State["meta_info"] != "initial" || stub.version != 3 || assembled != 1 {
		t.Fatalf("wrong release binding: %+v", bindings)
	}
	stub.record.Deleted = true
	bindings, err = sources.Lookup(t.Context(), "source")
	if err != nil || len(bindings) != 0 || assembled != 1 {
		t.Fatal("deleted source assembled")
	}
	stub.record.Deleted = false
	stub.release.Spec.Collection.Mode = "pull"
	bindings, err = sources.Lookup(t.Context(), "source")
	if err != nil || len(bindings) != 0 {
		t.Fatal("pull source exposed to HTTP")
	}
}

func TestPublishedSourceUnavailable(t *testing.T) {
	stub := &releaseStub{err: eventsource.ErrNotFound}
	sources := NewPublishedSources(stub, func(context.Context, eventsource.Release) (*Service, error) {
		t.Fatal("must not assemble unavailable source")
		return nil, nil
	})
	if _, err := sources.Read(t.Context(), "missing"); !errors.Is(err, ErrSourceUnauthorized) {
		t.Fatal(err)
	}
	stub.err = errors.New("storage unavailable")
	if _, err := sources.Read(t.Context(), "source"); !errors.Is(err, stub.err) {
		t.Fatal("storage failure concealed")
	}
}

func TestReplayUsesOriginalReleaseAndExecutionInput(t *testing.T) {
	request := pluginruntime.Request{RequestID: "original", SourceID: "source", TenantID: "tenant", SourceVersion: 3, Plugin: "old-plugin", Configs: map[string]any{"host": "old-host"}, State: map[string]any{"cursor": "first"}}
	stub := &releaseStub{
		err:     errors.New("current record must not be queried during replay"),
		record:  eventsource.Record{Published: 4},
		release: eventsource.Release{ID: "source", Version: 3, Spec: config.EventSource{RelatedTenantID: "tenant", Storage: config.EventSourceStorageConfig{Kafka: config.KafkaStorageConfig{Topic: "original-topic"}}}},
	}
	calls := 0
	sources := NewPublishedSources(stub, func(_ context.Context, release eventsource.Release) (*Service, error) {
		if release.Version != 3 || release.Spec.Storage.Kafka.Topic != "original-topic" {
			t.Fatal("assembled current instead of original release")
		}
		return New(executorFunc(func(_ context.Context, actual pluginruntime.Request) (pluginruntime.Result, error) {
			calls++
			if !reflect.DeepEqual(request, actual) {
				t.Fatal("replay changed plugin input")
			}
			return pluginruntime.Result{RequestID: actual.RequestID}, nil
		}), nil, &resultFixture{}), nil
	})
	result, err := sources.Replay(t.Context(), request)
	if err != nil || result.RequestID != request.RequestID || stub.version != 3 || calls != 1 {
		t.Fatalf("replay: %+v %v", result, err)
	}
}

func TestReplayReleaseFailureAndTenantBoundary(t *testing.T) {
	failure := errors.New("historical release unavailable")
	for _, tc := range []struct {
		name, tenant string
		releaseErr   error
		want         error
	}{
		{"missing historical release", "tenant", failure, failure},
		{"another tenant", "other", nil, ErrSourceUnauthorized},
		{"missing tenant", "", nil, ErrSourceUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &releaseStub{release: eventsource.Release{Spec: config.EventSource{RelatedTenantID: "tenant"}}, releaseErr: tc.releaseErr}
			sources := NewPublishedSources(stub, func(context.Context, eventsource.Release) (*Service, error) {
				t.Fatal("unauthorized replay assembled")
				return nil, nil
			})
			_, err := sources.Replay(t.Context(), pluginruntime.Request{TenantID: tc.tenant, SourceID: "source", SourceVersion: 3})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestPublishedSharedSourceIntake(t *testing.T) {
	stub := &releaseStub{record: eventsource.Record{Published: 3}, release: eventsource.Release{Version: 3, Spec: config.EventSource{Enabled: true, EventSourceID: "shared", Collection: &config.SourceCollection{
		Plugin: "rest_api_push", Mode: "push", TenantBindings: []config.CollectionTenantBinding{
			{TenantID: "one", Enabled: true, Secret: "first", PluginSourceID: "kac-one"},
			{TenantID: "two", Enabled: true, Secret: "second", PluginSourceID: "kac-two", SourceName: "来源二", Configs: map[string]any{"host": "tenant-two-host"}},
		},
	}}}}
	service := &Service{}
	sources := NewPublishedSources(stub, func(context.Context, eventsource.Release) (*Service, error) { return service, nil })
	saved := 0
	intake := NewSourceIntake(sources, receiptFunc(func(_ context.Context, r pluginruntime.Request) (pluginruntime.Request, error) {
		saved++
		return restoreReceipt(r, "1794657600000-1")
	}))
	request, actualService, err := intake.Prepare(t.Context(), "shared", "second", []byte(`{"bk_tenant_id":"one"}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.TenantID != "two" || request.SourceID != "shared" || request.PluginSourceID != "kac-two" || request.SourceName != "来源二" || request.Configs["host"] != "tenant-two-host" || request.SourceVersion != 3 || actualService != service {
		t.Fatalf("wrong shared binding: %+v", request)
	}
	stub.release.Spec.Collection.TenantBindings[1].Enabled = false
	if err := intake.Authorize(t.Context(), "shared", "second"); !errors.Is(err, ErrSourceUnauthorized) {
		t.Fatalf("disabled: %v", err)
	}
	stub.release.Spec.Collection.TenantBindings[1].Enabled = true
	stub.release.Spec.Collection.TenantBindings[1].Secret = "rotated"
	if err := intake.Authorize(t.Context(), "shared", "second"); !errors.Is(err, ErrSourceUnauthorized) {
		t.Fatalf("old secret: %v", err)
	}
	if err := intake.Authorize(t.Context(), "shared", "rotated"); err != nil {
		t.Fatal(err)
	}
	if saved != 1 {
		t.Fatal("unauthorized input persisted")
	}
}

func TestReplaySharedSourceUsesHistoricalTenantMembership(t *testing.T) {
	stub := &releaseStub{release: eventsource.Release{Version: 3, Spec: config.EventSource{Collection: &config.SourceCollection{TenantBindings: []config.CollectionTenantBinding{
		{TenantID: "one", Enabled: true}, {TenantID: "two", Enabled: false},
	}}}}}
	calls := 0
	sources := NewPublishedSources(stub, func(context.Context, eventsource.Release) (*Service, error) {
		return New(executorFunc(func(_ context.Context, request pluginruntime.Request) (pluginruntime.Result, error) {
			calls++
			return pluginruntime.Result{RequestID: request.RequestID}, nil
		}), nil, &resultFixture{}), nil
	})
	for _, tenant := range []string{"one", "two", "other", ""} {
		_, err := sources.Replay(t.Context(), pluginruntime.Request{RequestID: "request", SourceID: "shared", SourceVersion: 3, TenantID: tenant})
		if tenant == "one" {
			if err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrSourceUnauthorized) {
			t.Fatalf("tenant %q: %v", tenant, err)
		}
	}
	if calls != 1 {
		t.Fatalf("executions=%d", calls)
	}
}
