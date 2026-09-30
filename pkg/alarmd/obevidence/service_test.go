package obevidence

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/go-redis/redis/v8"
)

type commandLog struct {
	mu       sync.Mutex
	commands []string
}

func (log *commandLog) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	log.add(cmd)
	return ctx, nil
}
func (*commandLog) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (log *commandLog) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, cmd := range cmds {
		log.add(cmd)
	}
	return ctx, nil
}
func (*commandLog) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }
func (log *commandLog) add(cmd redis.Cmder) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.commands = append(log.commands, cmd.Name())
}
func (log *commandLog) reset() { log.mu.Lock(); defer log.mu.Unlock(); log.commands = nil }
func (log *commandLog) assertBounded(t *testing.T) {
	t.Helper()
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.commands) > MaxCommands {
		t.Fatalf("commands: %v", log.commands)
	}
	for _, cmd := range log.commands {
		switch cmd {
		case "multi", "exec", "type", "pttl", "getrange":
		default:
			t.Fatalf("unbounded/unexpected command %s", cmd)
		}
	}
}

func redisForTest(t *testing.T) *redis.Client {
	t.Helper()
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	_, port, _ := net.SplitHostPort(address)
	cmd := exec.Command(executable, "--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no", "--dir", t.TempDir(), "--loglevel", "warning")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: address, DB: 5, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if client.Ping(context.Background()).Err() == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("redis failed to start")
	return nil
}
func binding(client redis.Cmdable, role, prefix string) RedisBinding {
	return RedisBinding{Client: client, Location: Location{Role: role, Address: "fixture", Mode: "standalone", DB: 5, Prefix: prefix}}
}
func encoded(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSourceEvidencePreservesValuesAndWithheldDocuments(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	log := &commandLog{}
	client.AddHook(log)
	service := New(Options{SourceStrategy: binding(client, "strategy_cache", "source")})
	// Missing tenant/space is a real reason for withholding the source. It
	// must still be readable without an accepted Plan or directory projection.
	document := `{"id":7,"bk_biz_id":2,"password":"ROOT_SECRET","unknown_extension":{"safe_looking":"EXT_SECRET"},"items":[{"id":1,"query_configs":[{"agg_interval":60,"metric_field":"cpu","agg_condition":[{"key":"bk_host_id","method":"eq","value":["42"]}],"headers":{"Authorization":"HEADER_SECRET"}}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":80,"token":"NESTED_SECRET"}]]}],"target_plan":{"model_id":"host","target_rule":"host_id","static_keys":["42"]},"no_data_config":{"is_enabled":true,"continuous":5}}],"detects":[{"level":1,"trigger_config":{"count":2,"check_window":3,"uptime":{"time_ranges":[{"start":"09:00","end":"18:00"}]}},"recovery_config":{"check_window":2}}]}`
	if err := client.Set(ctx, "source.strategy_7", document, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	log.reset()
	r := service.StrategyConfig(ctx, ConfigRequest{View: "source", StrategyID: "7"})
	if r.Status != "ok" || !r.Complete || r.TTLMS == nil || *r.TTLMS <= 0 {
		t.Fatalf("result: %+v", r)
	}
	text := encoded(t, r)
	for _, secret := range []string{"ROOT_SECRET", "EXT_SECRET", "HEADER_SECRET", "NESTED_SECRET"} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
	for _, fact := range []string{`"threshold":80`, `"value":["42"]`, `"count":2`, `"start":"09:00"`, `"continuous":5`} {
		if !strings.Contains(text, fact) {
			t.Fatalf("fact missing %s in %s", fact, text)
		}
	}
	if len(r.Omitted) < 4 {
		t.Fatalf("missing omissions: %+v", r.Omitted)
	}
	log.assertBounded(t)
	// A source key proves its own content, not membership in an active list.
	if client.Exists(ctx, "source.strategy_ids").Val() != 0 {
		t.Fatal("test unexpectedly has directory")
	}
}

func TestRedisTypesAbsenceLimitsAndFailure(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	log := &commandLog{}
	client.AddHook(log)
	service := New(Options{SourceStrategy: binding(client, "strategy_cache", "source")})
	// Exactly fitting is a complete reading, not an overflow false positive.
	const frame = `{"id":7,"name":""}`
	exact := `{"id":7,"name":"` + strings.Repeat("a", MaxDocumentBytes-len(frame)) + `"}`
	if err := client.Set(ctx, "source.strategy_7", exact, 0).Err(); err != nil {
		t.Fatal(err)
	}
	log.reset()
	if r := service.Store(ctx, StoreRequest{Family: FamilySourceStrategy, StrategyID: "7"}); r.Status != "ok" || r.Limits.Bytes != MaxDocumentBytes {
		t.Fatalf("exact budget: %+v", r.Limits)
	}
	log.assertBounded(t)
	cases := []struct{ name, value, status string }{
		{"missing", "", "missing"}, {"empty", "", "empty"}, {"corrupt", "{", "invalid_document"},
		{"large", strings.Repeat("x", MaxDocumentBytes+1000), "budget_exceeded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = client.Del(ctx, "source.strategy_7").Err()
			if tc.name != "missing" {
				if err := client.Set(ctx, "source.strategy_7", tc.value, 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			log.reset()
			r := service.Store(ctx, StoreRequest{Family: FamilySourceStrategy, StrategyID: "7"})
			if r.Status != tc.status {
				t.Fatalf("status %s: %+v", tc.status, r)
			}
			if r.Limits.Bytes > MaxDocumentBytes+1 {
				t.Fatalf("allocated unbounded reply: %+v", r.Limits)
			}
			if tc.name == "large" && (r.Value != nil || r.Complete) {
				t.Fatal("decoded truncated document")
			}
			if tc.name == "missing" && (*r.TTLMS != -2 || r.Type != "none") {
				t.Fatalf("absence metadata: %+v", r)
			}
			if tc.name == "empty" && (*r.TTLMS != -1 || r.Type != "string") {
				t.Fatalf("empty metadata: %+v", r)
			}
			log.assertBounded(t)
		})
	}
	_ = client.Del(ctx, "source.strategy_7").Err()
	_ = client.HSet(ctx, "source.strategy_7", "field", "value").Err()
	log.reset()
	if r := service.Store(ctx, StoreRequest{Family: FamilySourceStrategy, StrategyID: "7"}); r.Status != "wrong_type" || r.Type != "hash" || r.Complete {
		t.Fatalf("wrong type: %+v", r)
	}
	log.assertBounded(t)
	_ = client.Close()
	r := service.Store(ctx, StoreRequest{Family: FamilySourceStrategy, StrategyID: "7"})
	if r.Status != "dependency_unavailable" || r.Complete {
		t.Fatalf("closed connection: %+v", r)
	}
	if r := New(Options{}).Store(ctx, StoreRequest{Family: FamilySourceStrategy, StrategyID: "7"}); r.Status != "not_configured" || r.ReadAt != nil {
		t.Fatalf("unconfigured: %+v", r)
	}
}

func TestTargetGroupAndDynamicConfigUseConfiguredKeys(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	log := &commandLog{}
	client.AddHook(log)
	service := New(Options{TargetGroup: binding(client, "target_group", "cw:"), DynamicConfig: binding(client, "dynamic_config", "cw:")})
	_ = client.Set(ctx, "cw:dynamic_group:12", `{"model_id":"host","model_inst_ids":["42"],"member_list":[{"model_id":"host","model_inst_id":"42","bk_host_id":42,"password":"MEMBER_SECRET"}],"token":"GROUP_SECRET"}`, time.Minute).Err()
	log.reset()
	r := service.Store(ctx, StoreRequest{Family: FamilyTargetGroup, GroupID: "12"})
	if r.Status != "ok" {
		t.Fatalf("group: %+v", r)
	}
	text := encoded(t, r)
	if strings.Contains(text, "SECRET") || !strings.Contains(text, `"bk_host_id":42`) {
		t.Fatal(text)
	}
	log.assertBounded(t)
	prefix := "cw:"
	_ = client.Set(ctx, platformsettings.RevisionKey(prefix), "r5", 0).Err()
	for _, field := range platformsettings.Fields {
		value := `["ignore"]`
		if field == platformsettings.FieldIsAccessBKData {
			value = "false"
		}
		_ = client.Set(ctx, platformsettings.ConfigKey(prefix, platformsettings.Tenant, field.DBKey()), value, 0).Err()
	}
	log.reset()
	r = service.Store(ctx, StoreRequest{Family: FamilyDynamicConfig})
	if r.Status != "ok" || !r.Complete {
		t.Fatalf("settings: %+v", r)
	}
	log.assertBounded(t)
	text = encoded(t, r)
	if !strings.Contains(text, `"publication_present":true`) || !strings.Contains(text, `"value":false`) {
		t.Fatal(text)
	}
	field := platformsettings.FieldFileSystemTypeIgnore
	_ = client.Set(ctx, platformsettings.ConfigKey(prefix, platformsettings.Tenant, field.DBKey()), "null", 0).Err()
	r = service.Store(ctx, StoreRequest{Family: FamilyDynamicConfig, Fields: []platformsettings.Field{field}})
	entries := r.Value.(map[string]any)["fields"].(map[platformsettings.Field]Result)
	if entries[field].Status != "ok" || !strings.Contains(encoded(t, entries[field]), `"value":null`) {
		t.Fatal("explicit JSON null was erased")
	}
	_ = client.Set(ctx, platformsettings.RevisionKey(prefix), "", 0).Err()
	if r := service.Store(ctx, StoreRequest{Family: FamilyDynamicConfig}); r.Status != "partial" || r.Complete {
		t.Fatalf("empty revision: %+v", r)
	}
	_ = client.Set(ctx, platformsettings.RevisionKey(prefix), "r5", 0).Err()
	for _, field := range platformsettings.Fields {
		_ = client.Set(ctx, platformsettings.ConfigKey(prefix, platformsettings.Tenant, field.DBKey()), strings.Repeat("x", MaxBytes), 0).Err()
	}
	log.reset()
	r = service.Store(ctx, StoreRequest{Family: FamilyDynamicConfig})
	if r.Status != "partial" || r.Complete || r.Limits.Bytes > MaxBytes || r.Limits.DocumentReadLimitBytes != MaxBytes/(len(platformsettings.Fields)+1)-1 {
		t.Fatalf("multi-document budget: %+v", r)
	}
	log.assertBounded(t)
}

func TestPublishedObjectChecksDigestAndSelectsOneStrategy(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	repository, err := controlplane.NewRedisCatalogRepository(client, "catalog", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service := New(Options{Published: binding(client, "state_redis", "catalog"), Catalog: repository})
	object := controlplane.QueryGroupObject{ContractVersion: "alarmd-query-group-object-v1", Identity: "group", Plans: []controlplane.QueryGroupPlanObject{
		{Identity: execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "7"}, PlanID: "p7", StrategyIR: contract.StrategyIRV2{Levels: []contract.LevelIRV2{{Definition: contract.LevelDefinitionV2{LevelID: 1}, DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: "Threshold", Config: json.RawMessage(`{"groups":[{"conditions":[{"operator":"gte","threshold_decimal":"80","token":"SECRET"}]}]}`)}}}}}}},
		{Identity: execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "8"}, PlanID: "SIBLING_PRIVATE"},
	}}
	raw, err := contract.CanonicalJSONV2(object)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := contract.DeriveCanonicalDigestV2(object.ContractVersion, object)
	if err != nil {
		t.Fatal(err)
	}
	key, err := repository.ObservationQueryGroupKey(execution.ObjectDigest(digest))
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Set(ctx, key, raw, 0).Err()
	request := ConfigRequest{View: "published", StrategyID: "7", QueryGroup: "group", ObjectDigest: digest}
	r := service.StrategyConfig(ctx, request)
	if r.Status != "ok" {
		t.Fatalf("published: %+v", r)
	}
	text := encoded(t, r)
	if strings.Contains(text, "SECRET") || strings.Contains(text, "SIBLING_PRIVATE") || !strings.Contains(text, `"threshold_decimal":"80"`) {
		t.Fatal(text)
	}
	request.StrategyID = "9"
	if r := service.StrategyConfig(ctx, request); r.Status != "plan_not_in_object" {
		t.Fatalf("wrong Plan: %+v", r)
	}
	request.StrategyID = "7"
	_ = client.Set(ctx, key, bytes.Replace(raw, []byte(`"80"`), []byte(`"81"`), 1), 0).Err()
	if r := service.StrategyConfig(ctx, request); r.Status != "object_corrupt" || r.Value != nil {
		t.Fatalf("digest mismatch: %+v", r)
	}
}

