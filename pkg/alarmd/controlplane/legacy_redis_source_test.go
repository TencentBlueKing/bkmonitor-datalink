package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestLegacyRedisStrategySourceReadsPythonStringContract(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1002, 1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"1001", "1002"} {
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(documents[index]), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}

	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	observation, err := controlplane.ObserveStable(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if observation.ObservationID == "" || len(observation.Strategies) != 2 {
		t.Fatalf("observation=%#v", observation)
	}
	if observation.Strategies[0].SourceID != "1001" || observation.Strategies[1].SourceID != "1002" {
		t.Fatalf("strategy order=%#v", observation.Strategies)
	}
}

func TestLegacyRedisStrategySourceReadsIdentityFromStrategyDocument(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	document := withWireIdentity(t, realThresholdDocuments(t)[0], "tenant-a", "bkcc__2")
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := source.Strategies(ctx, []string{"1001"})
	if err != nil {
		t.Fatal(err)
	}
	want := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	if len(strategies) != 1 || strategies[0].Identity != want || strategies[0].SourceDisposition != nil {
		t.Fatalf("strategies=%#v, want identity=%#v", strategies, want)
	}
}

func TestLegacyRedisStrategySourceIsolatesMissingWireIdentityFromHealthySibling(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	bad := withWireIdentity(t, documents[0], "tenant-a", "bkcc__2")
	var badObject map[string]any
	if err := json.Unmarshal(bad, &badObject); err != nil {
		t.Fatal(err)
	}
	delete(badObject, "space_uid")
	bad, _ = json.Marshal(badObject)
	healthy := withWireIdentity(t, documents[1], "tenant-a", "bkcc__2")
	for index, id := range []string{"1001", "1002"} {
		payload := [][]byte{bad, healthy}[index]
		if err := client.Set(ctx, "bkmonitor.cache.strategy_"+id, string(payload), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := source.Strategies(ctx, []string{"1001", "1002"})
	if err != nil {
		t.Fatal(err)
	}
	if len(strategies) != 2 || strategies[0].SourceDisposition == nil ||
		strategies[0].SourceDisposition.Disposition != controlplane.DispositionSourceIncomplete ||
		strategies[0].SourceDisposition.Reason != "SOURCE_IDENTITY_UNAVAILABLE" ||
		strategies[1].Identity.TenantID != "tenant-a" || strategies[1].Identity.SpaceScope != "bkcc__2" {
		t.Fatalf("strategies=%#v", strategies)
	}
	catalog, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: &recordingPlanner{facts: queryFacts(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 || catalog.QueryGroups[0].Plans[0].Identity.StrategyID != "1002" {
		t.Fatalf("catalog=%#v", catalog)
	}
}

func TestLegacyRedisStrategySourceRejectsInvalidWireIdentityShapes(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value any
	}{
		{name: "missing tenant", field: "bk_tenant_id", value: nil},
		{name: "empty tenant", field: "bk_tenant_id", value: ""},
		{name: "non-string tenant", field: "bk_tenant_id", value: 2},
		{name: "whitespace tenant", field: "bk_tenant_id", value: " tenant-a "},
		{name: "missing space", field: "space_uid", value: nil},
		{name: "empty space", field: "space_uid", value: ""},
		{name: "non-string space", field: "space_uid", value: false},
		{name: "whitespace space", field: "space_uid", value: " bkcc__2 "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newControlplaneRedis(t)
			ctx := context.Background()
			var value map[string]any
			if err := json.Unmarshal(realThresholdDocuments(t)[0], &value); err != nil {
				t.Fatal(err)
			}
			if test.value == nil {
				delete(value, test.field)
			} else {
				value[test.field] = test.value
			}
			payload, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", payload, 0).Err(); err != nil {
				t.Fatal(err)
			}
			source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
			if err != nil {
				t.Fatal(err)
			}
			strategies, err := source.Strategies(ctx, []string{"1001"})
			if err != nil {
				t.Fatal(err)
			}
			if len(strategies) != 1 || strategies[0].SourceDisposition == nil ||
				strategies[0].SourceDisposition.Disposition != controlplane.DispositionSourceIncomplete ||
				strategies[0].SourceDisposition.Reason != "SOURCE_IDENTITY_UNAVAILABLE" {
				t.Fatalf("strategies=%#v", strategies)
			}
		})
	}
}

func TestLegacyRedisStrategySourceDistinguishesEmptyAndIncompleteActiveSet(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.ActiveStrategyIDs(ctx); !errors.Is(err, controlplane.ErrLegacySourceIncomplete) {
		t.Fatalf("missing active set error=%v", err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	ids, err := source.ActiveStrategyIDs(ctx)
	if err != nil || ids == nil || len(ids) != 0 {
		t.Fatalf("empty active set=(%#v, %v), want non-nil empty", ids, err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `["1001"]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ActiveStrategyIDs(ctx); !errors.Is(err, controlplane.ErrLegacySourceIncomplete) {
		t.Fatalf("non-Python active set error=%v", err)
	}
}

func TestLegacyRedisStrategySourceIsolatesOldCacheWithoutIdentityFact(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	document := realThresholdDocuments(t)[0]
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	delete(value, "bk_tenant_id")
	delete(value, "space_uid")
	document, _ = json.Marshal(value)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := source.Strategies(ctx, []string{"1001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(strategies) != 1 || strategies[0].SourceDisposition == nil || strategies[0].SourceDisposition.Reason != "SOURCE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("strategies=%#v", strategies)
	}
	catalog, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: strategies, Planner: inertPlanner{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 0 || len(catalog.Dispositions) != 1 || catalog.Dispositions[0].Disposition != controlplane.DispositionSourceIncomplete {
		t.Fatalf("catalog=%#v", catalog)
	}
}

func TestLegacyRedisStrategySourceUsesExplicitNegativeBusinessIdentity(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	var value map[string]any
	if err := json.Unmarshal(realThresholdDocuments(t)[0], &value); err != nil {
		t.Fatal(err)
	}
	value["bk_biz_id"] = -7
	value["space_uid"] = "custom__42"
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	document := string(payload)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", document, 0).Err(); err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := source.Strategies(ctx, []string{"1001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(strategies) != 1 || strategies[0].Identity.SpaceScope != "custom__42" || strategies[0].Identity.BusinessID != "-7" {
		t.Fatalf("negative business identity=%#v", strategies)
	}
}

func TestLegacyRedisStrategySourceIsolatesMissingDetail(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	document := realThresholdDocuments(t)[0]
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := source.Strategies(ctx, []string{"1002"})
	if err != nil || len(strategies) != 1 || strategies[0].SourceDisposition == nil ||
		strategies[0].SourceDisposition.Disposition != controlplane.DispositionSourceIncomplete ||
		strategies[0].SourceDisposition.Reason != "SOURCE_OBJECT_INCOMPLETE" {
		t.Fatalf("missing detail strategy=(%#v, %v)", strategies, err)
	}
}

func TestLegacyRedisStrategySourceIsolatesStableInvalidDocument(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	documents := realThresholdDocuments(t)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", `{"id":1001,`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1002", string(documents[1]), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	observation, err := controlplane.ObserveStable(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Strategies: observation.Strategies, Planner: &recordingPlanner{facts: queryFacts(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 || catalog.QueryGroups[0].Plans[0].Identity.StrategyID != "1002" {
		t.Fatalf("invalid strategy blocked healthy sibling: %#v", catalog)
	}
	found := false
	for _, disposition := range catalog.Dispositions {
		if disposition.SourceID == "1001" && disposition.Disposition == controlplane.DispositionConfigRejected && disposition.Reason == "STRATEGY_DOCUMENT_INVALID" {
			found = true
		}
	}
	if !found {
		t.Fatalf("invalid strategy disposition=%#v", catalog.Dispositions)
	}
}

type inertPlanner struct{}

func (inertPlanner) CompilePrimaryQuery(context.Context, controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
	return execution.QueryPlanFacts{}, errors.New("planner must not be called for an isolated source")
}

func realThresholdDocuments(t *testing.T) []json.RawMessage {
	t.Helper()
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	for index := range documents {
		documents[index] = withWireIdentity(t, documents[index], "tenant-a", "bkcc__2")
	}
	return documents
}

func withWireIdentity(t *testing.T, document json.RawMessage, tenantID, spaceUID string) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(document, &value); err != nil {
		t.Fatal(err)
	}
	value["bk_tenant_id"] = tenantID
	value["space_uid"] = spaceUID
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
