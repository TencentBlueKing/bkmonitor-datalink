// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filterclose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/linkdmanage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// DecisionScopeChange is the strategy-wide scope close: the strategy's
// alerts of targets outside its current scope, decided by the control
// leader on its start and on each change of the scope.
const DecisionScopeChange = "scope_changed"

// TargetShape is how a strategy's alerts carry its target: the target
// identity's dimensions (contract.TargetPlanIdentityV1.Dimensions) and its
// model gate, a dimension that must hold one value, when it has one.
type TargetShape struct {
	Dimensions     []string
	ModelDimension string
	ModelValue     string
}

// ErrTargetListTooLarge is a scope whose current targets do not fit one
// request. The filter is not cut: a must_not split across requests would
// close, in each, the alerts of the targets the other requests name.
var ErrTargetListTooLarge = errors.New("alarmd filterclose: the current targets do not fit one filter")

// ScopeFilter is the strategy's alerts that carry every dimension of the
// shape (exists on each, and the model gate's value) and are of none of the
// current targets: the targets outside the scope. An alert without the
// target's dimensions - aggregated past them, or of another shape - is
// never selected: exists keeps the must_not from matching it. The leaf
// clauses the shape and the targets make are held to maxTargetClauses and
// their bytes to maxTargetBodyBytes, the same half of the bounds a target
// batch keeps; past either it is ErrTargetListTooLarge and nothing is
// built.
func ScopeFilter(scope Scope, shape TargetShape, current []Target) (Filter, error) {
	if len(shape.Dimensions) == 0 {
		return Filter{}, errors.New("alarmd filterclose: a target shape names at least one dimension")
	}
	names := sorted(shape.Dimensions)
	for i, name := range names {
		if name == "" || (i > 0 && names[i-1] == name) {
			return Filter{}, errors.New("alarmd filterclose: a target shape's dimensions are named and distinct")
		}
	}
	filter := make([]any, 0, len(names)+1)
	for _, name := range names {
		filter = append(filter, exists("dimensions."+name))
	}
	if shape.ModelDimension != "" {
		filter = append(filter, term("dimensions."+shape.ModelDimension, shape.ModelValue))
	}
	clauses := len(filter) + len(current)*len(names)
	if clauses > maxTargetClauses {
		return Filter{}, ErrTargetListTooLarge
	}
	mustNot := make([]any, 0, len(current))
	size := 0
	for _, target := range current {
		if len(target) != len(names) {
			return Filter{}, errors.New("alarmd filterclose: a current target names every dimension of the shape")
		}
		match := make([]any, 0, len(names))
		for _, name := range names {
			value, ok := target[name]
			if !ok {
				return Filter{}, errors.New("alarmd filterclose: a current target names every dimension of the shape")
			}
			match = append(match, term("dimensions."+name, value))
		}
		clause := object{"bool": object{"filter": match}}
		encoded, err := json.Marshal(clause)
		if err != nil {
			return Filter{}, err
		}
		if size += len(encoded) + 1; size > maxTargetBodyBytes {
			return Filter{}, ErrTargetListTooLarge
		}
		mustNot = append(mustNot, clause)
	}
	return build(scope, filter, mustNot)
}

// ScopeChange closes the strategy's alerts of targets outside its current
// scope, in one filter (ScopeFilter): refused, counted, without a request
// where the targets do not fit (OutcomeTargetListTooLarge). The expectation
// is the strategy record's entries whose target is outside the current
// ones; without a record each alert the filter pages is decided from its
// own dimensions and closed by its id. digest names the scope the decision
// is about, so a new scope is a new decision with its own command. period
// is how often the strategy is evaluated (settle).
func (closer *Closer) ScopeChange(ctx context.Context, scope Scope, shape TargetShape, current []Target, revision int64, digest string,
	period time.Duration) string {
	return closer.ScopeChangeResult(ctx, scope, shape, current, revision, digest, period).Word
}

// ScopeChangeResult is ScopeChange's Result: every part's word and what was
// closed.
func (closer *Closer) ScopeChangeResult(ctx context.Context, scope Scope, shape TargetShape, current []Target, revision int64, digest string,
	period time.Duration) Result {
	filter, err := ScopeFilter(scope, shape, current)
	switch {
	case errors.Is(err, ErrTargetListTooLarge):
		closer.count(OutcomeTargetListTooLarge, 1)
		return only(OutcomeTargetListTooLarge, jobTally{})
	case err != nil:
		closer.count(OutcomeFilterRefused, 1)
		return only(OutcomeFilterRefused, jobTally{})
	}
	names := sorted(shape.Dimensions)
	inScope := make(map[string]struct{}, len(current))
	for _, target := range current {
		inScope[targetText(names, target)] = struct{}{}
	}
	outside := func(target map[string]string) bool {
		for _, name := range names {
			if target[name] == "" {
				return false
			}
		}
		_, kept := inScope[targetText(names, target)]
		return !kept
	}
	match := func(entry openepisodes.Entry) bool { return len(entry.Target) > 0 && outside(entry.Target) }
	decide := func(alert linkdmanage.Alert) bool {
		if shape.ModelDimension != "" {
			if value, ok := openepisodes.Keyword(alert.Dimensions[shape.ModelDimension]); !ok || value != shape.ModelValue {
				return false
			}
		}
		target := openepisodes.TargetOf(names, alert.Dimensions)
		return target != nil && outside(target)
	}
	return closer.recorded(ctx, Job{Filter: filter, Decision: DecisionScopeChange, Digest: digest, Revision: revision, period: period,
		Reason: fmt.Sprintf("alarmd: strategy %s target out of scope", scope.StrategyID)}, match, decide)
}

// targetText is a target's values in the shape's order, unambiguous.
func targetText(names []string, target map[string]string) string {
	values := make([]string, 0, len(names))
	for _, name := range names {
		values = append(values, target[name])
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}
