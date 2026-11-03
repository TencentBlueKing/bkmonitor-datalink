// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// reconcileFake answers the binding and one reconciliation, after waiting on
// hold when it is set; it keeps the strategy keys it was asked.
type reconcileFake struct {
	mu     sync.Mutex
	result openalerts.Reconciliation
	err    error
	hold   chan struct{}
	asked  []openalerts.StrategyKey
}

func (fake *reconcileFake) inFlight() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return len(fake.asked)
}

func (fake *reconcileFake) Binding(context.Context) (openalerts.TargetBinding, error) {
	return openalerts.TargetBinding{EventSourceID: "source-7"}, nil
}

func (fake *reconcileFake) Reconcile(ctx context.Context, key openalerts.StrategyKey) (openalerts.Reconciliation, error) {
	fake.mu.Lock()
	fake.asked = append(fake.asked, key)
	fake.mu.Unlock()
	if fake.hold != nil {
		select {
		case <-fake.hold:
		case <-ctx.Done():
			return openalerts.Reconciliation{}, ctx.Err()
		}
	}
	return fake.result, fake.err
}

func reconcileResult() openalerts.Reconciliation {
	return openalerts.Reconciliation{EventSourceID: "source-7",
		Members: []string{"fp-both-1", "fp-both-2"}, Missing: []string{"fp-linkd"}, Suppressed: []string{"fp-alarmd-1", "fp-alarmd-2", "fp-alarmd-3"},
		Alerts: []openalerts.Alert{{AlertID: "a1", Fingerprint: "fp-both-1"}, {AlertID: "a2", Fingerprint: "fp-both-2"},
			{AlertID: "a3", Fingerprint: "fp-linkd"}, {AlertID: "a3", Fingerprint: "fp-linkd"}}}
}

func reconcileAnswerOf(t *testing.T, out obchannel.Outcome) (linkdReconcileAnswer, string) {
	t.Helper()
	answer, ok := out.Value.(linkdReconcileAnswer)
	if !ok {
		t.Fatalf("outcome %+v carries no answer", out)
	}
	raw, _ := json.Marshal(answer)
	return answer, string(raw)
}

// The drawer's Linkd block is counts in the server's words and the link keys:
// Linkd's alert ids, once each, and the event source. No fingerprint.
func TestLinkdReconcileCountsBothSidesAndGivesLinkKeysOnly(t *testing.T) {
	fake := &reconcileFake{result: reconcileResult()}
	at := time.Date(2026, 10, 17, 6, 0, 0, 0, time.UTC)
	out := readLinkdReconcile(context.Background(), fake, obchannel.Params{"strategy_id": "77", "tenant": "system"}, func() time.Time { return at }, time.Second, 1<<20)
	answer, encoded := reconcileAnswerOf(t, out)
	want := &linkdReconcileCounts{AlarmdOpen: 5, LinkdActive: 3, Both: 2, OnlyAlarmd: 3, OnlyLinkd: 1}
	if !answer.Answered || !reflect.DeepEqual(answer.Counts, want) || !reflect.DeepEqual(answer.LinkdAlertIDs, []string{"a1", "a2", "a3"}) ||
		answer.EventSourceID != "source-7" || !answer.ReadAt.Equal(at) || !out.Complete {
		t.Fatalf("answer %+v complete %v", answer, out.Complete)
	}
	if line := "alarmd 未恢复 5、Linkd 活跃 3；两边都有 2、只在 alarmd 3、只在 Linkd 1"; answer.Line != line || out.Summary != line {
		t.Fatalf("line %q summary %q", answer.Line, out.Summary)
	}
	if strings.Contains(encoded, "fp-") || strings.Contains(encoded, "fingerprint") {
		t.Fatalf("a fingerprint reached the answer: %s", encoded)
	}
	if !reflect.DeepEqual(fake.asked, []openalerts.StrategyKey{{TenantID: "system", StrategyID: "77"}}) {
		t.Fatalf("asked %v, want the system tenant's 77", fake.asked)
	}
}