type noSlots struct{}

func (noSlots) NextSlotAfter(context.Context, execution.QueryGroupIdentity, execution.EvaluationTime) (execution.EvaluationTime, error) {
	return 0, nil
}
func TestProgressUsesProductionKeyAndDecoder(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	owner, err := ownership.NewRedisStoreWithClient(client, "owner")
	if err != nil {
		t.Fatal(err)
	}
	store, err := progress.NewStore(progress.StoreOptions{Prefix: "runtime", Control: owner, Slots: noSlots{}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.ObservationKey("group")
	if err != nil {
		t.Fatal(err)
	}
	service := New(Options{QueryProgress: binding(client, "state_redis", "owner"), Progress: store})
	value := execution.ScheduleProgress{Identity: execution.ProgressIdentity{QueryGroup: "group"}, NextSlot: 120}
	raw, err := json.Marshal(map[string]any{"schema": "alarmd-schedule-progress-v2", "progress": value})
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Set(ctx, key, raw, 0).Err()
	r := service.Store(ctx, StoreRequest{Family: FamilyQueryProgress, QueryGroup: "group"})
	if r.Status != "ok" {
		t.Fatalf("progress: %+v", r)
	}
	if !strings.Contains(encoded(t, r), `"NextSlot":120`) {
		t.Fatal(encoded(t, r))
	}
	_ = client.Set(ctx, key, `{"schema":"unknown","progress":{}}`, 0).Err()
	if r := service.Store(ctx, StoreRequest{Family: FamilyQueryProgress, QueryGroup: "group"}); r.Status != "invalid_document" {
		t.Fatalf("invalid progress: %+v", r)
	}
}

func TestInvalidSelectorsNeverReadRedis(t *testing.T) {
	service := New(Options{})
	for _, request := range []StoreRequest{{Family: "arbitrary"}, {Family: FamilySourceStrategy, StrategyID: "../7"}, {Family: FamilySourceStrategy, StrategyID: "007"}, {Family: FamilyDynamicConfig, Fields: []platformsettings.Field{"password"}}, {Family: FamilyTargetGroup, GroupID: "12", StrategyID: "7"}} {
		if r := service.Store(context.Background(), request); r.Status != "invalid_input" {
			t.Fatalf("invalid selector: %+v", r)
		}
	}
}

// The pool record reads back as the owner wrote it; a record under another
// Query Group's key, one that does not decode, and a store with no prefix
// are each named rather than read as a record or as absent.
func TestQueryCooldownRecordIsReadAndCheckedAgainstItsKey(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	service := New(Options{QueryCooldown: binding(client, "runtime", "rt:phase-two:cooldown")})
	key := scheduler.QueryCooldownKey("rt:phase-two:cooldown", "group")
	read := func() Result {
		return service.Store(ctx, StoreRequest{Family: FamilyQueryCooldown, QueryGroup: "group"})
	}
	if r := read(); r.Status != "missing" || r.Location.Key != key {
		t.Fatalf("no record: %+v", r)
	}
	entered := time.Unix(1_790_000_000, 0).UTC()
	_ = client.Set(ctx, key, encoded(t, scheduler.QueryCooldownRecord{QueryGroup: "group", OwnerEpoch: 3, EnteredAt: entered, Failures: 32}), time.Hour).Err()
	r := read()
	record, ok := r.Value.(scheduler.QueryCooldownRecord)
	if r.Status != "ok" || !r.Complete || !ok || record.OwnerEpoch != 3 || record.Failures != 32 || !record.EnteredAt.Equal(entered) || r.TTLMS == nil || *r.TTLMS <= 0 {
		t.Fatalf("record: %+v", r)
	}
	_ = client.Set(ctx, key, encoded(t, scheduler.QueryCooldownRecord{QueryGroup: "other", OwnerEpoch: 3}), 0).Err()
	if r := read(); r.Status != "identity_mismatch" || r.Complete || r.Value != nil {
		t.Fatalf("another Query Group's record: %+v", r)
	}
	_ = client.Set(ctx, key, `{"query_group":`, 0).Err()
	if r := read(); r.Status != "invalid_document" || r.Complete || r.Value != nil {
		t.Fatalf("undecodable record: %+v", r)
	}
	for _, request := range []StoreRequest{{Family: FamilyQueryCooldown}, {Family: FamilyQueryCooldown, QueryGroup: "group", StrategyID: "7"},
		{Family: FamilyQueryCooldown, QueryGroup: "group", GroupID: "1"}, {Family: FamilyQueryCooldown, QueryGroup: "bad group"}} {
		if r := service.Store(ctx, request); r.Status != "invalid_input" {
			t.Fatalf("invalid selector %+v: %+v", request, r)
		}
	}
	for _, missing := range []Options{{}, {QueryCooldown: binding(client, "runtime", "")}} {
		if r := New(missing).Store(ctx, StoreRequest{Family: FamilyQueryCooldown, QueryGroup: "group"}); r.Status != "not_configured" {
			t.Fatalf("unconfigured: %+v", r)
		}
	}
}
