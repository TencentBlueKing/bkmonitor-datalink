// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
)

type preparationIndex struct {
	policyruntime.DependencyCandidateIndex
	calls []string
	fail  bool
}

func (i *preparationIndex) RetireDependencyNamespace(_ context.Context, tenant, digest string) error {
	i.calls = append(i.calls, "retire:"+tenant+":"+digest)
	if i.fail {
		return policy.ErrUnavailable
	}
	return nil
}

func (i *preparationIndex) ActivateDependencyNamespace(_ context.Context, tenant, digest string) error {
	i.calls = append(i.calls, "activate:"+tenant+":"+digest)
	return nil
}

type preparationRecords struct{}

func (preparationRecords) List(context.Context, policy.Scope, string, int) ([]policy.Record, error) {
	return nil, nil
}

type preparationCandidates struct {
	calls     int
	failure   error
	onRefresh func()
}

func (c *preparationCandidates) ListActiveAlerts(context.Context, string, string, int) (store.ActiveAlertPage, error) {
	return store.ActiveAlertPage{}, nil
}

func (c *preparationCandidates) RefreshActiveAlerts(context.Context) error {
	c.calls++
	if c.onRefresh != nil {
		c.onRefresh()
	}
	return c.failure
}

func preparationManager(t *testing.T) (*dependencyIndexManager, *preparationIndex, *preparationCandidates, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	index := &preparationIndex{}
	candidates := &preparationCandidates{}
	m := &dependencyIndexManager{wake: make(chan struct{}, 1), severity: runtimeconfig.NewSeverity(config.DefaultSeverityConfig()), now: func() time.Time { return now }}
	m.shielder = &policyruntime.Shielder{Index: index, Candidates: candidates, Loader: &policyruntime.Suppressor{Catalog: policy.NewCatalog(preparationRecords{})}, CurrentAlert: func(context.Context, string, string) (store.StoredAlert, error) { return store.StoredAlert{}, nil }}
	return m, index, candidates, &now
}

