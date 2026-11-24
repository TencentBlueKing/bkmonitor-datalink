// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
	"linkd/internal/suppressioncleanup"
)

type terminalBatchRepository struct {
	*memory.Repository
	writes int
}

func (r *terminalBatchRepository) GetAlertsCurrent(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
	return r.GetAlerts(ctx, tenant, ids)
}

func (r *terminalBatchRepository) CompareAndSetAlertsFromSnapshots(ctx context.Context, updates []store.AlertSnapshotUpdate) ([]store.AlertUpdateResult, error) {
	r.writes++
	out := make([]store.AlertUpdateResult, len(updates))
	for i, u := range updates {
		out[i].StoredAlert, out[i].Err = r.CompareAndSetAlert(ctx, u.Current.Alert.BKTenantID, u.Current.Alert.AlertID, u.Current.Version, u.Replacement)
	}
	return out, nil
}

func (*terminalBatchRepository) CompleteAlertChanges(context.Context, []store.StoredAlert, store.AlertChangeField) ([]store.AlertUpdateResult, error) {
	return nil, errors.New("unexpected pending change")
}

type terminalBatchDependency struct {
	DependencyRegistry
	observed, cleared int
}

func (d *terminalBatchDependency) ObserveDependencyAlert(context.Context, domain.Alert, func(string) (string, error)) error {
	return errors.New("individual dependency observation")
}

func (d *terminalBatchDependency) ClearDependency(context.Context, string, string) error {
	return errors.New("individual dependency cleanup")
}

func (d *terminalBatchDependency) ObserveTerminalDependencyAlerts(_ context.Context, a []domain.Alert) error {
	d.observed++
	return nil
}

func (d *terminalBatchDependency) ClearDependencies(_ context.Context, a []domain.Alert) []error {
	d.cleared++
	return make([]error, len(a))
}

type terminalBatchSuppressor struct {
	NewAlertSuppressor
	calls int
	fail  bool
}

func (s *terminalBatchSuppressor) Clear(context.Context, suppressioncleanup.Cause) error {
	return errors.New("individual suppression cleanup")
}

func (s *terminalBatchSuppressor) ClearBatch(_ context.Context, c []suppressioncleanup.Cause) []error {
	s.calls++
	e := make([]error, len(c))
	if s.fail {
		e[0] = errors.New("cleanup batch unavailable")
	}
	return e
}

type terminalBatchPublisher struct{ calls, items int }

func (p *terminalBatchPublisher) Publish(context.Context, domain.AlertChange) error {
	return errors.New("individual notification")
}

func (p *terminalBatchPublisher) PublishBatch(_ context.Context, c []domain.AlertChange) []error {
	p.calls++
	p.items += len(c)
	return make([]error, len(c))
}

type terminalBatchActive struct{ calls int }

func (*terminalBatchActive) ObserveAlert(context.Context, domain.Alert) error {
	return errors.New("individual active index")
}

func (a *terminalBatchActive) ObserveAlerts(context.Context, []domain.Alert) error {
	a.calls++
	return nil
}

func TestCloseBatchUsesBatchIndexesCleanupAndNotifications(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			repo := &terminalBatchRepository{Repository: memory.New()}
			p := newTestProcessor(t, repo, NoopFinalHook{})
			dependency := &terminalBatchDependency{}
			suppression := &terminalBatchSuppressor{fail: fail}
			publisher := &terminalBatchPublisher{}
			active := &terminalBatchActive{}
			p.dependency, p.suppressor, p.publisher, p.activeAlerts = dependency, suppression, publisher, active
			commands := make([]CloseAlertCommand, 8)
			for i := range commands {
				id := fmt.Sprintf("alert-%d", i)
				a := storetest.Alert("tenant", id, "opening", id, "warning")
				if _, err := repo.CreateAlert(t.Context(), a); err != nil {
					t.Fatal(err)
				}
				commands[i] = CloseAlertCommand{BKTenantID: "tenant", AlertID: id, OperationID: "batch", OperatorID: "tester", OperatorKind: domain.OperatorKindUser, Reason: "done", EffectiveAt: a.UpdateAt.Add(time.Minute)}
			}
			out := p.CloseAlerts(t.Context(), commands, 2)
			failed := 0
			for _, r := range out {
				if r.Alert.Status != domain.AlertStatusClosed {
					t.Fatal(r)
				}
				if r.Err != nil {
					failed++
				}
			}
			want := 0
			if fail {
				want = 1
			}
			if failed != want || repo.writes != 1 || dependency.observed != 1 || dependency.cleared != 1 || suppression.calls != 1 || publisher.calls != 1 || publisher.items != 8-want || active.calls != 1 {
				t.Fatalf("not a stage batch: failed=%d writes=%d dependency=%d/%d cleanup=%d notifications=%d/%d active=%d", failed, repo.writes, dependency.observed, dependency.cleared, suppression.calls, publisher.calls, publisher.items, active.calls)
			}
		})
	}
}
