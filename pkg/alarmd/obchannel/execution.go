// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

// Invocation is the bounded operation contract carried by an authenticated
// internal Worker RPC. It deliberately contains no CLI token or session.
// Params contains only domain fields; the entry separates targeting fields.
type Invocation struct {
	EnvironmentID string `json:"environment_id"`
	Version       string `json:"channel_version"`
	Revision      string `json:"expected_catalog_revision"`
	Operation     string `json:"operation"`
	RequestID     string `json:"request_id"`
	Params        Params `json:"params"`
	Target        Target `json:"target"`
}

type Target struct {
	Replica             string `json:"replica,omitempty"`
	OwnerQueryGroup     string `json:"owner_query_group,omitempty"`
	ExpectedIncarnation string `json:"expected_incarnation,omitempty"`
	// ControlLeader asks for whichever Worker holds the Control Leader lease
	// when the ingress routes the read. The ingress resolves it to Replica
	// from the same lease read it routes by, so the answer comes from the
	// Leader of that term or fails as control_changed -- never from another
	// instance.
	ControlLeader bool `json:"control_leader,omitempty"`
}

// Explicit is whether the read names a target at all.
func (t Target) Explicit() bool {
	return t.Replica != "" || t.OwnerQueryGroup != "" || t.ControlLeader
}

func (c *Channel) CatalogRevision() string { return c.revision }

func (c *Channel) localMeta(requestID string) Meta {
	if requestID == "" {
		var id [16]byte
		if _, err := rand.Read(id[:]); err == nil {
			requestID = hex.EncodeToString(id[:])
		}
	}
	return Meta{Version: Version, Revision: c.revision, EnvironmentID: c.options.EnvironmentID, AnsweredBy: c.options.Replica, Build: c.options.Build, Incarnation: c.options.Incarnation, RequestID: requestID}
}

// ExecuteEvidence executes only a local registered read. Its caller must first
// authenticate the internal Worker RPC; it is not an HTTP or CLI auth bypass.
// This method neither authenticates a CLI session nor renews or forwards it.
func (c *Channel) ExecuteEvidence(ctx context.Context, invocation Invocation) Response {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	meta := c.localMeta(invocation.RequestID)
	fail := func(code, message string) Response { return c.prepareResponse(failure(meta, code, message)) }
	if invocation.EnvironmentID != c.options.EnvironmentID {
		return fail("target_environment_mismatch", "Internal evidence request targets a different environment.")
	}
	if invocation.Version != Version {
		return fail("unsupported_channel_version", "This target supports "+Version)
	}
	if invocation.Revision != c.revision {
		return fail("target_catalog_mismatch", "The target operation catalog differs; this invocation did not execute.")
	}
	if len(invocation.RequestID) > 128 {
		meta.RequestID = ""
		return fail("invalid_input", "Internal request identifier exceeds its bound.")
	}
	if invocation.Target.Replica == "" || invocation.Target.Replica != c.options.Replica {
		return fail("target_changed", "This worker is not the resolved target replica.")
	}
	if err := validateInternalTarget(invocation.Target); err != nil {
		return fail("invalid_input", err.Error())
	}
	if expected := invocation.Target.ExpectedIncarnation; expected != "" && expected != c.options.Incarnation {
		return fail("target_changed", "The resolved target process incarnation changed.")
	}
	op, ok := c.ops[invocation.Operation]
	if !ok {
		return fail("unknown_operation", "Operation is not registered on the target.")
	}
	if !op.Targetable {
		return fail("operation_not_targetable", "This operation is deployment-scoped and does not accept a process target.")
	}
	if err := validate(op, invocation.Params); err != nil {
		return fail("invalid_input", err.Error())
	}
	if availability := available(op); !availability.Available {
		return fail("operation_unavailable", availability.Reason)
	}
	if ctx.Err() != nil {
		return fail("request_timeout", "Evidence execution context ended before execution.")
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return fail("request_budget_exceeded", "Target evidence readers are busy; retry this read later.")
	}
	return c.prepareResponse(c.run(ctx, op, invocation.Params, invocation.Target, meta))
}

func failure(meta Meta, code, message string) Response {
	return Response{Status: "error", Summary: message, Error: &Failure{Code: code, Message: message}, Meta: meta}
}

func (c *Channel) run(ctx context.Context, op Operation, params Params, target Target, meta Meta) Response {
	if ctx.Err() != nil {
		return failure(meta, "request_timeout", "Evidence execution context ended before execution.")
	}
	out := op.Run(ctx, params)
	if ctx.Err() != nil {
		return failure(meta, "request_timeout", "Evidence execution exceeded its request context; no complete result is returned.")
	}
	status := "ok"
	if !out.Complete {
		status = "partial"
	}
	if out.Error != nil {
		status = "error"
	}
	if out.Summary == "" {
		out.Summary = op.Summary
	}
	return Response{Status: status, Summary: out.Summary, Result: out.Value, Evidence: Evidence{Complete: out.Complete, Limitations: out.Limitations}, Next: c.pinNext(out.Next, target), Error: out.Error, Meta: meta}
}

