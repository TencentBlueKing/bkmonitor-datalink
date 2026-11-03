// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obevidence"
)

// FrontReads is what the front surface may read, the admin server's page:
// alarmd's own data and nothing of the cluster's, no raw store read, no
// re-query and no write.
var FrontReads = map[string]bool{
	"overview.get":   true,
	"trends.get":     true,
	"fleet.get":      true,
	"strategy.list":  true,
	"strategy.get":   true,
	"strategy.board": true,
	"strategy.view":  true,
	// The drawer's Linkd block: Linkd's read, bounded by its own time and
	// one in flight per replica (strategy.linkd_reconcile).
	"strategy.linkd_reconcile": true,
	"object.list":              true,
	"object.get":               true,
	"diagnose.environment":     true,
	"runtime.get":              true,
	// The changes page: the channel's change receipts and alarmd's starts
	// and stops, by name only (changes.list).
	"changes.list": true,
	// The strategy drawer's config block, asked for by the people who run the
	// deployment: the strategy's source configuration, which strategy.config
	// already reduces to what may be shown (keys this build does not know as
	// their shape, credential fields left out and listed). Its source view
	// only (FrontParams): the published view reads an object by the digest
	// and query group the page never holds.
	"strategy.config": true,
}

// FrontParams narrows a front read to some of its operation's variants: a
// parameter named here must hold the value given, or the read is refused as
// one the front does not make.
var FrontParams = map[string]map[string]string{
	"strategy.config": {"view": "source"},
}

// frontAnswers trims what a front read returns to what the page may hold.
// strategy.config names where it read from - the store's address, database,
// prefix and key - and, on a failed read, the error's own text; those are the
// cluster's, not alarmd's, so the front answer drops them. The CLI, which
// reads addresses already, keeps them. A value of any other shape than the
// one the trim knows is not passed through: the front answers that it could
// not shape it, so a change to the operation's result can only withhold, never
// leak.
var frontAnswers = map[string]func(any) (any, bool){
	"strategy.config": func(value any) (any, bool) {
		result, ok := value.(obevidence.Result)
		if !ok {
			return nil, false
		}
		result.Location = obevidence.Location{Role: result.Location.Role}
		result.ReasonText = ""
		return result, true
	},
}

// FrontReadHandler serves the front surface's read: an invoke of one of
// FrontReads, authorized by the administrator key through authorize. It
// writes no audit record: a page view is not audited.
func (c *Channel) FrontReadHandler(authorize func(*http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { c.serveFront(w, r, authorize) })
}

func (c *Channel) serveFront(w http.ResponseWriter, r *http.Request, authorize func(*http.Request) error) {
	meta := c.localMeta("")
	fail := func(status int, code, message string) {
		c.write(w, status, Response{Status: "error", Summary: message, Error: &Failure{Code: code, Message: message}, Meta: meta})
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(RequestReadTimeout))
	_ = controller.SetWriteDeadline(time.Now().Add(RequestReadTimeout + TransportMargin))
	defer func() {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		_ = controller.Flush()
		_ = controller.SetReadDeadline(time.Time{})
		_ = controller.SetWriteDeadline(time.Time{})
	}()
	if r.Method != http.MethodPost {
		fail(405, "method_not_allowed", "Use POST for the front read.")
		return
	}
	if authorize == nil || authorize(r) != nil {
		fail(403, "admin_unauthorized", "Deployment administrator authorization is required.")
		return
	}
	var req request
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, EnvelopeMaxBytes))
	if err != nil {
		fail(400, "invalid_input", "Request exceeds the transport byte limit or could not be read.")
		return
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		fail(400, "invalid_input", "Request must be one JSON envelope within the request byte limit.")
		return
	}
	if req.Version != Version {
		fail(400, "unsupported_channel_version", "This server supports "+Version)
		return
	}
	if req.Mode != "invoke" {
		fail(400, "invalid_mode", "The front read takes invoke only.")
		return
	}
	if !FrontReads[req.Operation] {
		fail(403, "op_not_permitted_for_front", "The front surface reads only alarmd's own data; use the CLI for this operation.")
		return
	}
	op, ok := c.ops[req.Operation]
	if !ok {
		fail(404, "unknown_operation", "Operation is not registered on this replica.")
		return
	}
	if req.Revision != "" && req.Revision != c.revision {
		fail(409, "catalog_changed", "Expected catalog revision does not match.")
		return
	}
	req.Revision = c.revision
	if len(body) > op.RequestMaxBytes {
		fail(400, "invalid_input", "Request exceeds this operation's described byte limit.")
		return
	}
	params, target, err := invocationParams(op, req.Params)
	if err != nil {
		fail(400, "invalid_input", err.Error())
		return
	}
	for name, want := range FrontParams[req.Operation] {
		if params.String(name) != want {
			fail(403, "op_not_permitted_for_front", "The front surface reads "+req.Operation+" with "+name+"="+want+" only; use the CLI for the others.")
			return
		}
	}
	if target.Explicit() {
		fail(400, "front_target_not_allowed", "The front read answers for the deployment; it takes no replica target.")
		return
	}
	if c.options.RouteControlDefaults && op.DefaultControlLeader {
		target.ControlLeader = true
	}
	if target.Explicit() && c.options.Route == nil {
		fail(503, "target_routing_unavailable", "Targeted evidence routing is not configured.")
		return
	}
	if !target.Explicit() {
		if availability := available(op); !availability.Available {
			fail(503, "operation_unavailable", availability.Reason)
			return
		}
	}
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Now().Add(op.ExecutionTimeout + TransportMargin))
	execCtx, cancel := context.WithTimeout(r.Context(), op.ExecutionTimeout)
	defer cancel()
	execCtx = WithCaller(execCtx, Caller{RequestID: meta.RequestID})
	var out Response
	if target.Explicit() {
		out = c.options.Route(execCtx, Invocation{EnvironmentID: c.options.EnvironmentID, Version: req.Version, Revision: req.Revision,
			OperationContractRevision: c.OperationContractRevision(op.ID), Operation: req.Operation, RequestID: meta.RequestID, Params: params, Target: target})
		out.Meta.Revision = c.revision
	} else {
		out = c.runHere(execCtx, op, req, params, meta)
	}
	if trim := frontAnswers[req.Operation]; trim != nil && out.Result != nil {
		trimmed, shaped := trim(out.Result)
		if !shaped {
			fail(502, "front_answer_unshaped", "The front surface could not reduce this answer to what a page may hold; use the CLI.")
			return
		}
		out.Result = trimmed
	}
	code := 200
	if out.Status == "error" {
		code = 502
	}
	c.write(w, code, out)
}
