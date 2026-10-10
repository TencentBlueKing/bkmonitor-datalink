// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package blackbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
)

const slotEvaluationTime = 1700124000
const slotSeries = `{"name":"a","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip_table1"],"group_values":["127.0.0.1"],"values":[[1700123456000,12.5],[1700123457000,19.25]]}`
const slotFullResponse = `{"series":[` + slotSeries + `],"is_partial":false,"result_table_id":["system.cpu"]}`

func TestActualCLISlotEvidenceAndOwnerRequery(t *testing.T) {
	h := newHarness(t, "private_ca")
	defer h.report()
	provider := newSlotUQFixture(t, slotFullResponse)
	h.secrets = append(h.secrets, provider.secret)
	plan := frozenSlotFixture(t)
	var retainedIncomplete atomic.Bool
	resolutions := map[string]*atomic.Int32{"entry": {}, "leader": {}, "worker": {}}
	routing := newSlotRoutingFixture(t, h, func(id string) []obchannel.Operation {
		client, err := uq.NewDiagnosticClient(provider.server.URL, "alarmd-slot-blackbox", provider.client)
		if err != nil {
			t.Fatal("cannot initialize real UQ diagnostic client")
		}
		operations := obchannel.NativeOperations(http.HandlerFunc(slotNativeFixture))
		return append(operations, obchannel.SlotOperations(obchannel.SlotOptions{
			UQ: client,
			Resolve: func(_ context.Context, slot execution.SlotIdentity) (obchannel.SlotPlan, error) {
				resolutions[id].Add(1)
				if slot != plan.Contract.Slot {
					return obchannel.SlotPlan{}, obchannel.ErrHistoricalContractUnavailable
				}
				return plan, nil
			},
			Evidence: func(context.Context, execution.SlotIdentity) (obchannel.SlotEvidence, error) {
				return obchannel.SlotEvidence{
					Records: []json.RawMessage{json.RawMessage(`{"slot_identity_known":true,"query_group_key":"fixture-qg","evaluation_time":1700124000,"producer":"historical-worker"}`)},
					Samples: []json.RawMessage{}, Complete: !retainedIncomplete.Load(),
					Limitations: []string{"Constructed retained record; original query inputs were not captured."},
				}, nil
			},
		})...)
	})
	defer func() { routing.report(provider.receipts()) }()
	grant := h.grant()
	h.run("slot-login", 0, grant+"\n", "auth", "login", "--ca-cert", h.caFile)
	token := h.profileToken()
	h.secrets = append(h.secrets, token)
	tokenHash := sha256.Sum256([]byte(token))
	forbidden := []string{token, hex.EncodeToString(tokenHash[:]), provider.secret, h.secrets[0], grant}
	// Grant secrets are also forbidden internally; control stream credentials
	// intentionally authenticate each RPC and are excluded from this list.
	for _, secret := range h.secrets {
		if !strings.HasPrefix(secret, "constructed-slot-") {
			forbidden = append(forbidden, secret)
		}
	}
	routing.forbidden.Store(forbidden)

	discovery := h.run("slot-discover", 0, "", "discover", "--env", environment)
	for _, operation := range []string{"slot.get", "slot.query"} {
		if !containsJSON(discovery, operation) {
			t.Fatalf("discovery omitted %s", operation)
		}
	}
	object := h.run("object-with-captured-slot", 0, "", "invoke", "object.get", "--env", environment, "--input", `{"query_group":"fixture-qg","records":20}`)
	if got := slotNextCall(t, object, "slot.get"); got["evaluation_time"] != float64(slotEvaluationTime) {
		t.Fatal("object suggestion did not preserve the captured Slot identity")
	}
	get := invokeSlotNext(t, h, "slot-preview-without-query", 0, object, "slot.get")
	if len(provider.receipts()) != 0 || routing.store.reads.Load() != 0 {
		t.Fatal("object or Slot preview queried UQ or routed a deployment read")
	}
	getResult := slotMap(t, get, "result")
	if getResult["kind"] != "reconstructed_from_contract" || getResult["historical_input_complete"] != false || !containsJSON(getResult, "historical-worker") {
		t.Fatal("preview confused reconstructed/retained facts with complete original inputs")
	}
	queries := getResult["queries"].([]any)
	preview := slotMap(t, queries[0].(map[string]any), "request")
	if containsJSON(preview, provider.server.URL) || containsJSON(preview, "Authorization") || !containsJSON(preview, "127.0.0.1") {
		t.Fatal("request preview hid business conditions or exposed connection configuration")
	}
	queryInput := slotNextCall(t, get, "slot.query")
	if queryInput["replica"] != nil || queryInput["owner_query_group"] != nil {
		t.Fatal("ordinary Slot preview unexpectedly pinned a process")
	}
	queried := invokeSlotNext(t, h, "slot-query-current-owner", 0, get, "slot.query")
	assertTargetReceipt(t, h, queried, "worker-boot-1")
	meta := slotMap(t, queried, "meta")
	owner := slotMap(t, meta, "owner")
	if owner["owner_id"] != "worker" || owner["owner_epoch"] != float64(19) || owner["query_group"] != "fixture-qg" {
		t.Fatal("query receipt lost the actual execution lease")
	}
	if resolutions["entry"].Load() != 1 || resolutions["worker"].Load() != 1 || resolutions["leader"].Load() != 0 {
		t.Fatal("Slot query did not execute exclusively on the current owner")
	}
	query := assertSlotPoints(t, queried, "FULL", true, 2)
	queriedResult := slotMap(t, queried, "result")
	queriedAt, err := time.Parse(time.RFC3339Nano, textField(queriedResult, "queried_at"))
	if err != nil || !queriedAt.After(time.Unix(slotEvaluationTime, 0)) || queriedResult["kind"] != "requery_now" {
		t.Fatal("query receipt did not distinguish present query time from historical Slot time")
	}
	if query["request_digest"] != preview["request_digest"] {
		t.Fatal("query did not retain the previewed wire request digest")
	}
	assertSlotWire(t, provider.receipts(), preview, 1)

	// The provider reports partial only after its complete series payload.
	provider.body.Store(`{"series":[` + slotSeries + `],"status":{"code":"QUERY_TS_PARTIAL","message":"constructed unavailable route"},"is_partial":true}`)
	partial := invokeSlotNext(t, h, "slot-query-provider-tail-partial", 3, get, "slot.query")
	assertSlotPoints(t, partial, "PARTIAL", false, 2)
	if !containsJSON(partial, "QUERY_TS_PARTIAL") {
		t.Fatal("tail provider status was lost after points were decoded")
	}
	assertSlotWire(t, provider.receipts(), preview, 2)
	provider.body.Store(slotFullResponse)
	limitedInput := slotNextCall(t, get, "slot.query")
	limitedInput["max_points"] = 1
	limited := invokeSlotInput(h, "slot-query-output-limit", 3, "slot.query", limitedInput)
	limitedQuery := assertSlotPoints(t, limited, "FULL", false, 1)
	if limitedQuery["truncated"] != true {
		t.Fatal("output point limit did not explain its partial result")
	}
	assertSlotWire(t, provider.receipts(), preview, 3)

	series := query["series"].([]any)[0].(map[string]any)
	selectedInput := slotNextCall(t, get, "slot.query")
	selectedInput["series_digest"] = series["series_digest"]
	selected := invokeSlotInput(h, "slot-query-normalized-series-selection", 0, "slot.query", selectedInput)
	assertSlotPoints(t, selected, "FULL", true, 2)
	assertSlotWire(t, provider.receipts(), preview, 4)

	for _, rejection := range []struct{ name, field, value, code string }{
		{"unknown-physical-query", "physical_query_digest", strings.Repeat("f", 64), "query_reference_unknown"},
		{"changed-contract", "contract_digest", strings.Repeat("f", 64), "slot_contract_changed"},
		{"changed-wire-preview", "request_digest", strings.Repeat("f", 64), "query_request_changed"},
	} {
		input := slotNextCall(t, get, "slot.query")
		input[rejection.field] = rejection.value
		result := invokeSlotInput(h, rejection.name, 1, "slot.query", input)
		assertRoutingError(t, result, rejection.code)
		assertTargetReceipt(t, h, result, "worker-boot-1")
		next := slotNextCall(t, result, "slot.get")
		if next["owner_query_group"] != "fixture-qg" || next["expected_incarnation"] != "worker-boot-1" {
			t.Fatal("failed query did not return a preview pinned to the actual answering process")
		}
		if len(provider.receipts()) != 4 {
			t.Fatal("rejected query triggered a UQ request")
		}
	}
	missing := map[string]any{"query_group": "fixture-qg", "evaluation_time": slotEvaluationTime - 3600}
	assertRoutingError(t, invokeSlotInput(h, "slot-history-unavailable", 1, "slot.get", missing), "historical_contract_unavailable")
	missingQuery := slotNextCall(t, get, "slot.query")
	missingQuery["evaluation_time"] = slotEvaluationTime - 3600
	assertRoutingError(t, invokeSlotInput(h, "slot-query-history-unavailable", 1, "slot.query", missingQuery), "historical_contract_unavailable")
	if len(provider.receipts()) != 4 {
		t.Fatal("unavailable historical contract fell back to a current query")
	}
	retainedIncomplete.Store(true)
	incomplete := invokeSlotNext(t, h, "slot-retained-evidence-partial", 3, object, "slot.get")
	if slotMap(t, incomplete, "result")["historical_input_complete"] != false || len(provider.receipts()) != 4 {
		t.Fatal("partial retained evidence was confused with queried historical data")
	}
	retainedIncomplete.Store(false)
	explicit := slotNextCall(t, get, "slot.query")
	explicit["replica"] = "entry"
	explicitResult := invokeSlotInput(h, "slot-query-explicit-replica", 0, "slot.query", explicit)
	if slotMap(t, explicitResult, "meta")["answered_by"] != "entry" || slotMap(t, explicitResult, "meta")["owner"] != nil {
		t.Fatal("explicit replica selection did not override default owner routing")
	}
	assertSlotPoints(t, explicitResult, "FULL", true, 2)
	assertSlotWire(t, provider.receipts(), preview, 5)
	if routing.nodes["leader"].auth.admissions.Load() != 0 || routing.nodes["worker"].auth.admissions.Load() != 0 {
		t.Fatal("internal Slot execution admitted or renewed a CLI session")
	}
	h.run("slot-logout", 0, "", "auth", "logout", "--env", environment)
	h.assertNoSecrets()
}

