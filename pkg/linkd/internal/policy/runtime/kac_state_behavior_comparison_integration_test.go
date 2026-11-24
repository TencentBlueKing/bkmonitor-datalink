// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store/memory"
)

type comparisonSequence struct {
	ID         string                     `json:"id"`
	Kind       string                     `json:"kind"`
	Cyclic     bool                       `json:"cyclic,omitempty"`
	Steps      []comparisonSequenceStep   `json:"steps"`
	Difference string                     `json:"difference,omitempty"`
	KAC        []comparisonSequenceResult `json:"expected_kac"`
	Linkd      []comparisonSequenceResult `json:"expected_linkd"`
}

type comparisonSequenceStep struct {
	At      int64                  `json:"at"`
	EventAt *int64                 `json:"event_at,omitempty"`
	IDs     []string               `json:"ids,omitempty"`
	Closed  []string               `json:"closed,omitempty"`
	Add     []comparisonMergeInput `json:"add,omitempty"`
	Judge   bool                   `json:"judge,omitempty"`
}

type comparisonMergeInput struct {
	ID     string `json:"id"`
	Groups []int  `json:"groups"`
}

type comparisonSequenceResult struct {
	At       int64    `json:"at"`
	Count    int      `json:"count,omitempty"`
	Admitted []string `json:"admitted,omitempty"`
	Owner    string   `json:"owner,omitempty"`
	Outcome  string   `json:"outcome,omitempty"`
	Members  []string `json:"members,omitempty"`
}

type comparisonSequenceResponse struct {
	Sources map[string]string `json:"source_sha256"`
	Runtime map[string]string `json:"runtime"`
	Results []struct {
		ID    string                     `json:"id"`
		Kind  string                     `json:"kind"`
		Steps []comparisonSequenceResult `json:"steps"`
	} `json:"results"`
}

type sequenceOutput struct {
	bytes.Buffer
	limit int
}

func (b *sequenceOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("comparison output exceeds budget")
	}
	return b.Buffer.Write(p)
}

