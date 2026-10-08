// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

func dependencyFixture() DependencyRequest {
	return DependencyRequest{TenantID: "tenant", Policy: domain.PolicyVersion{ID: "dependency", Version: 1, Digest: strings.Repeat("a", 64)}, Candidate: domain.DependencyMain{AlertID: "main-a", EventID: "event-a", EventSourceID: "source-a", Fingerprint: "fp-a", Severity: "warning"}}
}

func TestRedisDependencyAtomicRegistrationAndTerminalCleanup(t *testing.T) {
	s := redisClipStore(t)
	r := dependencyFixture()
	type result struct {
		request     DependencyRequest
		reservation DependencyReservation
		err         error
	}
	out := make(chan result, 32)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			v := r
			v.Candidate.AlertID = fmt.Sprintf("alert-%d", i)
			v.Candidate.EventID = fmt.Sprintf("event-%d", i)
			v.Candidate.EventSourceID = fmt.Sprintf("source-%d", i)
			d, err := s.ClaimDependencyMain(t.Context(), v)
			out <- result{v, d, err}
		})
	}
	wg.Wait()
	close(out)
	var winner result
	n := 0
	for v := range out {
		if v.err != nil {
			t.Fatal(v.err)
		}
		if v.reservation.Role == "candidate" {
			winner = v
			n++
		} else if v.reservation.Role != "pending" {
			t.Fatal("pending candidate visible as registered")
		}
	}
	if n != 1 {
		t.Fatal("multiple first candidates", n)
	}
	if _, ok, err := s.GetDependencyMain(t.Context(), "other-tenant", r.Policy); err != nil || ok {
		t.Fatal("tenant leaked", err)
	}
	d, ok, err := s.GetDependencyMain(t.Context(), r.TenantID, r.Policy)
	if err != nil || !ok || d.Role != "pending" {
		t.Fatal("missing pending", err)
	}
	if ok, err := s.CommitDependencyMain(t.Context(), winner.request); err != nil || !ok {
		t.Fatal("could not commit", err)
	}
	d, ok, err = s.GetDependencyMain(t.Context(), r.TenantID, r.Policy)
	if err != nil || !ok || d.Role != "registered" || d.Main != winner.request.Candidate {
		t.Fatal("wrong registered main", err)
	}
	if ok, err := s.CommitDependencyMain(t.Context(), winner.request); err != nil || !ok {
		t.Fatal("commit retry not idempotent", err)
	}
	changed := winner.request
	changed.Candidate.Severity = "critical"
	if _, err := s.ClaimDependencyMain(t.Context(), changed); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("same event accepted changed candidate", err)
	}
	// 清理旧 Event 后新 Event 可重新登记；迟到的旧释放和旧主终态清理不删除新主。
	if ok, err := s.ReleaseDependencyMain(t.Context(), winner.request); err != nil || !ok {
		t.Fatal("cannot release", err)
	}
	r.Candidate.AlertID = "new-main"
	r.Candidate.EventID = "new-event"
	if _, err := s.ClaimDependencyMain(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ReleaseDependencyMain(t.Context(), winner.request); err != nil || ok {
		t.Fatal("stale release removed new main", err)
	}
	if n, err := s.ClearDependencyOwner(t.Context(), r.TenantID, winner.request.Candidate.AlertID); err != nil || n != 0 {
		t.Fatal("old cleanup removed new main", err)
	}
	if n, err := s.ClearDependencyOwner(t.Context(), r.TenantID, r.Candidate.AlertID); err != nil || n != 1 {
		t.Fatal("new owner cleanup failed", err)
	}
}

func TestRedisDependencyLostWindowAndCanceledCalls(t *testing.T) {
	s := redisClipStore(t)
	r := dependencyFixture()
	if _, err := s.ClaimDependencyMain(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	// 模拟缓存丢失；旧提交不可重新创立过期窗口，新的 Event 按正常规则登记。
	if err := s.client.Del(t.Context(), s.dependencyKey(r.TenantID, r.Policy)).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CommitDependencyMain(t.Context(), r); err != nil || ok {
		t.Fatal("commit resurrected lost candidate", err)
	}
	r.Candidate.EventID = "new-event"
	if _, err := s.ClaimDependencyMain(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := s.GetDependencyMain(ctx, r.TenantID, r.Policy); err == nil {
		t.Fatal("ignored cancellation")
	}
	if err := s.client.Set(t.Context(), s.dependencyKey(r.TenantID, r.Policy), `{"state":"registered","main":{}}`, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetDependencyMain(t.Context(), r.TenantID, r.Policy); !errors.Is(err, ErrState) {
		t.Fatal("corrupt state became empty", err)
	}
}
