// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/node"
)

const (
	OpNodeExec   = "node.exec"
	OpNodeGet    = "node.execution.get"
	OpNodeCancel = "node.execution.cancel"
	OpNodeEnded  = "node.execution.ended"
)

// NodeExecutions separates the short channel request from the supervised
// command. All replicas resolve the same execution through shared state.
type NodeExecutions interface {
	Exec(context.Context, node.ExecRequest) (node.Result, error)
	Get(context.Context, node.GetRequest) (node.Result, error)
	Cancel(context.Context, node.CancelRequest) (node.Result, error)
}

func nodeOperations(options SREOptions, audit func(context.Context, time.Time, SREAction, error)) []Operation {
	available := func() Availability {
		if options.Nodes != nil {
			return Availability{Available: true}
		}
		reason := options.NodeReason
		if reason == "" {
			reason = "node_provider_unavailable: shared store and in-cluster runner are required"
		}
		return Availability{Reason: reason}
	}
	text := Field{Type: "string", MinLength: 1, MaxLength: 256}
	id := Field{Type: "string", MinLength: 36, MaxLength: 36, Description: "Client-generated UUIDv7; reuse this ID after a lost response, never automatically resubmit with a new ID."}
	positive, maxTimeout, maxOutput, maxPage := int64(1), int64(1800), int64(8<<20), int64(256<<10)
	fields := map[string]Field{
		"execution_id": id,
		"node":         {Type: "object", Properties: map[string]Field{"name": text, "uid": text}, Required: []string{"name", "uid"}},
		"mode":         {Type: "string", Enum: []string{"filesystem", "host"}},
		"cwd":          {Type: "string", MaxLength: 4096}, "user": text,
		"env":                {Type: "object", AdditionalProperties: true, Description: "Explicit string values; minimal PATH/LANG base, no inherited credentials."},
		"argv":               {Type: "array", MinItems: 1, MaxItems: 256, Items: &Field{Type: "string", MaxLength: 65536}},
		"stdin":              {Type: "string", Source: "stdin", MaxLength: 1 << 20},
		"timeout_seconds":    {Type: "integer", Minimum: &positive, Maximum: &maxTimeout},
		"output_limit_bytes": {Type: "integer", Minimum: &positive, Maximum: &maxOutput},
	}
	run := func(op string, ctx context.Context, p Params) Outcome {
		started := time.Now()
		raw, err := json.Marshal(p)
		if err != nil {
			return Outcome{Error: &Failure{Code: "invalid_input", Message: "parameters are not JSON"}}
		}
		var value node.Result
		switch op {
		case OpNodeExec:
			var request node.ExecRequest
			if err = json.Unmarshal(raw, &request); err == nil {
				caller := CallerFrom(ctx)
				request.Caller = node.Caller{SessionID: caller.SessionID, RequestID: caller.RequestID, Operator: caller.Operator, IssuedVia: caller.IssuedVia}
				value, err = options.Nodes.Exec(ctx, request)
			}
		case OpNodeGet:
			var request node.GetRequest
			if err = json.Unmarshal(raw, &request); err == nil {
				value, err = options.Nodes.Get(ctx, request)
			}
		case OpNodeCancel:
			var request node.CancelRequest
			if err = json.Unmarshal(raw, &request); err == nil {
				value, err = options.Nodes.Cancel(ctx, request)
			}
		}
		action := SREAction{Op: op, ExecutionID: p.String("execution_id"), Kind: "Node", Namespace: value.Namespace, Name: value.Node.Name, UID: value.Node.UID}
		if target, ok := p["node"].(map[string]any); ok {
			action.Name, _ = target["name"].(string)
			action.UID, _ = target["uid"].(string)
		}
		audit(ctx, started, action, err)
		out := Outcome{Value: value, Complete: err == nil, Limitations: []string{"Node execution has host administration privileges. Cancellation terminates the supervised process group; it does not undo external effects. Output is bounded and retained temporarily."}}
		if err != nil {
			var named *node.Error
			if errors.As(err, &named) {
				out.Error = &Failure{Code: "node_" + named.Code, Message: named.Message}
			} else {
				out.Error = &Failure{Code: "node_provider_failed", Message: "node execution control did not complete; query the same execution_id before any new execution"}
			}
		}
		next := Params{"execution_id": p.String("execution_id")}
		var page struct {
			NextCursor string `json:"next_cursor"`
		}
		_ = json.Unmarshal(value.Runner, &page)
		if page.NextCursor != "" {
			next["output_cursor"] = page.NextCursor
		}
		if out.Error == nil {
			switch value.State {
			case "failed", "timed_out", "cancelled":
				if op == OpNodeCancel {
					break
				}
				out.Error = &Failure{Code: "node_execution_" + value.State, Message: "Node command ended in " + value.State + "; inspect exit_code and output before any new execution."}
			case "unknown":
				out.Complete = false
				out.Error = &Failure{Code: "node_execution_unknown", Message: "Node command state is unconfirmed; query the same execution_id, do not replay."}
			}
		}
		if value.OutputUnavailable {
			out.Complete = false
			out.Limitations = append(out.Limitations, "Execution output is unavailable; the last confirmed command state is retained.")
		}
		if value.OutputIncomplete {
			out.Complete = false
			out.Limitations = append(out.Limitations, "Execution output is incomplete; missing bytes are not proof of absent events. The command state and available output remain usable.")
		}
		out.Next = []Call{{Operation: OpNodeGet, Params: next, Reason: "Read the same execution and its bounded output; do not submit a replacement command."}}
		return out
	}
	validateExec := func(p Params) error {
		if p.String("mode") == "host" && (p.String("cwd") == "" || p.String("user") == "") {
			return fmt.Errorf("host mode requires explicit cwd and user")
		}
		var req node.ExecRequest
		raw, _ := json.Marshal(p)
		if err := json.Unmarshal(raw, &req); err != nil {
			return fmt.Errorf("node parameters: %w", err)
		}
		return nil
	}
	limits := map[string]any{"max_timeout_seconds": maxTimeout, "max_output_bytes": maxOutput, "max_page_bytes": maxPage, "max_stdin_bytes": 1 << 20, "retry": "query_same_execution_id; never automatic execution replay", "scope": "current cluster Linux node; filesystem reads /host, host enters host namespaces; no arbitrary image or kubeconfig", "non_tty": true}
	return []Operation{
		{ID: OpNodeExec, Summary: "在当前集群 Linux Node 上启动一次有期限的非 TTY 执行；返回 execution_id，后续查询不依赖当前会话或接入副本。host 模式具有宿主管理权限。", Effect: "exec", EvidenceScope: "sre_node_exec", ExecutionPool: "exec", ExecutionTimeout: 90 * time.Second, RequestMaxBytes: EnvelopeMaxBytes, Availability: available, Fields: fields, Required: []string{"execution_id", "node", "mode", "argv"}, Validate: validateExec, Limits: limits, OutputSchema: SchemaOf(node.Result{}), Run: func(ctx context.Context, p Params) Outcome { return run(OpNodeExec, ctx, p) }},
		{ID: OpNodeGet, Summary: "按 execution_id 读取节点执行状态和有界输出；输出过期或不可达会显式报告，不重新执行命令。", EvidenceScope: "sre_node_exec", ExecutionPool: "sre", ExecutionTimeout: 30 * time.Second, Availability: available, Fields: map[string]Field{"execution_id": id, "output_cursor": {Type: "string", MaxLength: 4096}, "max_bytes": {Type: "integer", Minimum: &positive, Maximum: &maxPage}}, Required: []string{"execution_id"}, Limits: limits, OutputSchema: SchemaOf(node.Result{}), Run: func(ctx context.Context, p Params) Outcome { return run(OpNodeGet, ctx, p) }},
		{ID: OpNodeCancel, Summary: "持久记录取消意图并请求终止节点执行进程组；cancel_requested 与确认终止分别返回，取消不回滚文件或发布。", Effect: "exec", EvidenceScope: "sre_node_exec", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: available, Fields: map[string]Field{"execution_id": id}, Required: []string{"execution_id"}, Limits: limits, OutputSchema: SchemaOf(node.Result{}), Run: func(ctx context.Context, p Params) Outcome { return run(OpNodeCancel, ctx, p) }},
	}
}
