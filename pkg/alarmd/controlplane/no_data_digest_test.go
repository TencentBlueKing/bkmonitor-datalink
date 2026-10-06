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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A Plan that does not detect no-data must digest exactly as it did before the
// field existed.
//
// The ObjectDigest names execution content, and StateGeneration is derived from
// the same content at activation: a digest that moves for every Plan re-keys
// every Plan's runtime state on the rollout that introduces it. Adding the
// field with omitempty keeps the absent case byte-identical, so only the Plans
// that actually enable no-data get a new digest - which is what should happen,
// since their execution did change.
//
// The literal below was measured on the tree before the field was declared. It
// is pinned rather than recomputed because a digest compared against one
// computed in the same run agrees with itself no matter what moved; only a
// value carried across the change can say the absent case did not move.
//
// Two mutations were run against it, and both had to be made to compile and to
// actually apply before their verdict meant anything - the first attempt at
// each reported SURVIVED for neither reason. Dropping omitempty moves the
// digest to 558f636f... and this test fails. Building the object with a
// non-nil zero config survived this half, which constructs the object
// directly; the test below goes through the builder and catches it.
//
// If this fails, the change under it re-keys detection state fleet-wide on
// rollout, and the question to answer first is whether that is intended rather
// than how to update the constant.
func TestPlanWithoutNoDataDigestsAsItDidBeforeTheFieldExisted(t *testing.T) {
	const beforeTheField = "ad18d34f1af87e356277a6cb3cae7c6e74ff3b838b01fb5e3e9788783c3da06d"

	digest, err := contract.DeriveCanonicalDigestV2("probe", controlplane.QueryGroupPlanObject{PlanID: "1001"})
	if err != nil {
		t.Fatalf("DeriveCanonicalDigestV2() error = %v", err)
	}
	if digest != beforeTheField {
		t.Fatalf("digest = %s, want %s: a Plan that does not detect no-data changed its execution content, "+
			"which re-keys its runtime state on rollout", digest, beforeTheField)
	}

	enabled := controlplane.QueryGroupPlanObject{
		PlanID: "1001", NoData: &contract.NoDataConfigV1{Continuous: 5, Level: 2},
	}
	enabledDigest, err := contract.DeriveCanonicalDigestV2("probe", enabled)
	if err != nil {
		t.Fatalf("DeriveCanonicalDigestV2() error = %v", err)
	}
	if enabledDigest == digest {
		t.Fatal("enabling no-data left the execution digest where it was; the setting is not in the digest")
	}
}

// The builder must carry the Plan's own setting rather than always attaching
// one. An object built from a Plan that does not detect no-data has to come out
// with the section absent, or every Plan's digest moves on the rollout while
// the direct-construction check above stays green.
func TestBuildQueryGroupObjectCarriesTheNoDataSettingItWasGiven(t *testing.T) {
	built := controlplane.BuildQueryGroupObject(controlplane.QueryGroup{
		Plans: []controlplane.FrozenPlan{{Plan: contract.EvaluationPlanV2{PlanID: "1001"}}},
	})
	if len(built.Plans) != 1 {
		t.Fatalf("built %d Plans, want one", len(built.Plans))
	}
	if built.Plans[0].NoData != nil {
		t.Fatalf("a Plan that does not detect no-data was built with %+v", built.Plans[0].NoData)
	}

	enabled := controlplane.BuildQueryGroupObject(controlplane.QueryGroup{
		Plans: []controlplane.FrozenPlan{{Plan: contract.EvaluationPlanV2{
			PlanID: "1001", NoData: &contract.NoDataConfigV1{Continuous: 5, Level: 2},
		}}},
	})
	if got := enabled.Plans[0].NoData; got == nil || got.Continuous != 5 || got.Level != 2 {
		t.Fatalf("built no-data setting = %+v, want the Plan's own", got)
	}
}
