package obevidence

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// noDataMemoryFixture publishes one Query Group object naming one Plan and
// returns a function that writes that Plan's no-data memory through the
// store's own write path, and the request that reads it back.
func noDataMemoryFixture(t *testing.T, client *redis.Client) (func(groups []string), *Service, StoreRequest) {
	t.Helper()
	ctx := context.Background()
	const prefix = "alarmd"
	repository, err := controlplane.NewRedisCatalogRepository(client, prefix, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := state.NewRedisBackendWithClient("fixture", client)
	if err != nil {
		t.Fatal(err)
	}
	router, err := state.NewFixedRouter("primary", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.NewExecutionStore(state.ExecutionStoreOptions{Prefix: prefix, Router: router, MaxValueBytes: 1 << 20,
		MaxItemsPerCall: 64, MinTTL: time.Minute, MaxTTL: 720 * time.Hour, RestartMargin: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	plan := execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "121"}
	object := controlplane.QueryGroupObject{ContractVersion: "alarmd-query-group-object-v1", Identity: "group", Plans: []controlplane.QueryGroupPlanObject{
		{Identity: plan, PlanID: "p121", StateGeneration: "generation"},
	}}
	raw, err := contract.CanonicalJSONV2(object)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := contract.DeriveCanonicalDigestV2(object.ContractVersion, object)
	if err != nil {
		t.Fatal(err)
	}
	objectKey, err := repository.ObservationQueryGroupKey(execution.ObjectDigest(digest))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, objectKey, raw, 0).Err(); err != nil {
		t.Fatal(err)
	}
	write := func(groups []string) {
		t.Helper()
		memory := make([]execution.NoDataGroupMemory, 0, len(groups))
		for _, group := range groups {
			memory = append(memory, execution.NoDataGroupMemory{GroupKey: group, LastSeen: 1000})
		}
		version := execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 60, SlotDigest: "slot"}
		ref := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: 60}, SnapshotRevision: "snapshot",
			QueryRevision: "query", ScheduleRevision: "schedule", ScheduleSegmentStart: 60, DuePlanSetDigest: "plans"}
		mutation, err := execution.BuildPlanNoDataMutation(execution.PlanNoDataMemoryUpdate{
			DerivedFrom: execution.NoDataRepresentationNone, Identity: execution.PlanNoDataIdentity{Plan: plan, StateGeneration: "generation"},
			ApplyVersion: version, ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1", PresentAsOf: 1000, Memory: memory,
		})
		if err != nil {
			t.Fatal(err)
		}
		retention := execution.GenerationRetention{ByPlan: map[execution.PlanIdentity][]execution.StateRetentionRequirement{
			plan: {{LevelID: 1, RetentionPoints: 5, EvaluationInterval: time.Minute}},
		}}
		applied, err := store.ApplyNoData(ctx, execution.NoDataApplyRequest{Contract: ref, Items: []execution.PlanNoDataMutation{mutation}, Retention: retention})
		if err != nil || applied.Items[0].Status != execution.NoDataApplied {
			t.Fatalf("ApplyNoData() = (%+v, %v)", applied, err)
		}
	}
	service := New(Options{Published: binding(client, "runtime", prefix), Catalog: repository})
	return write, service, StoreRequest{Family: FamilyNoDataMemory, QueryGroup: "group", ObjectDigest: digest, StrategyID: "121"}
}

func readNoDataMemory(t *testing.T, service *Service, request StoreRequest, log *commandLog) PlanRecord {
	t.Helper()
	log.reset()
	r := service.Store(context.Background(), request)
	log.assertBounded(t)
	plans, _ := r.Value.([]PlanRecords)
	if r.Status != "ok" || !r.Complete || len(plans) != 1 || len(plans[0].Records) != 1 {
		t.Fatalf("no_data_memory = %+v, want one record read whole", r)
	}
	if r.Limits.Commands > MaxCommands || r.Limits.Bytes > MaxBytes {
		t.Fatalf("limits = %+v, want within the request budget", r.Limits)
	}
	return plans[0].Records[0]
}