func invokeSlotInput(h *harness, name string, want int, operation string, input map[string]any) map[string]any {
	h.t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		h.t.Fatal("cannot encode Slot input")
	}
	return h.run(name, want, "", "invoke", operation, "--env", environment, "--input", string(raw))
}

func slotMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("missing structured %s", key)
	}
	return value
}

func assertSlotPoints(t *testing.T, response map[string]any, completeness string, complete bool, points int) map[string]any {
	t.Helper()
	query := slotMap(t, slotMap(t, response, "result"), "query")
	if slotMap(t, query, "completion")["completeness"] != completeness || query["complete"] != complete || slotMap(t, response, "evidence")["complete"] != complete || slotMap(t, query, "scan")["complete"] != true {
		t.Fatal("provider completion, full tail scan and output completeness were conflated")
	}
	series, ok := query["series"].([]any)
	if !ok || len(series) != 1 {
		t.Fatal("query did not return exactly the constructed normalized series")
	}
	item := series[0].(map[string]any)
	if slotMap(t, item, "dimensions")["bk_target_ip"] != "127.0.0.1" || len(textField(item, "series_digest")) != 64 {
		t.Fatal("production normalization lost series identity or dimensions")
	}
	rows := item["points"].([]any)
	if len(rows) != points || query["returned_points"] != float64(points) {
		t.Fatal("query returned the wrong bounded point count")
	}
	first := rows[0].(map[string]any)
	if first["source_time"] != float64(1700123456) || first["value"] != 12.5 || textField(first, "record_id") == "" {
		t.Fatal("query lost normalized source time, raw point value or record identity")
	}
	return query
}

