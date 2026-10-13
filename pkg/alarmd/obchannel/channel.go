// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

// Package obchannel exposes registered, bounded evidence operations. Domain
// decisions remain with their existing producers; this package owns admission
// and the machine-readable transport, not a second health engine.
package obchannel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

const (
	Version          = "alarmd-ob/v1"
	MaxRequestBytes  = 64 << 10
	MaxResponseBytes = 2 << 20
	RequestTimeout   = 3 * time.Second
	// EnvelopeMaxBytes is the transport ceiling. Each operation keeps its
	// own smaller request bound; existing reads remain at MaxRequestBytes.
	EnvelopeMaxBytes    = 1 << 20
	TransportMargin     = 5 * time.Second
	MaxExecutionTimeout = 2 * time.Minute
	// InvokesPerSessionPerMinute is one session's budget of executed
	// invocations in a clock minute. Every native invocation is one whole
	// fleet snapshot read on the replica that answers, bounded only per
	// request (MaxResponseBytes, RequestTimeout) and by the one execution
	// slot; nothing bounded how many of them one credential could line up,
	// so a looping client held the slot against every other session for as
	// long as it looped. An investigation asks a handful of questions a
	// minute; thirty is well above that and caps a loop at thirty
	// snapshot reads a minute. Discovery, description, refused inputs and
	// refused budgets do not spend it: they read no evidence.
	InvokesPerSessionPerMinute = 30
	// sessionWindowSweep is how many sessions the gate remembers before it
	// forgets the ones whose minute has passed. It is not a bound: a sweep
	// forgets only past minutes, so a thousand sessions all live in the
	// current minute would all be kept. The bound is the authorization
	// gate's -- at most six grants a minute, sessions living an hour, so a
	// few hundred alive at once -- and this sweep only keeps the map from
	// carrying every session that ever was for the life of the process.
	sessionWindowSweep = 1024
)

type Authorizer interface {
	Authenticate(context.Context, string) (cliauth.Session, error)
	Admit(context.Context, cliauth.Session, bool) (cliauth.Session, error)
}

// Field defines both the advertised schema and the input validation. These
// validation recursively covers the same object/array shape describe exposes.
type Field struct {
	Type                 string           `json:"type"`
	Description          string           `json:"description"`
	Source               string           `json:"parameter_source,omitempty"`
	Enum                 []string         `json:"enum,omitempty"`
	Minimum              *int64           `json:"minimum,omitempty"`
	Maximum              *int64           `json:"maximum,omitempty"`
	MaxLength            int              `json:"maxLength,omitempty"`
	MinLength            int              `json:"minLength,omitempty"`
	Pattern              string           `json:"pattern,omitempty"`
	MaxItems             int              `json:"maxItems,omitempty"`
	MinItems             int              `json:"minItems,omitempty"`
	UniqueItems          bool             `json:"uniqueItems,omitempty"`
	Items                *Field           `json:"items,omitempty"`
	Properties           map[string]Field `json:"properties,omitempty"`
	Required             []string         `json:"required,omitempty"`
	AdditionalProperties bool             `json:"additionalProperties,omitempty"`
}

type Params map[string]any

func (p Params) String(key string) string { s, _ := p[key].(string); return s }
func (p Params) Bool(key string) bool     { b, _ := p[key].(bool); return b }
func (p Params) Int(key string, fallback int) int {
	n, ok := p[key].(json.Number)
	if !ok {
		return fallback
	}
	v, err := n.Int64()
	if err != nil {
		return fallback
	}
	return int(v)
}

