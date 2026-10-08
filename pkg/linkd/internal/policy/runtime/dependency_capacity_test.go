// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/onemodel"
	"linkd/internal/policy"
	"linkd/internal/store"
)

type capacityCandidates struct {
	template    store.StoredAlert
	count, seen int
	padding     string
	finalError  error
}

func (r *capacityCandidates) ListActiveAlerts(ctx context.Context, tenant, after string, limit int) (store.ActiveAlertPage, error) {
	if err := ctx.Err(); err != nil {
		return store.ActiveAlertPage{}, err
	}
	if err := store.ValidateActiveAlertPage(tenant, after, limit); err != nil {
		return store.ActiveAlertPage{}, err
	}
	if r.seen == r.count && r.finalError != nil {
		return store.ActiveAlertPage{}, r.finalError
	}
	page := store.ActiveAlertPage{}
	for len(page.Alerts) < limit && r.seen < r.count {
		a := r.template.Alert.Clone()
		a.AlertID = fmt.Sprintf("candidate-%05d", r.seen)
		a.BeginAt = a.BeginAt.Add(time.Duration(r.seen) * time.Millisecond)
		if r.padding != "" {
			a.Content = r.padding
		}
		page.Alerts = append(page.Alerts, store.StoredAlert{Alert: a, Version: r.template.Version})
		r.seen++
	}
	if r.seen < r.count || r.finalError != nil {
		page.Next = page.Alerts[len(page.Alerts)-1].Alert.AlertID
	}
	return page, nil
}

func TestDependencyMainSelectionCapacityRequiresCompleteScan(t *testing.T) {
	for _, tc := range []struct {
		name       string
		count      int
		padding    string
		finalError error
		wantErr    bool
	}{
		{name: "4096 complete candidates", count: 4096},
		{name: "4097 candidates reject partial choice", count: 4097, wantErr: true},
		{name: "32 MiB exceeded before row budget", count: 256, padding: strings.Repeat("x", 256<<10), wantErr: true},
		{name: "late page error rejects prior match", count: 32, finalError: errors.New("last page unavailable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, clock, s, _, _ := dependencyFixture(t, "custom_shield")
			a := admittedMainFixture(t, repo, clock, "template")
			stored, err := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
			if err != nil {
				t.Fatal(err)
			}
			reader := &capacityCandidates{template: stored, count: tc.count, padding: tc.padding, finalError: tc.finalError}
			s.Candidates = reader
			currentReads := 0
			s.CurrentAlert = func(_ context.Context, tenant, id string) (store.StoredAlert, error) {
				currentReads++
				if tenant != a.BKTenantID || id != fmt.Sprintf("candidate-%05d", tc.count-1) {
					t.Fatalf("wrong newest identity: tenant=%s id=%s", tenant, id)
				}
				stored.Alert.AlertID = id
				return stored, nil
			}
			release := dependencyRelease(t, "custom_shield")
			compiled, err := policy.Compile(release.Kind, release.Spec)
			if err != nil {
				t.Fatal(err)
			}
			chosen, found, err := s.admittedMain(t.Context(), policy.FrozenPolicy{Release: release, Compiled: compiled}, s.Loader.Targets, clock.at, nil)
			if tc.wantErr {
				if err == nil || found || currentReads != 0 {
					t.Fatalf("incomplete scan picked main: found=%t err=%v currentReads=%d", found, err, currentReads)
				}
				if tc.padding != "" && reader.seen >= tc.count {
					t.Fatal("byte budget did not stop scan")
				}
			} else if err != nil || !found || currentReads != 1 || chosen.alert.AlertID != fmt.Sprintf("candidate-%05d", tc.count-1) {
				t.Fatalf("complete scan not selected: found=%t reads=%d err=%v", found, currentReads, err)
			}
		})
	}
}

type capacityRelations struct {
	onemodel.Reader
	rows []onemodel.Instance
}

func (r capacityRelations) Related(context.Context, string, []onemodel.Instance, string, string, onemodel.Query) ([]onemodel.Instance, error) {
	return r.rows, nil
}

func TestDependencyRelationAdapterCapacity(t *testing.T) {
	for _, count := range []int{1024, 1025} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			rows := make([]onemodel.Instance, count)
			for i := range rows {
				rows[i] = onemodel.Instance{TenantID: "tenant", ModelCode: "cw-Host", InstanceID: fmt.Sprint(i)}
			}
			adapter := relationReader{reader: capacityRelations{rows: rows}, directory: relationDirectory{}}
			refs, err := adapter.Lookup(t.Context(), "tenant", onemodel.InstanceRef{ModelID: "cw-Switch", InstanceID: "s1", EntityUID: "cw-Switch|s1"}, "belongs", "cw-Host")
			if count == 1024 {
				if err != nil || len(refs) != count {
					t.Fatalf("boundary rejected: %d %v", len(refs), err)
				}
			} else if !errors.Is(err, policy.ErrUnavailable) || len(refs) != 0 {
				t.Fatalf("overflow accepted: %d %v", len(refs), err)
			}
		})
	}
}

// 完整关系在候选首项命中之后仍必须检验；损坏末项不能建立固定绑定。
func TestDependencyMalformedRelationAfterMatchSkipsWholePolicy(t *testing.T) {
	repo, clock, s, state, action := dependencyFixture(t, "cmdb_shield")
	s.Relations = relationReader{reader: capacityRelations{rows: []onemodel.Instance{{TenantID: "tenant", ModelCode: "cw-Host", InstanceID: "h1"}, {TenantID: "tenant", ModelCode: "cw-Host", InstanceID: strings.Repeat("x", 1025)}}}, directory: relationDirectory{}}
	p := dependencyProcessor(t, repo, clock, s, state, action)
	e := dependencyEvent("switch", "switch-source", "main", clock)
	e.Labels["model_id"] = domain.NewStringScalar("cw-Switch")
	e.Labels["model_inst_id"] = domain.NewStringScalar("sw1")
	mustProcessAggregation(t, repo, p, e)
	child := dependencyEvent("host", "host-source", "child", clock)
	child.Labels["model_id"] = domain.NewStringScalar("cw-Host")
	child.Labels["model_inst_id"] = domain.NewStringScalar("h1")
	result := mustProcessAggregation(t, repo, p, child)
	skipped := false
	if decision := result.Processing.PolicyDecision; decision != nil && decision.Shield != nil {
		for _, step := range decision.Shield.Steps {
			skipped = skipped || (step.Outcome == "skipped" && step.ReasonCode == "dependency_child_unavailable")
		}
	}
	if !skipped {
		t.Fatal("missing explicit relation failure skip")
	}
	current, err := repo.GetAlert(t.Context(), "tenant", result.Event.RelatedAlertIDs[0])
	if err != nil || current.Alert.Shield.Active || current.Alert.Admission.AdmittedAt == nil {
		t.Fatal("invalid relation set affected binding/admission", err)
	}
}
