package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
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

	resolver := &recordingIdentityResolver{facts: map[string]controlplane.SourceIdentity{
		"2": {TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	}}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache", resolver)
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
	if resolver.calls != 2 { // one business fact per independent stability cycle
		t.Fatalf("identity resolver calls=%d, want 2", resolver.calls)
	}
}

func TestLegacyRedisStrategySourceDistinguishesEmptyAndIncompleteActiveSet(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache", &recordingIdentityResolver{})
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

func TestLegacyRedisStrategySourceIsolatesMissingExplicitIdentityFact(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	document := realThresholdDocuments(t)[0]
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache", &recordingIdentityResolver{
		errors: map[string]error{"2": &controlplane.SourceFactUnavailableError{BusinessID: "2", Reason: "SPACE_FACT_NOT_FOUND"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := source.Strategies(ctx, []string{"1001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(strategies) != 1 || strategies[0].SourceDisposition == nil || strategies[0].SourceDisposition.Reason != "SPACE_FACT_NOT_FOUND" {
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
	document := strings.Replace(string(realThresholdDocuments(t)[0]), `"bk_biz_id": 2`, `"bk_biz_id": -7`, 1)
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", document, 0).Err(); err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache", &recordingIdentityResolver{
		facts: map[string]controlplane.SourceIdentity{
			"-7": {TenantID: "tenant-a", BusinessID: "-7", SpaceScope: "custom__42"},
		},
	})
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

func TestLegacyRedisStrategySourceInvalidatesMixedRefreshAndInfrastructureFailure(t *testing.T) {
	client := newControlplaneRedis(t)
	ctx := context.Background()
	document := realThresholdDocuments(t)[0]
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache", &recordingIdentityResolver{
		errors: map[string]error{"2": errors.New("metadata backend unavailable")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Strategies(ctx, []string{"1001"}); err == nil || errors.Is(err, controlplane.ErrObservationUnstable) {
		t.Fatalf("infrastructure error=%v", err)
	}

	resolver := &recordingIdentityResolver{facts: map[string]controlplane.SourceIdentity{
		"2": {TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
	}}
	source, err = controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache", resolver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Strategies(ctx, []string{"1002"}); !errors.Is(err, controlplane.ErrObservationUnstable) {
		t.Fatalf("missing detail error=%v", err)
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
	source, err := controlplane.NewLegacyRedisStrategySource(client, "bkmonitor.cache", &recordingIdentityResolver{
		facts: map[string]controlplane.SourceIdentity{
			"2": {TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
		},
	})
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

type recordingIdentityResolver struct {
	facts  map[string]controlplane.SourceIdentity
	errors map[string]error
	calls  int
}

func (resolver *recordingIdentityResolver) ResolveSourceIdentity(_ context.Context, businessID string) (controlplane.SourceIdentity, error) {
	resolver.calls++
	if err := resolver.errors[businessID]; err != nil {
		return controlplane.SourceIdentity{}, err
	}
	identity, ok := resolver.facts[businessID]
	if !ok {
		return controlplane.SourceIdentity{}, &controlplane.SourceFactUnavailableError{BusinessID: businessID, Reason: "IDENTITY_FACT_NOT_FOUND"}
	}
	return identity, nil
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
	return documents
}
