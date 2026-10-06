// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package evidenceroute routes bounded OB reads through the current Control
// Leader. It uses the existing Worker identity and ControlService listener;
// CLI sessions remain at the authenticated HTTP ingress.
package evidenceroute

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

type Store interface {
	ReadWorker(context.Context, string) (ownership.WorkerRegistration, bool, error)
	ReadActiveControlLeader(context.Context) (ownership.QueryGroupOwner, bool, error)
	ReadQueryGroupOwner(context.Context, execution.QueryGroupIdentity) (ownership.QueryGroupOwner, bool, error)
}

type Options struct {
	Store                                Store
	WorkerID, StreamToken, EnvironmentID string
	Build, Incarnation, CatalogRevision  string
	Execute                              func(context.Context, obchannel.Invocation) obchannel.Response
	Now                                  func() time.Time
}

type Router struct{ options Options }

func New(options Options) (*Router, error) {
	if options.Store == nil || options.WorkerID == "" || options.StreamToken == "" || options.EnvironmentID == "" || options.Execute == nil {
		return nil, errors.New("OB evidence routing requires Worker identity, diagnostic store and local executor")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Router{options: options}, nil
}

// Invoke is called only after the ingress has admitted the CLI session. Every
// targeted read, including one targeting the ingress itself, visits the Leader.
func (r *Router) Invoke(ctx context.Context, call obchannel.Invocation) obchannel.Response {
	ctx, cancel := context.WithTimeout(ctx, obchannel.RequestTimeout)
	defer cancel()
	params, err := json.Marshal(call.Params)
	if err != nil {
		return r.failure(call.RequestID, "invalid_input", "Evidence parameters cannot be encoded.")
	}
	leader, found, err := r.options.Store.ReadActiveControlLeader(ctx)
	if err != nil || !found {
		return r.failure(call.RequestID, "control_unavailable", "No readable active Control Leader; this targeted read did not fall back to another instance.")
	}
	target := call.Target.Replica
	if call.Target.ControlLeader {
		// Resolved from the lease this read is routed by, and the Leader
		// re-checks that term before it executes, so a handover in between
		// fails the read instead of answering from the old Leader.
		target = leader.OwnerID
	}
	req := &pb.EvidenceRequest{WorkerId: r.options.WorkerID, StreamToken: r.options.StreamToken, Phase: "route", EnvironmentId: call.EnvironmentID,
		RequestId: call.RequestID, ChannelVersion: call.Version, CatalogRevision: call.Revision, Operation: call.Operation, ParamsJson: params,
		TargetWorkerId: target, ExpectedIncarnation: call.Target.ExpectedIncarnation, OwnerQueryGroup: call.Target.OwnerQueryGroup, ControlEpoch: leader.OwnerEpoch}
	response, err := r.send(ctx, leader.OwnerID, req)
	if err != nil {
		response = r.transportFailure(call.RequestID, err)
	} else {
		response.Meta.Via = append([]string{r.options.WorkerID}, response.Meta.Via...)
	}
	if call.Target.ControlLeader {
		response.Meta.ControlLeader = &obchannel.LeaderMeta{OwnerID: leader.OwnerID, OwnerEpoch: leader.OwnerEpoch}
	}
	return response
}

// Handle is mounted on ControlService.ReadEvidence. All registry reads use the
// small diagnostic Redis pool. No CLI token, session hash or issuer key enters
// this protocol. The existing registered Worker token authenticates each hop.
func (r *Router) Handle(ctx context.Context, req *pb.EvidenceRequest) (*pb.EvidenceResult, error) {
	if req == nil || proto.Size(req) > obchannel.MaxRequestBytes {
		return nil, status.Error(codes.InvalidArgument, "Evidence request exceeds its budget.")
	}
	ctx, cancel := context.WithTimeout(ctx, obchannel.RequestTimeout)
	defer cancel()
	if req.EnvironmentId != r.options.EnvironmentID || req.ChannelVersion != obchannel.Version || req.RequestId == "" {
		return nil, status.Error(codes.FailedPrecondition, "Evidence environment or protocol does not match.")
	}
	caller, found, err := r.options.Store.ReadWorker(ctx, req.WorkerId)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "Worker registry is unavailable.")
	}
	if !found || !caller.ExpiresAt.After(r.options.Now()) || caller.StreamToken == "" || subtle.ConstantTimeCompare([]byte(caller.StreamToken), []byte(req.StreamToken)) != 1 {
		return nil, status.Error(codes.Unauthenticated, "Registered Worker identity is required.")
	}
	leader, found, err := r.options.Store.ReadActiveControlLeader(ctx)
	if err != nil || !found || req.ControlEpoch != leader.OwnerEpoch {
		return r.encode(r.failure(req.RequestId, "control_changed", "The active Control Leader is unavailable or its term changed."))
	}
	switch req.Phase {
	case "route":
		if leader.OwnerID != r.options.WorkerID {
			return r.encode(r.failure(req.RequestId, "control_changed", "This instance is no longer Control Leader."))
		}
		return r.route(ctx, req)
	case "execute":
		if caller.WorkerID != leader.OwnerID {
			return nil, status.Error(codes.PermissionDenied, "Only the active Control Leader can dispatch evidence execution.")
		}
		if req.TargetWorkerId != r.options.WorkerID {
			return r.encode(r.failure(req.RequestId, "target_changed", "The receiving Worker is not the requested target."))
		}
		return r.execute(ctx, req)
	default:
		return nil, status.Error(codes.InvalidArgument, "Unknown evidence phase.")
	}
}

