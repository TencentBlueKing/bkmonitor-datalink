// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// writerStrategyDocuments are the documents the strategy cache writer
// publishes, produced by running the writer's own projector and its contract
// check - not written by hand here. They are regenerated from the writer's
// source whenever the writer changes.
type writerStrategyDocuments struct {
	SourceCommit string `json:"source_commit"`
	Samples      []struct {
		Case     string         `json:"case"`
		Document map[string]any `json:"document"`
	} `json:"samples"`
}

func loadWriterStrategyDocuments(t *testing.T) writerStrategyDocuments {
	t.Helper()
	payload, err := os.ReadFile("testdata/writer-strategy-documents.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents writerStrategyDocuments
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	if len(documents.Samples) == 0 {
		t.Fatal("no writer documents")
	}
	return documents
}

// Every document the writer publishes is read, and compiled, the way it is
// expected to be: a refusal at the source is a contract the two sides do not
// share and fails here by case, reason and field; a compile-time outcome is
// held to the list below, so a new refusal - or a refusal that went away - is
// seen when the samples are regenerated. The writer's own check ran on every
// one of these before it was recorded.
func TestEveryDocumentTheStrategyWriterPublishesIsReadAsExpected(t *testing.T) {
	documents := loadWriterStrategyDocuments(t)
	client := newControlplaneRedis(t)
	ctx := context.Background()
	const prefix = "writer.cache"
	ids := make([]string, 0, len(documents.Samples))
	cases := map[string]string{}
	for index, sample := range documents.Samples {
		// The writer's tests reuse one strategy id; each sample gets its own so
		// they can be read side by side. Nothing else is changed.
		id := strconv.Itoa(5000 + index)
		document := sample.Document
		document["id"] = 5000 + index
		payload, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Set(ctx, prefix+".strategy_"+id, string(payload), 0).Err(); err != nil {
			t.Fatal(err)
		}
		ids, cases[id] = append(ids, id), sample.Case
	}
	source, err := controlplane.NewLegacyRedisStrategySource(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := source.Strategies(ctx, ids)
	if err != nil || len(strategies) != len(ids) {
		t.Fatalf("Strategies() = (%d, %v), want %d", len(strategies), err, len(ids))
	}
	for _, strategy := range strategies {
		if refused := strategy.SourceDisposition; refused != nil {
			t.Errorf("%s: refused at the source: %s %s at %q", cases[strategy.SourceID], refused.Disposition, refused.Reason, refused.FieldPath)
		}
	}
	if t.Failed() {
		return
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(ctx, controlplane.BuildRequest{Planner: planner, Strategies: strategies})
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string][]string{}
	for _, disposition := range catalog.Dispositions {
		if disposition.Disposition == controlplane.DispositionAccepted {
			continue
		}
		outcome := string(disposition.Disposition) + " " + disposition.Reason
		if disposition.FieldPath != "" {
			outcome += " at " + disposition.FieldPath
		}
		outcomes[cases[disposition.SourceID]] = append(outcomes[cases[disposition.SourceID]], outcome)
	}
	got := map[string]string{}
	for _, sample := range documents.Samples {
		sort.Strings(outcomes[sample.Case])
		got[sample.Case] = strings.Join(outcomes[sample.Case], "; ")
	}
	for name, outcome := range got {
		want, known := writerDocumentOutcomes[name]
		if !known {
			t.Errorf("%s: a writer case this test does not know; reads as %q", name, outcome)
			continue
		}
		if outcome != want {
			t.Errorf("%s: reads as %q, want %q", name, outcome, want)
		}
	}
	for name := range writerDocumentOutcomes {
		if _, present := got[name]; !present {
			t.Errorf("%s: expected here, no longer among the writer's samples", name)
		}
	}
	if t.Failed() {
		t.Logf("writer commit %s; every outcome:\n%s", documents.SourceCommit, formatOutcomes(got))
	}
}

func formatOutcomes(outcomes map[string]string) string {
	names := make([]string, 0, len(outcomes))
	for name := range outcomes {
		names = append(names, name)
	}
	sort.Strings(names)
	var builder strings.Builder
	for _, name := range names {
		fmt.Fprintf(&builder, "\t%q: %q,\n", name, outcomes[name])
	}
	return builder.String()
}

// writerDocumentOutcomes is what each writer case compiles to beyond an
// accepted Plan; "" is accepted and nothing else. The refusals are this
// build's named limits on what the writer publishes, not disagreements about
// the contract:
//   - an FTA event source is not supported;
//   - an AIOps algorithm is not migrated;
//   - three legacy target forms the writer publishes without a target_plan -
//     a dynamic group it could not convert without loss, a service topology
//     node, a set template - are withheld by name rather than run over a
//     scope this build cannot resolve.
var writerDocumentOutcomes = map[string]string{
	"test_projector_adds_committed_strategy_ref_and_legacy_query_fields[0]": "",
	"test_projector_adds_committed_strategy_ref_and_legacy_query_fields[1]": "",
	"test_projector_applies_fake_event_overlays":                            "",
	"test_projector_builds_canonical_target_plan":                           "",
	"test_projector_builds_kubernetes_cluster_and_node_matches[0]":          "",
	"test_projector_builds_kubernetes_cluster_and_node_matches[1]":          "",
	"test_projector_builds_kubernetes_workload_match":                       "",
	"test_projector_does_not_treat_neq_as_eq":                               "",
	"test_projector_normalizes_source_specific_query_contracts[0]":          "",
	"test_projector_normalizes_source_specific_query_contracts[1]":          "",
	"test_projector_normalizes_source_specific_query_contracts[2]":          "UNSUPPORTED_PHASE2_CAPABILITY QUERY_FTA_UNSUPPORTED at items[0].query_configs[0]",
	"test_projector_omits_model_inst_plan_when_model_mapping_is_missing":    "",
	"test_projector_omits_target_plan_when_conversion_is_not_lossless[0]":   "",
	"test_projector_omits_target_plan_when_conversion_is_not_lossless[1]":   "",
	"test_projector_omits_target_plan_when_conversion_is_not_lossless[2]":   "",
	"test_projector_omits_target_plan_when_conversion_is_not_lossless[3]":   "",
	"test_projector_omits_target_plan_when_conversion_is_not_lossless[4]":   "",
	"test_projector_omits_target_plan_when_conversion_is_not_lossless[5]":   "",
	"test_projector_omits_target_plan_when_conversion_is_not_lossless[6]":   "UNSUPPORTED_PHASE2_CAPABILITY UNSUPPORTED_TARGET_SCOPE at items[0].target",
	"test_projector_omits_target_plan_when_conversion_is_not_lossless[7]":   "",
	"test_projector_omits_target_plan_when_conversion_is_not_lossless[8]":   "UNSUPPORTED_PHASE2_CAPABILITY UNSUPPORTED_TARGET_SCOPE_UNRESOLVABLE at items[0].target",
	"test_projector_omits_target_plan_when_conversion_is_not_lossless[9]":   "",
	"test_projector_preserves_legacy_ip_target_without_target_plan":         "",
	"test_projector_preserves_levels_and_applies_aiops_trigger_override":    "UNSUPPORTED_PHASE2_CAPABILITY ALGORITHM_NOT_MIGRATED",
	"test_projector_preserves_mixed_query_identity_without_target_plan":     "",
	"test_projector_preserves_or_between_single_condition_groups":           "",
	"test_projector_preserves_template_target_without_target_plan":          "UNSUPPORTED_PHASE2_CAPABILITY UNSUPPORTED_TARGET_SCOPE at items[0].target",
}
