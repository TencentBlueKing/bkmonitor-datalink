// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"encoding/json"
	"testing"
)

// A series is evaluated against every level its Plan declares, or not at all.
//
// That is what makes a Plan result complete: a result built from some of the
// levels would be reported the same way as one built from all of them, and the
// levels that were not evaluated would simply be missing - no error, no gap,
// nothing saying a level was skipped this round.
//
// It had no test until now, and it decides the shape of anything new that wants
// to be evaluated. A no-data series, for instance, has one synthetic point and
// no data for the item's declared levels, so it cannot be handed to this
// evaluator at all - the refusal below is exactly what it would hit. That is a
// fact about this contract rather than a limitation to work around: loosening
// the cover check so such a series could pass would let any incomplete input
// produce a result that looks whole.
func TestAnEvaluationCoversEveryDeclaredLevelOrIsRefused(t *testing.T) {
	evaluator := newEvaluator(t)
	ctx := context.Background()

	// The control: the fixture as it stands, covering the one level the Plan
	// declares, is evaluated. Without this the refusal below could be about
	// anything.
	valid := requestFixture(t, json.RawMessage(`60`), nil)
	if _, err := evaluator.Evaluate(ctx, valid); err != nil {
		t.Fatalf("fixture: a request covering every level was refused: %v", err)
	}

	// The same request with its input addressed to a level the Plan does not
	// declare. Nothing else changes: same Plan, same series, same point.
	uncovered := requestFixture(t, json.RawMessage(`60`), nil)
	uncovered.Inputs[0].Consumer.LevelID = 2
	if _, err := evaluator.Evaluate(ctx, uncovered); err == nil {
		t.Fatal("a series was evaluated against a level its Plan does not declare, and the levels it does " +
			"declare were left unevaluated with nothing saying so")
	}

	// And an input for a level that is declared, sent alongside one that is
	// not, does not buy coverage for the missing one either.
	partial := requestFixture(t, json.RawMessage(`60`), nil)
	extra := partial.Inputs[0]
	extra.Consumer.LevelID = 2
	partial.Inputs = append(partial.Inputs, extra)
	if _, err := evaluator.Evaluate(ctx, partial); err == nil {
		t.Fatal("an input for an undeclared level was accepted beside the declared ones")
	}
}
