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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
)

const (
	Version          = "alarmd-ob/v1"
	MaxRequestBytes  = 64 << 10
	MaxResponseBytes = 2 << 20
	RequestTimeout   = 3 * time.Second
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
// operations intentionally have small, flat inputs, not a scripting language.
type Field struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Source      string   `json:"parameter_source,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Minimum     *int64   `json:"minimum,omitempty"`
	Maximum     *int64   `json:"maximum,omitempty"`
	MaxLength   int      `json:"maxLength,omitempty"`
	MinLength   int      `json:"minLength,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	MaxItems    int      `json:"maxItems,omitempty"`
	UniqueItems bool     `json:"uniqueItems,omitempty"`
	Items       *Field   `json:"items,omitempty"`
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
}
type Availability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
type Operation struct {
	ID            string
	Summary       string
	EvidenceScope string
	Targetable    bool
	// DefaultOwnerParam selects an execution owner when no explicit target is
	// supplied. The named domain field must be a required string identity.
	DefaultOwnerParam string
	Fields            map[string]Field
	Required          []string
	Examples          []Params
	OutputSchema      any
	Limits            any
	Availability      func() Availability
	InputRules        any
	Validate          func(Params) error
	Run               func(context.Context, Params) Outcome
}
type Options struct {
	Auth          Authorizer
	EnvironmentID string
	Replica       string
	Build         string
	Concurrency   int
	Operations    []Operation
	Now           func() time.Time
	Incarnation   string
	Route         func(context.Context, Invocation) Response
}
type Channel struct {
	options   Options
	ops       map[string]Operation
	ordered   []string
	revision  string
	slots     chan struct{}
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
	if options.Concurrency <= 0 {
		options.Concurrency = 1
	}
	if options.Concurrency > 4 {
		options.Concurrency = 4
	}
	c := &Channel{options: options, ops: make(map[string]Operation), slots: make(chan struct{}, options.Concurrency), httpSlots: make(chan struct{}, 4),
		windows: make(map[string]*sessionWindow)}
	for _, op := range options.Operations {
		if op.ID == "" || op.Run == nil || op.Summary == "" {
			return nil, errors.New("invalid OB operation registration")
		}
		if _, found := c.ops[op.ID]; found {
			return nil, fmt.Errorf("duplicate OB operation %q", op.ID)
		}
		if op.EvidenceScope == "" {
			op.EvidenceScope = "deployment"
		}
		if op.Targetable {
			for name := range targetFields() {
				if _, found := op.Fields[name]; found {
					return nil, fmt.Errorf("operation %q uses reserved targeting field %q", op.ID, name)
				}
			}
		}
		if op.DefaultOwnerParam != "" {
			field, exists := op.Fields[op.DefaultOwnerParam]
			required := false
			for _, name := range op.Required {
				required = required || name == op.DefaultOwnerParam
			}
			if !op.Targetable || !exists || field.Type != "string" || !required {
				return nil, fmt.Errorf("invalid default owner field for %q", op.ID)
			}
		}
		for _, name := range op.Required {
			if _, ok := op.Fields[name]; !ok {
				return nil, fmt.Errorf("unknown required field %q", name)
			}
		}
		c.ops[op.ID] = op
		c.ordered = append(c.ordered, op.ID)
	}
	sort.Strings(c.ordered)
	contracts := make([]any, 0, len(c.ordered))
	for _, id := range c.ordered {
		contracts = append(contracts, describe(c.ops[id]))
	}
	encoded, err := json.Marshal(contracts)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	c.revision = hex.EncodeToString(digest[:])
	return c, nil
}

