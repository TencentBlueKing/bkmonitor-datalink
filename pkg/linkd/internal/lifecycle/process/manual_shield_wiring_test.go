// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type manualPolicyDocuments struct {
	release json.RawMessage
	record  json.RawMessage
	lists   int
}

func (d *manualPolicyDocuments) Get(_ context.Context, kind, _ string) (json.RawMessage, string, error) {
	if kind == "records" {
		return d.record, "1", nil
	}
	return d.release, "1", nil
}

func (*manualPolicyDocuments) Put(context.Context, string, string, string, json.RawMessage) error {
	return fmt.Errorf("unexpected policy write")
}

func (d *manualPolicyDocuments) List(context.Context, string, string, string, int) ([]json.RawMessage, error) {
	d.lists++
	return nil, nil
}

// 只替代锁和候选索引的 Redis I/O；构造器、策略目录、BindShield/CAS 走真实实现。
type manualRedisHook struct{ cleared bool }

func (*manualRedisHook) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("unexpected Redis dial")
	}
}

func (h *manualRedisHook) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		switch c := cmd.(type) {
		case *redis.BoolCmd:
			c.SetVal(true)
		case *redis.Cmd:
			args := c.Args()
			if c.Name() == "eval" && args[2] == 3 && args[7] == true {
				h.cleared = true
			}
			c.SetVal(int64(1))
		default:
			return fmt.Errorf("unexpected Redis command: %s", cmd.Name())
		}
		return nil
	}
}

func (h *manualRedisHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if err := h.ProcessHook(nil)(ctx, cmd); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestManualShieldBinderIncludesDependencyCatalog(t *testing.T) {
	now := time.Now().UTC()
	raw := json.RawMessage(fmt.Sprintf(`{"name":"manual","is_enable":true,"updated_at":"2026-01-01T00:00:00Z","space_code":"bkcc__2","model_id":"cw-Host","target_descriptor":{"schema_version":1,"model_id":"cw-Host","selectors":[{"type":"instances","instances":[{"model_id":"cw-Host","model_inst_id":"101","entity_uid":"cw-Host|101"}]}]},"activate_times":[{"period":"once","open_datetime_once":%q,"close_datetime_once":%q}],"policy":{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"source"}},"shield_type":"time_shield","reason":"manual"}`, now.Add(-time.Hour).Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)))
	compiled, err := policy.Compile(policy.Shield, raw)
	if err != nil {
		t.Fatal(err)
	}
	release := policy.Release{Scope: policy.Scope{TenantID: "tenant", Kind: policy.Shield}, ID: "manual", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
	encoded, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(policy.Record{Scope: release.Scope, ID: release.ID, Revision: 1, Published: 1, Spec: release.Spec, Compiled: release.Compiled})
	if err != nil {
		t.Fatal(err)
	}
	docs := &manualPolicyDocuments{release: encoded, record: record}
	policies := policy.NewService(docs)
	resources := &policyruntime.Runtime{Catalog: policy.NewCatalog(policies)}
	repo := memory.New()
	a := storetest.Alert("tenant", "alert", "opening", "fp", "warning")
	a.Admission = domain.AlertAdmission{AdmittedAt: &a.UpdateAt, Severity: a.Severity, CauseType: "source_event", CauseID: a.TriggerEventID}
	created, err := repo.CreateAlert(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: "unused.invalid:1", ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	hook := &manualRedisHook{}
	client.AddHook(hook)
	source := eventsource.Release{ID: a.EventSourceID, Version: 1}
	shared := &AlertProcessorResources{repository: &storeassembly.Runtime{Repository: repo, Backend: config.RepositoryTypeMySQL}, client: client, hooks: map[controlHookKey]*controlHooks{{source.ID, 1}: {release: source}}, latest: map[string]controlHookKey{}}
	sources := &batchSourceReader{}
	sources.version.Store(1)
	cfg := config.Config{Storage: &config.StorageConfig{Redis: &config.RedisConfig{}}, Lifecycle: &config.LifecycleConfig{}}
	binder := NewManualShieldBinder(cfg, sources, policies, resources, runtimeconfig.NewSeverity(config.DefaultSeverityConfig()), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	command := lifecycle.ShieldCommand{TenantID: a.BKTenantID, AlertID: a.AlertID, OperationID: "manual", OperatorID: "user", ExpectedRevision: created.Alert.Revision, Policy: domain.PolicyVersion{ID: release.ID, Version: release.Version, Digest: release.Compiled.Digest}, EffectiveAt: now}
	result, err := binder.BindShield(shared.context(t.Context()), command)
	if err != nil || !result.Alert.Shield.Active || !hook.cleared || docs.lists == 0 {
		t.Fatalf("manual binding/index cleanup failed: active=%v clear=%v lists=%d error=%v", result.Alert.Shield.Active, hook.cleared, docs.lists, err)
	}
	replay, err := binder.BindShield(shared.context(t.Context()), command)
	if err != nil || !replay.AlreadyApplied || replay.Alert.Revision != result.Alert.Revision {
		t.Fatal("manual retry changed binding", err)
	}
}
