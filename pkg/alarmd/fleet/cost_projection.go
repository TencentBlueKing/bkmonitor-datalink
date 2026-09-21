// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

var ErrCostProjectionBudget = errors.New("cost projection resource budget exhausted")

// These allowances come from the observation resource allocation. ReadBytes is
// the entire refresh's wire allowance, not an allowance multiplied by replicas.
// Use a diagnostics client: this store must not borrow the execution pool.
type CostProjectionLimits struct {
	PublishBytes int
	ReadBytes    int
	ReadCommands int
	Timeout      time.Duration
	TTL          time.Duration
	FreshFor     time.Duration
}

type CostProjectionStore struct {
	client redis.Cmdable
	prefix string
	limits CostProjectionLimits
	readMu sync.Mutex
	next   int
}

type CostProjectionPublish struct {
	WrittenBytes  int
	MarkerWritten bool
}

type CostProjectionRecord struct {
	Replica    string          `json:"replica"`
	ObservedAt time.Time       `json:"observed_at"`
	Cost       json.RawMessage `json:"cost"`
}

type CostProjectionGap struct {
	Replica string `json:"replica,omitempty"`
	Reason  string `json:"reason"`
	Count   int    `json:"count,omitempty"`
}

type CostProjectionView struct {
	RegistryComplete bool `json:"registry_complete"`
	// Complete describes projection retrieval only. Each CostSnapshot retains
	// its own observed-work coverage; retrieval never makes local candidates a
	// complete deployment ranking or fills unobserved work with zero.
	Complete     bool                   `json:"complete"`
	Expected     int                    `json:"expected"`
	Attempted    int                    `json:"attempted"`
	Deferred     int                    `json:"deferred"`
	ReadBytes    int                    `json:"read_bytes"`
	ReadCommands int                    `json:"read_commands"`
	Snapshots    []CostProjectionRecord `json:"replicas"`
	Gaps         []CostProjectionGap    `json:"gaps"`
}

type costProjectionWire struct {
	Version     int                         `json:"version"`
	ReplicaHash string                      `json:"replica_hash"`
	ObservedAt  time.Time                   `json:"observed_at"`
	Status      string                      `json:"status"`
	Cost        *observability.CostSnapshot `json:"cost,omitempty"`
}

func NewCostProjectionStore(client redis.Cmdable, prefix string, limits CostProjectionLimits) (*CostProjectionStore, error) {
	if client == nil || prefix == "" || limits.PublishBytes <= 0 || limits.ReadBytes <= 0 || limits.ReadCommands <= 0 || limits.Timeout <= 0 || limits.FreshFor <= 0 || limits.TTL <= limits.FreshFor {
		return nil, errors.New("cost projection requires a diagnostics client and finite resource/freshness allowances")
	}
	// Even a rejected payload must fit a small unavailable marker so the old
	// available value is not left looking like a successful fresh publication.
	marker := costProjectionWire{Version: 1, ReplicaHash: strings.Repeat("0", 64), ObservedAt: time.Now(), Status: "PUBLISH_BUDGET"}
	if !costJSONFits(reflect.ValueOf(marker), limits.PublishBytes) {
		return nil, ErrCostProjectionBudget
	}
	return &CostProjectionStore{client: client, prefix: prefix, limits: limits}, nil
}

func costReplicaHash(replica string) string {
	digest := sha256.Sum256([]byte(replica))
	return hex.EncodeToString(digest[:])
}

func (s *CostProjectionStore) key(hash string) string {
	return s.prefix + ":cost-projection:v1:" + hash
}