func (c *Channel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	meta := c.localMeta("")
	fail := func(status int, code, message string) {
		c.write(w, status, Response{Status: "error", Summary: message, Error: &Failure{code, message}, Meta: meta})
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(RequestTimeout))
	_ = controller.SetWriteDeadline(time.Now().Add(RequestTimeout))
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
		fail(authStatus(err), cliauth.ErrorCode(err), "Session unavailable; run auth login if expired or revoked.")
		return
	}
	if session.EnvironmentID != c.options.EnvironmentID || session.Scope != cliauth.ScopeReadonly {
		fail(403, "permission_denied", "Session does not authorize this deployment.")
		return
	}
	meta.Session = &SessionMeta{ID: session.ID, ExpiresAt: session.ExpiresAt}
	var req request
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
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
	switch req.Mode {
	case "discover":
		rows := make([]any, 0, len(c.ordered))
		for _, id := range c.ordered {
			op := c.ops[id]
			rows = append(rows, map[string]any{"operation": id, "summary": op.Summary, "evidence_scope": op.EvidenceScope, "targetable": op.Targetable, "effect": "read", "required_scope": cliauth.ScopeReadonly, "authorized": true, "availability": available(op), "next_call": map[string]string{"mode": "describe", "operation": id}})
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
		value["availability"] = available(op)
		c.write(w, 200, Response{Status: "ok", Summary: op.Summary, Result: value, Evidence: Evidence{Complete: true}, Meta: meta})
		return
	}
	if req.Revision != c.revision {
		c.write(w, 409, Response{Status: "error", Summary: "Catalog changed; describe the operation again. This invocation did not execute or renew the session.", Error: &Failure{"catalog_changed", "Expected catalog revision does not match."}, Next: []Call{{Mode: "describe", Operation: op.ID, Params: Params{}, Reason: "Describe this operation before retrying."}}, Meta: meta})
		return
	}
	params, target, err := invocationParams(op, req.Params)
	if err != nil {
		fail(400, "invalid_input", err.Error())
		return
	}
	if target.Explicit() {
		if c.options.Route == nil {
			fail(503, "target_routing_unavailable", "Targeted evidence routing is not configured.")
			return
		}
		// The external entry owns CLI admission and renewal exactly once. The
		// internal route carries operation context, never the CLI credential.
		session, err = c.options.Auth.Admit(ctx, session, req.Renew)
		if err != nil {
			fail(authStatus(err), cliauth.ErrorCode(err), "Session unavailable at routing admission.")
			return
		}
		out := c.options.Route(ctx, Invocation{EnvironmentID: c.options.EnvironmentID, Version: req.Version, Revision: req.Revision, Operation: req.Operation, RequestID: meta.RequestID, Params: params, Target: target})
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
	// The session's own budget, spent only by an invocation that would
	// execute: a refused input or an unavailable operation cost nothing and
	// counts for nothing. Spent before the slot, so a session over budget
	// never contends for it, and before admission, so it never renews.
	if allowed, retryAfter := c.allowInvoke(session.ID); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		fail(429, "rate_limited", fmt.Sprintf("This session has spent its %d invocations for this minute; retry when the minute turns.", InvokesPerSessionPerMinute))
		return
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		fail(429, "request_budget_exceeded", "OB evidence readers are busy; retry this read later.")
		return
	}
	// Final admission checks revocation again. Discovery, invalid inputs and
	// rejected budgets do not count as activity and cannot prolong a session.
	session, err = c.options.Auth.Admit(ctx, session, req.Renew)
	if err != nil {
		fail(authStatus(err), cliauth.ErrorCode(err), "Session unavailable at execution admission.")
		return
	}
	meta.Session = &SessionMeta{ID: session.ID, ExpiresAt: session.ExpiresAt, Renewed: session.Renewed}
	out := c.run(ctx, op, params, Target{}, meta)
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

func (c *Channel) prepareResponse(response Response) Response {
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
		response.Error = &Failure{"response_budget_exceeded", response.Summary}
	}
	return response
}

func available(op Operation) Availability {
	if op.Availability != nil {
		return op.Availability()
	}
	return Availability{Available: true}
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
	value := map[string]any{"operation": op.ID, "summary": op.Summary, "evidence_scope": op.EvidenceScope, "targetable": op.Targetable, "effect": "read", "required_scope": cliauth.ScopeReadonly, "input_schema": input, "output_schema": op.OutputSchema, "examples": examples, "limits": op.Limits, "time_semantics": "meta.responded_at is response time; source observation times and versions remain in result. Multiple reads are not an atomic snapshot."}
	if op.DefaultOwnerParam != "" {
		value["default_owner_parameter"] = op.DefaultOwnerParam
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
	case "array":
		values, ok := value.([]any)
		if !ok || (f.MaxItems > 0 && len(values) > f.MaxItems) {
			return errors.New("array exceeds the described limit or has the wrong type")
		}
		if f.Items != nil {
			for _, item := range values {
				if err := validateField(*f.Items, item); err != nil {
					return err
				}
			}
		}
	default:
		return errors.New("unsupported parameter type")
	}
	return nil
}
