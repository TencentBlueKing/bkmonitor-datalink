// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
)

type boundaryTargets struct {
	scopeErr, resolveErr error
	resolved             onemodel.TargetResult
	reads                int
}

func (r *boundaryTargets) ResolveBusinessScope(_ context.Context, tenant, space string, _ int64) (onemodel.TargetScope, error) {
	return onemodel.TargetScope{TenantID: tenant, SpaceCode: space, BusinessIDs: []int64{2}}, r.scopeErr
}

func (r *boundaryTargets) Resolve(context.Context, string, string, onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	r.reads++
	return r.resolved, r.resolveErr
}

type boundaryMembershipTargets struct {
	*boundaryTargets
	found           bool
	membershipErr   error
	membershipReads int
}

func (r *boundaryMembershipTargets) ContainsTarget(context.Context, string, string, *onemodel.TargetDescriptor, onemodel.InstanceRef) (bool, error) {
	r.membershipReads++
	return r.found, r.membershipErr
}

func targetBoundaryFixture(t *testing.T) (Release, *Compiled, *FactView, time.Time, onemodel.TargetResult) {
	t.Helper()
	var spec map[string]any
	if err := json.Unmarshal(testSpec("target-boundary"), &spec); err != nil {
		t.Fatal(err)
	}
	ref := onemodel.InstanceRef{ModelID: "cw-Host", InstanceID: "1", EntityUID: "cw-Host|1"}
	spec["model_id"] = "cw-Host"
	spec["target_descriptor"] = onemodel.TargetDescriptor{SchemaVersion: 1, ModelID: "cw-Host", Selectors: []onemodel.TargetSelector{{Type: "instances", Instances: []onemodel.InstanceRef{ref}}}}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := Compile(Suppression, raw)
	if err != nil {
		t.Fatal(err)
	}
	release := Release{Scope: Scope{TenantID: "tenant", Kind: Suppression}, ID: "p", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}
	event := policyEvent(`[]`, domain.EnrichStatusSucceeded)
	event.Labels["model_id"] = domain.NewStringScalar(ref.ModelID)
	event.Labels["model_inst_id"] = domain.NewStringScalar(ref.InstanceID)
	view := mustEventView(t, event, nil)
	at := time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC)
	valid := onemodel.TargetResult{Scope: onemodel.TargetScope{TenantID: "tenant", SpaceCode: "bkcc__2", BusinessIDs: []int64{2}}, Instances: []onemodel.InstanceRef{ref}}
	return release, compiled, view, at, valid
}

func TestEvaluateTargetMembershipPreservesAccessAndIdentitySemantics(t *testing.T) {
	release, compiled, view, at, valid := targetBoundaryFixture(t)
	malformed := valid
	malformed.Instances = []onemodel.InstanceRef{{ModelID: "cw-Host", InstanceID: "1", EntityUID: "cw-Host|wrong"}}
	foreign := valid
	foreign.Scope.TenantID = "other"
	tests := []struct {
		name      string
		old       boundaryTargets
		fastErr   error
		fastFound bool
		wantErr   error
		reason    string
		matched   bool
	}{
		{name: "matched", old: boundaryTargets{resolved: valid}, fastFound: true, matched: true},
		{name: "outside target", old: boundaryTargets{resolved: onemodel.TargetResult{Scope: valid.Scope}}, reason: "outside_target_instances"},
		{name: "scope access error", old: boundaryTargets{scopeErr: ErrAccess}, wantErr: ErrAccess},
		{name: "target access error", old: boundaryTargets{resolveErr: ErrAccess}, fastErr: ErrAccess, wantErr: ErrAccess},
		{name: "foreign target scope", old: boundaryTargets{resolved: foreign}, fastErr: ErrAccess, wantErr: ErrAccess},
		{name: "malformed target identity", old: boundaryTargets{resolved: malformed}, fastErr: ErrTargetIdentity, reason: "target_identity_invalid"},
		{name: "target unavailable", old: boundaryTargets{resolveErr: ErrUnavailable}, fastErr: ErrUnavailable, reason: "target_resolution_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			old := test.old
			fastBase := test.old
			fast := &boundaryMembershipTargets{boundaryTargets: &fastBase, membershipErr: test.fastErr, found: test.fastFound}
			for _, reader := range []TargetReader{&old, fast} {
				result, err := EvaluateConditions(t.Context(), release, compiled, view, reader, at, false)
				if test.wantErr != nil {
					if !errors.Is(err, test.wantErr) {
						t.Fatalf("%T swallowed access: verdict=%+v err=%v", reader, result, err)
					}
					continue
				}
				if err != nil || result.Matched != test.matched || result.Reason != test.reason {
					t.Fatalf("%T changed result: verdict=%+v err=%v", reader, result, err)
				}
				if (test.reason == "target_identity_invalid" || test.reason == "target_resolution_failed") && result.Evaluated {
					t.Fatalf("%T dependency failure became not-matched", reader)
				}
			}
			if fastBase.reads != 0 {
				t.Fatal("membership reader still expanded full target list")
			}
			if test.old.scopeErr == nil && fast.membershipReads != 1 {
				t.Fatal("membership bypass not exercised", fast.membershipReads)
			}
		})
	}
}

type boundaryPreparedTargets struct {
	*boundaryTargets
	prepared TargetReader
	err      error
	calls    int
}

func (r *boundaryPreparedTargets) PrepareTargets(context.Context, string, string, *onemodel.TargetDescriptor) (TargetReader, error) {
	r.calls++
	return r.prepared, r.err
}

func TestEvaluatePreparedTargetsPreservesErrorSemantics(t *testing.T) {
	release, compiled, view, at, valid := targetBoundaryFixture(t)
	for _, test := range []struct {
		name, reason string
		err          error
		hard         bool
	}{
		{name: "matched"},
		{name: "unavailable", err: ErrUnavailable, reason: "target_resolution_failed"},
		{name: "identity", err: ErrTargetIdentity, reason: "target_identity_invalid"},
		{name: "access", err: ErrAccess, hard: true},
		{name: "canceled", err: context.Canceled, reason: "target_resolution_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fast := &boundaryMembershipTargets{boundaryTargets: &boundaryTargets{resolved: valid}, found: true}
			reader := &boundaryPreparedTargets{boundaryTargets: &boundaryTargets{scopeErr: ErrAccess}, prepared: fast, err: test.err}
			result, err := EvaluateConditions(t.Context(), release, compiled, view, reader, at, false)
			if reader.calls != 1 {
				t.Fatal("target preparation bypassed", reader.calls)
			}
			if test.hard {
				if !errors.Is(err, test.err) {
					t.Fatal("preparation access error hidden", err)
				}
				return
			}
			if err != nil || result.Reason != test.reason || result.Matched != (test.err == nil) || result.Evaluated != (test.err == nil) {
				t.Fatal("preparation changed evaluation semantics", result, err)
			}
			if test.err == nil && (fast.membershipReads != 1 || fast.reads != 0) {
				t.Fatal("prepared O(1) membership not used", fast.membershipReads, fast.reads)
			}
		})
	}
}
