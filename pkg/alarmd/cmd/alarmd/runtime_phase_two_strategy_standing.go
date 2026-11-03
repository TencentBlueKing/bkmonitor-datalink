// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// strategyLookupSource answers the fleet's strategy standing from the
// reconciler's catalog memory, field for field. The control plane's words
// for a disposition are the fleet's already: the first screen counts by
// them.
func strategyLookupSource(reconciler *controlplane.SourceReconciler) fleet.StrategyLookupFunc {
	if reconciler == nil {
		return nil
	}
	return func(strategyID string) fleet.StrategyLookupFacts {
		return strategyLookupFactsOf(reconciler.LookupStrategy(strategyID))
	}
}

func strategyLookupFactsOf(lookup controlplane.StrategyLookup) fleet.StrategyLookupFacts {
	facts := fleet.StrategyLookupFacts{
		Available: lookup.Available, Found: lookup.Found, Retained: lookup.Retained, Global: lookup.Global,
		Publication: fleet.StrategyPublication{SnapshotRevision: string(lookup.Publication.SnapshotRevision), Epoch: lookup.Publication.PublicationEpoch},
	}
	for _, plan := range lookup.Plans {
		facts.Plans = append(facts.Plans, fleet.StrategyPlanRef{
			Tenant: plan.Plan.TenantID, Business: plan.Plan.BusinessID, QueryGroup: string(plan.QueryGroup),
			ObjectDigest: string(plan.ObjectDigest), SnapshotRevision: string(plan.SnapshotRevision),
			QueryRevision: string(plan.QueryRevision), ScheduleRevision: string(plan.ScheduleRevision),
			SourceSemantics: append([]string(nil), plan.SourceSemantics...), GroupBy: append([]string(nil), plan.GroupBy...),
			GroupByTotal: plan.GroupByTotal, PromQL: plan.PromQL,
		})
	}
	for _, disposition := range lookup.Dispositions {
		facts.Dispositions = append(facts.Dispositions, fleet.StrategyDisposition{
			Scope: disposition.Scope, LevelID: disposition.LevelID, Disposition: string(disposition.Disposition),
			Reason: disposition.Reason, FieldPath: disposition.FieldPath, Detail: disposition.Detail,
		})
	}
	return facts
}

// catalogAbsenceSource reads why this process holds no published catalog,
// live, from the same state the first screen's control source facts are
// built from. The fleet decides the word from these facts; this only hands
// them over.
//
// Read live rather than from the fleet snapshot on purpose: the snapshot is
// a published copy up to a publication interval old, and the two states this
// refusal most has to tell apart (leading, not leading) are exactly the ones
// that change at a hand-over.
// The bundle is taken as a getter because the API is wrapped before the
// bundle that owns the state exists; by the time a request arrives it does.
//
// That shape -- a variable declared before the handler and assigned after,
// read from a listener goroutine that is already running -- is the shape of
// an unsynchronised read, and the reason it is not one lives in another
// package. The listener starts first (runtime_phase_two.go, "go
// server.Run"), the bundle is assigned inside the call on the line below it,
// and the API is installed on the line after that call returns, through
// Server.SetAPI -- which stores into an atomic.Pointer that the serving
// goroutine loads before it can reach any handler (service/http/server.go).
// The atomic pair carries the assignment with it: a request that arrives
// before SetAPI cannot reach this handler at all, and one that reaches it
// has the assignment. Verified under -race over this package and the fleet.
//
// Written out because the next reader will stop at the declaration and the
// fact that settles it is two files away. The nil branch below is not
// standing in for that ordering -- it is for the readers wired before the
// bundle exists at all, which is the truth rather than a role invented here.
func catalogAbsenceSource(bundleOf func() *phaseTwoWorkerBundle) fleet.CatalogAbsenceFunc {
	if bundleOf == nil {
		return nil
	}
	return func() fleet.CatalogAbsenceFacts {
		bundle := bundleOf()
		if bundle == nil {
			// Wired, and with nothing to report yet: the word for that is
			// the one for "could not be read", not a role invented here.
			return fleet.CatalogAbsenceFacts{}
		}
		view := bundle.controlSourceView()
		facts := fleet.CatalogAbsenceFacts{
			Known: view.known, Role: string(view.role), Exit: view.lastFailureExit, Text: view.lastFailure,
		}
		if !view.degradedSince.IsZero() {
			seconds := view.now.Sub(view.degradedSince).Seconds()
			facts.FailingSeconds = &seconds
		}
		return facts
	}
}

