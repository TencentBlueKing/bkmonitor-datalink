package controlplane_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

func TestCatalogSnapshotRevisionValidationAndFreeze(t *testing.T) {
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	var previous *contract.EvaluationPlanV2
	var stateHash string
	for _, raw := range []string{"", "7", "8", "null", `"7"`, "0", "-1", "1.5", "1e2", "9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			var source map[string]json.RawMessage
			if err := json.Unmarshal(documents[0], &source); err != nil {
				t.Fatal(err)
			}
			if raw != "" {
				source["strategy_revision"] = json.RawMessage(raw)
			}
			document, err := json.Marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
				Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: document, Identity: identity}, {SourceID: "1002", Document: documents[1], Identity: identity}},
				Planner:    &recordingPlanner{facts: queryFacts(t)},
			})
			if err != nil {
				t.Fatal(err)
			}
			valid := raw == "" || raw == "7" || raw == "8"
			wantPlans := 1
			if valid {
				wantPlans = 2
			}
			if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != wantPlans {
				t.Fatalf("bad strategy isolation: %+v", catalog)
			}
			if !valid {
				return
			}
			plan := catalog.QueryGroups[0].Plans[0].Plan
			bytes, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			var frozen contract.EvaluationPlanV2
			if err := json.Unmarshal(bytes, &frozen); err != nil {
				t.Fatal(err)
			}
			compiled := compileWithEvaluationCore(t, frozen, catalog.QueryGroups[0].QueryPlan.Normalization.DatasetContract)
			var revision int64
			if raw != "" {
				if err := json.Unmarshal([]byte(raw), &revision); err != nil {
					t.Fatal(err)
				}
			}
			if compiled.StrategyRef().SnapshotRevision != revision {
				t.Fatalf("revision lost: %+v", compiled.StrategyRef())
			}
			if stateHash != "" && stateHash != compiled.StateCompatibilityHash() {
				t.Fatal("snapshot revision changed runtime state identity")
			}
			stateHash = compiled.StateCompatibilityHash()
			if previous != nil && previous.StrategyRef.SnapshotRevision != 7 {
				t.Fatal("prior frozen revision was mutated")
			}
			if revision == 7 {
				previous = &frozen
			}
		})
	}
}