type Call struct {
	Mode      string `json:"mode,omitempty"`
	Operation string `json:"operation"`
	Params    Params `json:"params"`
	Reason    string `json:"reason"`
}
type Outcome struct {
	Value       any
	Summary     string
	Complete    bool
	Limitations []string
	Next        []Call
	Error       *Failure
}
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Reason is why a store the read depends on did not answer, when that is
	// what failed: one of redisfailure's reasons.
	Reason string `json:"reason,omitempty"`
}
type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
type Operation struct {
	ID string
	// Zero values retain the original readonly, three-second contract.
	Effect           string
	RequiredScope    string
	ExecutionTimeout time.Duration
	RequestMaxBytes  int
	// ExecutionPool is evidence, sre or exec. Exec cannot enter an internal
	// Worker route; SRE providers use their own local admission budget.
	ExecutionPool string
	// ContractVersion versions execution semantics not expressed by schemas.
	// Bump it when validation or execution changes without a schema change.
	ContractVersion string
	Summary         string
	EvidenceScope   string
	Targetable      bool
	// DefaultOwnerParam selects an execution owner when no explicit target is
	// supplied. The named domain field must be a required string identity.
	DefaultOwnerParam string
	// DefaultControlLeader identifies operations whose complete untargeted
	// answer requires the control Leader's in-memory facts. An ingress may
	// enable this route when it does not host the business runtime.
	DefaultControlLeader bool
	// RouteWhenElsewhere is asked about an untargeted answer: the Query Group
	// whose lease holder to ask the same read of, when the answering replica
	// could only say which replica holds the answer. Only then is the read
	// routed, so an answer any replica can give costs no extra hop; when the
	// routed read fails, the local answer stands with the failure named.
	RouteWhenElsewhere func(Params, any) (string, bool)
	Fields             map[string]Field
	Required           []string
	Examples           []Params
	OutputSchema       any
	Limits             any
	Availability       func() Availability
	InputRules         any
	Validate           func(Params) error
	Run                func(context.Context, Params) Outcome
}
type Options struct {
	Auth                 Authorizer
	EnvironmentID        string
	Replica              string
	Build                string
	Concurrency          int
	SREConcurrency       int
	ExecConcurrency      int
	Operations           []Operation
	Now                  func() time.Time
	Incarnation          string
	Route                func(context.Context, Invocation) Response
	Executor             *EvidenceExecutor
	RouteControlDefaults bool
	RequireWorkerTarget  map[string]bool
	Roles                roles.Set
}
type Channel struct {
	*EvidenceExecutor
	options   Options
	httpSlots chan struct{}
	// windows is each session's spend of its invocation budget in the
	// current clock minute, keyed by session ID; see allowInvoke.
	windowsMu sync.Mutex
	windows   map[string]*sessionWindow
}

// sessionWindow is one session's count of executed invocations in one clock
// minute; a new minute starts the count over.
type sessionWindow struct {
	minute int64
	count  int
}

// allowInvoke spends one of the session's invocations for this minute and
// says whether there was one to spend, with the seconds left in the minute
// when there was not. Same shape as the authorization gate's rate window,
// per session rather than per process, because the thing being protected --
// the one execution slot and the snapshot read behind it -- is shared by
// every session, and one session must not be able to spend it all.
func (c *Channel) allowInvoke(sessionID string) (allowed bool, retryAfter time.Duration) {
	now := c.options.Now()
	minute := now.Unix() / 60
	c.windowsMu.Lock()
	defer c.windowsMu.Unlock()
	if len(c.windows) >= sessionWindowSweep {
		for id, window := range c.windows {
			if window.minute != minute {
				delete(c.windows, id)
			}
		}
	}
	window := c.windows[sessionID]
	if window == nil || window.minute != minute {
		window = &sessionWindow{minute: minute}
		c.windows[sessionID] = window
	}
	if window.count >= InvokesPerSessionPerMinute {
		return false, time.Unix((minute+1)*60, 0).Sub(now)
	}
	window.count++
	return true, 0
}