// strategyObjectLoader is the catalog's point read of one Query Group
// object by digest, for include=config: the same read the Worker makes to
// run the object, once per Plan the reader asked about, and nothing else.
func strategyObjectLoader(repository *controlplane.RedisCatalogRepository) fleet.StrategyObjectLoader {
	if repository == nil {
		return nil
	}
	return func(ctx context.Context, objectDigest string) (controlplane.QueryGroupObject, error) {
		return repository.LoadQueryGroupObject(ctx, execution.ObjectDigest(objectDigest))
	}
}

// leaderDiscovery is the one question the forwarder asks: who leads, and
// where. The view stream's discovery answers it from the ownership store,
// and the endpoint it names is the Leader's HTTP listener -- the stream
// shares it.
type leaderDiscovery interface {
	Leader(ctx context.Context) (viewstream.LeaderEndpoint, string, error)
}

// strategyStandingForwardTimeout and directoryForwardTimeout bound a hop
// to the Leader only for a reader that set no deadline of its own (a plain
// HTTP request); a reader with one -- every CLI and admin read, through the
// channel's operation context -- bounds the hop by it (leaderForwarderWithin).
// A standing is the Leader's memory read; a directory page reads objects,
// and its own time is the reader's (controlplane.NewDirectoryView).
const (
	strategyStandingForwardTimeout = 2 * time.Second
	directoryForwardTimeout        = 2500 * time.Millisecond
)

// forwardReturnMargin is what a hop keeps back from the reader's deadline
// for the Leader's answer to come back: one transit on the cluster network
// and the copy of the body to the reader.
const forwardReturnMargin = time.Second

// forwardBodyBytes bounds the body a hop carries: the one request that has
// one, a sample window's open, is itself read within the same bound.
const forwardBodyBytes = 64 << 10

// leaderForwarder hands a request to the Leader's HTTP listener once, with
// its method and, for a write, its body. It
// marks the request so the Leader answers or refuses it and never hands it
// on; it copies the Leader's status and body back as they are. No Leader
// -- no lease, a lease holder without a registration, a registration
// without an endpoint -- is the view stream's word for which, and the
// caller carries it in the refusal.
func leaderForwarder(discovery leaderDiscovery, replica string, client *http.Client, observe forwardObserver) fleet.LeaderForward {
	return leaderForwarderWithin(discovery, replica, client, strategyStandingForwardTimeout, "strategy", observe)
}

// forwardObserver records one hop by route and result
// (metric.LeaderForwardResults) and how long the replica waited on it.
type forwardObserver func(route, result string, waited time.Duration)

