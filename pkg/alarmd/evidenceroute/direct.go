// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package evidenceroute

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

type instanceStore interface {
	ReadInstance(context.Context, string) (ownership.InstanceRegistration, bool, error)
}

type registration struct {
	id, endpoint, incarnation, token string
	expiresAt                        time.Time
	roles                            roles.Set
	legacy                           bool
	// controlCapable is a registration that can hold the Control Leader
	// lease: the control role, or a Worker standing as a fallback
	// candidate. Who is the Leader is the lease's to say; this only says
	// the process the lease names is one that can be.
	controlCapable bool
}

// Absence may name an older binary's Worker registration. A malformed,
// unreadable or expired new record remains authoritative and never falls back.
func (r *Router) readRegistration(ctx context.Context, id string) (registration, bool, error) {
	if store, supported := r.options.Store.(instanceStore); supported {
		instance, found, err := store.ReadInstance(ctx, id)
		if err != nil {
			return registration{}, false, err
		}
		if found {
			if err := instance.Validate(); err != nil {
				return registration{}, false, err
			}
			if instance.InstanceID != id {
				return registration{}, false, errors.New("registered instance identity does not match")
			}
			return registration{id: instance.InstanceID, endpoint: instance.Endpoint, incarnation: instance.Incarnation,
				token: instance.InternalToken, expiresAt: instance.ExpiresAt, roles: instance.Roles,
				controlCapable: instance.ControlCapable()}, true, nil
		}
	}
	worker, found, err := r.options.Store.ReadWorker(ctx, id)
	if err != nil || !found {
		return registration{}, found, err
	}
	return registration{id: worker.WorkerID, endpoint: worker.Endpoint, token: worker.StreamToken, expiresAt: worker.ExpiresAt, legacy: true}, true, nil
}

func (r *Router) invokeDirect(ctx context.Context, call obchannel.Invocation) (obchannel.Response, bool) {
	// A legacy caller keeps the established Leader-dispatch contract. Only
	// an independently registered channel/control may originate a direct hop.
	if _, supported := r.options.Store.(instanceStore); !supported {
		return obchannel.Response{}, false
	}
	caller, found, err := r.readRegistration(ctx, r.options.WorkerID)
	if err != nil {
		return r.failure(call.RequestID, "instance_registry_unavailable", "The calling instance registration could not be read."), true
	}
	if !found || caller.legacy {
		return obchannel.Response{}, false
	}
	if !caller.expiresAt.After(r.options.Now()) || !(caller.roles.Has(roles.Channel) || caller.roles.Has(roles.Control)) {
		return r.failure(call.RequestID, "instance_identity_rejected", "An active channel or control instance is required to dispatch evidence."), true
	}
	if call.Target.ControlLeader && (call.Target.Replica != "" || call.Target.OwnerQueryGroup != "") || !call.Target.ControlLeader && ((call.Target.Replica == "") == (call.Target.OwnerQueryGroup == "")) {
		return r.failure(call.RequestID, "invalid_input", "Choose one instance, Query Group owner or control Leader."), true
	}
	target := call.Target.Replica
	var owner ownership.QueryGroupOwner
	var leader ownership.QueryGroupOwner
	if call.Target.ControlLeader {
		leader, found, err = r.options.Store.ReadActiveControlLeader(ctx)
		if err != nil || !found {
			return r.failure(call.RequestID, "control_unavailable", "No readable active control Leader is registered."), true
		}
		target = leader.OwnerID
	} else if call.Target.OwnerQueryGroup != "" {
		owner, found, err = r.options.Store.ReadQueryGroupOwner(ctx, execution.QueryGroupIdentity(call.Target.OwnerQueryGroup))
		if err != nil || !found {
			return r.failure(call.RequestID, "owner_unavailable", "This Query Group has no readable active execution owner."), true
		}
		target = owner.OwnerID
	}
	fail := func(code, message string) obchannel.Response {
		response := r.failure(call.RequestID, code, message)
		if call.Target.ControlLeader {
			response.Meta.ControlLeader = &obchannel.LeaderMeta{OwnerID: leader.OwnerID, OwnerEpoch: leader.OwnerEpoch}
		}
		return response
	}
	identity, found, err := r.readRegistration(ctx, target)
	if err != nil || !found || !identity.expiresAt.After(r.options.Now()) {
		return fail("target_unavailable", "Target registration is unavailable or expired."), true
	}
	if identity.legacy {
		return obchannel.Response{}, false
	}
	if call.Target.ControlLeader && !identity.controlCapable || call.Target.OwnerQueryGroup != "" && !identity.roles.Has(roles.Worker) {
		return fail("target_role_mismatch", "The registered target no longer declares the requested business role."), true
	}
	expected := call.Target.ExpectedIncarnation
	if expected == "" {
		expected = identity.incarnation
	}
	params, err := json.Marshal(call.Params)
	if err != nil {
		return fail("invalid_input", "Evidence parameters cannot be encoded."), true
	}
	req := &pb.EvidenceRequest{WorkerId: r.options.WorkerID, StreamToken: r.options.StreamToken, CallerIncarnation: r.options.Incarnation,
		Phase: "direct", EnvironmentId: call.EnvironmentID, RequestId: call.RequestID, ChannelVersion: call.Version,
		CatalogRevision: call.Revision, OperationContractRevision: call.OperationContractRevision, Operation: call.Operation, ParamsJson: params,
		TargetWorkerId: target, ExpectedIncarnation: expected, OwnerQueryGroup: call.Target.OwnerQueryGroup, OwnerEpoch: owner.OwnerEpoch,
		ControlLeaderTarget: call.Target.ControlLeader, ControlEpoch: leader.OwnerEpoch}
	response, err := r.send(ctx, target, req)
	if err != nil {
		response = r.transportFailure(call.RequestID, err)
	}
	response.Meta.Via = []string{r.options.WorkerID}
	if call.Target.ControlLeader {
		response.Meta.ControlLeader = &obchannel.LeaderMeta{OwnerID: leader.OwnerID, OwnerEpoch: leader.OwnerEpoch}
	}
	return response, true
}

