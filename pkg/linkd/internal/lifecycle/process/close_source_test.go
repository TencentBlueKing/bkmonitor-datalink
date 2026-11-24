// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/runtimeconfig"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
	"linkd/internal/suppressioncleanup"
)

type closeSourceCleanup struct{}

func (closeSourceCleanup) Run(ctx context.Context, _ suppressioncleanup.Cause, run func(context.Context) (suppressioncleanup.Result, error)) error {
	_, err := run(ctx)
	return err
}

type closeSourceFixture struct {
	record   eventsource.Record
	tenant   string
	missing  bool
	versions []int64
}

func (s *closeSourceFixture) Get(context.Context, string) (eventsource.Record, error) {
	return s.record, nil
}

func (s *closeSourceFixture) GetRelease(_ context.Context, id string, version int64) (eventsource.Release, error) {
	s.versions = append(s.versions, version)
	if s.missing {
		return eventsource.Release{}, eventsource.ErrNotFound
	}
	return eventsource.Release{ID: id, Version: version, Spec: config.EventSource{RelatedTenantID: s.tenant}}, nil
}

func TestCloseAlertSourceAvailability(t *testing.T) {
	for _, tc := range []struct {
		name        string
		published   int64
		deleted     bool
		tenant      string
		missing     bool
		wantVersion int64
		wantCode    string
		multiTenant bool
	}{
		{name: "published", published: 3, tenant: "tenant-a", wantVersion: 3},
		{name: "unpublished", tenant: "tenant-a", wantVersion: 1},
		{name: "deleted", published: 3, deleted: true, tenant: "tenant-a", wantVersion: 1},
		{name: "deleted_unpublished", deleted: true, tenant: "tenant-a", wantVersion: 1},
		{name: "multi_tenant_mismatch", tenant: "other", multiTenant: true, wantVersion: 1, wantCode: "tenant_mismatch"},
		{name: "single_tenant_mismatch_allowed", tenant: "other", wantVersion: 1},
		{name: "multi_tenant_match", tenant: "tenant-a", multiTenant: true, wantVersion: 1},
		{name: "multi_tenant_unbound", multiTenant: true, wantVersion: 1},
		{name: "missing_historical_release", missing: true, wantVersion: 1, wantCode: "not_found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := memory.New()
			command := validCloseCommand()
			a := storetest.Alert(command.BKTenantID, command.AlertID, "opening", "fp", "warning")
			if _, err := repo.CreateAlert(t.Context(), a); err != nil {
				t.Fatal(err)
			}
			client := redis.NewClient(&redis.Options{Addr: "unused.invalid:1", ContextTimeoutEnabled: true})
			t.Cleanup(func() { _ = client.Close() })
			client.AddHook(&manualRedisHook{})
			shared := &AlertProcessorResources{repository: &storeassembly.Runtime{Repository: repo, Backend: config.RepositoryTypeMySQL}, client: client, cleanup: closeSourceCleanup{}, hooks: map[controlHookKey]*controlHooks{}, latest: map[string]controlHookKey{}}
			sources := &closeSourceFixture{record: eventsource.Record{ID: a.EventSourceID, Published: tc.published, Deleted: tc.deleted}, tenant: tc.tenant, missing: tc.missing}
			cfg := config.Config{Storage: &config.StorageConfig{Redis: &config.RedisConfig{}}, Lifecycle: &config.LifecycleConfig{}}
			cfg.Blueking.EnableMultiTenantMode = tc.multiTenant
			closer := NewAlertCloser(cfg, sources, runtimeconfig.NewSeverity(config.DefaultSeverityConfig()), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
			closer.UseResources(shared)
			result, err := closer.CloseAlert(t.Context(), command)
			if tc.wantCode != "" {
				var classified *CloseError
				if !errors.As(err, &classified) || classified.Code != tc.wantCode {
					t.Fatalf("error=%v, want %s", err, tc.wantCode)
				}
				stored, readErr := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
				if readErr != nil || stored.Alert.Status != domain.AlertStatusActive {
					t.Fatalf("failed close changed alert: %+v %v", stored, readErr)
				}
			} else if err != nil || result.Alert.Status != domain.AlertStatusClosed {
				t.Fatalf("close status=%s error=%v cause=%v", result.Alert.Status, err, errors.Unwrap(err))
			}
			if len(sources.versions) != 1 || sources.versions[0] != tc.wantVersion {
				t.Fatalf("release versions=%v, want %d", sources.versions, tc.wantVersion)
			}
		})
	}
}

func TestCloseBatchUnpublishedSourceUsesEachHistoricalVersion(t *testing.T) {
	fixture, repo, _, commands, held := closeBatchFixture(t, 1)
	a := storetest.Alert("tenant", "historical-v2", "opening-v2", "fp-v2", "warning")
	a.EventSourceVersion = 2
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	second := commands[0]
	second.AlertID = a.AlertID
	commands = append(commands, second)
	client := redis.NewClient(&redis.Options{Addr: "unused.invalid:1", ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	client.AddHook(&manualRedisHook{})
	shared := fixture.resources
	shared.client = client
	shared.cleanup = closeSourceCleanup{}
	shared.hooks = map[controlHookKey]*controlHooks{}
	shared.latest = map[string]controlHookKey{}
	sources := &closeSourceFixture{record: eventsource.Record{ID: a.EventSourceID}, tenant: "tenant"}
	cfg := config.Config{Storage: &config.StorageConfig{Redis: &config.RedisConfig{}}, Lifecycle: &config.LifecycleConfig{}}
	closer := NewAlertCloser(cfg, sources, runtimeconfig.NewSeverity(config.DefaultSeverityConfig()), fixture.logger, nil, nil)
	closer.UseResources(shared)
	closer.batchLocker = fixture.batchLocker
	for _, result := range closer.CloseAlerts(t.Context(), commands) {
		if result.Err != nil || result.Alert.Status != domain.AlertStatusClosed {
			t.Fatalf("close status=%s error=%v", result.Alert.Status, result.Err)
		}
	}
	if len(sources.versions) != 2 || sources.versions[0]+sources.versions[1] != 3 {
		t.Fatalf("historical releases=%v", sources.versions)
	}
	if held.Load() != 0 {
		t.Fatal("close leaked leases")
	}
}
