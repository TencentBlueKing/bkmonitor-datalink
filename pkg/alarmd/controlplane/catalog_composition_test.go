package controlplane_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// revisionedCatalog is the two fixture strategies, each with a Python
// strategy_revision so a forced choice can be honoured, built under the given
// output protocol. Revisions are what make the frozen word differ from what
// the automatic rule would say: under auto a revisioned strategy publishes the
// trigger event, so a forced native or legacy choice freezes a word the rule
// would not have produced, and a read that reports the frozen word cannot be
// mistaken for one that re-derives it.
func revisionedCatalog(t *testing.T, outputProtocol string) controlplane.Catalog {
	t.Helper()
	documents := realThresholdDocuments(t)
	revisioned := func(document json.RawMessage, business int, space string) []byte {
		var decoded map[string]any
		if err := json.Unmarshal(withWireIdentity(t, document, "tenant-a", space), &decoded); err != nil {
			t.Fatal(err)
		}
		decoded["bk_biz_id"] = float64(business)
		decoded["strategy_revision"] = float64(7)
		payload, err := json.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	planner := &businessPlanner{facts: map[string]execution.QueryPlanFacts{"2": queryFactsFor(t, "2", "bkcc__2"), "3": queryFactsFor(t, "3", "bkcc__3")}}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{
		{SourceID: "1001", Document: revisioned(documents[0], 2, "bkcc__2"), Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
		{SourceID: "1002", Document: revisioned(documents[1], 3, "bkcc__3"), Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "3", SpaceScope: "bkcc__3"}},
	}, Planner: planner, OutputProtocol: outputProtocol})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 2 {
		t.Fatalf("expected two Query Groups under %q, got %d with dispositions %+v", outputProtocol, len(catalog.QueryGroups), catalog.Dispositions)
	}
	return catalog
}

// The composition counts the Plans that carry an authoritative strategy
// revision beside every Plan, from the frozen Plans themselves: a source
// whose documents publish no revision composes to zero revisioned, and one
// whose documents do composes to all of them. It is the number that decides
// whether the standard output path is reachable under the automatic choice,
// and it was established on a live deployment by a Kafka read instead.
func TestTheCompositionCountsRevisionedPlansFromTheFrozenPlans(t *testing.T) {
	without := controlplane.ComposeCatalog(objectCatalogTwoGroups(t, 80))
	if without.PlansTotal != 2 || without.RevisionedPlans != 0 {
		t.Fatalf("unrevisioned source = %d plans, %d revisioned; want 2 and 0", without.PlansTotal, without.RevisionedPlans)
	}
	with := controlplane.ComposeCatalog(revisionedCatalog(t, ""))
	if with.PlansTotal != 2 || with.RevisionedPlans != 2 {
		t.Fatalf("revisioned source = %d plans, %d revisioned; want 2 and 2", with.PlansTotal, with.RevisionedPlans)
	}
}

// The composition counts every Plan by the wire format its events go out
// as, resolved the way the sink resolves it, every format present at zero:
// an unrevisioned source composes to all Python-compatible and none standard,
// and a revisioned one to the reverse. The number that answers "how many
// strategies publish the standard raw event" is this one; before it the
// answer was a Kafka read, and a log search for the word found no line.
func TestTheCompositionCountsPlansByTheWireFormatTheSinkResolves(t *testing.T) {
	without := controlplane.ComposeCatalog(objectCatalogTwoGroups(t, 80))
	if got := without.PlansByWireFormat; got[contract.WireFormatPythonCompatible] != 2 || got[contract.WireFormatStandardRawEvent] != 0 ||
		got[observability.WireFormatOther] != 0 || len(got) != len(observability.WireFormats) {
		t.Fatalf("unrevisioned source by wire format = %v, want 2 python_compatible and every other format at zero", got)
	}
	with := controlplane.ComposeCatalog(revisionedCatalog(t, ""))
	if got := with.PlansByWireFormat; got[contract.WireFormatStandardRawEvent] != 2 || got[contract.WireFormatPythonCompatible] != 0 {
		t.Fatalf("revisioned source by wire format = %v, want 2 standard_raw_event and 0 python_compatible", got)
	}
	total := 0
	for _, count := range with.PlansByWireFormat {
		total += count
	}
	if total != with.PlansTotal {
		t.Fatalf("by-format counts sum to %d, want the %d Plans: the counts must partition", total, with.PlansTotal)
	}
	// Plans frozen before the word existed carry none, and one frozen under
	// the historical spelling carries a word the sink never writes: both are
	// counted under what the sink resolves them to, not under _other and not
	// under the historical word.
	historical := revisionedCatalog(t, "")
	for index := range historical.QueryGroups {
		for planIndex := range historical.QueryGroups[index].Plans {
			historical.QueryGroups[index].Plans[planIndex].Plan.WireFormat = contract.WireFormatTriggerEvent
		}
	}
	unworded := objectCatalogTwoGroups(t, 80)
	for index := range unworded.QueryGroups {
		for planIndex := range unworded.QueryGroups[index].Plans {
			unworded.QueryGroups[index].Plans[planIndex].Plan.WireFormat = ""
		}
	}
	if got := controlplane.ComposeCatalog(historical).PlansByWireFormat; got[contract.WireFormatStandardRawEvent] != 2 || got[observability.WireFormatOther] != 0 {
		t.Fatalf("historical word by wire format = %v, want 2 standard_raw_event and nothing under _other", got)
	}
	if got := controlplane.ComposeCatalog(unworded).PlansByWireFormat; got[contract.WireFormatPythonCompatible] != 2 || got[observability.WireFormatOther] != 0 {
		t.Fatalf("no word, no revision by wire format = %v, want 2 python_compatible and nothing under _other", got)
	}
}