func TestKACStateSequenceBehaviorComparison(t *testing.T) {
	root, address := os.Getenv("LINKD_TEST_KAC_SOURCE_ROOT"), os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if root == "" || address == "" {
		t.Skip("set LINKD_TEST_KAC_SOURCE_ROOT and LINKD_TEST_REDIS_ADDRESS for state sequence comparison")
	}
	python := os.Getenv("LINKD_TEST_KAC_PYTHON")
	if python == "" {
		python = "python3"
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "kac_behavior_comparison", "testdata", "state-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []comparisonSequence
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	call, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	//nolint:gosec // G204: 显式本地测试目录与解释器独立传参，源文件在执行前校验摘要，无 shell。
	cmd := exec.CommandContext(call, python, "-B", filepath.Join("..", "..", "..", "tests", "kac_behavior_comparison", "kac_state_behavior_comparison.py"), root)
	cmd.Env = append(os.Environ(), "PYTHONHASHSEED=0", "TZ=UTC", "PYTHONDONTWRITEBYTECODE=1")
	cmd.Stdin = bytes.NewReader(raw)
	out, stderr := &sequenceOutput{limit: 1 << 20}, &sequenceOutput{limit: 8192}
	cmd.Stdout, cmd.Stderr = out, stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("KAC state comparison: %v %s", err, stderr.String())
	}
	var response comparisonSequenceResponse
	decoder = json.NewDecoder(bytes.NewReader(out.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("trailing comparison JSON", err)
	}
	if len(response.Results) != len(cases) || len(response.Sources) != 4 || response.Runtime["hash_seed"] != "0" || response.Runtime["timezone"] != "UTC" {
		t.Fatal("incomplete comparison response")
	}
	t.Logf("state comparison runtime: %s", response.Runtime["version"])
	reports := []map[string]any{}
	for i, tc := range cases {
		t.Run(tc.ID, func(t *testing.T) {
			old := response.Results[i]
			if old.ID != tc.ID || old.Kind != tc.Kind {
				t.Fatal("comparison identity mismatch")
			}
			state := realSuppressionState(t)
			var result []comparisonSequenceResult
			switch tc.Kind {
			case "clip":
				result = runBehaviorComparisonClip(t, state, tc)
			case "aggregation":
				result = runBehaviorComparisonAggregation(t, state, tc)
			case "merge":
				result = runBehaviorComparisonMerge(t, state, tc)
			default:
				t.Fatal("unknown sequence kind")
			}
			assertBehaviorComparisonTrace(t, "KAC", old.Steps, tc.KAC)
			assertBehaviorComparisonTrace(t, "Linkd", result, tc.Linkd)
			a, _ := json.Marshal(old.Steps)
			b, _ := json.Marshal(result)
			if (tc.Difference == "") != bytes.Equal(a, b) {
				t.Fatalf("unexpected difference classification %s: KAC=%s Linkd=%s", tc.Difference, a, b)
			}
			reports = append(reports, map[string]any{"case": tc, "kac": old.Steps, "linkd": result})
		})
	}
	if t.Failed() {
		return
	}
	if path := os.Getenv("LINKD_TEST_KAC_STATE_REPORT"); path != "" {
		data, err := json.MarshalIndent(map[string]any{"source_sha256": response.Sources, "runtime": response.Runtime, "cases": reports}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		//nolint:gosec // G703: 输出位置仅由显式本地测试环境变量指定，不从业务载荷取路径。
		if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("verified %d state sequences against real Linkd Redis; KAC uses explicit sequential storage/query/queue fixtures", len(cases))
}

func assertBehaviorComparisonTrace(t *testing.T, name string, got, want []comparisonSequenceResult) {
	t.Helper()
	a, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("%s trace differs: got=%s want=%s", name, a, b)
	}
}

func comparisonAt(second int64) time.Time { return time.Unix(1790726400+second, 0).UTC() }

func runBehaviorComparisonClip(t *testing.T, state *redisstate.Store, tc comparisonSequence) []comparisonSequenceResult {
	t.Helper()
	results := []comparisonSequenceResult{}
	for _, step := range tc.Steps {
		out := comparisonSequenceResult{At: step.At}
		for _, id := range step.IDs {
			d, err := state.Clip(t.Context(), redisstate.ClipRequest{Identity: redisstate.Identity{TenantID: "tenant", SourceID: "source", Fingerprint: "fingerprint"}, PolicyID: "comparison", Version: 1, Digest: strings.Repeat("a", 64), EventID: id, At: comparisonAt(step.At), Duration: time.Minute, Threshold: 3})
			if err != nil {
				t.Fatal(err)
			}
			out.Count = d.Count
			if d.Allowed {
				out.Admitted = append(out.Admitted, id)
			}
		}
		slices.Sort(out.Admitted)
		results = append(results, out)
	}
	return results
}

func runBehaviorComparisonAggregation(t *testing.T, state *redisstate.Store, tc comparisonSequence) []comparisonSequenceResult {
	t.Helper()
	results := []comparisonSequenceResult{}
	owners := map[string]redisstate.AggregationDecision{}
	group, err := policy.GroupKey([]any{"shared"})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range tc.Steps {
		for _, id := range step.Closed {
			d, ok := owners[id]
			if !ok {
				t.Fatal("unknown closing owner")
			}
			if released, err := state.ReleaseAggregation(t.Context(), "tenant", d); err != nil || !released {
				t.Fatal("owner release failed", err)
			}
		}
		out := comparisonSequenceResult{At: step.At}
		for _, id := range step.IDs {
			request := redisstate.AggregationRequest{Identity: redisstate.Identity{TenantID: "tenant", SourceID: "source-" + id, Fingerprint: id}, PolicyID: "comparison", Version: 1, Digest: strings.Repeat("a", 64), GroupKey: group, EventID: "event-" + id, CandidateAlertID: id, At: comparisonAt(step.At), Duration: time.Minute}
			decision, err := state.ClaimAggregation(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			switch decision.Role {
			case "candidate":
				if ok, err := state.CommitAggregationOwner(t.Context(), request, decision); err != nil || !ok {
					t.Fatal("commit owner", err)
				}
				owners[id] = decision
				out.Admitted = append(out.Admitted, id)
			case "owner":
				if _, ok, err := state.ConfirmAggregationSuppression(t.Context(), request, decision); err != nil || !ok {
					t.Fatal("confirm member", err)
				}
			case "suppressed":
			default:
				t.Fatal("unexpected aggregation role", decision.Role)
			}
			out.Owner = decision.OwnerAlertID
		}
		slices.Sort(out.Admitted)
		results = append(results, out)
	}
	return results
}

func runBehaviorComparisonMerge(t *testing.T, state *redisstate.Store, tc comparisonSequence) []comparisonSequenceResult {
	t.Helper()
	repo := memory.New()
	clock := &policyTestClock{at: comparisonAt(0)}
	release := mergeRelease(t)
	var spec map[string]any
	if err := json.Unmarshal(release.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	spec["is_cycle_merge"] = tc.Cyclic
	spec["policy"] = []any{map[string]any{"expression": "A", "A": map[string]any{"condition": "term", "target_key": "name", "target_value": []string{"db", "both"}}}, map[string]any{"expression": "A", "A": map[string]any{"condition": "term", "target_key": "name", "target_value": []string{"app", "both"}}}}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := policy.Compile(policy.Merge, raw)
	if err != nil {
		t.Fatal(err)
	}
	release.Spec, release.Compiled = compiled.Canonical, compiled.Summary
	directory := &shieldDirectory{releases: []policy.Release{release}}
	catalog := policy.NewCatalog(directory)
	loader := &Suppressor{Catalog: catalog, Releases: directory, Targets: shieldTargets{}}
	action := &shieldHook{}
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "action", Purpose: "action", Hook: action}}, config.DefaultSeverityConfig(), clock, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithPolicySnapshotter(Snapshotter{Catalog: catalog}), lifecycle.WithMergeEvaluator(&Merger{Loader: loader, State: state}))
	if err != nil {
		t.Fatal(err)
	}
	judge := &MergeJudge{Loader: loader, Windows: state, CurrentAlert: repo.GetAlert}
	aliases := map[string]string{}
	windowID := ""
	results := []comparisonSequenceResult{}
	for _, step := range tc.Steps {
		clock.at = comparisonAt(step.At)
		for _, input := range step.Add {
			title := "db"
			if reflect.DeepEqual(input.Groups, []int{1}) {
				title = "app"
			}
			if len(input.Groups) == 2 {
				title = "both"
			}
			e := dependencyEvent(input.ID, "source-"+input.ID, title, clock)
			stored := mustProcessAggregation(t, repo, processor, e)
			if len(stored.Event.RelatedAlertIDs) != 1 {
				t.Fatal("merge Event missing Alert")
			}
			id := stored.Event.RelatedAlertIDs[0]
			aliases[id] = input.ID
			current, err := repo.GetAlert(t.Context(), "tenant", id)
			if err != nil {
				t.Fatal(err)
			}
			if current.Alert.Merge == nil || len(current.Alert.Merge.Pending) != 1 || current.Alert.Admission.AdmittedAt != nil {
				t.Fatal("merge admission not persisted")
			}
			wait := current.Alert.Merge.Pending[0]
			if !slices.Equal(wait.Groups, input.Groups) {
				t.Fatalf("actual condition groups=%v expected=%v", wait.Groups, input.Groups)
			}
			if windowID == "" {
				windowID = wait.WindowID
			}
		}
		if step.Judge {
			decision, err := judge.Evaluate(t.Context(), "tenant", windowID, clock.at, nil)
			if err != nil {
				t.Fatal(err)
			}
			out := comparisonSequenceResult{At: step.At, Outcome: "waiting"}
			if decision.Frozen {
				out.Outcome = decision.Window.Frozen.Outcome
				for _, id := range decision.Window.Frozen.MemberIDs {
					alias, ok := aliases[id]
					if !ok {
						t.Fatal("unknown selected Alert")
					}
					out.Members = append(out.Members, alias)
				}
				slices.Sort(out.Members)
			}
			results = append(results, out)
		}
	}
	if len(action.calls) != 0 {
		t.Fatalf("window verdict caused premature actions: %d", len(action.calls))
	}
	return results
}