func targetFields() map[string]Field {
	return map[string]Field{
		"replica":              {Type: "string", MinLength: 1, MaxLength: 256, Pattern: "^[^\\x00-\\x1f\\x7f]+$", Description: "Worker identity for registry lookup only; never interpreted as an endpoint.", Source: "OB worker identity"},
		"owner_query_group":    {Type: "string", MinLength: 1, MaxLength: 256, Pattern: "^[^\\x00-\\x1f\\x7f]+$", Description: "Query Group whose current execution lease selects the worker.", Source: "strategy.get plans[].query_group"},
		"expected_incarnation": {Type: "string", MinLength: 1, MaxLength: 256, Pattern: "^[^\\x00-\\x1f\\x7f]+$", Description: "Expected process incarnation; requires an explicit target.", Source: "meta.incarnation from prior targeted evidence"},
		"control_leader":       {Type: "boolean", Description: "true reads the current Control Leader, resolved from the lease the read is routed by; meta.control_leader names it and its term, on a failed read too. Refused as target_routing_unavailable where targeted routing is not configured, like any targeted read.", Source: "Control Leader lease"},
	}
}

func targetRules() []any {
	return []any{
		map[string]any{"not": map[string]any{"required": []string{"replica", "owner_query_group"}}},
		map[string]any{"not": map[string]any{"required": []string{"replica", "control_leader"}}},
		map[string]any{"not": map[string]any{"required": []string{"owner_query_group", "control_leader"}}},
		map[string]any{"if": map[string]any{"required": []string{"expected_incarnation"}}, "then": map[string]any{"anyOf": []any{map[string]any{"required": []string{"replica"}}, map[string]any{"required": []string{"owner_query_group"}}, map[string]any{"required": []string{"control_leader"}}}}},
	}
}

func invocationParams(op Operation, input Params) (Params, Target, error) {
	if !op.Targetable {
		return input, Target{}, validate(op, input)
	}
	params := make(Params, len(input))
	fields := targetFields()
	for name, value := range input {
		if field, target := fields[name]; target {
			if err := validateField(field, value); err != nil {
				return nil, Target{}, fmt.Errorf("%s: %s", name, err)
			}
		} else {
			params[name] = value
		}
	}
	target := Target{Replica: input.String("replica"), OwnerQueryGroup: input.String("owner_query_group"), ExpectedIncarnation: input.String("expected_incarnation"), ControlLeader: input.Bool("control_leader")}
	if value, given := input["control_leader"]; given && value != true {
		return nil, Target{}, errors.New("control_leader, when given, must be true")
	}
	named := 0
	for _, set := range []bool{target.Replica != "", target.OwnerQueryGroup != "", target.ControlLeader} {
		if set {
			named++
		}
	}
	if named > 1 {
		return nil, Target{}, errors.New("replica, owner_query_group and control_leader are mutually exclusive")
	}
	if target.ExpectedIncarnation != "" && !target.Explicit() {
		return nil, Target{}, errors.New("expected_incarnation requires replica, owner_query_group or control_leader")
	}
	if err := validate(op, params); err != nil {
		return nil, Target{}, err
	}
	if !target.Explicit() && op.DefaultOwnerParam != "" {
		target.OwnerQueryGroup = params.String(op.DefaultOwnerParam)
	}
	return params, target, nil
}

func validateInternalTarget(target Target) error {
	for name, value := range map[string]string{"replica": target.Replica, "owner_query_group": target.OwnerQueryGroup, "expected_incarnation": target.ExpectedIncarnation} {
		if value == "" {
			continue
		}
		if err := validateField(targetFields()[name], value); err != nil {
			return fmt.Errorf("%s: %s", name, err)
		}
	}
	return nil
}

func (c *Channel) pinNext(calls []Call, target Target) []Call {
	if target.Replica == "" && target.OwnerQueryGroup == "" {
		return calls
	}
	result := append([]Call(nil), calls...)
	for i, call := range result {
		op, found := c.ops[call.Operation]
		if !found || !op.Targetable || (call.Mode != "" && call.Mode != "invoke") {
			continue
		}
		params := make(Params, len(call.Params)+2)
		for name, value := range call.Params {
			params[name] = value
		}
		// A next call that asks for the Leader is left to be resolved when it
		// is made: the Leader may have changed by then. The Worker executing
		// this read sees only the Replica the ingress resolved, so a pinned
		// call goes to that same process.
		replica, group := params.String("replica"), params.String("owner_query_group")
		if params.Bool("control_leader") {
			continue
		}
		if replica != "" || group != "" {
			same := (group != "" && group == target.OwnerQueryGroup && replica == "") || (replica != "" && replica == target.Replica && group == "")
			if !same {
				continue
			}
		} else {
			if target.OwnerQueryGroup != "" {
				params["owner_query_group"] = target.OwnerQueryGroup
			} else {
				params["replica"] = target.Replica
			}
		}
		if c.options.Incarnation != "" {
			params["expected_incarnation"] = c.options.Incarnation
		}
		result[i].Params = params
	}
	return result
}
