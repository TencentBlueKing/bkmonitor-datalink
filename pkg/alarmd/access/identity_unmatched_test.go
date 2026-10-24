// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// gatedPlanContext is an object-model target as the writer freezes it today:
// keyed by cw_object_model_inst_id, gated on cw_object_model_id = "8".
func gatedPlanContext() admission.PlanContext {
	return admission.PlanContext{TargetPlan: &admission.TargetPlanContext{
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"cw_object_model_inst_id"}, ModelDimension: "cw_object_model_id", ModelValue: "8"},
		Members:  memberSet{"22": {}}}}
}

// placeholderDims is a series as the collection writes it now: canonical
// labels right, legacy ones holding their own names as placeholders.
func placeholderDims() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"cw_object_model_id": json.RawMessage(`"cw_object_model_id"`), "cw_object_model_inst_id": json.RawMessage(`"cw_object_model_inst_id"`),
		"model_id": json.RawMessage(`"cw-MySQL"`), "model_inst_id": json.RawMessage(`"22"`), "entity_uid": json.RawMessage(`"cw-MySQL|22"`),
	}
}

// A series whose model gate does not hold is withheld as one the target's
// identity cannot read: counted apart from outside-the-target, and sampled
// with the gate's expected value, the actual one, the key field and the
// canonical fields beside them.
func TestASeriesFailingItsModelGateIsCountedKeyMissingWithItsIdentity(t *testing.T) {
	completion := withheldCompletion(t, targetChain(), placeholderDims(), gatedPlanContext())
	if completion.DataState != execution.DataStateEmpty || completion.Withheld != 1 || completion.WithheldKeyMissing != 1 || completion.WithheldOutsideTarget != 0 {
		t.Fatalf("completion %+v, want EMPTY with the series withheld as key missing", completion)
	}
	want := []execution.IdentitySample{{Fields: []execution.IdentityField{
		{Name: "cw_object_model_id", Expected: "8", Actual: "cw_object_model_id", Present: true},
		{Name: "cw_object_model_inst_id", Actual: "cw_object_model_inst_id", Present: true},
		{Name: "model_id", Actual: "cw-MySQL", Present: true, Canonical: true},
		{Name: "model_inst_id", Actual: "22", Present: true, Canonical: true},
	}}}
	if !reflect.DeepEqual(completion.IdentitySamples, want) {
		t.Fatalf("samples %+v\nwant %+v", completion.IdentitySamples, want)
	}
	if encoded, _ := json.Marshal(completion.IdentitySamples); strings.Contains(string(encoded), "entity_uid") {
		t.Fatalf("a field outside the identity reached the sample: %s", encoded)
	}
}

// A series whose key is built and is not a member is outside the target, not
// key missing; and every Plan has to refuse the series as key missing for
// the series to count so.
func TestOnlyAKeyEveryPlanCannotBuildIsKeyMissing(t *testing.T) {
	member := placeholderDims()
	member["cw_object_model_id"], member["cw_object_model_inst_id"] = json.RawMessage(`"8"`), json.RawMessage(`"99"`)
	outside := withheldCompletion(t, targetChain(), member, gatedPlanContext())
	if outside.WithheldKeyMissing != 0 || len(outside.IdentitySamples) != 0 {
		t.Fatalf("a built key outside the members read as key missing: %+v", outside)
	}
	// One Plan cannot build its key; the other builds a host key outside its
	// members: the series is not one every Plan's identity failed to read.
	withHost := placeholderDims()
	withHost["bk_host_id"] = json.RawMessage(`"202"`)
	mixed := withheldCompletion(t, targetChain(), withHost, gatedPlanContext(), hostPlanContext(true))
	if mixed.WithheldKeyMissing != 0 {
		t.Fatalf("a series one Plan refused for another reason counted as key missing: %+v", mixed)
	}
}

// The samples stop at their bound and every value is cut.
func TestIdentitySamplesAreBounded(t *testing.T) {
	adapter, _ := scopeDropAdapter(t, targetChain(), gatedPlanContext(), &recordingSink{}, scopeOutput)
	long := placeholderDims()
	long["cw_object_model_id"] = json.RawMessage(`"` + strings.Repeat("x", 200) + `"`)
	for index := 0; index < execution.IdentitySamplesPerQuery+4; index++ {
		deliverSeries(t, adapter, long)
	}
	completion := adapter.reconcileCompletion(execution.ProviderCompletion{Completeness: execution.CompletenessFull, DataState: execution.DataStateData})
	if completion.WithheldKeyMissing != uint64(execution.IdentitySamplesPerQuery+4) || len(completion.IdentitySamples) != execution.IdentitySamplesPerQuery {
		t.Fatalf("key missing %d samples %d", completion.WithheldKeyMissing, len(completion.IdentitySamples))
	}
	if got := completion.IdentitySamples[0].Fields[0].Actual; len(got) != execution.IdentityValueBytes {
		t.Fatalf("value of %d bytes, want cut to %d", len(got), execution.IdentityValueBytes)
	}
}