func (r *Router) route(ctx context.Context, req *pb.EvidenceRequest) (*pb.EvidenceResult, error) {
	if (req.TargetWorkerId == "") == (req.OwnerQueryGroup == "") || req.OwnerEpoch != 0 {
		return nil, status.Error(codes.InvalidArgument, "Choose one Worker or Query Group owner.")
	}
	// Copy before replacing the caller: never mutate a request shared with its
	// caller, and never carry the ingress credential into the next hop.
	target := proto.Clone(req).(*pb.EvidenceRequest)
	if target.OwnerQueryGroup != "" {
		owner, found, err := r.options.Store.ReadQueryGroupOwner(ctx, execution.QueryGroupIdentity(target.OwnerQueryGroup))
		if err != nil {
			return r.encode(r.failure(req.RequestId, "owner_unavailable", "Execution ownership could not be read."))
		}
		if !found {
			return r.encode(r.failure(req.RequestId, "owner_unavailable", "This Query Group has no active execution owner."))
		}
		target.TargetWorkerId, target.OwnerEpoch = owner.OwnerID, owner.OwnerEpoch
	}
	target.Phase, target.WorkerId, target.StreamToken = "execute", r.options.WorkerID, r.options.StreamToken
	response, err := r.send(ctx, target.TargetWorkerId, target)
	if err != nil {
		response = r.transportFailure(req.RequestId, err)
	}
	response.Meta.Via = []string{r.options.WorkerID}
	return r.encode(response)
}

func (r *Router) execute(ctx context.Context, req *pb.EvidenceRequest) (*pb.EvidenceResult, error) {
	var ownerMeta *obchannel.OwnerMeta
	if req.OwnerQueryGroup != "" {
		owner, found, err := r.options.Store.ReadQueryGroupOwner(ctx, execution.QueryGroupIdentity(req.OwnerQueryGroup))
		if err != nil {
			return r.encode(r.failure(req.RequestId, "owner_unavailable", "Execution ownership could not be verified at the target."))
		}
		if !found || owner.OwnerID != r.options.WorkerID || owner.OwnerEpoch != req.OwnerEpoch {
			return r.encode(r.failure(req.RequestId, "target_changed", "Execution ownership changed before the target admitted this read."))
		}
		ownerMeta = &obchannel.OwnerMeta{QueryGroup: req.OwnerQueryGroup, OwnerID: owner.OwnerID, OwnerEpoch: owner.OwnerEpoch, Deadline: owner.Deadline, ObservedAt: owner.ObservedAt}
	} else if req.OwnerEpoch != 0 {
		return nil, status.Error(codes.InvalidArgument, "An owner epoch requires a Query Group.")
	}
	params := obchannel.Params{}
	decoder := json.NewDecoder(bytes.NewReader(req.ParamsJson))
	decoder.UseNumber()
	if err := decoder.Decode(&params); err != nil {
		return nil, status.Error(codes.InvalidArgument, "Invalid evidence parameters.")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, status.Error(codes.InvalidArgument, "Only one parameters object is accepted.")
	}
	response := r.options.Execute(ctx, obchannel.Invocation{EnvironmentID: req.EnvironmentId, Version: req.ChannelVersion, Revision: req.CatalogRevision, Operation: req.Operation, RequestID: req.RequestId, Params: params,
		Target: obchannel.Target{Replica: req.TargetWorkerId, OwnerQueryGroup: req.OwnerQueryGroup, ExpectedIncarnation: req.ExpectedIncarnation}})
	response.Meta.Owner = ownerMeta
	return r.encode(response)
}

