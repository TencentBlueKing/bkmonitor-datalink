// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package runtime

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"linkd/internal/policy"
	"linkd/internal/store/memory"
)

func TestShieldBindingSurvivesCatalogEviction(t *testing.T) {
	for _, mode := range []string{"evicted", "reload-failed", "deleted", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			repo := memory.New()
			clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
			directory := &shieldDirectory{releases: []policy.Release{timeShieldRelease(t)}}
			p, s := newShieldProcessor(t, repo, clock, directory, &shieldHook{}, &shieldHook{})
			opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
			current, err := repo.GetAlert(t.Context(), "tenant", opening.Event.RelatedAlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			// 过期目录会在其他租户加载时被清除；本次 Event 已持有合法候选快照。
			s.Loader.Catalog = policy.NewCatalogWithTTL(directory, -time.Second)
			snapshot, err := (Snapshotter{Catalog: s.Loader.Catalog}).Snapshot(t.Context(), opening.Event, clock.at)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Loader.Catalog.Freeze(t.Context(), "other"); err != nil {
				t.Fatal(err)
			}
			if _, found := s.Loader.Catalog.FindLatest(directory.releases[0].Scope, directory.releases[0].ID); found {
				t.Fatal("test did not evict tenant")
			}
			if mode == "reload-failed" {
				directory.err = errors.New("directory unavailable")
			}
			if mode == "deleted" {
				directory.releases = nil
			}
			if mode == "disabled" {
				next := directory.releases[0]
				compiled, err := policy.Compile(policy.Shield, bytes.ReplaceAll(next.Spec, []byte(`"is_enable":true`), []byte(`"is_enable":false`)))
				if err != nil {
					t.Fatal(err)
				}
				next.Version, next.Spec, next.Compiled = 2, compiled.Canonical, compiled.Summary
				directory.releases = append(directory.releases, next)
			}
			keep, reason, err := s.keepBinding(t.Context(), "tenant", current.Alert, current.Alert.Shield.Bindings[0], snapshot, nil)
			if err != nil || keep != (mode == "evicted" || mode == "reload-failed") {
				t.Fatalf("keep=%v reason=%s error=%v", keep, reason, err)
			}
		})
	}
}

func TestShieldRecheckUsesConfiguredCandidateLevel(t *testing.T) {
	repo := memory.New()
	clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
	old := timeShieldRelease(t)
	directory := &shieldDirectory{releases: []policy.Release{old}}
	p, s := newShieldProcessor(t, repo, clock, directory, &shieldHook{}, &shieldHook{})
	opening := mustProcessAggregation(t, repo, p, shieldEvent("opening", clock))
	current, err := repo.GetAlert(t.Context(), "tenant", opening.Event.RelatedAlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	raw := bytes.ReplaceAll(old.Spec, []byte(`"close_datetime_once":"2026-09-30T00:00:10Z"`), []byte(`"close_datetime_once":"2026-09-30T00:01:00Z"`))
	raw = bytes.ReplaceAll(raw, []byte(`"target_key":"source_id"`), []byte(`"target_key":"level"`))
	raw = bytes.ReplaceAll(raw, []byte(`"target_value":"source"`), []byte(`"target_value":"custom-warning"`))
	compiled, err := policy.Compile(policy.Shield, raw)
	if err != nil {
		t.Fatal(err)
	}
	next := old
	next.ID, next.Spec, next.Compiled = "next", compiled.Canonical, compiled.Summary
	directory.releases = append(directory.releases, next)
	s.Loader.Catalog.Invalidate("tenant")
	result, err := s.Recheck(t.Context(), current.Alert, clock.at.Add(11*time.Second), func(string) (string, error) { return "custom-warning", nil })
	if err != nil || !result.Shield.Active || len(result.Shield.Bindings) != 1 || result.Shield.Bindings[0].Policy.ID != "next" {
		t.Fatalf("replacement shield excluded by wrong level mapper: %+v, %v", result.Shield, err)
	}
}
