// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package runtime

import (
	"encoding/json"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store/memory"
)

func modelScopeRelease(t *testing.T, release policy.Release, model string) policy.Release {
	t.Helper()
	var spec map[string]any
	if err := json.Unmarshal(release.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	spec["model_id"], spec["target_descriptor"] = model, nil
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	c, err := policy.Compile(release.Kind, raw)
	if err != nil {
		t.Fatal(err)
	}
	release.Spec, release.Compiled = c.Canonical, c.Summary
	return release
}

func TestModelScopeTimeShieldLifecycleWithoutInstance(t *testing.T) {
	for _, name := range []string{"with instance", "without instance", "wrong model", "wrong business"} {
		t.Run(name, func(t *testing.T) {
			repo := memory.New()
			clock := &policyTestClock{at: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)}
			directory := &shieldDirectory{releases: []policy.Release{modelScopeRelease(t, timeShieldRelease(t), "cw-Host")}}
			state, action := &shieldHook{}, &shieldHook{}
			processor, shielder := newShieldProcessor(t, repo, clock, directory, state, action)
			shielder.Loader.Targets = suppressionTargets{} // Resolve 会返回错误，不能用成员解析通过此用例。
			event := shieldEvent("model-shield", clock)
			switch name {
			case "without instance":
				delete(event.Labels, "model_inst_id")
			case "wrong model":
				event.Labels["model_id"] = domain.NewStringScalar("cw-Service")
			case "wrong business":
				event.Dimensions["bk_biz_id"] = domain.NewStringScalar("3")
			}
			result := mustProcessAggregation(t, repo, processor, event)
			stored, err := repo.GetAlert(t.Context(), "tenant", result.Event.RelatedAlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			want := name == "with instance" || name == "without instance"
			if stored.Alert.Shield.Active != want || (len(action.calls) == 0) != want {
				t.Fatalf("shield=%+v actions=%d", stored.Alert.Shield, len(action.calls))
			}
		})
	}
}

func TestModelScopeSuppressionRuntimeWithoutInstance(t *testing.T) {
	for _, model := range []string{"cw-Host", "cw-Service"} {
		t.Run(model, func(t *testing.T) {
			s, event, snapshot, state := suppressionFixture(t)
			reader := s.Releases.(*suppressionReleaseReader)
			reader.release = modelScopeRelease(t, reader.release, "cw-Host")
			snapshot.Releases[0].Digest = reader.release.Compiled.Digest
			event.Labels["model_id"] = domain.NewStringScalar(model)
			delete(event.Labels, "model_inst_id")
			result, err := s.Check(t.Context(), event, event.Evaluations[0], snapshot, nil)
			want := model == "cw-Host"
			if err != nil || result.Suppressed != want || (len(state.requests) == 1) != want {
				t.Fatalf("result=%+v state writes=%d err=%v", result, len(state.requests), err)
			}
		})
	}
}

func TestModelScopeCMDBDependencyStillRequiresRealOrigin(t *testing.T) {
	for _, withInstance := range []bool{false, true} {
		t.Run(map[bool]string{false: "no origin instance", true: "real origin instance"}[withInstance], func(t *testing.T) {
			repo, clock, shielder, state, action := dependencyFixture(t, "cmdb_shield")
			directory := shielder.Loader.Releases.(*shieldDirectory)
			directory.releases[0] = modelScopeRelease(t, directory.releases[0], "cw-Switch")
			relations := &dependencyRelations{}
			shielder.Relations = relations
			processor := dependencyProcessor(t, repo, clock, shielder, state, action)
			event := dependencyEvent("switch", "switch-source", "main", clock)
			event.Labels["model_id"] = domain.NewStringScalar("cw-Switch")
			delete(event.Labels, "model_inst_id")
			if withInstance {
				event.Labels["model_inst_id"] = domain.NewStringScalar("sw1")
			}
			mustProcessAggregation(t, repo, processor, event)
			child := mustProcessAggregation(t, repo, processor, dependencyEvent("host", "host-source", "host", clock))
			stored, err := repo.GetAlert(t.Context(), "tenant", child.Event.RelatedAlertIDs[0])
			if err != nil || stored.Alert.Shield.Active != withInstance {
				t.Fatal(stored, err)
			}
			if withInstance && relations.origin.EntityUID != "cw-Switch|sw1" {
				t.Fatal("relation origin was not the real instance")
			}
			if !withInstance && relations.origin.EntityUID != "" {
				t.Fatal("relation origin was invented")
			}
		})
	}
}