// leaderForwarderWithin is leaderForwarder with the route its hops are
// recorded under, and timeout, the hop's bound for a reader that set no
// deadline. A reader with a deadline bounds the hop by it, and the Leader is
// told how long it has (fleet.WithForwardedDeadline): that deadline less
// forwardReturnMargin, so it answers -- partial where it has to -- before
// the reader's wait ends. A fixed bound per hop used to be the cut whatever
// the reader allowed. Every hop is recorded, answered or not:
// the reply only ever said FORWARD_FAILED, and a Leader that timed out, one
// not listening and one that reset the connection are three different
// things to go and look at.
//
// A request is handed on only by a process that cannot answer it: no
// catalog (a standing, a diagnosis page), no directory, not leading (the
// absent candidates). The Leader itself is in that state before its first
// successful refresh round, which with a source that fails from the start
// is for as long as it fails (2026-10-19: the strategy cache had lapsed
// and the restarted Leader never got a catalog). Discovery then names
// this replica, and the hop went to its own listener; on a deployment
// whose public surface is restricted that listener refuses the route, so
// every such read answered "moved behind the CLI" instead of why there is
// no catalog. A Leader that is this replica is never dialed: the forward
// reports nothing sent and no refusal, and the caller answers here.
//
// A hop to another replica presents the Leader's internal token, the one
// its registration carries and the evidence RPC is admitted by, so a
// restricted listener can tell a peer from the public.
func leaderForwarderWithin(discovery leaderDiscovery, replica string, client *http.Client, timeout time.Duration, route string, observe forwardObserver) fleet.LeaderForward {
	if discovery == nil {
		return nil
	}
	if client == nil {
		client = &http.Client{}
	}
	if observe == nil {
		observe = func(string, string, time.Duration) {}
	}
	return func(response http.ResponseWriter, request *http.Request) (bool, string) {
		started := time.Now()
		leader, miss, err := discovery.Leader(request.Context())
		if err != nil {
			observe(route, "no_leader", time.Since(started))
			return false, viewstream.MissDiscoveryFailed
		}
		if miss != "" {
			observe(route, "no_leader", time.Since(started))
			return false, miss
		}
		if self := strategyStandingReplica(replica); self != "" && strategyStandingReplica(leader.WorkerID) == self {
			observe(route, "self", time.Since(started))
			return false, ""
		}
		if _, _, splitErr := net.SplitHostPort(leader.Endpoint); splitErr != nil {
			observe(route, "no_leader", time.Since(started))
			return false, viewstream.MissLeaderNoEndpoint
		}
		var body io.Reader
		if request.Method != http.MethodGet && request.Body != nil {
			payload, readErr := io.ReadAll(io.LimitReader(request.Body, forwardBodyBytes+1))
			if readErr != nil || len(payload) > forwardBodyBytes {
				observe(route, "error", time.Since(started))
				return false, fleet.ForwardFailed
			}
			body = bytes.NewReader(payload)
		}
		ctx, budget := request.Context(), time.Duration(0)
		if deadline, bounded := ctx.Deadline(); bounded {
			if budget = time.Until(deadline) - forwardReturnMargin; budget <= 0 {
				observe(route, "timeout", time.Since(started))
				return false, fleet.ForwardFailed
			}
		} else {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		forwarded, err := http.NewRequestWithContext(ctx, request.Method, "http://"+leader.Endpoint+request.URL.RequestURI(), body)
		if err != nil {
			observe(route, "error", time.Since(started))
			return false, fleet.ForwardFailed
		}
		if budget > 0 {
			forwarded.Header.Set(fleet.ForwardedDeadlineHeader(), strconv.FormatInt(budget.Milliseconds(), 10))
		}
		if contentType := request.Header.Get("Content-Type"); contentType != "" && body != nil {
			forwarded.Header.Set("Content-Type", contentType)
		}
		forwarded.Header.Set(fleet.ForwardedHeader(), replica)
		if leader.InternalToken != "" {
			forwarded.Header.Set(fleet.ForwardTokenHeader(), leader.InternalToken)
		}
		reply, err := client.Do(forwarded)
		if err != nil {
			// The reader leaving first is not the Leader failing: a CLI
			// giving up, or its own deadline, cancels this hop too.
			result := forwardFailureOf(err)
			if errors.Is(request.Context().Err(), context.Canceled) {
				result = "canceled"
			}
			observe(route, result, time.Since(started))
			return false, fleet.ForwardFailed
		}
		defer reply.Body.Close()
		observe(route, "answered", time.Since(started))
		if contentType := reply.Header.Get("Content-Type"); contentType != "" {
			response.Header().Set("Content-Type", contentType)
		}
		response.Header().Set("X-Alarmd-Answered-By", leader.WorkerID)
		response.WriteHeader(reply.StatusCode)
		_, _ = io.Copy(response, reply.Body)
		return true, ""
	}
}

// forwardFailureOf names a failed hop: its bound ran out, nothing was
// listening, or anything else.
func forwardFailureOf(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	default:
		return "error"
	}
}

// strategyStandingReplica is the name a replica answers under: its Worker
// id, as every other fact of the fleet names it.
func strategyStandingReplica(workerID string) string {
	return strings.TrimSpace(workerID)
}