// Publish admits the typed payload before JSON allocation. A rejected snapshot
// overwrites its old value with an unavailable marker. Any write failure is
// returned; the caller must expose it locally, since an unreachable Redis cannot
// be made to invalidate its previous value. Retention/freshness still bound it.
func (s *CostProjectionStore) Publish(ctx context.Context, replica string, at time.Time, snapshot observability.CostSnapshot) (CostProjectionPublish, error) {
	if s == nil || replica == "" || at.IsZero() {
		return CostProjectionPublish{}, errors.New("cost projection publication identity/time missing")
	}
	ctx, cancel := context.WithTimeout(ctx, s.limits.Timeout)
	defer cancel()
	wire := costProjectionWire{Version: 1, ReplicaHash: costReplicaHash(replica), ObservedAt: snapshot.GeneratedAt, Status: "AVAILABLE", Cost: &snapshot}
	var rejected error
	if !snapshot.Enabled || snapshot.GeneratedAt.IsZero() || snapshot.ProcessID == "" || snapshot.Scope != "process_observed_candidates" {
		wire.Status, rejected = "NOT_OBSERVED", errors.New("cost summary disabled or not yet published")
	} else if !costJSONFits(reflect.ValueOf(wire), s.limits.PublishBytes) {
		wire.Status, rejected = "PUBLISH_BUDGET", ErrCostProjectionBudget
	}
	if rejected != nil {
		wire.Cost, wire.ObservedAt = nil, at
	}
	payload, err := json.Marshal(wire)
	if err != nil || len(payload) > s.limits.PublishBytes {
		wire.Cost, wire.ObservedAt, wire.Status = nil, at, "ENCODE_FAILED"
		payload, _ = json.Marshal(wire)
		rejected = errors.New("cost projection encoding failed")
	}
	if err = s.client.Set(ctx, s.key(wire.ReplicaHash), payload, s.limits.TTL).Err(); err != nil {
		return CostProjectionPublish{}, err
	}
	return CostProjectionPublish{WrittenBytes: len(payload), MarkerWritten: rejected != nil}, rejected
}

// Load receives the already-budgeted replica list from the existing registry.
// It performs no registry enumeration, SCAN, MGET, or full fleet-snapshot read.
// Missing/oversized replicas also advance the cursor, so one cannot starve the
// next. Deferred population is a count, not an unbounded list of gap objects.
func (s *CostProjectionStore) Load(ctx context.Context, replicas []string, registryComplete bool, at time.Time) CostProjectionView {
	view := CostProjectionView{Expected: len(replicas), RegistryComplete: registryComplete}
	if !registryComplete {
		view.Gaps = append(view.Gaps, CostProjectionGap{Reason: "REGISTRY_INCOMPLETE"})
	}
	if s == nil || !s.readMu.TryLock() {
		view.Deferred = len(replicas)
		view.Gaps = append(view.Gaps, CostProjectionGap{Reason: "PROJECTION_UNAVAILABLE", Count: len(replicas)})
		return view
	}
	defer s.readMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, s.limits.Timeout)
	defer cancel()
	if len(replicas) == 0 {
		view.Complete = registryComplete
		return view
	}
	start := s.next % len(replicas)
	for step := 0; step < len(replicas); step++ {
		remaining := s.limits.ReadBytes - view.ReadBytes
		if remaining <= 0 || view.ReadCommands >= s.limits.ReadCommands || ctx.Err() != nil {
			break
		}
		index := (start + step) % len(replicas)
		replica := replicas[index]
		hash := costReplicaHash(replica)
		// The extra byte detects a value over its per-publication allowance,
		// but still fits within this refresh's remaining total allowance.
		limit := min(remaining, s.limits.PublishBytes+1)
		view.Attempted++
		view.ReadCommands++
		s.next = (index + 1) % len(replicas)
		payload, err := s.client.GetRange(ctx, s.key(hash), 0, int64(limit-1)).Bytes()
		view.ReadBytes += len(payload)
		reason := ""
		var wire struct {
			Version     int             `json:"version"`
			ReplicaHash string          `json:"replica_hash"`
			ObservedAt  time.Time       `json:"observed_at"`
			Status      string          `json:"status"`
			Cost        json.RawMessage `json:"cost"`
		}
		switch {
		case err != nil:
			reason = "READ_UNAVAILABLE"
		case len(payload) == 0:
			reason = "MISSING"
		case len(payload) > s.limits.PublishBytes:
			reason = "READ_BUDGET"
		case json.Unmarshal(payload, &wire) != nil:
			reason = "INVALID_PAYLOAD"
			if len(payload) == limit {
				reason = "READ_BUDGET"
			}
		case wire.Version != 1 || wire.ReplicaHash != hash || wire.ObservedAt.IsZero():
			reason = "INVALID_IDENTITY"
		case at.Sub(wire.ObservedAt) > s.limits.FreshFor || wire.ObservedAt.Sub(at) > s.limits.FreshFor:
			reason = "STALE"
		case wire.Status == "PUBLISH_BUDGET" || wire.Status == "NOT_OBSERVED" || wire.Status == "ENCODE_FAILED":
			reason = wire.Status
		case wire.Status != "AVAILABLE":
			reason = "INVALID_PAYLOAD"
		case len(wire.Cost) == 0 || string(wire.Cost) == "null":
			reason = "INVALID_PAYLOAD"
		}
		if reason == "" {
			var metadata struct {
				Enabled     bool      `json:"enabled"`
				ProcessID   string    `json:"process_id"`
				Scope       string    `json:"scope"`
				GeneratedAt time.Time `json:"generated_at"`
			}
			if json.Unmarshal(wire.Cost, &metadata) != nil || !metadata.Enabled || metadata.ProcessID == "" || metadata.Scope != "process_observed_candidates" || !metadata.GeneratedAt.Equal(wire.ObservedAt) {
				reason = "INVALID_PAYLOAD"
			}
		}
		if reason != "" {
			view.Gaps = append(view.Gaps, CostProjectionGap{Replica: replica, Reason: reason})
			continue
		}
		view.Snapshots = append(view.Snapshots, CostProjectionRecord{Replica: replica, ObservedAt: wire.ObservedAt, Cost: wire.Cost})
	}
	view.Deferred = view.Expected - view.Attempted
	if view.Deferred > 0 {
		view.Gaps = append(view.Gaps, CostProjectionGap{Reason: "REFRESH_BUDGET", Count: view.Deferred})
	}
	view.Complete = registryComplete && len(view.Gaps) == 0 && len(view.Snapshots) == view.Expected
	return view
}

