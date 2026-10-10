// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The predicate that decides whether an empty Slot counts towards a warmup,
// asked directly, including of shapes the binding contract refuses upstream.
//
// It is asked directly because the two halves of its last condition are not
// independent today: validateSeriesBindingAvailability already makes AVAILABLE
// imply FULL, so a Slot reaching this predicate through the coordinator can
// never separate "not FULL" from "not AVAILABLE", and a mutant deleting the
// completeness half survives every end-to-end case. The half is here so that
// this predicate is right on its own terms rather than on a rule in another
// file, and the only way to say that is to hand it the shape that rule
// forbids.
func TestAnEmptySlotCountsTowardsAWarmupOnlyWhenEveryBindingIsWhole(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	whole := func(role execution.InputRole) execution.NamedInputBinding {
		return execution.NamedInputBinding{
			Consumer: execution.ConsumerRef{Plan: plan}, Role: role,
			Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty,
			Disposition: execution.AccessAvailable,
		}
	}
	for name, testCase := range map[string]struct {
		bindings []execution.NamedInputBinding
		want     bool
	}{
		"every binding whole": {
			[]execution.NamedInputBinding{whole(execution.InputRolePrimary), whole(execution.InputRoleAlgorithmDependency)}, true,
		},
		"a dependency that did not answer": {
			[]execution.NamedInputBinding{whole(execution.InputRolePrimary), func() execution.NamedInputBinding {
				binding := whole(execution.InputRoleAlgorithmDependency)
				binding.Completeness, binding.DataState = execution.CompletenessUnavailable, execution.DataStateUnknown
				binding.Disposition = execution.AccessUnavailable
				return binding
			}()}, false,
		},
		// FULL and degraded: the disposition alone refuses this one, and the
		// completeness half would not.
		"a dependency answered whole but degraded": {
			[]execution.NamedInputBinding{whole(execution.InputRolePrimary), func() execution.NamedInputBinding {
				binding := whole(execution.InputRoleAlgorithmDependency)
				binding.Disposition = execution.AccessDegraded
				return binding
			}()}, false,
		},
		// PARTIAL and available: the shape the binding contract refuses, and
		// the one the completeness half is here for.
		"a dependency answered in part and called itself available": {
			[]execution.NamedInputBinding{whole(execution.InputRolePrimary), func() execution.NamedInputBinding {
				binding := whole(execution.InputRoleAlgorithmDependency)
				binding.Completeness = execution.CompletenessPartial
				return binding
			}()}, false,
		},
		// Another Plan's bindings are not this Plan's evidence either way.
		"another Plan's dependency did not answer": {
			[]execution.NamedInputBinding{whole(execution.InputRolePrimary), func() execution.NamedInputBinding {
				binding := whole(execution.InputRoleAlgorithmDependency)
				binding.Consumer.Plan.BusinessID = "3"
				binding.Completeness, binding.DataState = execution.CompletenessUnavailable, execution.DataStateUnknown
				binding.Disposition = execution.AccessUnavailable
				return binding
			}()}, true,
		},
		"no PRIMARY binding at all": {
			[]execution.NamedInputBinding{whole(execution.InputRoleAlgorithmDependency)}, false,
		},
	} {
		if got := emptySourceSlotIsWholeInput(testCase.bindings, plan); got != testCase.want {
			t.Errorf("%s: emptySourceSlotIsWholeInput() = %v, want %v", name, got, testCase.want)
		}
	}
}
