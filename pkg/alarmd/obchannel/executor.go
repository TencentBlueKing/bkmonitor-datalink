// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// ExecutorOptions describes one local evidence provider. HTTP authorization is
// deliberately absent: the internal RPC authenticates the registered caller.
type ExecutorOptions struct {
	EnvironmentID, Replica, Build, Incarnation string
	Concurrency                                int
	SREConcurrency, ExecConcurrency            int
	Operations                                 []Operation
	Now                                        func() time.Time
	Roles                                      roles.Set
}

// EvidenceExecutor owns registration, validation and the local execution slots
// shared by HTTP and internal RPC. Constructing it performs no evidence I/O.
type EvidenceExecutor struct {
	options   ExecutorOptions
	ops       map[string]Operation
	ordered   []string
	revision  string
	contracts map[string]string
	slots     chan struct{}
	sreSlots  chan struct{}
	execSlots chan struct{}
}

func NewEvidenceExecutor(options ExecutorOptions) (*EvidenceExecutor, error) {
	if options.EnvironmentID == "" {
		return nil, errors.New("OB evidence executor requires environment identity")
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
	if options.SREConcurrency <= 0 {
		options.SREConcurrency = 1
	}
	if options.ExecConcurrency <= 0 {
		options.ExecConcurrency = 1
	}
	if options.SREConcurrency > 4 || options.ExecConcurrency > 4 {
		return nil, errors.New("SRE and exec concurrency must be at most four per process")
	}
	e := &EvidenceExecutor{options: options, ops: make(map[string]Operation), contracts: make(map[string]string), slots: make(chan struct{}, options.Concurrency), sreSlots: make(chan struct{}, options.SREConcurrency), execSlots: make(chan struct{}, options.ExecConcurrency)}
	for _, op := range options.Operations {
		if op.ID == "" || op.Run == nil || op.Summary == "" {
			return nil, errors.New("invalid OB operation registration")
		}
		if _, found := e.ops[op.ID]; found {
			return nil, fmt.Errorf("duplicate OB operation %q", op.ID)
		}
		if op.ContractVersion == "" {
			op.ContractVersion = "1"
		}
		if op.EvidenceScope == "" {
			op.EvidenceScope = "deployment"
		}
		if op.Effect == "" {
			op.Effect = "read"
		}
		if op.RequiredScope == "" {
			op.RequiredScope = cliauth.ScopeReadonly
		}
		if op.ExecutionPool == "" {
			op.ExecutionPool = "evidence"
			if op.Effect == "exec" {
				op.ExecutionPool = "exec"
			}
		}
		if op.ExecutionTimeout == 0 {
			op.ExecutionTimeout = RequestTimeout
		}
		if op.RequestMaxBytes == 0 {
			op.RequestMaxBytes = MaxRequestBytes
		}
		if (op.Effect != "read" && op.Effect != "exec") || !cliauth.ValidScope(op.RequiredScope) ||
			(op.Effect == "exec" && (op.ExecutionPool != "exec" || op.Targetable || op.RouteWhenElsewhere != nil || op.DefaultControlLeader)) ||
			(op.Effect == "read" && op.ExecutionPool != "evidence" && op.ExecutionPool != "sre") ||
			(op.ExecutionPool != "evidence" && (op.Targetable || op.RouteWhenElsewhere != nil || op.DefaultControlLeader)) ||
			op.ExecutionTimeout < time.Millisecond || op.ExecutionTimeout > MaxExecutionTimeout || op.RequestMaxBytes < 1 || op.RequestMaxBytes > EnvelopeMaxBytes {
			return nil, fmt.Errorf("invalid execution contract for %q", op.ID)
		}
		for name, field := range op.Fields {
			if err := validateFieldDefinition(field); err != nil {
				return nil, fmt.Errorf("operation %q field %q: %w", op.ID, name, err)
			}
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
		e.ops[op.ID] = op
		e.ordered = append(e.ordered, op.ID)
		contract := describe(op)
		// Catalog presentation and examples do not change execution semantics.
		for _, key := range []string{"summary", "examples", "time_semantics"} {
			delete(contract, key)
		}
		// The original evidence contract digest stays valid across upgrades.
		// New limits are execution semantics only when they differ from its
		// historical readonly, evidence-pool, three-second, 64KiB defaults.
		if op.ExecutionTimeout == RequestTimeout {
			delete(contract, "execution_timeout_ms")
		}
		if op.ExecutionPool == "evidence" {
			delete(contract, "execution_pool")
		}
		if op.RequestMaxBytes == MaxRequestBytes {
			delete(contract, "request_max_bytes")
		}
		digest, err := contractDigest(contract)
		if err != nil {
			return nil, err
		}
		e.contracts[op.ID] = digest
	}
	sort.Strings(e.ordered)
	catalog := make([]any, 0, len(e.ordered))
	for _, id := range e.ordered {
		catalog = append(catalog, describe(e.ops[id]))
	}
	var err error
	e.revision, err = contractDigest(catalog)
	return e, err
}

func (e *EvidenceExecutor) executionSlots(op Operation) chan struct{} {
	if op.ExecutionPool == "exec" {
		return e.execSlots
	}
	if op.ExecutionPool == "sre" {
		return e.sreSlots
	}
	return e.slots
}

func (e *EvidenceExecutor) requestLimits(op Operation) map[string]any {
	return map[string]any{"request_max_bytes": op.RequestMaxBytes, "response_max_bytes": MaxResponseBytes,
		"execution_timeout_ms": op.ExecutionTimeout.Milliseconds(), "transport_margin_ms": TransportMargin.Milliseconds(),
		"admission_timeout_ms": RequestTimeout.Milliseconds(), "concurrency": cap(e.executionSlots(op)),
		"execution_pool": op.ExecutionPool, "budget_scope": "process", "http_request_slots": 4,
		"invokes_per_session_per_minute": InvokesPerSessionPerMinute}
}

func contractDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (e *EvidenceExecutor) OperationContractRevision(operation string) string {
	return e.contracts[operation]
}

// AllowsTargetedOperation is the internal dispatch allow-list. Unregistered
// and deployment-only operations are rejected before routing or execution.
func (e *EvidenceExecutor) AllowsTargetedOperation(operation string) bool {
	op, found := e.ops[operation]
	return found && (op.Targetable || op.DefaultControlLeader)
}