func TestDependencyPreparationStartupRetiresOnceAndForceCannotBypassDeadline(t *testing.T) {
	m, index, _, now := preparationManager(t)
	if err := m.Schedule(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	digest := m.severity.SeveritySnapshot().Digest
	deadline := now.Add(15 * time.Second)
	*now = now.Add(5 * time.Second)
	if err := m.Schedule(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	if len(index.calls) != 1 || !m.tenants["tenant"].next.Equal(deadline) {
		t.Fatal("repeat Schedule changed preparation", index.calls, m.tenants["tenant"])
	}
	if jobs := m.selectJobs(*now, m.severity.SeveritySnapshot(), []string{"tenant"}, false, 4); len(jobs) != 0 {
		t.Fatal("force bypassed preparation", jobs)
	}
	*now = deadline
	jobs := m.selectJobs(*now, m.severity.SeveritySnapshot(), nil, false, 4)
	if len(jobs) != 1 || !jobs[0].force || !jobs[0].prepare || jobs[0].snapshot.Digest != digest {
		t.Fatal(jobs)
	}
	if duplicate := m.selectJobs(*now, m.severity.SeveritySnapshot(), nil, true, 4); len(duplicate) != 0 {
		t.Fatal("same tenant concurrent job", duplicate)
	}
	if err := m.execute(t.Context(), jobs[0]); err != nil {
		t.Fatal(err)
	}
	if len(index.calls) != 2 || index.calls[1] != "activate:tenant:"+digest {
		t.Fatal(index.calls)
	}
}

func TestDependencyPreparationRetirementFailureAndRefreshFailureRetry(t *testing.T) {
	m, index, candidates, now := preparationManager(t)
	index.fail = true
	if err := m.Schedule(t.Context(), "tenant"); !errors.Is(err, policy.ErrUnavailable) {
		t.Fatal(err)
	}
	if m.tenants["tenant"].initialized {
		t.Fatal("failed retirement opened catalog")
	}
	index.fail = false
	if err := m.Schedule(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(15 * time.Second)
	job := m.selectJobs(*now, m.severity.SeveritySnapshot(), nil, false, 1)[0]
	candidates.failure = policy.ErrUnavailable
	err := m.execute(t.Context(), job)
	if !errors.Is(err, policy.ErrUnavailable) || len(index.calls) != 2 {
		t.Fatal("failed refresh activated", err, index.calls)
	}
	m.finish(job, err)
	if !m.tenants["tenant"].force || !m.tenants["tenant"].prepare {
		t.Fatal("retry lost preparation flags")
	}
	candidates.failure = nil
	*now = now.Add(5 * time.Second)
	retry := m.selectJobs(*now, m.severity.SeveritySnapshot(), nil, false, 1)[0]
	if err := m.execute(t.Context(), retry); err != nil {
		t.Fatal(err)
	}
	if candidates.calls != 2 || len(index.calls) != 3 {
		t.Fatal(candidates.calls, index.calls)
	}
}

func TestDependencyPreparationPublicationDuringJobDoesNotActivate(t *testing.T) {
	m, index, candidates, now := preparationManager(t)
	if err := m.Schedule(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(15 * time.Second)
	job := m.selectJobs(*now, m.severity.SeveritySnapshot(), nil, false, 1)[0]
	candidates.onRefresh = func() { m.Invalidate("tenant") }
	err := m.execute(t.Context(), job)
	if !errors.Is(err, policyruntime.ErrDependencyIndexNotReady) || len(index.calls) != 1 {
		t.Fatal("obsolete job reopened namespace", err, index.calls)
	}
	m.finish(job, err)
	if jobs := m.selectJobs(*now, m.severity.SeveritySnapshot(), []string{"tenant"}, false, 1); len(jobs) != 0 {
		t.Fatal("new force crossed publication deadline")
	}
	candidates.onRefresh = nil
	*now = now.Add(15 * time.Second)
	retry := m.selectJobs(*now, m.severity.SeveritySnapshot(), nil, false, 1)[0]
	if err := m.execute(t.Context(), retry); err != nil {
		t.Fatal(err)
	}
	if len(index.calls) != 2 {
		t.Fatal(index.calls)
	}
}

func TestDependencyPreparationSeverityRollbackRetiresBothNamespaces(t *testing.T) {
	m, index, _, now := preparationManager(t)
	if err := m.Schedule(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	a := m.severity.SeveritySnapshot()
	*now = now.Add(15 * time.Second)
	old := m.selectJobs(*now, a, nil, false, 1)[0]
	b := a
	b.Digest = ""
	b.NativeNames = !b.NativeNames
	if err := m.severity.Install(b); err != nil {
		t.Fatal(err)
	}
	b = m.severity.SeveritySnapshot()
	if err := m.prepareSeverity(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if err := m.execute(t.Context(), old); !errors.Is(err, policyruntime.ErrDependencyIndexNotReady) {
		t.Fatal("old severity job succeeded", err)
	}
	m.finish(old, policyruntime.ErrDependencyIndexNotReady)
	if err := m.severity.Install(a); err != nil {
		t.Fatal(err)
	}
	if err := m.prepareSeverity(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	want := []string{"retire:tenant:" + a.Digest, "retire:tenant:" + a.Digest, "retire:tenant:" + b.Digest, "retire:tenant:" + b.Digest, "retire:tenant:" + a.Digest}
	if !reflect.DeepEqual(index.calls, want) {
		t.Fatal(index.calls, want)
	}
	*now = now.Add(15 * time.Second)
	current := m.selectJobs(*now, a, nil, false, 1)
	if len(current) != 1 || !current[0].force {
		t.Fatal(current)
	}
	if err := m.execute(t.Context(), current[0]); err != nil {
		t.Fatal(err)
	}
	if index.calls[len(index.calls)-1] != "activate:tenant:"+a.Digest {
		t.Fatal(index.calls)
	}
}

func TestDependencyPreparationStartupHasOneFinalSettleWithoutPeriodicFullScan(t *testing.T) {
	m, _, candidates, now := preparationManager(t)
	start := *now
	if err := m.Schedule(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	run := func(at time.Time, changed []string, periodic bool) []dependencyIndexJob {
		t.Helper()
		*now = at
		jobs := m.selectJobs(*now, m.severity.SeveritySnapshot(), changed, periodic, 1)
		for _, job := range jobs {
			err := m.execute(t.Context(), job)
			m.finish(job, err)
			if err != nil {
				t.Fatal(err)
			}
		}
		return jobs
	}
	initial := run(start.Add(15*time.Second), nil, false)
	if len(initial) != 1 || !initial[0].settleAt.IsZero() {
		t.Fatal(initial)
	}
	if jobs := run(start.Add(59*time.Second), nil, false); len(jobs) != 0 {
		t.Fatal("early settle", jobs)
	}
	final := run(start.Add(time.Minute), nil, false)
	if len(final) != 1 || !final[0].force || final[0].settleAt.IsZero() || !m.tenants["tenant"].settleAt.IsZero() {
		t.Fatal(final, m.tenants["tenant"])
	}
	if candidates.calls != 2 {
		t.Fatal("startup and final should each refresh", candidates.calls)
	}
	periodic := run(start.Add(2*time.Minute), nil, true)
	if len(periodic) != 1 || periodic[0].force || periodic[0].prepare || candidates.calls != 2 {
		t.Fatal("periodic refresh turned into full scan", periodic, candidates.calls)
	}
	if jobs := run(start.Add(3*time.Minute), nil, false); len(jobs) != 0 {
		t.Fatal("completed settle repeated", jobs)
	}
}

func TestDependencyPreparationContinuousChangesPreserveEarliestSettle(t *testing.T) {
	m, _, _, now := preparationManager(t)
	start := *now
	if err := m.Schedule(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	run := func(seconds int, changed bool) dependencyIndexJob {
		t.Helper()
		*now = start.Add(time.Duration(seconds) * time.Second)
		var tenants []string
		if changed {
			tenants = []string{"tenant"}
		}
		jobs := m.selectJobs(*now, m.severity.SeveritySnapshot(), tenants, false, 1)
		if len(jobs) != 1 {
			t.Fatal("missing job", seconds, jobs)
		}
		err := m.execute(t.Context(), jobs[0])
		m.finish(jobs[0], err)
		if err != nil {
			t.Fatal(err)
		}
		return jobs[0]
	}
	run(15, false)
	run(20, true)
	run(50, true)
	if !m.tenants["tenant"].settleAt.Equal(start.Add(time.Minute)) {
		t.Fatal("continuous changes postponed earliest settle")
	}
	first := run(60, false)
	if first.settleAt.IsZero() || !m.tenants["tenant"].settleAt.Equal(start.Add(110*time.Second)) {
		t.Fatal(first, m.tenants["tenant"])
	}
	run(65, true)
	if !m.tenants["tenant"].settleAt.Equal(start.Add(110 * time.Second)) {
		t.Fatal("later change postponed pending settle")
	}
	run(110, false)
	if !m.tenants["tenant"].settleAt.Equal(start.Add(125 * time.Second)) {
		t.Fatal("later change lost final settle", m.tenants["tenant"])
	}
	run(125, false)
	if !m.tenants["tenant"].settleAt.IsZero() {
		t.Fatal("settle did not finish")
	}
}

func TestDependencyPreparationFailedFinalSettleIsRetried(t *testing.T) {
	m, _, candidates, now := preparationManager(t)
	start := *now
	if err := m.Schedule(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	*now = start.Add(time.Minute)
	job := m.selectJobs(*now, m.severity.SeveritySnapshot(), nil, false, 1)[0]
	candidates.failure = policy.ErrUnavailable
	err := m.execute(t.Context(), job)
	m.finish(job, err)
	if m.tenants["tenant"].settleAt.IsZero() {
		t.Fatal("failed final scan discarded")
	}
	candidates.failure = nil
	*now = now.Add(5 * time.Second)
	retry := m.selectJobs(*now, m.severity.SeveritySnapshot(), nil, false, 1)[0]
	err = m.execute(t.Context(), retry)
	m.finish(retry, err)
	if err != nil || !m.tenants["tenant"].settleAt.IsZero() {
		t.Fatal("final retry failed", err, m.tenants["tenant"])
	}
}

func TestDependencyPreparationSeverityChangesDuringFinalJobCannotActivate(t *testing.T) {
	m, index, candidates, now := preparationManager(t)
	if err := m.Schedule(t.Context(), "tenant"); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	job := m.selectJobs(*now, m.severity.SeveritySnapshot(), nil, false, 1)[0]
	candidates.onRefresh = func() {
		next := m.severity.SeveritySnapshot()
		next.Digest = ""
		next.NativeNames = !next.NativeNames
		if err := m.severity.Install(next); err != nil {
			t.Fatal(err)
		}
	}
	err := m.execute(t.Context(), job)
	m.finish(job, err)
	if !errors.Is(err, policyruntime.ErrDependencyIndexNotReady) || len(index.calls) != 1 || m.tenants["tenant"].settleAt.IsZero() {
		t.Fatal("old completed job opened namespace or lost final retry", err, index.calls, m.tenants["tenant"])
	}
}
