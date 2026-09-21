// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// The Leader's side of decision-016's view stream, wired into the
// reconcile round: the round that placed every Query Group hands the
// stream the desired set it arrived at, and the stream projects it per
// Worker. Nothing in execution reads the stream in this step (the shadow
// of 016 section 7.1); a failure anywhere in it is reported and the round
// stands.

// viewStreamRegistry is what admission reads: the Worker's own
// registration, where it wrote the token it now presents.
type viewStreamRegistry interface {
	ReadWorker(context.Context, string) (ownership.WorkerRegistration, bool, error)
}

// viewStreamAdmission admits a Hello when a live registration of the
// Worker carries the token the Hello presents. The registry is the trust
// root; a registration without a token is a binary from before the stream,
// and its Hello -- which such a binary never sends -- is refused as a bad
// token rather than admitted on absence.
type viewStreamAdmission struct {
	registry viewStreamRegistry
	now      func() time.Time
}

func (admission viewStreamAdmission) Admit(ctx context.Context, workerID, token string) (string, error) {
	if admission.registry == nil {
		return "", errors.New("phase-two view stream: worker registry is required")
	}
	registration, found, err := admission.registry.ReadWorker(ctx, workerID)
	if err != nil {
		return "", err
	}
	if !found || !registration.ExpiresAt.After(admission.now()) {
		return viewstream.RefusalUnknownWorker, nil
	}
	if registration.StreamToken == "" || subtle.ConstantTimeCompare([]byte(registration.StreamToken), []byte(token)) != 1 {
		return viewstream.RefusalBadToken, nil
	}
	return "", nil
}

// viewStreamIdentity is what this process advertises in its registration
// for the stream: where it serves it and the token a Worker must present
// to it. Minted once per process; the token never leaves the registration
// and the Hello.
type viewStreamIdentity struct {
	Endpoint string
	Token    string
	// Unadvertised names why Endpoint is empty, so a process that serves
	// the stream without being reachable says so once at start instead of
	// every other Worker saying it every reconnect.
	Unadvertised string
}

// newViewStreamIdentity derives the endpoint from the HTTP listener's port
// and the address this process reaches Redis from -- the stream shares the
// HTTP listener, and the interface that reaches the control plane is the
// one other replicas reach this one on. A listener bound to a specific
// address advertises that address. An endpoint that cannot be derived is
// left empty: the process serves the stream but is not advertised, which
// a Worker reads as "the Leader has no endpoint" and reports.
// The routes are the addresses this process dials Redis at; for a wildcard
// bind the endpoint's host is the local address the kernel would route to
// the first of them that resolves. A Sentinel deployment has no Redis
// address, only sentinel addresses, and a process handed the empty address
// alone advertised no endpoint at all: every Worker's discovery then found
// the Leader's lease, read its registration, saw no endpoint and reported
// NO_LEADER on a fleet whose Leader was publishing (#216). When no route
// resolves, the first global unicast address of an interface is used; only
// a process with neither is left unadvertised, and the bundle says so.
func newViewStreamIdentity(listen string, routes ...string) (viewStreamIdentity, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return viewStreamIdentity{}, fmt.Errorf("phase-two view stream: mint token: %w", err)
	}
	identity := viewStreamIdentity{Token: hex.EncodeToString(token)}
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		identity.Unadvertised = "LISTENER_UNPARSABLE"
		return identity, nil
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = ""
		for _, route := range routes {
			if host = outboundAddress(route); host != "" {
				break
			}
		}
		if host == "" {
			host = interfaceAddress()
		}
	}
	if host == "" {
		identity.Unadvertised = "NO_ROUTE"
		return identity, nil
	}
	identity.Endpoint = net.JoinHostPort(host, port)
	return identity, nil
}

