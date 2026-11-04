// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redisstate

import (
	"testing"
	"time"
)

func TestRedisDependencyClearDoesNotRetainHistory(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "never_indexed"
		if existing {
			name = "indexed_in_multiple_configs"
		}
		t.Run(name, func(t *testing.T) {
			s := redisClipStore(t)
			p := dependencyFixture().Policy
			u := DependencyCandidateUpdate{TenantID: "tenant", AlertID: "main", Revision: 1, BeginAt: time.Now().UTC(), Memberships: []DependencyMembership{{Policy: p, Matched: true, ObservedAt: time.Now().UTC()}}}
			keys := []string{s.dependencyCandidateAllOwners(u.TenantID, u.AlertID)}
			for _, config := range []string{"old", "current"} {
				u.ConfigDigest = config
				owner := s.dependencyCandidateOwner(u.TenantID, u.AlertID, config)
				keys = append(keys, owner, owner+":times", s.dependencyCandidateKey(u.TenantID, DependencyIndexRef{PolicyVersion: p, ConfigDigest: config}))
				if existing {
					if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
						t.Fatal(err)
					}
				}
			}
			other := u
			other.TenantID = "other-tenant"
			if err := s.ApplyDependencyCandidates(t.Context(), other); err != nil {
				t.Fatal(err)
			}
			u.Clear, u.Revision = true, 2
			for range 2 {
				if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
					t.Fatal(err)
				}
				if n, err := s.client.Exists(t.Context(), keys...).Result(); err != nil || n != 0 {
					t.Fatalf("clear retained or recreated per-alert history: keys=%d err=%v", n, err)
				}
			}
			if n, err := s.client.Exists(t.Context(), s.dependencyCandidateAllOwners(other.TenantID, other.AlertID)).Result(); err != nil || n != 1 {
				t.Fatalf("clear crossed tenant boundary: %d %v", n, err)
			}
		})
	}
}

func TestRedisDependencyLegacyTombstoneIsRemovedOnMaintenance(t *testing.T) {
	for _, clear := range []bool{false, true} {
		name := "active_update"
		if clear {
			name = "repeated_clear"
		}
		t.Run(name, func(t *testing.T) {
			s := redisClipStore(t)
			key := s.dependencyCandidateAllOwners("tenant", "main")
			if err := s.client.HSet(t.Context(), key, "_revision", 2, "_cleared", 2).Err(); err != nil {
				t.Fatal(err)
			}
			if err := s.client.Expire(t.Context(), key, 30*24*time.Hour).Err(); err != nil {
				t.Fatal(err)
			}
			u := DependencyCandidateUpdate{TenantID: "tenant", AlertID: "main", Revision: 3, BeginAt: time.Now().UTC(), Clear: clear}
			if clear {
				u.Revision = 2
			}
			if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
				t.Fatal(err)
			}
			if clear {
				if n, err := s.client.Exists(t.Context(), key).Result(); err != nil || n != 0 {
					t.Fatalf("legacy tombstone survived clear: %d %v", n, err)
				}
				return
			}
			if ttl, err := s.client.TTL(t.Context(), key).Result(); err != nil || ttl != -1 {
				t.Fatalf("active owner inherited tombstone expiration: %v %v", ttl, err)
			}
			if exists, err := s.client.HExists(t.Context(), key, "_cleared").Result(); err != nil || exists {
				t.Fatalf("active owner retained legacy clear marker: %t %v", exists, err)
			}
		})
	}
}

func TestRedisDependencyOlderClearPreservesNewerActiveOwner(t *testing.T) {
	s := redisClipStore(t)
	p := dependencyFixture().Policy
	u := DependencyCandidateUpdate{TenantID: "tenant", AlertID: "main", Revision: 3, BeginAt: time.Now().UTC(), Memberships: []DependencyMembership{{Policy: p, Matched: true}}}
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDependencyIndexReady(t.Context(), u.TenantID, DependencyIndexRef{PolicyVersion: p}); err != nil {
		t.Fatal(err)
	}
	u.Clear, u.Revision = true, 2
	if err := s.ApplyDependencyCandidates(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	row, ready, err := s.TopDependencyCandidate(t.Context(), u.TenantID, DependencyIndexRef{PolicyVersion: p})
	if err != nil || !ready || row.AlertID != u.AlertID || row.Revision != 3 {
		t.Fatalf("older clear removed current active main: %+v %t %v", row, ready, err)
	}
}