func assertSlotWire(t *testing.T, receipts []slotUQReceipt, preview map[string]any, want int) {
	t.Helper()
	if len(receipts) != want {
		t.Fatal("Slot query performed an unexpected number of UQ requests")
	}
	for _, receipt := range receipts {
		if receipt.Path != preview["path"] || stringMustJSON(receipt.Body) != stringMustJSON(preview["body"]) || receipt.Tenant != "fixture-tenant" || receipt.Space != "bkcc__2" || receipt.QuerySource != "alarmd-slot-blackbox" || !receipt.ConfiguredCredentialUsed {
			t.Fatal("actual UQ request differed from the safe preview or lost its server configuration")
		}
	}
}

func slotNativeFixture(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/objects/fixture-qg" {
		nativeFixture(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"complete":true,"records_status":"available","records":[{"slot_identity_known":false,"evaluation_time":1700123000},{"slot_identity_known":true,"query_group_key":"other","evaluation_time":1700123000},{"slot_identity_known":true,"query_group_key":"fixture-qg","evaluation_time":1700124000,"producer":"historical-worker"}]}`))
}

// Immutable references and a prepared physical plan are constructed here. The
// production historical catalog resolver is covered by service-side tests.
func frozenSlotFixture(t *testing.T) obchannel.SlotPlan {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-main", TenantID: "fixture-tenant", BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList: []execution.QueryClause{{DataSource: "bkmonitor", TableID: "system.cpu", FieldName: "usage", ReferenceName: "a", Driver: "influxdb", TimeField: "time",
			TimeAggregation: execution.QueryFunction{Method: "avg", Position: 0, Window: "60s"},
			Conditions:      execution.QueryConditions{Fields: []execution.QueryConditionField{{Field: "host", Operator: "eq", Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "127.0.0.1"}}, Wildcard: "false", Prefix: "true", Suffix: "false"}}}, OffsetForward: "false"}},
		MetricMerge: "a", StepMillis: 60000, AlignmentMillis: 60000, DownSampleRange: execution.DownSampleNone, Timezone: "UTC",
		Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64), IdentityFields: []string{"bk_target_ip"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond, SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1,
			GroupKeyRule: execution.GroupKeyStripTableSuffixV1, ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value", ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1"},
	})
	if err != nil {
		t.Fatal("cannot build constructed query plan facts")
	}
	spec, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: facts, LogicalWindow: execution.QueryWindow{Start: 1700123000, End: slotEvaluationTime}, ProviderRange: execution.QueryWindow{Start: 1700123000, End: slotEvaluationTime}, AcceptedRange: execution.QueryWindow{Start: 1700123000, End: slotEvaluationTime}, RequiredColumns: []string{"value"}})
	if err != nil {
		t.Fatal("cannot build constructed physical query")
	}
	return obchannel.SlotPlan{
		Contract:     execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "fixture-qg", EvaluationTime: slotEvaluationTime}, SnapshotRevision: "historical-snapshot", QueryRevision: facts.QueryRevision, ScheduleRevision: "historical-schedule", ScheduleSegmentStart: slotEvaluationTime - 3600, DuePlanSetDigest: execution.DuePlanSetDigest(strings.Repeat("c", 64))},
		ObjectDigest: execution.ObjectDigest(strings.Repeat("d", 64)),
		Prepared:     access.PreparedExecution{Queries: []access.PlannedQuery{{Spec: spec, Requirements: []execution.DataRequirement{{RequirementID: "primary", DatasetName: "primary", Role: execution.InputRolePrimary}}}}},
	}
}
