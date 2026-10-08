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
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy/redisstate"
	"linkd/internal/suppressioncleanup"
)

// state 只由一次串行模拟使用，生命周期与计时检查共用该实例；从不共享到其他请求。
// 模拟使用虚拟时间清理保留期，身份/去重/边界与正式 Redis 状态由同场景对照测试保护。
type state struct {
	now            time.Time
	clips          map[string]*clip
	clipOps        map[string]clipOperation
	aggregations   map[string]redisstate.AggregationDecision
	aggregationOps map[string]redisstate.AggregationDecision
	windows        map[string]redisstate.MergeWindow
	groups         map[string]string
}

type clip struct {
	identity     redisstate.Identity
	epoch, owner string
	expires      time.Time
	events       map[string]int64
}

type clipOperation struct {
	decision redisstate.ClipDecision
	expires  time.Time
}

func newState() *state {
	return &state{clips: map[string]*clip{}, clipOps: map[string]clipOperation{}, aggregations: map[string]redisstate.AggregationDecision{}, aggregationOps: map[string]redisstate.AggregationDecision{}, windows: map[string]redisstate.MergeWindow{}, groups: map[string]string{}}
}

func hash(parts ...any) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func clipKey(r redisstate.ClipRequest) string {
	return hash("clip", r.Identity, r.PolicyID, r.Version, r.Digest)
}

func (s *state) Clip(ctx context.Context, r redisstate.ClipRequest) (redisstate.ClipDecision, error) {
	if err := ctx.Err(); err != nil {
		return redisstate.ClipDecision{}, err
	}
	key := clipKey(r)
	op := hash(key, r.EventID)
	if previous, ok := s.clipOps[op]; ok && s.now.Before(previous.expires) {
		d := previous.decision
		d.Replayed = true
		return d, nil
	}
	c := s.clips[key]
	if c == nil || !s.now.Before(c.expires) {
		c = &clip{identity: r.Identity, epoch: r.EventID, events: map[string]int64{}}
		s.clips[key] = c
	}
	at := r.At.UnixMilli()
	if saved, ok := c.events[r.EventID]; ok {
		at = saved
	}
	for id, t := range c.events {
		if t < at-r.Duration.Milliseconds() {
			delete(c.events, id)
		}
	}
	c.events[r.EventID] = at
	count := 0
	for _, t := range c.events {
		if t <= at && t >= at-r.Duration.Milliseconds() {
			count++
		}
	}
	c.expires = s.now.Add(r.Duration + time.Minute)
	d := redisstate.ClipDecision{Allowed: count >= r.Threshold, Count: count, Threshold: r.Threshold, EvaluatedAtMillis: at, Epoch: c.epoch, CounterID: key}
	s.clipOps[op] = clipOperation{decision: d, expires: c.expires}
	return d, nil
}

func (s *state) BindClipOwner(ctx context.Context, r redisstate.ClipRequest, d redisstate.ClipDecision, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c := s.clips[clipKey(r)]; c != nil && c.epoch == d.Epoch {
		c.owner = id
	}
	return nil
}

func (s *state) ClearClipIdentity(ctx context.Context, identity redisstate.Identity, owner string) ([]suppressioncleanup.Window, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []suppressioncleanup.Window{}
	for key, c := range s.clips {
		if c.identity == identity && c.owner == owner {
			out = append(out, suppressioncleanup.Window{ID: hash(identity.SourceID, identity.Fingerprint) + ":" + key, Epoch: c.epoch})
			delete(s.clips, key)
		}
	}
	return out, nil
}

func aggregationKey(r redisstate.AggregationRequest) string {
	return hash("aggregation", r.TenantID, r.PolicyID, r.Version, r.Digest, r.GroupKey)
}

func (s *state) ClaimAggregation(ctx context.Context, r redisstate.AggregationRequest) (redisstate.AggregationDecision, error) {
	if err := ctx.Err(); err != nil {
		return redisstate.AggregationDecision{}, err
	}
	key := aggregationKey(r)
	op := hash(key, r.EventID)
	if d, ok := s.aggregationOps[op]; ok && s.now.UnixMilli() < d.ExpiresAtMillis+60000 {
		d.Replayed = true
		return d, nil
	}
	d, ok := s.aggregations[key]
	if !ok || r.At.UnixMilli() > d.ExpiresAtMillis {
		d = redisstate.AggregationDecision{Role: "candidate", WindowID: key, Epoch: r.EventID, OwnerEventID: r.EventID, OwnerAlertID: r.CandidateAlertID, OwnerSourceID: r.SourceID, OwnerFingerprint: r.Fingerprint, StartedAtMillis: r.At.UnixMilli(), ExpiresAtMillis: r.At.Add(r.Duration).UnixMilli()}
		s.aggregations[key] = d
		return d, nil
	}
	if d.Role == "candidate" && d.OwnerEventID != r.EventID {
		d.Role = "pending"
	}
	return d, nil
}

func (s *state) CommitAggregationOwner(ctx context.Context, r redisstate.AggregationRequest, d redisstate.AggregationDecision) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	current, ok := s.aggregations[d.WindowID]
	if !ok || current.Epoch != d.Epoch {
		return false, nil
	}
	current.Role = "owner"
	s.aggregations[d.WindowID] = current
	return true, nil
}

