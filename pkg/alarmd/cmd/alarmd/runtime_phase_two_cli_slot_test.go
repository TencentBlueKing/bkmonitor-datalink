package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/go-redis/redis/v8"
)

type cliSlotSource struct{ document json.RawMessage }

func (s cliSlotSource) ActiveStrategyIDs(context.Context) ([]string, error) {
	return []string{"1001"}, nil
}
func (s cliSlotSource) Strategies(context.Context, []string) ([]controlplane.SourceStrategy, error) {
	return []controlplane.SourceStrategy{{SourceID: "1001", Document: s.document, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}}, nil
}

func cliSlotFixture(t *testing.T) (config.Config, *redis.Client, execution.SlotIdentity, *controlplane.RedisCatalogRepository) {
	t.Helper()
	_, client := startPhaseTwoRedis(t)
	cfg := config.Default()
	cfg.Redis.StatePrefix = "alarmd-slot-test"
	ctx := context.Background()
	repo, err := controlplane.NewRedisCatalogRepository(client, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog"), phaseTwoCatalogRetention(cfg))
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), cfg.CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	sm, err := state.RuntimeStateSemantics()
	if err != nil {
		t.Fatal(err)
	}
	semantics := strategy.StateSemantics{StateSchemaVersion: sm.StateSchemaVersion, CodecSemanticsVersion: sm.CodecSemanticsVersion, IdentitySchemaDigest: sm.IdentitySchemaDigest, SourceTimeSemanticsVersion: sm.SourceTimeSemanticsVersion, HistoryCellSemanticsVersion: sm.HistoryCellSemanticsVersion}
	accessBKData := true
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", controlplane.LegacyQueryRuntimeFacts{AccessBKData: &accessBKData, BKDataCMDBLevelTables: []string{}, SystemDiskFilter: controlplane.LegacyRuntimeFilterFact{FieldName: "device_type", Values: []string{"iso9660"}}, SystemNetworkFilter: controlplane.LegacyRuntimeFilterFact{FieldName: "device_name", Values: []string{"lo"}}})
	if err != nil {
		t.Fatal(err)
	}
	source := cliSlotSource{document: json.RawMessage(`{"id":1001,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"fixture-query","expression":"a","unit":"","query_configs":[{"data_source_label":"bk_monitor","data_type_label":"time_series","metric_field":"usage","alias":"a","agg_dimension":["host"],"agg_method":"MAX","agg_interval":60,"result_table_id":"system.cpu"}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":80}]]}]}],"detects":[{"level":1,"priority":1,"connector":"and","trigger_config":{"count":1,"check_window":1}}]}`)}
	reconciler, err := controlplane.NewSourceReconciler(repo, compiler, semantics)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reconciler.Refresh(ctx, source, planner); err != nil {
		t.Fatal(err)
	}
	result, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || result.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("publish: %+v %v", result, err)
	}
	activator, err := controlplane.NewInitialScheduleActivator(repo, compiler, semantics, func() time.Time { return time.Unix(60, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = activator.Ensure(ctx, result.Publication); err != nil {
		t.Fatal(err)
	}
	manifest, err := repo.LoadCatalogManifest(ctx, result.Publication.SnapshotRevision)
	if err != nil || len(manifest.QueryGroups) != 1 {
		t.Fatalf("manifest: %+v %v", manifest, err)
	}
	// Keep a genuinely historical Segment: activate a changed threshold at a
	// later boundary while retaining the old Slot's object and output context.
	source.document = json.RawMessage(strings.ReplaceAll(strings.ReplaceAll(string(source.document), `"threshold":80`, `"threshold":90`), `"update_time":1`, `"update_time":2`))
	if _, err = reconciler.Refresh(ctx, source, planner); err != nil {
		t.Fatal(err)
	}
	updated, err := reconciler.Refresh(ctx, source, planner)
	if err != nil || updated.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("updated publication: %+v %v", updated, err)
	}
	cutover, err := controlplane.NewScheduleActivationReconciler(repo, compiler, semantics, func() time.Time { return time.Unix(180, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cutover.Ensure(ctx, updated.Publication); err != nil {
		t.Fatal(err)
	}
	return cfg, client, execution.SlotIdentity{QueryGroup: manifest.QueryGroups[0].QueryGroup, EvaluationTime: 60}, repo
}

type cliSlotCommandLog struct {
	mu       sync.Mutex
	commands [][]interface{}
}

func (h *cliSlotCommandLog) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	h.mu.Lock()
	h.commands = append(h.commands, append([]interface{}{}, cmd.Args()...))
	h.mu.Unlock()
	return ctx, nil
}
func (*cliSlotCommandLog) AfterProcess(context.Context, redis.Cmder) error { return nil }
func (h *cliSlotCommandLog) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	for _, c := range cmds {
		h.BeforeProcess(ctx, c)
	}
	return ctx, nil
}
func (*cliSlotCommandLog) AfterProcessPipeline(context.Context, []redis.Cmder) error { return nil }
func (h *cliSlotCommandLog) clear()                                                  { h.mu.Lock(); h.commands = nil; h.mu.Unlock() }
func (h *cliSlotCommandLog) assertReadOnly(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, args := range h.commands {
		switch args[0] {
		case "getrange", "strlen", "exists":
		default:
			t.Fatalf("unexpected catalog command: %v", args)
		}
		for _, arg := range args {
			if s, ok := arg.(string); ok && (strings.Contains(s, ":manifest:") || strings.Contains(s, ":runtime:") || strings.Contains(s, ":progress:")) {
				t.Fatalf("unexpected read scope: %v", args)
			}
		}
	}
	if len(h.commands) > cliSlotReadCommands {
		t.Fatalf("commands=%d", len(h.commands))
	}
}

func TestCLISlotResolverReadsRetainedContractWithoutExecutionOrFleetFallback(t *testing.T) {
	cfg, client, slot, repo := cliSlotFixture(t)
	ctx := context.Background()
	latest, err := repo.LoadLatestPublication(ctx)
	if err != nil {
		t.Fatal(err)
	}
	log := &cliSlotCommandLog{}
	client.AddHook(log)
	resolve := newCLISlotResolver(cfg, client)
	plan, err := resolve(ctx, slot)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Contract.Slot != slot || plan.Contract.SnapshotRevision == latest.SnapshotRevision || plan.ObjectDigest == "" || len(plan.Prepared.Queries) != 1 || plan.Prepared.Header.Contract != plan.Contract {
		t.Fatalf("invalid plan: %+v", plan)
	}
	log.assertReadOnly(t)
	current, err := resolve(ctx, execution.SlotIdentity{QueryGroup: slot.QueryGroup, EvaluationTime: 240})
	if err != nil || current.Contract.SnapshotRevision == plan.Contract.SnapshotRevision || current.ObjectDigest == plan.ObjectDigest {
		t.Fatalf("historical/current contract not distinguished: %+v %v", current.Contract, err)
	}
	log.clear()
	// A poisoned latest manifest is irrelevant: the Segment is the history.
	prefix := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog")
	client.Set(ctx, prefix+":manifest:"+string(latest.SnapshotRevision), "not a manifest", time.Hour)
	log.clear()
	again, err := resolve(ctx, slot)
	if err != nil || !reflect.DeepEqual(plan, again) {
		t.Fatalf("retained result changed: %v", err)
	}
	log.assertReadOnly(t)
	key, err := repo.ObservationQueryGroupKey(plan.ObjectDigest)
	if err != nil {
		t.Fatal(err)
	}
	object, err := client.Get(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	client.Set(ctx, key, `{}`, time.Hour)
	log.clear()
	if _, err = resolve(ctx, slot); !errors.Is(err, obchannel.ErrHistoricalContractUnavailable) {
		t.Fatalf("corrupt object: %v", err)
	}
	log.assertReadOnly(t)
	client.Set(ctx, key, object, time.Hour)
	client.Del(ctx, key)
	log.clear()
	if _, err = resolve(ctx, slot); !errors.Is(err, obchannel.ErrHistoricalContractUnavailable) {
		t.Fatalf("missing object: %v", err)
	}
	log.assertReadOnly(t)
	client.Del(ctx, prefix+":schedule_timeline:"+string(slot.QueryGroup))
	log.clear()
	if _, err = resolve(ctx, slot); !errors.Is(err, obchannel.ErrHistoricalContractUnavailable) {
		t.Fatalf("reclaimed timeline: %v", err)
	}
	log.assertReadOnly(t)
}

func TestCLISlotResolverRefusesOversizeBeforeDecode(t *testing.T) {
	cfg, client, slot, _ := cliSlotFixture(t)
	ctx := context.Background()
	key := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog") + ":schedule_timeline:" + string(slot.QueryGroup)
	if err := client.Set(ctx, key, strings.Repeat("x", cliSlotDocumentBytes+1), time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := newCLISlotResolver(cfg, client)(ctx, slot); !errors.Is(err, obchannel.ErrSlotBudgetExceeded) {
		t.Fatalf("oversize: %v", err)
	}
}

func TestCLISlotReadBudgetAndDependencyErrors(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	if err := client.Set(ctx, "small", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	r := newCLISlotRedis(client)
	for i := 0; i < cliSlotReadCommands; i++ {
		if err := r.Get(ctx, "small").Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Get(ctx, "small").Err(); !errors.Is(err, obchannel.ErrSlotBudgetExceeded) {
		t.Fatalf("commands: %v", err)
	}
	client.Set(ctx, "full", strings.Repeat("x", cliSlotDocumentBytes), 0)
	r = newCLISlotRedis(client)
	for i := 0; i < cliSlotReadBytes/cliSlotDocumentBytes; i++ {
		if err := r.Get(ctx, "full").Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Get(ctx, "small").Err(); !errors.Is(err, obchannel.ErrSlotBudgetExceeded) {
		t.Fatalf("bytes: %v", err)
	}
	r = newCLISlotRedis(client)
	if err := r.Get(ctx, "absent").Err(); !errors.Is(err, redis.Nil) || r.failure != nil {
		t.Fatalf("missing: %v", err)
	}
	client.Set(ctx, "empty", "", 0)
	if v, err := r.Get(ctx, "empty").Result(); err != nil || v != "" {
		t.Fatalf("empty: %q %v", v, err)
	}
	// Even an accidental unsupported operation inside a catalog pipeline never
	// executes: the adapter only issues its declared bounded reads.
	if _, err := r.Pipelined(ctx, func(p redis.Pipeliner) error { p.Set(ctx, "must-not-write", "x", 0); return nil }); !errors.Is(err, obchannel.ErrSlotDependencyUnavailable) {
		t.Fatalf("write pipeline: %v", err)
	}
	if client.Exists(ctx, "must-not-write").Val() != 0 {
		t.Fatal("diagnostic pipeline wrote Redis")
	}
	client.Close()
	_, err := newCLISlotResolver(config.Default(), client)(ctx, execution.SlotIdentity{QueryGroup: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", EvaluationTime: 60})
	if err != obchannel.ErrSlotDependencyUnavailable {
		t.Fatalf("unsafe dependency error: %v", err)
	}
}

func TestCLISlotEvidenceMatchesSlotAndReadsSamplesWithNoSampler(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	cfg := config.Default()
	cfg.Redis.StatePrefix = "slot-evidence"
	qg := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	prefix := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "fleet")
	record := `{"slot_identity_known":true,"query_group_key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","evaluation_time":60,"process_id":"old-worker","facts":{"run_id":9}}`
	other := `{"slot_identity_known":true,"query_group_key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","evaluation_time":120}`
	unknown := `{"slot_identity_known":false,"query_group_key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","evaluation_time":60}`
	sample := `{"kind":"series_sample","query_group":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","slot":60,"process_id":"old-worker","execution_id":"historical-run","evaluation_provisional":true}`
	if err := client.RPush(ctx, prefix+":diag:v1:"+qg, record, other, unknown).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.RPush(ctx, prefix+":diag:series:v1:"+qg, sample, strings.Replace(sample, `"slot":60`, `"slot":120`, 1)).Err(); err != nil {
		t.Fatal(err)
	}
	read := newCLISlotEvidenceReader(cfg, client)
	out, err := read(ctx, execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(qg), EvaluationTime: 60})
	if err != nil || !out.Complete || len(out.Records) != 1 || len(out.Samples) != 1 || string(out.Records[0]) != record || string(out.Samples[0]) != sample {
		t.Fatalf("retained evidence: %+v %v", out, err)
	}
	client.LPush(ctx, prefix+":diag:v1:"+qg, "not json")
	out, err = read(ctx, execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(qg), EvaluationTime: 60})
	if err != nil || out.Complete || len(out.Records) != 1 {
		t.Fatalf("malformed row: %+v %v", out, err)
	}
	client.Del(ctx, prefix+":diag:v1:"+qg, prefix+":diag:series:v1:"+qg)
	for i := 0; i < cliSlotRecordLimit; i++ {
		client.RPush(ctx, prefix+":diag:v1:"+qg, fmt.Sprintf(`{"slot_identity_known":true,"query_group_key":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","evaluation_time":%d}`, 120+i))
	}
	client.RPush(ctx, prefix+":diag:v1:"+qg, record)
	out, err = read(ctx, execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(qg), EvaluationTime: 60})
	if err != nil || out.Complete || len(out.Records) != 0 {
		t.Fatalf("bounded search: %+v %v", out, err)
	}
	client.LPush(ctx, prefix+":diag:v1:"+qg, strings.Repeat("x", 4097))
	if _, err = read(ctx, execution.SlotIdentity{QueryGroup: execution.QueryGroupIdentity(qg), EvaluationTime: 60}); !errors.Is(err, obchannel.ErrSlotBudgetExceeded) {
		t.Fatalf("oversize list member: %v", err)
	}
}
