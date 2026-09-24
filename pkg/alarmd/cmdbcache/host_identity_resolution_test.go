// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

const (
	hostByIdentity101 = `{"bk_host_id":101,"bk_host_innerip":"192.0.2.101","bk_cloud_id":0,"bk_biz_id":2,"model_id":"cw-Host","model_inst_id":"101",
		"topo_link":{"module|31":[{"bk_obj_id":"module","bk_inst_id":31},{"bk_obj_id":"set","bk_inst_id":12}]}}`
	hostByIdentity102 = `{"bk_host_id":102,"bk_host_innerip":"192.0.2.102","bk_cloud_id":0,"bk_biz_id":2,"model_id":"cw-Host","model_inst_id":"102",
		"topo_link":{"module|31":[{"bk_obj_id":"module","bk_inst_id":31},{"bk_obj_id":"set","bk_inst_id":12}]}}`
	// The same hosts as a writer before decision-017 B wrote them: no
	// canonical identity on the record.
	hostWithoutIdentity101 = `{"bk_host_id":101,"bk_host_innerip":"192.0.2.101","bk_cloud_id":0,"bk_biz_id":2,
		"topo_link":{"module|31":[{"bk_obj_id":"module","bk_inst_id":31},{"bk_obj_id":"set","bk_inst_id":12}]}}`
)