// viewStreamRoutes lists the addresses a Redis connection dials, in the
// order the endpoint derivation should try them: the Redis address when
// there is one, then the sentinels.
func viewStreamRoutes(connection config.RedisConnectionConfig) []string {
	routes := make([]string, 0, 1+len(connection.SentinelAddress))
	if connection.Address != "" {
		routes = append(routes, connection.Address)
	}
	for _, sentinel := range connection.SentinelAddress {
		if sentinel != "" {
			routes = append(routes, sentinel)
		}
	}
	return routes
}

// outboundAddress is the local address a UDP socket toward target would
// send from: no packet is sent, the kernel only picks the route. Empty
// when the route cannot be resolved.
func outboundAddress(target string) string {
	if target == "" {
		return ""
	}
	conn, err := net.Dial("udp", target)
	if err != nil {
		return ""
	}
	defer conn.Close()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || local.IP == nil {
		return ""
	}
	return local.IP.String()
}

// interfaceAddress is the first global unicast address of any interface,
// the address a Pod is reachable at when no route could be asked about.
// Empty when the process has none, which on a Pod means no network.
func interfaceAddress() string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addresses {
		network, ok := address.(*net.IPNet)
		if !ok || network.IP == nil || !network.IP.IsGlobalUnicast() {
			continue
		}
		return network.IP.String()
	}
	return ""
}

// viewSource is what the round needs of the catalog to build the desired
// set: the activation and the published content it names.
type viewSource interface {
	LoadActivation(context.Context) (controlplane.ActivationState, error)
	LoadPublishedContent(context.Context, controlplane.SnapshotPublicationRef) (controlplane.PublishedContent, error)
}

// viewLead and viewStepDown follow the control leader authority: a term
// begins a publisher, losing the authority ends it. Both are no-ops for a
// runtime without a stream.
func (runtime *productionPhaseTwoOwnership) viewLead(controlEpoch uint64) {
	if runtime.dependencies.ViewStream == nil {
		return
	}
	_ = runtime.dependencies.ViewStream.Lead(controlEpoch)
}

func (runtime *productionPhaseTwoOwnership) viewStepDown() {
	if runtime.dependencies.ViewStream == nil {
		return
	}
	runtime.dependencies.ViewStream.StepDown()
}

// publishView hands the stream the desired set this round arrived at:
// the activation the fleet executes, the content it publishes for each
// Query Group, and the Assignment record of each as the round settled it,
// with the owner the round's rebalance moved it to. Advisory: a failure is
// reported under the stream's own stage and the round stands.
func (runtime *productionPhaseTwoOwnership) publishView(
	ctx context.Context,
	authority ownership.PublicationAuthority,
	records map[execution.QueryGroupIdentity]ownership.AssignmentRecord,
	owners map[execution.QueryGroupIdentity]string,
) {
	stream, source := runtime.dependencies.ViewStream, runtime.dependencies.ViewSource
	if stream == nil || source == nil {
		return
	}
	report := func(err error) {
		observeRuntime(ctx, runtime.dependencies.Observer, observability.Observation{
			Component: observability.ComponentOwnership, Stage: observability.StageViewPublished,
			Result: observability.ResultDegraded, ReasonCode: observability.ReasonInternalUnknown, Err: err,
			ViewStream: &observability.ViewStreamFacts{Event: "publish_failed", ControlEpoch: authority.Fence.OwnerEpoch, Reason: err.Error()},
		})
	}
	state, err := source.LoadActivation(ctx)
	if err != nil {
		report(fmt.Errorf("read activation: %w", err))
		return
	}
	published, err := source.LoadPublishedContent(ctx, state.Current)
	if err != nil {
		report(fmt.Errorf("read published content %s: %w", state.Current.SnapshotRevision, err))
		return
	}
	desired := viewstream.Desired{
		ControlEpoch: authority.Fence.OwnerEpoch,
		Publication: viewstream.Publication{SnapshotRevision: state.Current.SnapshotRevision, PublicationEpoch: state.Current.PublicationEpoch,
			ActivationRecordRevision: state.RecordRevision},
		Content:     make(map[execution.QueryGroupIdentity]viewstream.Content, len(published.Groups)),
		Assignments: make(map[execution.QueryGroupIdentity]viewstream.Assignment, len(records)),
	}
	for identity, entry := range published.Groups {
		content := viewstream.Content{ObjectDigest: entry.Digest, OutputContexts: make([]viewstream.OutputContextRef, 0, len(entry.Refs))}
		for _, ref := range entry.Refs {
			content.OutputContexts = append(content.OutputContexts, viewstream.OutputContextRef{Plan: ref.Plan, Digest: ref.Digest})
		}
		desired.Content[identity] = content
	}
	for identity, record := range records {
		assignment := viewstream.Assignment{
			DesiredWorkerID: record.DesiredWorkerID, Revision: record.RecordRevision,
			ContentScope: record.ContentScope, PendingContentScope: record.PendingContentScope,
		}
		if !record.EffectiveAt.IsZero() {
			assignment.EffectiveAtMs = record.EffectiveAt.UnixMilli()
		}
		if owner, moved := owners[identity]; moved && owner != "" {
			// The round's rebalance moved it after the record was read; the
			// revision is the read one and the next round's read corrects it.
			assignment.DesiredWorkerID = owner
		}
		desired.Assignments[identity] = assignment
	}
	if _, err := stream.Publish(ctx, desired); err != nil {
		report(err)
	}
}