// A Linkd that does not answer within the bound is answered=false with no
// counts -- the page shows unknown, never zero -- and is not the call failing.
func TestLinkdReconcileSaysLinkdDidNotAnswerWithoutCounts(t *testing.T) {
	fake := &reconcileFake{result: reconcileResult(), hold: make(chan struct{})}
	out := readLinkdReconcile(context.Background(), fake, obchannel.Params{"strategy_id": "77", "tenant": "system"}, time.Now, 20*time.Millisecond, 1<<20)
	answer, encoded := reconcileAnswerOf(t, out)
	if out.Error != nil || answer.Answered || answer.Counts != nil || out.Complete || answer.Line != linkdReconcileNotAnswered {
		t.Fatalf("a silent Linkd answered %s complete %v error %+v", encoded, out.Complete, out.Error)
	}
	if strings.Contains(encoded, `"counts"`) {
		t.Fatalf("counts reached a silent answer: %s", encoded)
	}
}

// Linkd answering with a refusal is the call failing with Linkd's words, not
// Linkd being silent.
func TestLinkdReconcileRefusalIsAFailureNotSilence(t *testing.T) {
	fake := &reconcileFake{err: &openalerts.LinkRefusal{Status: 400, Code: "invalid_query", Message: "unknown strategy"}}
	out := readLinkdReconcile(context.Background(), fake, obchannel.Params{"strategy_id": "77", "tenant": "system"}, time.Now, time.Second, 1<<20)
	if out.Error == nil || !strings.HasPrefix(out.Error.Code, "linkd_") || !strings.Contains(out.Error.Message, "unknown strategy") {
		t.Fatalf("a refusal answered %+v", out)
	}
}

// Reads are not gated: two drawers open at once both read Linkd, neither is
// refused for the other being in flight.
func TestLinkdReconcileReadsAreNotGated(t *testing.T) {
	fake := &reconcileFake{result: reconcileResult(), hold: make(chan struct{})}
	op := cliLinkdReconcileOperation(fake)
	outs := make(chan obchannel.Outcome, 2)
	for _, id := range []string{"77", "78"} {
		go func(id string) {
			outs <- op.Run(context.Background(), obchannel.Params{"strategy_id": id, "tenant": "system"})
		}(id)
	}
	deadline := testwait.Deadline(t)
	for fake.inFlight() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
	if fake.inFlight() != 2 {
		t.Fatalf("%d reads reached Linkd, want both at once", fake.inFlight())
	}
	close(fake.hold)
	for range 2 {
		if out := <-outs; out.Error != nil {
			t.Fatalf("a concurrent read failed: %+v", out.Error)
		}
	}
}

// The ids stop at their share of the response budget and say so; the counts
// stay whole. Without a Console the operation is listed and unavailable.
func TestLinkdReconcileCutsIdsAtTheirBudgetAndNeedsAConsole(t *testing.T) {
	out := readLinkdReconcile(context.Background(), &reconcileFake{result: reconcileResult()}, obchannel.Params{"strategy_id": "77", "tenant": "system"}, time.Now, time.Second, 6)
	answer, _ := reconcileAnswerOf(t, out)
	if !answer.AlertIDsCut || !reflect.DeepEqual(answer.LinkdAlertIDs, []string{"a1"}) || answer.Counts.LinkdActive != 3 || out.Complete {
		t.Fatalf("cut answer %+v complete %v", answer, out.Complete)
	}
	if availability := cliLinkdReconcileOperation(nil).Availability(); availability.Available ||
		!strings.Contains(availability.Reason, "linkd_console_not_configured") {
		t.Fatalf("without a Console: %+v", availability)
	}
	if !obchannel.FrontReads["strategy.linkd_reconcile"] {
		t.Fatal("strategy.linkd_reconcile is not a front read")
	}
}

// The reconcile has no tenant of its own to fall back on: the tenant is
// required, and says it is read from the strategy's row.
func TestLinkdReconcileRequiresTheRowsTenant(t *testing.T) {
	op := cliLinkdReconcileOperation(nil)
	required := strings.Join(op.Required, ",")
	if required != "strategy_id,tenant" || !strings.Contains(op.Fields["tenant"].Source, "strategy.board") {
		t.Fatalf("required %q tenant source %q", required, op.Fields["tenant"].Source)
	}
}
