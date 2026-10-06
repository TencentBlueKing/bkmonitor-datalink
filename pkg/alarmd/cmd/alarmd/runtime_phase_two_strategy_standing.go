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
	"context"
	"errors"
	"io"
	"net"
	"net/http"
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
		Available: lookup.Available, Found: lookup.Found, Retained: lookup.Retained,
		Publication: fleet.StrategyPublication{SnapshotRevision: string(lookup.Publication.SnapshotRevision), Epoch: lookup.Publication.PublicationEpoch},
	}
	for _, plan := range lookup.Plans {
		facts.Plans = append(facts.Plans, fleet.StrategyPlanRef{
			Tenant: plan.Plan.TenantID, Business: plan.Plan.BusinessID, QueryGroup: string(plan.QueryGroup),
			ObjectDigest: string(plan.ObjectDigest), SnapshotRevision: string(plan.SnapshotRevision),
			QueryRevision: string(plan.QueryRevision), ScheduleRevision: string(plan.ScheduleRevision),
		})
	}
	for _, disposition := range lookup.Dispositions {
		facts.Dispositions = append(facts.Dispositions, fleet.StrategyDisposition{
			Scope: disposition.Scope, LevelID: disposition.LevelID, Disposition: string(disposition.Disposition),
			Reason: disposition.Reason, FieldPath: disposition.FieldPath,
		})
	}
	return facts
}

// catalogAbsenceSource reads why this process holds no published catalog,
// live, from the same state the first screen's control source facts are
// built from. The fleet decides the word from these facts; this only hands
// them over, plus whether the strategy directory -- the other route that
// answers the same question, from the store -- is mounted on this process.
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
func catalogAbsenceSource(bundleOf func() *phaseTwoWorkerBundle, directoryMounted bool) fleet.CatalogAbsenceFunc {
	if bundleOf == nil {
		return nil
	}
	return func() fleet.CatalogAbsenceFacts {
		bundle := bundleOf()
		if bundle == nil {
			// Wired, and with nothing to report yet: the word for that is
			// the one for "could not be read", not a role invented here.
			return fleet.CatalogAbsenceFacts{DirectoryMounted: directoryMounted}
		}
		view := bundle.controlSourceView()
		facts := fleet.CatalogAbsenceFacts{
			Known: view.known, Role: string(view.role), Exit: view.lastFailureExit, Text: view.lastFailure,
			DirectoryMounted: directoryMounted,
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

// strategyStandingForwardTimeout bounds the one hop. The Leader answers from
// memory; a hop that takes longer is a Leader that is not answering, and
// the reader is told that rather than kept waiting.
const strategyStandingForwardTimeout = 2 * time.Second

// leaderForwarder hands a request to the Leader's HTTP listener once. It
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

// leaderForwarderWithin is leaderForwarder with the hop's own bound, and the
// route its hops are recorded under. Every hop is recorded, answered or not:
// the reply only ever said FORWARD_FAILED, and a Leader that timed out, one
// not listening and one that reset the connection are three different
// things to go and look at.
func leaderForwarderWithin(discovery leaderDiscovery, replica string, client *http.Client, timeout time.Duration, route string, observe forwardObserver) fleet.LeaderForward {
	if discovery == nil {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
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
		if _, _, splitErr := net.SplitHostPort(leader.Endpoint); splitErr != nil {
			observe(route, "no_leader", time.Since(started))
			return false, viewstream.MissLeaderNoEndpoint
		}
		ctx, cancel := context.WithTimeout(request.Context(), timeout)
		defer cancel()
		forwarded, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+leader.Endpoint+request.URL.RequestURI(), nil)
		if err != nil {
			observe(route, "error", time.Since(started))
			return false, fleet.ForwardFailed
		}
		forwarded.Header.Set(fleet.ForwardedHeader(), replica)
		reply, err := client.Do(forwarded)
		if err != nil {
			// The reader leaving first is not the Leader failing: a CLI
			// giving up, or its own deadline, cancels this hop too.
			result := forwardFailureOf(err)
			if request.Context().Err() != nil {
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
