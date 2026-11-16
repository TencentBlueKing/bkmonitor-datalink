// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

const (
	leaseQueryGroup         = "query_group"
	leaseControlLeader      = "control_leader"
	leaseWorkerRegistration = "worker_registration"
	leaseWorkerSession      = "worker_session"

	leaseRenewalRenewed = "renewed"
	leaseRenewalFailed  = "failed"
	leaseRenewalRefused = "refused"
	leaseRenewalNone    = "none"
)

// newLeaseRenewals is the count of renewal attempts by what became of them. A
// script's count cannot be this: a refusal is the script answering normally,
// which reads as ok, the query groups and the control leader run one script, and
// a failure's reason is only kept by client and job.
//
// Only the cells a renewal can reach exist from startup, so that a zero is a
// reading in every one of them: a worker registration has no refusals, its
// writes answer plain errors, and its success is not observed per attempt.
func newLeaseRenewals() *prometheus.CounterVec {
	renewals := prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: metricSubsystem, Name: "lease_renewal_total",
		Help: "Lease renewal attempts by lease (query_group, control_leader, worker_registration, worker_session), result and reason. " +
			"One per attempt: a round makes at most three, spread over its renew interval, so failed is not a lost lease, which " +
			"ownership_transition_total{transition=\"assignment_lost\"} reads. renewed has reason none. failed is a " +
			"store that could not be asked, by the words of redis_failure_reason_total, or other for an error that " +
			"is none of them. refused is one of the four words of ownership_refusals_total: the store's own answer, " +
			"or the same word given locally with no script sent, when the session has already stopped accepting or " +
			"the fence is invalid, at most once per session. worker_registration counts failures only: its writes " +
			"answer plain errors and its success is not observed per attempt. The early renewal of the view gate is " +
			"not counted here. worker_session counts the worker's session, its acquisitions with its renewals. " +
			"Every cell a renewal can reach exists from startup, so a zero is a count.",
	}, []string{"lease", "result", "reason"})
	for _, lease := range []string{leaseQueryGroup, leaseControlLeader, leaseWorkerSession} {
		renewals.WithLabelValues(lease, leaseRenewalRenewed, leaseRenewalNone)
		for _, reason := range ownership.RefusalReasons {
			renewals.WithLabelValues(lease, leaseRenewalRefused, reason)
		}
	}
	for _, lease := range []string{leaseQueryGroup, leaseControlLeader, leaseWorkerRegistration, leaseWorkerSession} {
		for _, reason := range redisfailure.Reasons {
			renewals.WithLabelValues(lease, leaseRenewalFailed, reason)
		}
	}
	return renewals
}

// leaseRenewalCell reads a lease_renewed observation. Which lease it is about
// is in its trace, the three places that report the stage each naming it their
// own way: a renewal of the control leader names the leader's identity, a
// worker's registration is a worker-level event and names nothing.
func leaseRenewalCell(observation observability.Observation) (lease, result, reason string, renewal bool) {
	if observation.Component != observability.ComponentOwnership || observation.Stage != observability.StageLeaseRenewed {
		return "", "", "", false
	}
	switch key := observation.Trace.QueryGroupKey; {
	case key == "":
		lease = leaseWorkerRegistration
	case key == string(ownership.ControlLeaderIdentity):
		lease = leaseControlLeader
	case ownership.IsWorkerSessionIdentity(execution.QueryGroupIdentity(key)):
		lease = leaseWorkerSession
	default:
		lease = leaseQueryGroup
	}
	switch observation.Result {
	case observability.ResultSuccess:
		return lease, leaseRenewalRenewed, leaseRenewalNone, true
	case observability.ResultFailed:
		if word, refused := ownership.RefusalReason(observation.Err); refused {
			return lease, leaseRenewalRefused, word, true
		}
		// An attempt reported without its error still failed; a label must
		// not be empty.
		if word := redisfailure.Reason(observation.Err); word != "" {
			return lease, leaseRenewalFailed, word, true
		}
		return lease, leaseRenewalFailed, redisfailure.Other, true
	}
	// A registration's recovery says the failures ended, not that an attempt
	// was made.
	return "", "", "", false
}