type request struct {
	Version   string `json:"channel_version"`
	Mode      string `json:"mode"`
	Operation string `json:"operation,omitempty"`
	Revision  string `json:"expected_catalog_revision,omitempty"`
	Params    Params `json:"params,omitempty"`
	Renew     bool   `json:"renew_if_due,omitempty"`
}
type Evidence struct {
	Complete    bool     `json:"complete"`
	Limitations []string `json:"limitations"`
}
type SessionMeta struct {
	ID        string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Renewed   bool      `json:"renewed"`
}
type Meta struct {
	Version       string       `json:"channel_version"`
	Revision      string       `json:"catalog_revision"`
	EnvironmentID string       `json:"environment_id"`
	AnsweredBy    string       `json:"answered_by"`
	Build         string       `json:"build"`
	RequestID     string       `json:"request_id"`
	RespondedAt   time.Time    `json:"responded_at"`
	Session       *SessionMeta `json:"session,omitempty"`
	Incarnation   string       `json:"incarnation,omitempty"`
	Roles         roles.Set    `json:"roles,omitempty"`
	Via           []string     `json:"via,omitempty"`
	Owner         *OwnerMeta   `json:"owner,omitempty"`
	// ControlLeader is the lease a control_leader read was resolved from:
	// the answer is that Worker in that term, or the read failed.
	ControlLeader *LeaderMeta `json:"control_leader,omitempty"`
}

// LeaderMeta is the Control Leader lease the routing layer resolved.
type LeaderMeta struct {
	OwnerID    string `json:"owner_id"`
	OwnerEpoch uint64 `json:"owner_epoch"`
}

// OwnerMeta is a lease observation made by the routing layer. It is distinct
// from Incarnation, which identifies the answering process, not its lease.
type OwnerMeta struct {
	QueryGroup string    `json:"query_group"`
	OwnerID    string    `json:"owner_id"`
	OwnerEpoch uint64    `json:"owner_epoch"`
	Deadline   time.Time `json:"deadline"`
	ObservedAt time.Time `json:"observed_at"`
}
type Response struct {
	Status   string   `json:"status"`
	Summary  string   `json:"summary"`
	Result   any      `json:"result"`
	Evidence Evidence `json:"evidence"`
	Next     []Call   `json:"next_call"`
	Error    *Failure `json:"error,omitempty"`
	Meta     Meta     `json:"meta"`
}

