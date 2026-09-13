// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Limits on what an observation window may ask for.
//
// The ceiling on how long one may stay open is what makes it a window rather
// than configuration: a window that never expires is the static whitelist again,
// just written from a different place.
const (
	// MaxWindowTTL bounds how long one window stays open.
	MaxWindowTTL = 30 * time.Minute
	// MaxOpenWindows bounds how many objects may be observed at once. Windows
	// are now the only way anything is selected, so the whole diagnostic budget
	// is theirs: the per-minute record and byte budgets are shared across
	// everything observed, and that sharing is what the limit is protecting.
	MaxOpenWindows = observability.TargetFlowMaxGroups
)

// Window is one object being observed, and who asked for it.
//
// OpenedBy is recorded because a window costs shared diagnostic budget and
// expires on its own: without it, an operator finding an object observed has no
// way to tell whether someone is mid-investigation or something was left behind.
type Window struct {
	QueryGroup string    `json:"query_group"`
	OpenedBy   string    `json:"opened_by"`
	OpenedAt   time.Time `json:"opened_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// WindowStore keeps the open windows in the control plane, where every replica
// reads them on the tick it already runs.
//
// Expiry lives in a sorted set scored by deadline rather than in a key TTL over
// the whole set, so two people opening different windows at the same moment
// cannot overwrite each other: each writes only its own member. The audit fields
// sit in a companion hash keyed the same way.
type WindowStore struct {
	client   redis.Cmdable
	deadline string
	audit    string
}

// NewWindowStore builds the store. The prefix is the caller's namespace, the
// same one the other phase-two objects use.
func NewWindowStore(client redis.Cmdable, prefix string) (*WindowStore, error) {
	if client == nil {
		return nil, errors.New("alarmd fleet: window store requires a redis client")
	}
	if prefix == "" {
		return nil, errors.New("alarmd fleet: window store requires a prefix")
	}
	return &WindowStore{
		client:   client,
		deadline: prefix + ":observation-window",
		audit:    prefix + ":observation-window-audit",
	}, nil
}

// ValidateQueryGroup rejects anything that is not a query group identity.
//
// The check matters more than it looks: a window on a key no object has is not
// an error anyone would notice, it is simply an observation that never records
// anything, and the operator waits for output that cannot come.
func ValidateQueryGroup(queryGroup string) error {
	decoded, err := hex.DecodeString(queryGroup)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("query group %q is not a SHA256 identity", queryGroup)
	}
	return nil
}

// Open records windows on the given objects, replacing any window already open
// on the same object. It returns what is open afterwards.
func (store *WindowStore) Open(ctx context.Context, queryGroups []string, openedBy string, ttl time.Duration, now time.Time) ([]Window, error) {
	if len(queryGroups) == 0 {
		return nil, errors.New("at least one query group is required")
	}
	if openedBy == "" {
		return nil, errors.New("opened_by is required, so an open window can be traced to whoever opened it")
	}
	if ttl <= 0 || ttl > MaxWindowTTL {
		return nil, fmt.Errorf("ttl must be positive and at most %s", MaxWindowTTL)
	}
	seen := make(map[string]struct{}, len(queryGroups))
	for _, queryGroup := range queryGroups {
		if err := ValidateQueryGroup(queryGroup); err != nil {
			return nil, err
		}
		seen[queryGroup] = struct{}{}
	}
	// Checked against what is already open, not just against this request: the
	// budget is shared, and a caller that only counts its own request would let
	// two callers each stay under the cap and together exceed it.
	open, err := store.Load(ctx, now)
	if err != nil {
		return nil, err
	}
	for _, window := range open {
		seen[window.QueryGroup] = struct{}{}
	}
	if len(seen) > MaxOpenWindows {
		return nil, fmt.Errorf("at most %d objects may be observed at once; %d would be open", MaxOpenWindows, len(seen))
	}

	expiresAt := now.Add(ttl)
	members := make([]*redis.Z, 0, len(queryGroups))
	audit := make(map[string]any, len(queryGroups))
	for queryGroup := range seen {
		if !containsString(queryGroups, queryGroup) {
			continue
		}
		members = append(members, &redis.Z{Score: float64(expiresAt.UnixMilli()), Member: queryGroup})
		encoded, err := json.Marshal(Window{
			QueryGroup: queryGroup, OpenedBy: openedBy, OpenedAt: now, ExpiresAt: expiresAt,
		})
		if err != nil {
			return nil, err
		}
		audit[queryGroup] = string(encoded)
	}
	if err := store.client.ZAdd(ctx, store.deadline, members...).Err(); err != nil {
		return nil, err
	}
	if err := store.client.HSet(ctx, store.audit, audit).Err(); err != nil {
		return nil, err
	}
	// The keys outlive the longest window by a margin rather than by exactly the
	// window, so a clock skew between writer and Redis cannot expire the set
	// while a window inside it is still supposed to be open.
	expiry := ttl + MaxWindowTTL
	_ = store.client.Expire(ctx, store.deadline, expiry).Err()
	_ = store.client.Expire(ctx, store.audit, expiry).Err()
	return store.Load(ctx, now)
}

// Close ends the windows on the given objects immediately.
func (store *WindowStore) Close(ctx context.Context, queryGroups []string, now time.Time) ([]Window, error) {
	if len(queryGroups) == 0 {
		return nil, errors.New("at least one query group is required")
	}
	members := make([]any, 0, len(queryGroups))
	fields := make([]string, 0, len(queryGroups))
	for _, queryGroup := range queryGroups {
		if err := ValidateQueryGroup(queryGroup); err != nil {
			return nil, err
		}
		members = append(members, queryGroup)
		fields = append(fields, queryGroup)
	}
	if err := store.client.ZRem(ctx, store.deadline, members...).Err(); err != nil {
		return nil, err
	}
	if err := store.client.HDel(ctx, store.audit, fields...).Err(); err != nil {
		return nil, err
	}
	return store.Load(ctx, now)
}

// Load returns the windows that are open at now, dropping those that expired.
//
// Expired members are removed here rather than left to a key TTL, because the
// key holds the whole set and the members expire one at a time.
func (store *WindowStore) Load(ctx context.Context, now time.Time) ([]Window, error) {
	cutoff := strconv.FormatInt(now.UnixMilli(), 10)
	expired, err := store.client.ZRangeByScore(ctx, store.deadline,
		&redis.ZRangeBy{Min: "-inf", Max: "(" + cutoff}).Result()
	if err != nil {
		return nil, err
	}
	if len(expired) > 0 {
		fields := make([]string, 0, len(expired))
		members := make([]any, 0, len(expired))
		for _, queryGroup := range expired {
			fields = append(fields, queryGroup)
			members = append(members, queryGroup)
		}
		if err := store.client.ZRem(ctx, store.deadline, members...).Err(); err != nil {
			return nil, err
		}
		if err := store.client.HDel(ctx, store.audit, fields...).Err(); err != nil {
			return nil, err
		}
	}
	live, err := store.client.ZRangeByScore(ctx, store.deadline,
		&redis.ZRangeBy{Min: cutoff, Max: "+inf"}).Result()
	if err != nil {
		return nil, err
	}
	if len(live) == 0 {
		return []Window{}, nil
	}
	records, err := store.client.HMGet(ctx, store.audit, live...).Result()
	if err != nil {
		return nil, err
	}
	windows := make([]Window, 0, len(live))
	for index, record := range records {
		text, ok := record.(string)
		if !ok {
			// The deadline says it is open and the audit cannot say by whom.
			// Reporting the window without its provenance is better than hiding
			// an object that is spending budget.
			windows = append(windows, Window{QueryGroup: live[index]})
			continue
		}
		var window Window
		if err := json.Unmarshal([]byte(text), &window); err != nil {
			windows = append(windows, Window{QueryGroup: live[index]})
			continue
		}
		windows = append(windows, window)
	}
	return windows, nil
}

// QueryGroups reduces the open windows to the selection the diagnostics take.
func QueryGroups(windows []Window) []string {
	queryGroups := make([]string, 0, len(windows))
	for _, window := range windows {
		queryGroups = append(queryGroups, window.QueryGroup)
	}
	return queryGroups
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