// An operator reading a Plan's no-data memory sees which groups it tracks,
// not only how many: the first fifty group keys in byte order and how many
// there are in all. The count beside them is unchanged.
func TestANoDataMemoryListsItsFirstGroupKeysAndHowManyThereAre(t *testing.T) {
	client := redisForTest(t)
	write, service, request := noDataMemoryFixture(t, client)
	log := &commandLog{}
	client.AddHook(log)

	// 120 groups, written in an order that is not their byte order.
	var groups []string
	for i := 120; i >= 1; i-- {
		groups = append(groups, fmt.Sprintf("serverIp=192.0.2.%d", i))
	}
	write(groups)
	record := readNoDataMemory(t, service, request, log)
	if record.Fields == nil || *record.Fields != 121 {
		t.Fatalf("fields = %v, want the 120 groups and the header", record.Fields)
	}
	listing := record.GroupKeys
	if listing == nil {
		t.Fatalf("record = %+v, want its group keys listed", record)
	}
	// Byte order, written out by hand: "1", "10", "100".."109", "11", "110"..
	want := []string{"1", "10", "100", "101", "102", "103", "104", "105", "106", "107", "108", "109",
		"11", "110", "111", "112", "113", "114", "115", "116", "117", "118", "119", "12", "120",
		"13", "14", "15", "16", "17", "18", "19", "2", "20", "21", "22", "23", "24", "25", "26", "27", "28", "29",
		"3", "30", "31", "32", "33", "34", "35"}
	for i := range want {
		want[i] = "serverIp=192.0.2." + want[i]
	}
	if strings.Join(listing.Keys, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v,\nwant %v", listing.Keys, want)
	}
	if listing.Total != 120 || listing.Limit != 50 || listing.Scan != GroupKeyScanComplete || listing.CutBy != GroupKeysCutByCount ||
		listing.ScannedFields != 121 || listing.UnknownFields != 0 {
		t.Fatalf("listing = %+v, want the first 50 of 120 after a complete scan, cut by count", listing)
	}

	// Fewer groups than the limit: every one of them, nothing cut. A fresh
	// record, as a new generation's first write leaves.
	if err := client.Del(context.Background(), record.Key).Err(); err != nil {
		t.Fatal(err)
	}
	write([]string{"serverIp=192.0.2.9", "serverIp=192.0.2.10", "serverIp=192.0.2.8"})
	record = readNoDataMemory(t, service, request, log)
	listing = record.GroupKeys
	if record.Fields == nil || *record.Fields != 4 || listing == nil ||
		strings.Join(listing.Keys, ",") != "serverIp=192.0.2.10,serverIp=192.0.2.8,serverIp=192.0.2.9" ||
		listing.Total != 3 || listing.CutBy != "" || listing.Scan != GroupKeyScanComplete {
		t.Fatalf("record = %+v listing %+v, want all three keys of three", record, listing)
	}

	// A record that is not a hash has no group keys to list.
	if err := client.Set(context.Background(), record.Key, "not a hash", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	log.reset()
	r := service.Store(context.Background(), request)
	if plans, _ := r.Value.([]PlanRecords); len(plans) != 1 || plans[0].Records[0].Status != "wrong_type" || plans[0].Records[0].GroupKeys != nil {
		t.Fatalf("a string under the key = %+v, want wrong_type and no listing", r)
	}
}

// The listing has its own byte bound, and the scan stays within what is left
// of the request's budget: a scan that cannot finish says where it stopped,
// and its keys are only the first of what it saw.
func TestGroupKeyScanSaysWhichBoundStoppedIt(t *testing.T) {
	client := redisForTest(t)
	ctx := context.Background()
	b := binding(client, "runtime", "alarmd")

	long := "hash"
	var values []any
	for i := 0; i < MaxGroupKeys; i++ {
		values = append(values, fmt.Sprintf("g:%02d%s", i, strings.Repeat("x", 1<<10)), "p")
	}
	values = append(values, state.NoDataHeaderField, "{}", "stray", "p")
	if err := client.HSet(ctx, long, values...).Err(); err != nil {
		t.Fatal(err)
	}
	listing, commands, _ := scanGroupKeys(ctx, b, long, MaxGroupKeys, MaxCommands, MaxBytes)
	// Each key is 1026 bytes: fifteen fit in sixteen KiB, the sixteenth does not.
	if listing.Scan != GroupKeyScanComplete || len(listing.Keys) != 15 || listing.CutBy != GroupKeysCutByBytes ||
		!strings.HasPrefix(listing.Keys[0], "00") || !strings.HasPrefix(listing.Keys[14], "14") || listing.UnknownFields != 1 || commands < 1 {
		t.Fatalf("listing = %+v (%d commands), want the first 15 keys cut by bytes and the stray field counted", listing, commands)
	}

	wide := "wide"
	values = values[:0]
	for i := 0; i < 3*groupKeyScanCount; i++ {
		values = append(values, fmt.Sprintf("g:serverIp=192.0.2.%d/%d", i%250, i), "p")
	}
	if err := client.HSet(ctx, wide, values...).Err(); err != nil {
		t.Fatal(err)
	}
	listing, commands, _ = scanGroupKeys(ctx, b, wide, 3*groupKeyScanCount, 1, MaxBytes)
	if listing.Scan != GroupKeyScanCommands || commands != 1 || listing.ScannedFields >= 3*groupKeyScanCount || listing.Total != 3*groupKeyScanCount {
		t.Fatalf("listing = %+v (%d commands), want the scan stopped by the one command left", listing, commands)
	}
	listing, _, scanned := scanGroupKeys(ctx, b, wide, 3*groupKeyScanCount, MaxCommands, 1)
	if listing.Scan != GroupKeyScanBytes || listing.ScannedFields >= 3*groupKeyScanCount || scanned < 1 {
		t.Fatalf("listing = %+v, want the scan stopped by the byte left", listing)
	}
	listing, _, _ = scanGroupKeys(ctx, b, wide, 3*groupKeyScanCount, MaxCommands, MaxBytes)
	if listing.Scan != GroupKeyScanComplete || len(listing.Keys) != MaxGroupKeys || listing.CutBy != GroupKeysCutByCount ||
		listing.Keys[0] != "serverIp=192.0.2.0/0" {
		t.Fatalf("listing = %+v, want the first 50 of 3000 after a complete scan", listing)
	}
}
