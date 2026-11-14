// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

func renewalObservation(queryGroup string, result observability.Result, err error) observability.Observation {
	return observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageLeaseRenewed, Result: result, Err: err,
		Trace: observability.TraceFields{QueryGroupKey: queryGroup},
	}
}

func TestARenewalAttemptIsCountedByLeaseResultAndReason(t *testing.T) {
	controlLeader := string(ownership.ControlLeaderIdentity)
	for _, test := range []struct {
		name                  string
		observation           observability.Observation
		lease, result, reason string
	}{
		{"a query group renewed", renewalObservation("query-group-1", observability.ResultSuccess, nil), leaseQueryGroup, leaseRenewalRenewed, leaseRenewalNone},
		{"the control leader renewed", renewalObservation(controlLeader, observability.ResultSuccess, nil), leaseControlLeader, leaseRenewalRenewed, leaseRenewalNone},
		{"a query group timing out", renewalObservation("query-group-1", observability.ResultFailed, context.DeadlineExceeded), leaseQueryGroup, leaseRenewalFailed, redisfailure.Timeout},
		{"the control leader's connection refused", renewalObservation(controlLeader, observability.ResultFailed, fmt.Errorf("dial tcp 192.0.2.1:6379: %w", syscall.ECONNREFUSED)), leaseControlLeader, leaseRenewalFailed, redisfailure.ConnectionRefused},
		{"a registration timing out", renewalObservation("", observability.ResultFailed, context.DeadlineExceeded), leaseWorkerRegistration, leaseRenewalFailed, redisfailure.Timeout},
		{"a worker session renewed", renewalObservation(string(ownership.WorkerSessionIdentity("worker-1")), observability.ResultSuccess, nil), leaseWorkerSession, leaseRenewalRenewed, leaseRenewalNone},
		{"a worker session already held", renewalObservation(string(ownership.WorkerSessionIdentity("worker-1")), observability.ResultFailed, ownership.ErrLeaseBusy), leaseWorkerSession, leaseRenewalRefused, "OWNERSHIP_LEASE_BUSY"},
		{"a stale query group", renewalObservation("query-group-1", observability.ResultFailed, ownership.ErrStaleFence), leaseQueryGroup, leaseRenewalRefused, "OWNERSHIP_STALE_FENCE"},
		{"a query group no longer desired", renewalObservation("query-group-1", observability.ResultFailed, ownership.ErrNotDesired), leaseQueryGroup, leaseRenewalRefused, "OWNERSHIP_NOT_DESIRED"},
		{"a stale control leader", renewalObservation(controlLeader, observability.ResultFailed, fmt.Errorf("wrapped: %w", ownership.ErrStaleFence)), leaseControlLeader, leaseRenewalRefused, "OWNERSHIP_STALE_FENCE"},
		{"an error that names no reason", renewalObservation("query-group-1", observability.ResultFailed, errors.New("something else")), leaseQueryGroup, leaseRenewalFailed, redisfailure.Other},
		{"a failure reported without its error", renewalObservation("query-group-1", observability.ResultFailed, nil), leaseQueryGroup, leaseRenewalFailed, redisfailure.Other},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := NewRecorder(BuildInfo{})
			before := testutil.CollectAndCount(recorder.phaseTwo.leaseRenewals)
			recorder.Observe(context.Background(), test.observation)
			if got := testutil.ToFloat64(recorder.phaseTwo.leaseRenewals.WithLabelValues(test.lease, test.result, test.reason)); got != 1 {
				t.Errorf("the cell reads %v", got)
			}
			if after := testutil.CollectAndCount(recorder.phaseTwo.leaseRenewals); after != before {
				t.Errorf("the observation made a series that did not exist from startup: %d to %d", before, after)
			}
		})
	}
}

// Read directly as well: the recorder drops a pair it does not know before this
// is asked, which is no reason for this to count one.
func TestOnlyTheOwnershipsRenewalStageIsARenewal(t *testing.T) {
	for _, observation := range []observability.Observation{
		{Component: observability.ComponentState, Stage: observability.StageLeaseRenewed, Result: observability.ResultFailed},
		{Component: observability.ComponentOwnership, Stage: observability.StageFenceChecked, Result: observability.ResultFailed},
	} {
		if lease, result, reason, renewal := leaseRenewalCell(observation); renewal {
			t.Errorf("%s %s reads as a renewal: %s %s %s", observation.Component, observation.Stage, lease, result, reason)
		}
	}
}

func TestWhatIsNotARenewalAttemptIsNotCounted(t *testing.T) {
	for _, test := range []struct {
		name        string
		observation observability.Observation
	}{
		{"a registration recovering", renewalObservation("", observability.ResultResumed, nil)},
		{"another stage", observability.Observation{Component: observability.ComponentOwnership, Stage: observability.StageFenceChecked, Result: observability.ResultFailed, Err: ownership.ErrStaleFence}},
		{"another component", observability.Observation{Component: observability.ComponentState, Stage: observability.StageLeaseRenewed, Result: observability.ResultFailed}},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := NewRecorder(BuildInfo{})
			recorder.Observe(context.Background(), test.observation)
			if got := redisCounterSum(t, recorder, "alarmd_lease_renewal_total", nil); got != 0 {
				t.Errorf("counted %v", got)
			}
		})
	}
}

// A zero has to be a reading in every cell that exists, so the cells a renewal
// cannot reach do not exist: a registration has no refusals and reports no
// plain success.
func TestOnlyTheCellsARenewalCanReachExistFromStartup(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	cells := matchingCells(t, recorder)
	if want := 3*(1+len(ownership.RefusalReasons)+len(redisfailure.Reasons)) + len(redisfailure.Reasons); len(cells) != want {
		t.Fatalf("%d cells at startup, want %d", len(cells), want)
	}
	for _, lease := range []string{leaseQueryGroup, leaseControlLeader} {
		if !cells[lease+" "+leaseRenewalRenewed+" "+leaseRenewalNone] {
			t.Errorf("%s has no renewed cell", lease)
		}
		for _, reason := range ownership.RefusalReasons {
			if !cells[lease+" "+leaseRenewalRefused+" "+reason] {
				t.Errorf("%s has no refused cell for %s", lease, reason)
			}
		}
	}
	for _, lease := range []string{leaseQueryGroup, leaseControlLeader, leaseWorkerRegistration} {
		for _, reason := range redisfailure.Reasons {
			if !cells[lease+" "+leaseRenewalFailed+" "+reason] {
				t.Errorf("%s has no failed cell for %s", lease, reason)
			}
		}
	}
	for cell := range cells {
		if strings.HasPrefix(cell, leaseWorkerRegistration+" ") && !strings.HasPrefix(cell, leaseWorkerRegistration+" "+leaseRenewalFailed+" ") {
			t.Errorf("the registration has the cell %q, which it cannot reach", cell)
		}
	}
}

func matchingCells(t *testing.T, recorder *Recorder) map[string]bool {
	t.Helper()
	cells := map[string]bool{}
	for _, sample := range redisSamples(t, recorder, "alarmd_lease_renewal_total", nil) {
		cells[labelOf(sample, "lease")+" "+labelOf(sample, "result")+" "+labelOf(sample, "reason")] = true
	}
	return cells
}