// costJSONFits walks only the fixed CostSnapshot schema and its bounded slices.
// It stops at the byte allowance before serialization, without building a JSON
// copy to truncate. Strings follow encoding/json's HTML escaping upper bound;
// omitempty fields are deliberately charged even when encoding omits them.
// Reflection avoids a second hand-maintained list of scalar fields that could
// silently omit the size of a newly added observation field.
func costJSONFits(value reflect.Value, budget int) bool {
	return costJSONBudget(value, &budget)
}

func costJSONBudget(v reflect.Value, remaining *int) bool {
	charge := func(n int) bool { *remaining -= n; return *remaining >= 0 }
	if v.Type() == reflect.TypeOf(time.Time{}) {
		return charge(37)
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return charge(4)
		}
		return costJSONBudget(v.Elem(), remaining)
	case reflect.Bool:
		return charge(5)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var buf [20]byte
		return charge(len(strconv.AppendInt(buf[:0], v.Int(), 10)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var buf [20]byte
		return charge(len(strconv.AppendUint(buf[:0], v.Uint(), 10)))
	case reflect.String:
		if !charge(2) {
			return false
		}
		for _, b := range []byte(v.String()) {
			n := 1
			if b == '"' || b == '\\' {
				n = 2
			} else if b < 32 || b >= 128 || b == '<' || b == '>' || b == '&' {
				n = 6
			}
			if !charge(n) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if v.IsNil() {
			return charge(4)
		}
		if !charge(2 + v.Len()) {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if !costJSONBudget(v.Index(i), remaining) {
				return false
			}
		}
		return true
	case reflect.Struct:
		if !charge(2) {
			return false
		}
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if field.PkgPath != "" || name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			if !charge(len(name)+4) || !costJSONBudget(v.Field(i), remaining) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
