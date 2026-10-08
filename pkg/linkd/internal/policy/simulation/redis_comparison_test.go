// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package simulation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/domain"
	"linkd/internal/policy/redisstate"
)

func comparisonRedis(t *testing.T) *redisstate.Store {
	t.Helper()
	address := os.Getenv("LINKD_TEST_REDIS_ADDRESS")
	if address == "" {
		t.Skip("set LINKD_TEST_REDIS_ADDRESS for simulation/real Redis comparison")
	}
	client := redis.NewClient(&redis.Options{Addr: address, Password: os.Getenv("LINKD_TEST_REDIS_PASSWORD"), ContextTimeoutEnabled: true, MaxRetries: -1})
	deployment := fmt.Sprintf("simulation-comparison-%d-%d", os.Getpid(), time.Now().UnixNano())
	state, err := redisstate.New(client, deployment)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]string{"deployment", deployment})
	sum := sha256.Sum256(raw)
	prefix := fmt.Sprintf("linkd:policies:%x:*", sum)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var cursor uint64
		for {
			keys, next, err := client.Scan(ctx, cursor, prefix, 100).Result()
			if err != nil {
				t.Error(err)
				break
			}
			if len(keys) > 0 {
				if err := client.Del(ctx, keys...).Err(); err != nil {
					t.Error(err)
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		_ = client.Close()
	})
	return state
}

func TestSimulationMatchesRedisCountingAndMergeDecisions(t *testing.T) {
	real := comparisonRedis(t)
	sim := newState()
	base := redisstate.ClipRequest{Identity: redisstate.Identity{TenantID: "tenant", SourceID: "source", Fingerprint: "f"}, PolicyID: "p", Version: 1, Digest: strings.Repeat("a", 64), Duration: time.Minute, Threshold: 3}
	for i, sec := range []int{0, 30, 60, 61, 61, 120} {
		r := base
		r.At = start.Add(time.Duration(sec) * time.Second)
		r.EventID = fmt.Sprint(i)
		if i == 4 {
			r.EventID = "3"
		}
		sim.now = r.At
		a, err := sim.Clip(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		b, err := real.Clip(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		if a.Count != b.Count || a.Allowed != b.Allowed || a.Replayed != b.Replayed || a.EvaluatedAtMillis != b.EvaluatedAtMillis {
			t.Fatalf("counter %d sim=%+v redis=%+v", sec, a, b)
		}
	}
	for _, cyclic := range []bool{false, true} {
		r := redisstate.MergeRequest{TenantID: "tenant", Policy: domain.PolicyVersion{ID: fmt.Sprintf("merge-%t", cyclic), Version: 1, Digest: base.Digest}, GroupKey: strings.Repeat("b", 64), Member: domain.DependencyMain{AlertID: "a", EventID: "a", EventSourceID: "source", Fingerprint: "a", Severity: "warning"}, Groups: []int{0}, GroupCount: 2, At: start, Duration: time.Minute, Cyclic: cyclic}
		a, err := sim.JoinMergeWindow(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		b, err := real.JoinMergeWindow(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sim.CommitMergeMember(t.Context(), "tenant", "a", a); err != nil {
			t.Fatal(err)
		}
		if _, err := real.CommitMergeMember(t.Context(), "tenant", "a", b); err != nil {
			t.Fatal(err)
		}
		r.Member = domain.DependencyMain{AlertID: "b", EventID: "b", EventSourceID: "other", Fingerprint: "b", Severity: "warning"}
		r.Groups = []int{1}
		r.At = start.Add(time.Second)
		secondA, err := sim.JoinMergeWindow(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		secondB, err := real.JoinMergeWindow(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sim.CommitMergeMember(t.Context(), "tenant", "b", secondA); err != nil {
			t.Fatal(err)
		}
		if _, err := real.CommitMergeMember(t.Context(), "tenant", "b", secondB); err != nil {
			t.Fatal(err)
		}
		candidates := []redisstate.MergeCandidate{{AlertID: "a", Groups: []int{0}}, {AlertID: "b", Groups: []int{1}}}
		for _, at := range []time.Time{start.Add(time.Second), start.Add(time.Minute)} {
			wa, _, err := sim.ReadMergeWindow(t.Context(), "tenant", a.WindowID)
			if err != nil {
				t.Fatal(err)
			}
			wb, _, err := real.ReadMergeWindow(t.Context(), "tenant", b.WindowID)
			if err != nil {
				t.Fatal(err)
			}
			ja, fa, err := sim.FreezeMergeWindow(t.Context(), "tenant", a.WindowID, wa.Revision, at, candidates)
			if err != nil {
				t.Fatal(err)
			}
			jb, fb, err := real.FreezeMergeWindow(t.Context(), "tenant", b.WindowID, wb.Revision, at, candidates)
			if err != nil {
				t.Fatal(err)
			}
			if fa != fb || (fa && (ja.Frozen.Outcome != jb.Frozen.Outcome || len(ja.Frozen.MemberIDs) != len(jb.Frozen.MemberIDs))) {
				t.Fatalf("merge parity cyclic=%t sim=%+v redis=%+v", cyclic, ja, jb)
			}
		}
	}
}

func TestSimulationAggregationExpiryMatchesRedis(t *testing.T) {
	real := comparisonRedis(t)
	sim := newState()
	r := redisstate.AggregationRequest{Identity: redisstate.Identity{TenantID: "tenant", SourceID: "source", Fingerprint: "f"}, PolicyID: "agg", Version: 1, Digest: strings.Repeat("a", 64), GroupKey: strings.Repeat("b", 64), EventID: "owner", CandidateAlertID: "alert", At: start, Duration: time.Minute}
	sim.now = start
	a, err := sim.ClaimAggregation(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := real.ClaimAggregation(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sim.CommitAggregationOwner(t.Context(), r, a); err != nil {
		t.Fatal(err)
	}
	if _, err := real.CommitAggregationOwner(t.Context(), r, b); err != nil {
		t.Fatal(err)
	}
	for i, sec := range []int{59, 60, 61} {
		r.EventID = fmt.Sprint(i)
		r.CandidateAlertID = "other" + r.EventID
		r.SourceID = "other"
		r.At = start.Add(time.Duration(sec) * time.Second)
		sim.now = r.At
		a, err = sim.ClaimAggregation(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		b, err = real.ClaimAggregation(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		if a.Role != b.Role {
			t.Fatalf("expiry %d sim=%s real=%s", sec, a.Role, b.Role)
		}
	}
}