func New(options Options) (*Channel, error) {
	if options.Auth == nil || options.EnvironmentID == "" {
		return nil, errors.New("OB channel requires authorization and environment identity")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	executor := options.Executor
	if executor == nil {
		var err error
		executor, err = NewEvidenceExecutor(ExecutorOptions{EnvironmentID: options.EnvironmentID, Replica: options.Replica, Build: options.Build,
			Incarnation: options.Incarnation, Concurrency: options.Concurrency, SREConcurrency: options.SREConcurrency, ExecConcurrency: options.ExecConcurrency, Operations: options.Operations, Now: options.Now, Roles: options.Roles})
		if err != nil {
			return nil, err
		}
	} else if executor.options.EnvironmentID != options.EnvironmentID || executor.options.Replica != options.Replica || executor.options.Incarnation != options.Incarnation {
		return nil, errors.New("OB channel and evidence executor identities must match")
	}
	c := &Channel{EvidenceExecutor: executor, options: options, httpSlots: make(chan struct{}, 4), windows: make(map[string]*sessionWindow)}
	return c, nil
}

func (c *Channel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	meta := c.localMeta("")
	fail := func(status int, code, message string) {
		c.write(w, status, Response{Status: "error", Summary: message, Error: &Failure{Code: code, Message: message}, Meta: meta})
	}
	// An authorization that failed says why the store did not answer.
	failAuth := func(err error, message string) {
		c.write(w, authStatus(err), Response{Status: "error", Summary: message,
			Error: &Failure{Code: cliauth.ErrorCode(err), Message: message, Reason: authReason(err)}, Meta: meta})
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(RequestTimeout))
	_ = controller.SetWriteDeadline(time.Now().Add(RequestTimeout + TransportMargin))
	admitted := false
	// The slot and deadlines include draining and flushing, not just the
	// handler body. Global deadlines would break the shared h2c streams.
	defer func() {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		_ = controller.Flush()
		_ = controller.SetReadDeadline(time.Time{})
		_ = controller.SetWriteDeadline(time.Time{})
		if admitted {
			<-c.httpSlots
		}
	}()
	select {
	case c.httpSlots <- struct{}{}:
		admitted = true
	default:
		_ = controller.SetReadDeadline(time.Now())
		fail(429, "request_budget_exceeded", "OB channel request slots are busy.")
		return
	}
	if r.Method != http.MethodPost {
		fail(405, "method_not_allowed", "Use POST for the OB channel.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), RequestTimeout)
	defer cancel()
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		fail(401, "unauthorized", "Run auth login with an OB authorization code.")
		return
	}
	token := strings.TrimPrefix(values[0], "Bearer ")
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		fail(401, "unauthorized", "Invalid bearer credential.")
		return
	}
	session, err := c.options.Auth.Authenticate(ctx, token)
	if err != nil {
		failAuth(err, "Session unavailable; run auth login if expired or revoked.")
		return
	}
	if session.EnvironmentID != c.options.EnvironmentID || !cliauth.AllowsScope(session.Scope, cliauth.ScopeReadonly) {
		fail(403, "permission_denied", "Session does not authorize this deployment.")
		return
	}
	meta.Session = &SessionMeta{ID: session.ID, ExpiresAt: session.ExpiresAt}
	var req request
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, EnvelopeMaxBytes))
	if err != nil {
		fail(400, "invalid_input", "Request exceeds the transport byte limit or could not be read.")
		return
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		fail(400, "invalid_input", "Request must be one JSON envelope within the request byte limit.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		fail(400, "invalid_input", "Only one JSON envelope is accepted.")
		return
	}
	if req.Version != Version {
		fail(400, "unsupported_channel_version", "This server supports "+Version)
		return
	}
	if req.Mode != "invoke" && len(body) > MaxRequestBytes {
		fail(400, "invalid_input", "Discovery and description exceed the request byte limit.")
		return
	}
	switch req.Mode {
	case "discover":
		rows := make([]any, 0, len(c.ordered))
		for _, id := range c.ordered {
			op := c.ops[id]
			rows = append(rows, map[string]any{"operation": id, "summary": op.Summary, "evidence_scope": op.EvidenceScope, "targetable": op.Targetable, "effect": op.Effect, "required_scope": op.RequiredScope, "authorized": cliauth.AllowsScope(session.Scope, op.RequiredScope), "availability": available(op), "next_call": map[string]string{"mode": "describe", "operation": id}})
		}
		c.write(w, 200, Response{Status: "ok", Summary: "Discover operations, describe one, then invoke it in this environment.",
			Result:   map[string]any{"operations": rows, "budget": map[string]any{"invokes_per_session_per_minute": InvokesPerSessionPerMinute}},
			Evidence: Evidence{Complete: true}, Meta: meta})
		return
	case "describe", "invoke":
	default:
		fail(400, "invalid_mode", "Use discover, describe or invoke.")
		return
	}
	op, ok := c.ops[req.Operation]
	if !ok {
		fail(404, "unknown_operation", "Operation is not registered; use discover.")
		return
	}
	if req.Mode == "describe" {
		value := describe(op)
		value["operation_contract_revision"] = c.OperationContractRevision(op.ID)
		value["availability"] = available(op)
		value["authorized"] = cliauth.AllowsScope(session.Scope, op.RequiredScope)
		value["request_limits"] = c.requestLimits(op)
		c.write(w, 200, Response{Status: "ok", Summary: op.Summary, Result: value, Evidence: Evidence{Complete: true}, Meta: meta})
		return
	}
	if !cliauth.AllowsScope(session.Scope, op.RequiredScope) {
		fail(403, "permission_denied", "This operation requires "+op.RequiredScope+"; log in with an explicitly granted scope.")
		return
	}
	if len(body) > op.RequestMaxBytes {
		fail(400, "invalid_input", "Request exceeds this operation's described byte limit.")
		return
	}
	if req.Revision != c.revision {
		c.write(w, 409, Response{Status: "error", Summary: "Catalog changed; describe the operation again. This invocation did not execute or renew the session.", Error: &Failure{Code: "catalog_changed", Message: "Expected catalog revision does not match."}, Next: []Call{{Mode: "describe", Operation: op.ID, Params: Params{}, Reason: "Describe this operation before retrying."}}, Meta: meta})
		return
	}
	params, target, err := invocationParams(op, req.Params)
	if err != nil {
		fail(400, "invalid_input", err.Error())
		return
	}
	if !target.Explicit() && c.options.RouteControlDefaults && op.DefaultControlLeader {
		target.ControlLeader = true
	}
	if !target.Explicit() && c.options.RequireWorkerTarget[op.ID] {
		fail(400, "worker_target_required", "This operation requires an explicit business Worker target on this channel instance.")
		return
	}
	// Input/authentication retains its short admission deadline. Execution
	// starts from the request's original context so a longer operation is not
	// accidentally capped by the legacy three-second context.
	executionContext := func() (context.Context, context.CancelFunc) {
		_ = controller.SetReadDeadline(time.Time{})
		_ = controller.SetWriteDeadline(time.Now().Add(op.ExecutionTimeout + TransportMargin))
		return context.WithTimeout(r.Context(), op.ExecutionTimeout)
	}
	// The session's own budget, spent only by an invocation that would
	// execute, here or on the replica it targets: a refused input, an
	// unavailable operation or a missing route cost nothing and count for
	// nothing. Spent before the slot, so a session over budget never contends
	// for it, and before admission, so it never renews. The target executes
	// without the session and spends none, so a read routed back to this
	// replica is counted once. A target that answers unavailable has still
	// been asked, and the read is counted.
	overBudget := func() bool {
		allowed, retryAfter := c.allowInvoke(session.ID)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
			fail(429, "rate_limited", fmt.Sprintf("This session has spent its %d invocations for this minute; retry when the minute turns.", InvokesPerSessionPerMinute))
		}
		return !allowed
	}
	if target.Explicit() {
		if c.options.Route == nil {
			fail(503, "target_routing_unavailable", "Targeted evidence routing is not configured.")
			return
		}
		if op.ExecutionPool != "evidence" || op.Effect != "read" {
			fail(403, "operation_not_internal_evidence", "Internal Worker routes allow only registered evidence reads.")
			return
		}
		if overBudget() {
			return
		}
		// The external entry owns CLI admission and renewal exactly once. The
		// internal route carries operation context, never the CLI credential.
		session, err = c.options.Auth.Admit(ctx, session, req.Renew)
		if err != nil {
			failAuth(err, "Session unavailable at routing admission.")
			return
		}
		if !cliauth.AllowsScope(session.Scope, op.RequiredScope) || ctx.Err() != nil {
			fail(403, "permission_denied", "Routing admission ended or no longer authorizes this operation.")
			return
		}
		routeCtx, routeCancel := executionContext()
		defer routeCancel()
		out := c.options.Route(routeCtx, Invocation{EnvironmentID: c.options.EnvironmentID, Version: req.Version, Revision: req.Revision, OperationContractRevision: c.OperationContractRevision(op.ID), Operation: req.Operation, RequestID: meta.RequestID, Params: params, Target: target})
		out.Meta.Revision = c.revision
		out.Meta.Session = &SessionMeta{ID: session.ID, ExpiresAt: session.ExpiresAt, Renewed: session.Renewed}
		code := 200
		if out.Status == "error" {
			code = 502
		}
		c.write(w, code, out)
		return
	}
	availability := available(op)
	if !availability.Available {
		fail(503, "operation_unavailable", availability.Reason)
		return
	}
	if overBudget() {
		return
	}
	pool := c.executionSlots(op)
	select {
	case pool <- struct{}{}:
		defer func() { <-pool }()
	default:
		fail(429, "request_budget_exceeded", "OB evidence readers are busy; retry this read later.")
		return
	}
	// Final admission checks revocation again. Discovery, invalid inputs and
	// rejected budgets do not count as activity and cannot prolong a session.
	session, err = c.options.Auth.Admit(ctx, session, req.Renew)
	if err != nil {
		failAuth(err, "Session unavailable at execution admission.")
		return
	}
	meta.Session = &SessionMeta{ID: session.ID, ExpiresAt: session.ExpiresAt, Renewed: session.Renewed}
	if ctx.Err() != nil {
		fail(408, "request_timeout", "Admission context ended before operation execution.")
		return
	}
	if !cliauth.AllowsScope(session.Scope, op.RequiredScope) {
		fail(403, "permission_denied", "The admitted session does not authorize this operation.")
		return
	}
	execCtx, execCancel := executionContext()
	defer execCancel()
	// Who asked, for the audit record of an execution or management call.
	execCtx = WithCaller(execCtx, Caller{SessionID: session.ID, RequestID: meta.RequestID})
	out := c.run(execCtx, op, params, Target{}, meta)
	if op.RouteWhenElsewhere != nil && c.options.Route != nil && out.Status != "error" {
		if group, elsewhere := op.RouteWhenElsewhere(params, out.Result); elsewhere {
			routed := c.options.Route(execCtx, Invocation{EnvironmentID: c.options.EnvironmentID, Version: req.Version, Revision: req.Revision, OperationContractRevision: c.OperationContractRevision(op.ID),
				Operation: req.Operation, RequestID: meta.RequestID, Params: params, Target: Target{OwnerQueryGroup: group}})
			if routed.Status != "error" {
				routed.Meta.Revision = c.revision
				routed.Meta.Session = meta.Session
				out = routed
			} else {
				reason := "routed read failed"
				if routed.Error != nil {
					reason = routed.Error.Code + ": " + observability.SanitizeErrorText(routed.Error.Message)
				}
				// Partial, not ok: run() derived the status from the evidence
				// before this step, and a reader acting on the exit code must
				// see the answer is not whole.
				out.Evidence.Complete = false
				if out.Status == "ok" {
					out.Status = "partial"
				}
				out.Evidence.Limitations = append(out.Evidence.Limitations,
					"The answer is held by another replica and the read routed to its lease holder failed ("+reason+"); tracked_by names the holder as of its latest snapshot.")
			}
		}
	}
	code := 200
	if out.Status == "error" {
		code = 502
	}
	c.write(w, code, out)
}