// viewStreamDiscovery finds the Leader's stream from the records that
// already exist: the control leader lease names who leads, that worker's
// own registration names where. A Leader whose registration carries no
// endpoint is a binary from before the stream, and "not found" until it
// is replaced.
type viewStreamDiscovery struct {
	store interface {
		ReadControlLeader(context.Context) (ownership.ControlLeader, bool, error)
		ReadWorker(context.Context, string) (ownership.WorkerRegistration, bool, error)
	}
}

// Leader names each way of not finding one apart: the three are read by
// different people. No lease is the control plane between terms; a lease
// naming a Worker without a live registration is a Leader that died with
// its lease; a registration without an endpoint is a Leader that could not
// work out its own address (#216: every Worker reported the third as if it
// were the first).
func (discovery viewStreamDiscovery) Leader(ctx context.Context) (viewstream.LeaderEndpoint, string, error) {
	if discovery.store == nil {
		return viewstream.LeaderEndpoint{}, "", errors.New("phase-two view stream: ownership store is required")
	}
	leader, found, err := discovery.store.ReadControlLeader(ctx)
	if err != nil {
		return viewstream.LeaderEndpoint{}, "", err
	}
	if !found {
		return viewstream.LeaderEndpoint{}, viewstream.MissNoLeader, nil
	}
	registration, found, err := discovery.store.ReadWorker(ctx, leader.OwnerID)
	if err != nil {
		return viewstream.LeaderEndpoint{}, "", err
	}
	if !found {
		return viewstream.LeaderEndpoint{}, viewstream.MissLeaderUnregistered, nil
	}
	if registration.Endpoint == "" {
		return viewstream.LeaderEndpoint{}, viewstream.MissLeaderNoEndpoint, nil
	}
	return viewstream.LeaderEndpoint{WorkerID: leader.OwnerID, ControlEpoch: leader.OwnerEpoch, Endpoint: registration.Endpoint}, "", nil
}

// newViewStreamIncarnation names this process for the stream: the id the
// Leader's ledger tells one process of a Worker from the next. Random, so
// two starts of one Pod within a second are two incarnations.
func newViewStreamIncarnation() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("phase-two view stream: mint incarnation: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// runViewClient keeps the Worker's stream to the Leader up for the life of
// the bundle. Its failures are its own: nothing in execution waits on it.
func (bundle *phaseTwoWorkerBundle) runViewClient() {
	defer bundle.maintenanceWG.Done()
	_ = bundle.dependencies.ViewClient.Run(bundle.maintenanceCtx)
}
