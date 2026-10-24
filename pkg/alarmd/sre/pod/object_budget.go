// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pod

import (
	"context"
	"time"
)

// objectBudget decides whether one more object of a manifest may start before
// the operation's deadline. The apiserver's latency is not known in advance,
// so the margin is learned from this run: twice the slowest object so far.
// The first object always starts. An object that would not finish is not
// started: the run stops between objects, answers what it did, and names
// where to resume, rather than being cut mid-object and losing the answer.
type objectBudget struct {
	deadline time.Time
	bounded  bool
	slowest  time.Duration
	now      func() time.Time
}

func newObjectBudget(ctx context.Context, now func() time.Time) *objectBudget {
	deadline, bounded := ctx.Deadline()
	return &objectBudget{deadline: deadline, bounded: bounded, now: now}
}

// canStart says whether the next object has room.
func (budget *objectBudget) canStart() bool {
	if !budget.bounded || budget.slowest == 0 {
		return true
	}
	return budget.now().Add(2 * budget.slowest).Before(budget.deadline)
}

// took records how long one object took.
func (budget *objectBudget) took(elapsed time.Duration) {
	if elapsed > budget.slowest {
		budget.slowest = elapsed
	}
}
