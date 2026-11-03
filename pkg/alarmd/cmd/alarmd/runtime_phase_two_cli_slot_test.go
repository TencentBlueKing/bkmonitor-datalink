package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/go-redis/redis/v8"
)

type cliSlotSource struct{ document json.RawMessage }

func (s cliSlotSource) ActiveStrategyIDs(context.Context) ([]string, error) {
	return []string{"101"}, nil
}
func (s cliSlotSource) Strategies(context.Context, []string) ([]controlplane.SourceStrategy, error) {
	return []controlplane.SourceStrategy{{SourceID: "101", Document: s.document, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}}, nil
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
	source := cliSlotSource{document: json.RawMessage(`{"id":101,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"fixture-query","expression":"a","unit":"","query_configs":[{"data_source_label":"bk_monitor","data_type_label":"time_series","metric_field":"usage","alias":"a","agg_dimension":["host"],"agg_method":"MAX","agg_interval":60,"result_table_id":"system.cpu"}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gte","threshold":80}]]}]}],"detects":[{"level":1,"priority":1,"connector":"and","trigger_config":{"count":1,"check_window":1}}]}`)}
	reconciler, err := controlplane.NewSourceReconciler(repo, compiler, semantics)
	if err != nil {
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
	qg := manifest.QueryGroups[0].QueryGroup
	// Historical hold is a retained fact, never a default invented by CLI.
	key := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership") + ":{" + ownership.ControlHashTag(qg) + "}:" + productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule") + ":" + readhold.Namespace
	if err := client.Set(ctx, key, `{"hold_ms":0,"since_slot":1}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	return cfg, client, execution.SlotIdentity{QueryGroup: qg, EvaluationTime: 60}, repo
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

// Through the CLI as built for a deployment: slot.get on a historical Slot
// sets the catalog's latest publication beside the Slot's own and explains
// the difference, so the reader does not stop on it. The latest is the one
// the catalog names now, not the Slot's.
func TestSlotGetThroughTheBuiltCLICarriesTheLatestPublication(t *testing.T) {
	cfg, client, slot, repo := cliSlotFixture(t)
	ctx := context.Background()
	holdKey := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership") + ":{" + ownership.ControlHashTag(slot.QueryGroup) + "}:" + productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule") + ":" + readhold.Namespace
	if err := client.Set(ctx, holdKey, `{"hold_ms":60000,"since_slot":1}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	actual, err := newCLISlotResolver(cfg, client)(ctx, slot)
	if err != nil || actual.Contract.ReadHoldMillis != 60_000 {
		t.Fatalf("actual held contract=%+v err=%v", actual.Contract, err)
	}
	if err := client.Set(ctx, holdKey, `{"hold_ms":0,"since_slot":180,"previous_hold_ms":60000,"previous_since_slot":1}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	latest, err := repo.LoadLatestPublication(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Redis.Address = client.Options().Addr
	cfg.PhaseTwo.Worker.ID = "test-worker"
	cfg.PhaseTwo.Access.UQEndpoint = "http://127.0.0.1:1"
	cfg.CLI = config.CLIConfig{EnvironmentID: "test", EnvironmentName: "Test",
		PublicBaseURL: "https://ob.example/alarmd/", AdminKey: strings.Repeat("k", 40)}
	h, closeCLI, _ := buildPhaseTwoCLI(cfg, standInAPI(), repo, nil, nil, func() *observability.RuntimeConfigFacts { return nil },
		cliControlBinding{Incarnation: "test-process", PublicWindows: windowsStandIn})
	t.Cleanup(func() { _ = closeCLI() })
	session := openCLISession(t, h, cfg)
	revision := cliDiscover(t, h, session.AccessToken)
	out := cliCall(t, h, session.AccessToken, map[string]any{"channel_version": obchannel.Version, "mode": "invoke", "operation": "slot.get",
		"params": map[string]any{"query_group": string(slot.QueryGroup), "evaluation_time": slot.EvaluationTime}, "expected_catalog_revision": revision})
	if out.Error != nil {
		t.Fatalf("slot.get through the CLI failed: %+v", out.Error)
	}
	raw, _ := json.Marshal(out.Result)
	var view obchannel.SlotGetResult
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.LatestPublication == nil || view.LatestPublication.SnapshotRevision != latest.SnapshotRevision ||
		view.LatestPublication.PublicationEpoch != latest.PublicationEpoch || view.LatestPublication.SameAsSlot {
		t.Fatalf("latest publication = %+v, want the catalog's %+v beside an older Slot %s", view.LatestPublication, latest, view.Slot.SnapshotRevision)
	}
	if view.Slot.SnapshotRevision == latest.SnapshotRevision || view.SnapshotNote == "" {
		t.Fatalf("the historical Slot is not told apart from the latest: slot %s note %q", view.Slot.SnapshotRevision, view.SnapshotNote)
	}
	encoded, _ := json.Marshal(struct {
		Contract     execution.FrozenExecutionContractRef
		ObjectDigest execution.ObjectDigest
	}{actual.Contract, actual.ObjectDigest})
	digest := sha256.Sum256(encoded)
	if view.Slot.ContractDigest != hex.EncodeToString(digest[:]) {
		t.Fatalf("historical digest=%s; want %x", view.Slot.ContractDigest, digest)
	}
	if err := client.Del(ctx, holdKey).Err(); err != nil {
		t.Fatal(err)
	}
	// The fixture's Slot is older than a record's lifetime: with no record,
	// it may predate a lowering whose record has expired.
	if _, err := newCLISlotResolver(cfg, client)(ctx, slot); !errors.Is(err, obchannel.ErrHistoricalReadHoldUnknown) {
		t.Fatalf("absent hold of a Slot older than a record's lifetime became zero: %v", err)
	}
}

// A Query Group with no read hold record held none: a past Slot of it within
// a record's lifetime reads with a hold of zero, and says it was read from no
// record. One older than that, or a record that does not decode, is not
// known; a record that reaches back to the Slot says it was read from it.
func TestAPastSlotOfAGroupWithNoReadHoldRecordReadsWithNoHold(t *testing.T) {
	cfg, client, slot, _ := cliSlotFixture(t)
	ctx := context.Background()
	holdKey := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership") + ":{" + ownership.ControlHashTag(slot.QueryGroup) + "}:" + productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule") + ":" + readhold.Namespace
	at := time.Unix(int64(slot.EvaluationTime), 0)
	resolve := func(now time.Time) (obchannel.SlotPlan, error) {
		return newCLISlotResolverAt(cfg, client, func() time.Time { return now })(ctx, slot)
	}
	if err := client.Del(ctx, holdKey).Err(); err != nil {
		t.Fatal(err)
	}
	plan, err := resolve(at.Add(time.Hour))
	if err != nil || plan.Contract.ReadHoldMillis != 0 || plan.ReadHoldBasis != obchannel.ReadHoldNoRecord {
		t.Fatalf("a Slot of a group with no record: plan %+v basis %q err %v, want hold 0 read from no record", plan.Contract, plan.ReadHoldBasis, err)
	}
	if _, err := resolve(at.Add(readhold.RecordTTL + time.Hour)); !errors.Is(err, obchannel.ErrHistoricalReadHoldUnknown) {
		t.Fatalf("a Slot older than a record's lifetime with no record: %v, want unknown", err)
	}
	if err := client.Set(ctx, holdKey, `{"hold_ms":-1,"since_slot":1}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(at.Add(time.Hour)); !errors.Is(err, obchannel.ErrHistoricalReadHoldUnknown) {
		t.Fatalf("a record that does not decode: %v, want unknown", err)
	}
	if err := client.Set(ctx, holdKey, `{"hold_ms":60000,"since_slot":1}`, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	plan, err = resolve(at.Add(time.Hour))
	if err != nil || plan.Contract.ReadHoldMillis != 60_000 || plan.ReadHoldBasis != obchannel.ReadHoldFromRecord {
		t.Fatalf("a held record: plan %+v basis %q err %v, want 60 s read from the record", plan.Contract, plan.ReadHoldBasis, err)
	}
}

// A Slot its Query Group's Progress still carries is read with the hold its
// contract was frozen with, and says it was read from Progress.
func TestASlotProgressStillCarriesIsReadFromProgress(t *testing.T) {
	ctx := context.Background()
	f := startCutoverFixture(t, nil)
	_ = runOneSlotFull(t, f)
	last := f.progress(ctx).LastCompletion
	if last == nil {
		t.Fatal("the fixture completed no Slot")
	}
	plan, err := newCLISlotResolverAt(f.cfg, f.redisClient, f.now)(ctx, last.Contract.Slot)
	if err != nil || plan.ReadHoldBasis != obchannel.ReadHoldFromProgress || plan.Contract.ReadHoldMillis != last.Contract.ReadHoldMillis {
		t.Fatalf("plan %+v basis %q err %v, want the completed contract's hold %d read from Progress",
			plan.Contract, plan.ReadHoldBasis, err, last.Contract.ReadHoldMillis)
	}
}