func (c *Channel) write(w http.ResponseWriter, status int, response Response) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	response = c.prepareResponse(response)
	if response.Error != nil && response.Error.Code == "response_budget_exceeded" {
		status = 502
	}
	encoded, _ := json.Marshal(response)
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

func (c *EvidenceExecutor) prepareResponse(response Response) Response {
	if response.Meta.RespondedAt.IsZero() {
		response.Meta.RespondedAt = c.options.Now().UTC()
	}
	if response.Evidence.Limitations == nil {
		response.Evidence.Limitations = []string{}
	}
	if response.Next == nil {
		response.Next = []Call{}
	}
	encoded, err := json.Marshal(response)
	if err != nil || len(encoded) > MaxResponseBytes {
		response.Status = "error"
		response.Summary = "Evidence exceeds the response budget or cannot be encoded."
		response.Result = nil
		response.Evidence = Evidence{Limitations: []string{"result_not_returned"}}
		response.Next = []Call{}
		response.Error = &Failure{Code: "response_budget_exceeded", Message: response.Summary}
	}
	return response
}

func available(op Operation) Availability {
	if op.Availability != nil {
		return op.Availability()
	}
	return Availability{Available: true}
}
func authReason(err error) string {
	var detail *cliauth.Error
	if errors.As(err, &detail) {
		return detail.Reason
	}
	return ""
}
func authStatus(err error) int {
	var detail *cliauth.Error
	if errors.As(err, &detail) {
		return detail.HTTPStatus
	}
	return 503
}
func describe(op Operation) map[string]any {
	required := op.Required
	if required == nil {
		required = []string{}
	}
	examples := op.Examples
	if examples == nil {
		examples = []Params{}
	}
	fields := op.Fields
	if op.Targetable {
		fields = make(map[string]Field, len(op.Fields)+3)
		for name, field := range op.Fields {
			fields[name] = field
		}
		for name, field := range targetFields() {
			fields[name] = field
		}
	}
	input := map[string]any{"type": "object", "properties": fields, "required": required, "additionalProperties": false}
	if op.InputRules != nil {
		input["allOf"] = op.InputRules
	}
	if op.Targetable {
		rules := targetRules()
		if op.InputRules != nil {
			rules = append([]any{map[string]any{"allOf": op.InputRules}}, rules...)
		}
		input["allOf"] = rules
	}
	value := map[string]any{"operation": op.ID, "summary": op.Summary, "evidence_scope": op.EvidenceScope, "targetable": op.Targetable, "contract_version": op.ContractVersion, "effect": op.Effect, "required_scope": op.RequiredScope, "execution_timeout_ms": op.ExecutionTimeout.Milliseconds(), "execution_pool": op.ExecutionPool, "request_max_bytes": op.RequestMaxBytes, "input_schema": input, "output_schema": op.OutputSchema, "examples": examples, "limits": op.Limits, "time_semantics": "meta.responded_at is response time; source observation times and versions remain in result. Multiple reads are not an atomic snapshot."}
	if op.DefaultOwnerParam != "" {
		value["default_owner_parameter"] = op.DefaultOwnerParam
	}
	if op.DefaultControlLeader {
		value["default_control_leader"] = true
	}
	return value
}
func validate(op Operation, params Params) error {
	for _, name := range op.Required {
		if _, ok := params[name]; !ok {
			return fmt.Errorf("required parameter: %s", name)
		}
	}
	for name, value := range params {
		field, ok := op.Fields[name]
		if !ok {
			return fmt.Errorf("unknown parameter: %s", name)
		}
		if err := validateField(field, value); err != nil {
			return fmt.Errorf("%s: %s", name, err)
		}
	}
	if op.Validate != nil {
		return op.Validate(params)
	}
	return nil
}
func validateField(f Field, value any) error {
	switch f.Type {
	case "string":
		s, ok := value.(string)
		if !ok {
			return errors.New("must be a string")
		}
		if n := utf8.RuneCountInString(s); n < f.MinLength || (f.MaxLength > 0 && n > f.MaxLength) {
			return errors.New("string is outside the described length limit")
		}
		if f.Pattern != "" {
			matched, err := regexp.MatchString(f.Pattern, s)
			if err != nil || !matched {
				return errors.New("string does not match the described pattern")
			}
		}
		if len(f.Enum) > 0 {
			for _, allowed := range f.Enum {
				if s == allowed {
					return nil
				}
			}
			return errors.New("value is not in the described enum")
		}
	case "integer":
		n, ok := value.(json.Number)
		if !ok {
			return errors.New("must be an integer")
		}
		i, err := n.Int64()
		if err != nil || (f.Minimum != nil && i < *f.Minimum) || (f.Maximum != nil && i > *f.Maximum) {
			return errors.New("integer is outside the described range")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return errors.New("must be a boolean")
		}
	case "number":
		n, ok := value.(json.Number)
		if !ok {
			return errors.New("must be a number")
		}
		v, err := n.Float64()
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || (f.Minimum != nil && v < float64(*f.Minimum)) || (f.Maximum != nil && v > float64(*f.Maximum)) {
			return errors.New("number is outside the described range")
		}
	case "object":
		values, ok := value.(map[string]any)
		if !ok {
			return errors.New("must be an object")
		}
		for _, name := range f.Required {
			if _, exists := values[name]; !exists {
				return fmt.Errorf("required property: %s", name)
			}
		}
		for name, item := range values {
			field, known := f.Properties[name]
			if !known {
				if !f.AdditionalProperties {
					return fmt.Errorf("unknown property: %s", name)
				}
				continue
			}
			if err := validateField(field, item); err != nil {
				return fmt.Errorf("%s: %s", name, err)
			}
		}
	case "array":
		values, ok := value.([]any)
		if !ok || len(values) < f.MinItems || (f.MaxItems > 0 && len(values) > f.MaxItems) {
			return errors.New("array exceeds the described limit or has the wrong type")
		}
		if f.Items != nil {
			for i, item := range values {
				if err := validateField(*f.Items, item); err != nil {
					return fmt.Errorf("item %d: %s", i, err)
				}
			}
		}
		if f.UniqueItems {
			seen := map[string]bool{}
			for _, item := range values {
				encoded, err := json.Marshal(item)
				if err != nil || seen[string(encoded)] {
					return errors.New("array items must be unique")
				}
				seen[string(encoded)] = true
			}
		}
	default:
		return errors.New("unsupported parameter type")
	}
	return nil
}