// writerModelInstancePlans are the model_inst_id plans the strategy cache
// writer actually emitted, decoded the way the compiler decodes them: the
// platform's default identity pair and no model_match, which the writer
// never sends. Three name the host model, one names a database model.
func writerModelInstancePlans(t *testing.T) map[string]*contract.TargetPlanV1 {
	t.Helper()
	raw, err := os.ReadFile("../targetplan/testdata/writer-observed-plans.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct {
			Case       string          `json:"case"`
			TargetPlan json.RawMessage `json:"target_plan"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	plans := make(map[string]*contract.TargetPlanV1)
	for _, test := range file.Cases {
		plan, refusal := targetplan.Decode(test.TargetPlan, targetplan.Options{ObjectIdentities: [][2]string{{"cw_object_model_id", "cw_object_model_inst_id"}}})
		if refusal != nil || plan.Rule != contract.TargetPlanRuleModelInstID {
			continue
		}
		plans[test.Case] = plan
	}
	if len(plans) != 4 {
		t.Fatalf("the writer's file holds %d model_inst_id plans, want the 4 it emitted", len(plans))
	}
	return plans
}

// A model_inst_id plan without a model_match is read by host identity, and
// whether its members are hosts is the host cache's answer: the writer's
// three host-model plans resolve to host ids and are Complete, the
// database-model plan resolves to nothing and is Unavailable by name. The
// name is the same word the compiler used to refuse all four with - now
// said per Slot, on the page, about the one plan it is true of.
func TestTheWritersHostPlansResolveThroughTheHostCacheAndTheDatabasePlanIsNamedUnresolved(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	client := &groupClient{values: map[string]string{
		"cw:dynamic_group:1001": `{"model_id":"cw-Host","model_inst_ids":["101","102"],"member_list":[{"model_id":"cw-Host","model_inst_id":"101","bk_host_id":101},{"model_id":"cw-Host","model_inst_id":"102","bk_host_id":102}]}`,
		"cw:dynamic_group:2001": `{"model_id":"cw-MySQL","model_inst_ids":["mysql-prod-01"],"member_list":[{"model_id":"cw-MySQL","model_inst_id":"mysql-prod-01"}]}`,
	}}
	reader, _ := NewGroupReader(client, "cw:")
	groups, err := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	hosts := hostStore(t, clock, []string{"101", hostByIdentity101, "102", hostByIdentity102}, []string{"set|12", "module|31"})
	resolver := NewTargetResolver(groups, hosts, clock)

	for name, plan := range writerModelInstancePlans(t) {
		t.Run(name, func(t *testing.T) {
			if !plan.Identity.HostIdentity || len(plan.StaticKeys) != 0 || len(plan.StaticMembers) == 0 {
				t.Fatalf("the writer's plan was not frozen as read by host identity: %+v", *plan)
			}
			resolution := resolver.Resolve(context.Background(), plan, time.Minute)
			static := selector(resolution, targetplan.SelectorKindStatic, plan.ModelID)
			if plan.ModelID == "cw-MySQL" {
				// The cache lists no host of this model: the members cannot be
				// placed against the data, and the plan says so by name rather
				// than reading as an empty target.
				if static.State != targetplan.SelectorUnavailable || static.Reason != targetplan.ReasonModelUnresolved || static.Kept != 0 {
					t.Fatalf("database-model plan static selector = %+v, want Unavailable %s", static, targetplan.ReasonModelUnresolved)
				}
				if resolution.State != targetplan.ResolutionUnavailable || len(resolution.Failures) == 0 {
					t.Fatalf("database-model plan resolution = %s failures %v, want Unavailable with the selector named", resolution.State, resolution.Failures)
				}
				if resolution.Contains("mysql-prod-01") || resolution.Contains("cw-MySQL|mysql-prod-01") {
					t.Fatal("a member the cache cannot place was read as in the target")
				}
				return
			}
			if static.State != targetplan.SelectorOK || static.Reason != targetplan.ReasonNone || static.Dropped != 0 {
				t.Fatalf("host-model plan %s static selector = %+v, want OK", name, static)
			}
			if resolution.State != targetplan.ResolutionComplete {
				t.Fatalf("host-model plan %s resolution = %s failures %v, want Complete", name, resolution.State, resolution.Failures)
			}
			for _, member := range plan.StaticMembers {
				if !resolution.Contains(member.ModelInstID) {
					t.Fatalf("host-model plan %s does not contain host %s, the id the cache maps its member to", name, member.ModelInstID)
				}
			}
			if resolution.Contains("cw-Host|101") {
				t.Fatal("the member is held under the (model, instance) spelling, not the host id")
			}
			// Group and topology members of the same plan are held under
			// their host ids too, so a record placed by either identity
			// matches whichever selector named its host.
			if len(plan.DynamicGroups) > 0 {
				group := selector(resolution, targetplan.SelectorKindGroup, "1001")
				if got := sortedKeys(group.Members); !reflect.DeepEqual(got, []string{"101", "102"}) {
					t.Fatalf("group members = %v, want the host ids", got)
				}
			}
			if len(plan.DynamicTopologies) > 0 {
				topology := selector(resolution, targetplan.SelectorKindTopology, "2|set|12")
				if got := sortedKeys(topology.Members); !reflect.DeepEqual(got, []string{"101", "102"}) {
					t.Fatalf("topology members = %v, want the host ids", got)
				}
			}
		})
	}
}

// A host cache whose writer has not put the canonical (model, instance)
// identity on its records cannot say which members are hosts. The plan's
// members are then unresolved by name, not read as hosts on the strength of
// their spelling: the model_inst_id of a host member is only known to be
// its host id by the cache, and a reader that assumed it would admit
// records for a member it never verified.
func TestAHostCacheWithoutTheCanonicalIdentityLeavesHostMembersUnresolvedByName(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	reader, _ := NewGroupReader(&groupClient{values: map[string]string{}}, "cw:")
	groups, err := NewGroupStore(reader, GroupStoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	hosts := hostStore(t, clock, []string{"101", hostWithoutIdentity101}, []string{"set|12", "module|31"})
	if hosts.Current().ModelledHosts() != 0 || hosts.Current().Hosts() != 1 {
		t.Fatalf("fixture: modelled %d of %d hosts, want a cache with hosts and no identity", hosts.Current().ModelledHosts(), hosts.Current().Hosts())
	}
	resolver := NewTargetResolver(groups, hosts, clock)
	plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleModelInstID,
		Identity:      contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true},
		StaticKeys:    []string{},
		StaticMembers: []contract.TargetPlanMemberV1{{ModelID: "cw-Host", ModelInstID: "101"}}}
	resolution := resolver.Resolve(context.Background(), plan, time.Minute)
	static := selector(resolution, targetplan.SelectorKindStatic, "cw-Host")
	if static.State != targetplan.SelectorUnavailable || static.Reason != targetplan.ReasonModelUnresolved {
		t.Fatalf("static selector on a cache without the identity = %+v, want Unavailable %s", static, targetplan.ReasonModelUnresolved)
	}
	if resolution.Contains("101") {
		t.Fatal("a member the cache never verified as a host was read as one on the strength of its spelling")
	}

	// Half the members known: the known one is a host, the other is dropped
	// and counted, and the plan is Incomplete rather than empty or whole.
	partial := hostStore(t, clock, []string{"101", hostByIdentity101}, []string{"set|12"})
	plan.StaticMembers = append(plan.StaticMembers, contract.TargetPlanMemberV1{ModelID: "cw-Host", ModelInstID: "102"})
	resolution = NewTargetResolver(groups, partial, clock).Resolve(context.Background(), plan, time.Minute)
	static = selector(resolution, targetplan.SelectorKindStatic, "cw-Host")
	if static.State != targetplan.SelectorIncomplete || static.Reason != targetplan.ReasonMembersDropped || static.Kept != 1 || static.Dropped != 1 {
		t.Fatalf("static selector with one member unknown = %+v, want Incomplete members_dropped 1/1", static)
	}
	if !resolution.Contains("101") || resolution.Contains("102") {
		t.Fatal("the known member is a host and the unknown one is not")
	}
	if len(resolution.Failures) != 1 || resolution.Failures[0].Reason != targetplan.ReasonMembersDropped || resolution.Failures[0].Kind != targetplan.SelectorKindStatic {
		t.Fatalf("failures = %+v, want the static selector named with its dropped members", resolution.Failures)
	}
}