func (s *state) ConfirmAggregationSuppression(ctx context.Context, r redisstate.AggregationRequest, d redisstate.AggregationDecision) (redisstate.AggregationDecision, bool, error) {
	if err := ctx.Err(); err != nil {
		return d, false, err
	}
	current, ok := s.aggregations[d.WindowID]
	if !ok || current.Epoch != d.Epoch || current.Role != "owner" {
		return d, false, nil
	}
	d.Role = "suppressed"
	s.aggregationOps[hash(aggregationKey(r), r.EventID)] = d
	return d, true, nil
}

func (s *state) ReleaseAggregation(ctx context.Context, _ string, d redisstate.AggregationDecision) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	current, ok := s.aggregations[d.WindowID]
	if ok && current.Epoch == d.Epoch {
		delete(s.aggregations, d.WindowID)
		return true, nil
	}
	return false, nil
}

func (s *state) ClearAggregationOwner(ctx context.Context, _ string, owner string) ([]suppressioncleanup.Window, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := []suppressioncleanup.Window{}
	for id, d := range s.aggregations {
		if d.OwnerAlertID == owner {
			out = append(out, suppressioncleanup.Window{ID: id, Epoch: d.Epoch})
			delete(s.aggregations, id)
		}
	}
	return out, nil
}

func (s *state) JoinMergeWindow(ctx context.Context, r redisstate.MergeRequest) (domain.MergeWait, error) {
	if err := ctx.Err(); err != nil {
		return domain.MergeWait{}, err
	}
	group := hash(r.TenantID, r.Policy, r.GroupKey)
	w, ok := s.windows[s.groups[group]]
	if !ok || w.Frozen != nil || r.At.UnixMilli() >= w.DeadlineMillis {
		w = redisstate.MergeWindow{ID: hash("merge", group, r.Member.EventID), TenantID: r.TenantID, Policy: r.Policy, GroupKey: r.GroupKey, StartedAtMillis: r.At.UnixMilli(), DeadlineMillis: r.At.Add(r.Duration).UnixMilli(), GroupCount: r.GroupCount, Cyclic: r.Cyclic, Revision: 1}
		s.groups[group] = w.ID
	}
	m := redisstate.MergeMember{Main: r.Member, Groups: slices.Clone(r.Groups), FirstAtMillis: r.At.UnixMilli()}
	found := false
	for _, existing := range w.Members {
		if existing.Main.AlertID == r.Member.AlertID {
			m = existing
			found = true
			break
		}
	}
	if !found {
		w.Members = append(w.Members, m)
		w.Revision++
	}
	s.windows[w.ID] = w
	return domain.MergeWait{WindowID: w.ID, Policy: w.Policy, GroupKey: w.GroupKey, MemberEventID: m.Main.EventID, Severity: m.Main.Severity, Groups: slices.Clone(m.Groups), StartedAt: time.UnixMilli(w.StartedAtMillis), Deadline: time.UnixMilli(w.DeadlineMillis)}, nil
}

func (s *state) CommitMergeMember(ctx context.Context, _ string, alertID string, wait domain.MergeWait) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	w, ok := s.windows[wait.WindowID]
	if !ok {
		return false, nil
	}
	for i := range w.Members {
		if w.Members[i].Main.AlertID == alertID {
			w.Members[i].Committed = true
			w.Revision++
			s.windows[w.ID] = w
			return true, nil
		}
	}
	return false, nil
}

func (s *state) ReadMergeWindow(ctx context.Context, _ string, id string) (redisstate.MergeWindow, bool, error) {
	if err := ctx.Err(); err != nil {
		return redisstate.MergeWindow{}, false, err
	}
	w, ok := s.windows[id]
	w.Members = slices.Clone(w.Members)
	slices.SortFunc(w.Members, func(a, b redisstate.MergeMember) int {
		if a.Main.AlertID < b.Main.AlertID {
			return -1
		}
		if a.Main.AlertID > b.Main.AlertID {
			return 1
		}
		return 0
	})
	return w, ok, nil
}

func (s *state) FreezeMergeWindow(ctx context.Context, tenant, id string, revision int64, at time.Time, candidates []redisstate.MergeCandidate) (redisstate.MergeWindow, bool, error) {
	if err := ctx.Err(); err != nil {
		return redisstate.MergeWindow{}, false, err
	}
	w, ok := s.windows[id]
	if !ok || w.Revision != revision {
		return w, false, nil
	}
	if w.Frozen != nil {
		return w, true, nil
	}
	if at.UnixMilli() < w.StartedAtMillis || w.Cyclic && at.UnixMilli() < w.DeadlineMillis {
		return w, false, nil
	}
	groups := map[int]bool{}
	members := []string{}
	for _, c := range candidates {
		members = append(members, c.AlertID)
		for _, g := range c.Groups {
			groups[g] = true
		}
	}
	success := len(members) >= 2 && len(groups) == w.GroupCount
	if !success && at.UnixMilli() < w.DeadlineMillis {
		return w, false, nil
	}
	outcome := "failed"
	if success {
		outcome = "succeeded"
	}
	operation, err := domain.MergeDecisionID(tenant, id)
	if err != nil {
		return w, false, err
	}
	slices.Sort(members)
	w.Frozen = &redisstate.MergeVerdict{Outcome: outcome, OperationID: operation, AtMillis: at.UnixMilli(), MemberIDs: members}
	w.Revision++
	s.windows[id] = w
	return w, true, nil
}
