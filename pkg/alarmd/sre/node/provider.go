// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package node

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func New(o Options) (*Provider, error) {
	if o.Redis == nil || o.Client == nil || o.Executor == nil || o.KeyPrefix == "" || o.DefaultNamespace == "" || o.Image == "" || o.DeploymentID == "" {
		return nil, fail("node_unavailable", "node execution requires shared storage, cluster access and deployment identity")
	}
	return &Provider{options: o}, nil
}
func normalize(r *ExecRequest) error {
	id, err := uuid.Parse(r.ExecutionID)
	if err != nil || id.Version() != 7 || id.String() != r.ExecutionID {
		return fail("invalid_execution_id", "execution_id must be a canonical UUIDv7")
	}
	if r.Node.Name == "" || r.Node.UID == "" || (r.Mode != "filesystem" && r.Mode != "host") || len(r.Argv) == 0 || r.Argv[0] == "" {
		return fail("invalid_params", "node name/UID, mode and argv are required")
	}
	if r.Mode == "host" && (r.User == "" || !strings.HasPrefix(r.Cwd, "/")) {
		return fail("invalid_params", "host mode requires user and absolute cwd")
	}
	if r.Mode == "filesystem" && r.Cwd == "" {
		r.Cwd = "/"
	}
	if !strings.HasPrefix(r.Cwd, "/") {
		return fail("invalid_params", "cwd must be absolute")
	}
	if r.TimeoutSeconds == 0 {
		r.TimeoutSeconds = 60
	}
	if r.OutputLimitBytes == 0 {
		r.OutputLimitBytes = 8 << 20
	}
	if r.TimeoutSeconds < 1 || r.TimeoutSeconds > 1800 || r.OutputLimitBytes < 1 || r.OutputLimitBytes > 8<<20 || len(r.Stdin) > 1<<20 {
		return fail("invalid_params", "execution exceeds time, output or input bounds")
	}
	b, _ := json.Marshal(r)
	if len(b) > 16<<20 {
		return fail("invalid_params", "request exceeds 16 MiB")
	}
	return nil
}
func (p *Provider) Exec(ctx context.Context, request ExecRequest) (Result, error) {
	if err := normalize(&request); err != nil {
		return Result{}, err
	}
	if p.options.FingerprintKeySource == nil {
		return Result{}, fail("shared_key_unavailable", "shared fingerprint key unavailable")
	}
	key := p.options.FingerprintKeySource()
	if len(key) == 0 {
		return Result{}, fail("shared_key_unavailable", "shared fingerprint key unavailable")
	}
	raw, _ := json.Marshal(request)
	mac := hmac.New(sha256.New, key)
	mac.Write(raw)
	digest := hex.EncodeToString(mac.Sum(nil))
	existing, old, cancel, err := p.read(ctx, request.ExecutionID)
	if err == nil {
		if old != digest {
			return Result{}, fail("execution_conflict", "execution ID already binds a different request or fingerprint key")
		}
		return Result{Record: existing, CancelRequested: cancel}, nil
	}
	if e, ok := err.(*Error); !ok || e.Code != "record_expired" {
		return Result{}, err
	}
	id, _ := uuid.Parse(request.ExecutionID)
	sec, nsec := id.Time().UnixTime()
	age := time.Since(time.Unix(sec, nsec))
	if age > 5*time.Minute || age < -time.Minute {
		return Result{}, fail("execution_expired", "new execution ID is outside the submission time window")
	}
	node, err := p.options.Client.CoreV1().Nodes().Get(ctx, request.Node.Name, metav1.GetOptions{})
	if err != nil {
		return Result{}, err
	}
	if string(node.UID) != request.Node.UID || node.Status.NodeInfo.OperatingSystem != "linux" {
		return Result{}, fail("target_changed", "target must be the selected Linux node UID")
	}
	sum := sha256.Sum256([]byte(p.options.DeploymentID + request.ExecutionID))
	now := time.Now().UTC()
	r := Record{Caller: request.Caller, ExecutionID: request.ExecutionID, Node: request.Node, Mode: request.Mode, Namespace: p.options.DefaultNamespace, JobName: "alarmd-node-" + hex.EncodeToString(sum[:16]), State: "preparing", CreatedAt: now, Deadline: now.Add(time.Duration(request.TimeoutSeconds+60+900+10) * time.Second), Cleanup: "retained"}
	data, _ := json.Marshal(r)
	created, err := createScript.Run(ctx, p.options.Redis, []string{p.key(r.ExecutionID), p.index()}, string(data), digest, now.Unix(), r.ExecutionID).Int()
	if err != nil {
		return Result{}, err
	}
	if created != 1 {
		return p.Exec(ctx, request)
	}
	// Only the creator owns this volatile input. Recovery never re-dispatches it.
	go p.prepare(request, r)
	return Result{Record: r}, nil
}
func (p *Provider) Get(ctx context.Context, q GetRequest) (Result, error) {
	if _, err := uuid.Parse(q.ExecutionID); err != nil {
		return Result{}, fail("invalid_execution_id", "invalid execution ID")
	}
	if q.MaxBytes < 0 || q.MaxBytes > 256<<10 {
		return Result{}, fail("invalid_params", "max_bytes must be at most 256 KiB")
	}
	r, _, cancel, err := p.read(ctx, q.ExecutionID)
	if err != nil {
		return Result{}, err
	}
	result := Result{Record: r, CancelRequested: cancel}
	if r.PodUID == "" {
		return result, nil
	}
	response, err := p.control(ctx, r, map[string]any{"action": "get", "execution_id": r.ExecutionID, "output_cursor": q.OutputCursor, "max_bytes": q.MaxBytes})
	if err != nil {
		result.OutputUnavailable = true
		if !terminal(r.State) {
			result.State = "unknown"
		}
		result.Detail = "runner unavailable: " + err.Error()
		return result, nil
	}
	var state struct {
		State        string `json:"state"`
		ExitCode     *int   `json:"exit_code"`
		Error        string `json:"error"`
		ControlError bool   `json:"control_error"`
	}
	if err = json.Unmarshal(response, &state); err != nil {
		return Result{}, err
	}
	if state.Error != "" && state.ControlError {
		return Result{Record: r, Runner: response, CancelRequested: cancel}, fail("runner_error", safeError(errors.New(state.Error)))
	}
	result.Runner = response
	// Execution/output errors accompany useful execution facts. Preserve the
	// page, exit status and terminal state; only invalid controls are refused.
	if state.State != "" {
		next := r
		next.State = state.State
		next.ExitCode = state.ExitCode
		if state.Error != "" {
			next.OutputIncomplete = true
			next.Detail = safeError(errors.New(state.Error))
		}
		result.Record = next
		if !sameRecord(r, next) {
			err = p.replace(ctx, r, next, false)
		} else {
			err = nil
		}
		if err == nil {
			p.finish(ctx, next)
		}
	}
	return result, nil
}