func (r *Router) send(ctx context.Context, workerID string, req *pb.EvidenceRequest) (obchannel.Response, error) {
	if proto.Size(req) > obchannel.MaxRequestBytes {
		return obchannel.Response{}, status.Error(codes.ResourceExhausted, "Evidence request exceeds its budget.")
	}
	var result *pb.EvidenceResult
	var err error
	if workerID == r.options.WorkerID {
		result, err = r.Handle(ctx, req)
	} else {
		worker, found, readErr := r.options.Store.ReadWorker(ctx, workerID)
		if readErr != nil || !found || !worker.ExpiresAt.After(r.options.Now()) {
			return obchannel.Response{}, status.Error(codes.Unavailable, "Target registration is unavailable or expired.")
		}
		host, port, addressErr := net.SplitHostPort(worker.Endpoint)
		if addressErr != nil || host == "" || port == "" {
			return obchannel.Response{}, status.Error(codes.Unavailable, "Target has no registered control endpoint.")
		}
		// The same private-deployment h2c transport as ControlService.Connect.
		// Each bounded read owns its connection; there is no new discovery loop.
		conn, dialErr := grpc.DialContext(ctx, worker.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(),
			grpc.WithDefaultCallOptions(grpc.MaxCallSendMsgSize(obchannel.MaxRequestBytes), grpc.MaxCallRecvMsgSize(obchannel.MaxResponseBytes)))
		if dialErr != nil {
			if err := ctx.Err(); err != nil {
				return obchannel.Response{}, status.FromContextError(err).Err()
			}
			return obchannel.Response{}, status.Error(codes.Unavailable, "Target control endpoint is unreachable.")
		}
		defer conn.Close()
		result, err = pb.NewControlServiceClient(conn).ReadEvidence(ctx, req)
	}
	if err != nil {
		return obchannel.Response{}, err
	}
	if result == nil || len(result.ResponseJson) > obchannel.MaxResponseBytes {
		return obchannel.Response{}, status.Error(codes.DataLoss, "Invalid evidence response.")
	}
	var response obchannel.Response
	decoder := json.NewDecoder(bytes.NewReader(result.ResponseJson))
	decoder.UseNumber()
	if err := decoder.Decode(&response); err != nil || response.Meta.EnvironmentID != r.options.EnvironmentID || response.Meta.Version != obchannel.Version || response.Meta.RequestID != req.RequestId || response.Meta.Session != nil {
		return obchannel.Response{}, status.Error(codes.DataLoss, "Evidence response identity does not match this request.")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return obchannel.Response{}, status.Error(codes.DataLoss, "Only one evidence response is accepted.")
	}
	if req.Phase == "execute" && response.Meta.AnsweredBy != workerID {
		return obchannel.Response{}, status.Error(codes.DataLoss, "Evidence response came from a different Worker.")
	}
	return response, nil
}

func (r *Router) encode(response obchannel.Response) (*pb.EvidenceResult, error) {
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded)+16 > obchannel.MaxResponseBytes {
		return nil, status.Error(codes.ResourceExhausted, "Evidence response exceeds its budget.")
	}
	return &pb.EvidenceResult{ResponseJson: encoded}, nil
}

func (r *Router) failure(requestID, code, message string) obchannel.Response {
	return obchannel.Response{Status: "error", Summary: message, Error: &obchannel.Failure{Code: code, Message: message}, Evidence: obchannel.Evidence{Limitations: []string{"targeted_evidence_not_returned"}}, Next: []obchannel.Call{},
		Meta: obchannel.Meta{Version: obchannel.Version, Revision: r.options.CatalogRevision, EnvironmentID: r.options.EnvironmentID, AnsweredBy: r.options.WorkerID, Build: r.options.Build, Incarnation: r.options.Incarnation, RequestID: requestID, RespondedAt: r.options.Now().UTC()}}
}

func (r *Router) transportFailure(requestID string, err error) obchannel.Response {
	code, message := "target_unavailable", "The control-plane evidence request failed; no other instance was queried."
	switch status.Code(err) {
	case codes.Unimplemented:
		code, message = "target_unsupported", "The Leader or target does not support control-plane evidence reads; upgrade that instance."
	case codes.ResourceExhausted:
		code, message = "request_budget_exceeded", "Control-plane evidence slots or byte limits were exceeded."
	case codes.Unauthenticated, codes.PermissionDenied:
		code, message = "worker_identity_rejected", "The control-plane Worker identity was not accepted."
	case codes.FailedPrecondition:
		code, message = "target_environment_mismatch", "The target environment or evidence protocol does not match."
	case codes.DataLoss:
		code, message = "target_response_invalid", "The target returned an invalid evidence response."
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		code, message = "target_timeout", "The targeted read exceeded its shared deadline."
	}
	return r.failure(requestID, code, message)
}