func (r *Router) handleDirect(ctx context.Context, req *pb.EvidenceRequest, caller registration) (*pb.EvidenceResult, error) {
	if caller.legacy || !(caller.roles.Has(roles.Channel) || caller.roles.Has(roles.Control)) {
		return nil, status.Error(codes.PermissionDenied, "Only registered channel or control roles may dispatch direct evidence.")
	}
	if req.TargetWorkerId != r.options.WorkerID {
		return r.encode(r.failure(req.RequestId, "target_changed", "The receiving instance is not the requested target."))
	}
	local, found, err := r.readRegistration(ctx, r.options.WorkerID)
	if err != nil || !found || local.legacy || !local.expiresAt.After(r.options.Now()) || local.incarnation != r.options.Incarnation || local.incarnation != req.ExpectedIncarnation {
		return r.encode(r.failure(req.RequestId, "target_changed", "The target's registered incarnation is unavailable or changed."))
	}
	if req.ControlLeaderTarget {
		leader, found, err := r.options.Store.ReadActiveControlLeader(ctx)
		if err != nil || !found || leader.OwnerID != r.options.WorkerID || leader.OwnerEpoch != req.ControlEpoch || !local.controlCapable {
			return r.encode(r.failure(req.RequestId, "control_changed", "The control Leader or its lease term changed before this read."))
		}
		if req.OwnerQueryGroup != "" || req.OwnerEpoch != 0 {
			return nil, status.Error(codes.InvalidArgument, "Control and execution owner targets are mutually exclusive.")
		}
	} else {
		if req.ControlEpoch != 0 {
			return nil, status.Error(codes.InvalidArgument, "A control epoch requires a control Leader target.")
		}
		if req.OwnerQueryGroup != "" && !local.roles.Has(roles.Worker) {
			return r.encode(r.failure(req.RequestId, "target_role_mismatch", "The execution owner no longer declares a Worker role."))
		}
	}
	return r.execute(ctx, req)
}