func sameRecord(a, b Record) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
func (p *Provider) Cancel(ctx context.Context, q CancelRequest) (Result, error) {
	r, _, _, err := p.read(ctx, q.ExecutionID)
	if err != nil {
		return Result{}, err
	}
	if err = cancelScript.Run(ctx, p.options.Redis, []string{p.key(q.ExecutionID)}).Err(); err != nil {
		return Result{}, err
	}
	// Re-read after the durable intent: selection can have raced the first
	// read. A later selection is fenced by dispatchScript's cancel check.
	r, _, _, err = p.read(ctx, q.ExecutionID)
	if err != nil {
		return Result{}, err
	}
	result := Result{Record: r, CancelRequested: true}
	if r.PodUID != "" {
		response, e := p.control(ctx, r, map[string]any{"action": "cancel", "execution_id": q.ExecutionID})
		if e != nil {
			result.Detail = "cancel intent saved; termination unconfirmed"
		} else {
			result.Runner = response
		}
	}
	return result, nil
}
func terminal(s string) bool {
	return s == "succeeded" || s == "failed" || s == "timed_out" || s == "cancelled"
}
func (p *Provider) finish(ctx context.Context, r Record) {
	if !terminal(r.State) {
		return
	}
	if p.options.Final != nil {
		won, err := p.options.Redis.HSetNX(ctx, p.key(r.ExecutionID), "audited", "1").Result()
		if err == nil && won {
			p.options.Final(r)
		}
	}
	// Keep observing retained carriers to expose their eventual deletion.
}
func (p *Provider) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.observe(ctx)
		}
	}
}
func (p *Provider) observe(ctx context.Context) {
	// SSCAN-style bounded pages; no admission limit or concurrency semaphore.
	var offset int64
	for {
		ids, err := p.options.Redis.ZRange(ctx, p.index(), offset, offset+63).Result()
		if err != nil || len(ids) == 0 {
			return
		}
		for _, id := range ids {
			// Observation leases only suppress duplicate polling. They never
			// authorize command dispatch or limit execution concurrency.
			claimed, e := p.options.Redis.SetNX(ctx, p.key(id)+":observer", "1", 8*time.Second).Result()
			if e != nil || !claimed {
				continue
			}
			r, _, cancellation, err := p.read(ctx, id)
			if err != nil {
				if _, ok := err.(*Error); ok {
					p.options.Redis.ZRem(ctx, p.index(), id)
				}
				continue
			}
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			if cancellation && r.PodUID != "" && !terminal(r.State) {
				_, _ = p.control(c, r, map[string]any{"action": "cancel", "execution_id": id})
			}
			if r.PodUID != "" {
				_, _ = p.Get(c, GetRequest{ExecutionID: id, MaxBytes: 1})
			}
			if time.Now().After(r.Deadline.Add(5 * time.Minute)) {
				job, e := p.options.Client.BatchV1().Jobs(r.Namespace).Get(c, r.JobName, metav1.GetOptions{})
				_ = job
				if e != nil && isNotFound(e) {
					next := r
					next.Cleanup = "deleted"
					if !terminal(r.State) {
						next.State = "unknown"
						next.Detail = "carrier disappeared before terminal result was observed"
					}
					if p.replace(c, r, next, false) == nil {
						p.options.Redis.ZRem(c, p.index(), id)
					}
				}
			}
			if r.PodUID == "" && r.State == "preparing" && time.Since(r.CreatedAt) > time.Minute {
				next := r
				next.State = "unknown"
				next.Detail = "preparation interrupted; input will not be replayed"
				_ = p.replace(c, r, next, false)
			}
			cancel()
		}
		offset += int64(len(ids))
		if len(ids) < 64 {
			return
		}
	}
}
