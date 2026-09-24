// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "errors"

// PlanViewFor is the one place that decides which levels a series is judged
// against, and it decides it by choosing which view of the Plan everything
// downstream sees.
//
// That question is asked in sixteen places across five packages - the worker's
// own EffectiveTime binding, the evaluator, the detector, the trigger and this
// package's validators - and all sixteen ask it the same way, by calling
// Levels() on the Plan they were handed. Threading a level set through them
// would have left the seventeenth, added later by someone with no reason to
// know the rule existed. Answering the existing question differently leaves
// nothing to thread.
//
// It lives here rather than in either caller because both the worker and the
// evaluator need it, and two copies of a decision are not one decision. A test
// fails if anything outside this function chooses a view.
func PlanViewFor(due DuePlan, kind SeriesKind) (DuePlan, error) {
	switch kind {
	case SeriesKindReal:
		return due, nil
	case SeriesKindNoData:
		view := due.CompiledPlan.NoDataView()
		if view == nil {
			// A synthetic series for a Plan that does not detect no-data. The
			// worker builds these from the Plan's own configuration, so this is
			// the two disagreeing, and guessing which is right would either
			// judge absence a strategy never asked for or feed an answer to a
			// level expecting a measurement.
			return DuePlan{}, errors.New("alarmd execution: no-data series for a Plan with no no-data level")
		}
		due.CompiledPlan = view
		// The view's records are held to the no-data Level's refs, which
		// are published as their own set; the declared Levels' refs would
		// name the source Level this one shares an ID with.
		due.LevelContractRefs = due.NoDataLevelContractRefs
		due.NoDataLevelContractRefs = nil
		return due, nil
	default:
		return DuePlan{}, errors.New("alarmd execution: unknown series kind")
	}
}
