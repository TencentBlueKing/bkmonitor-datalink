// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/k8sread"
)

// k8sBoundary is said on every answer: this path reads through a running
// replica, so it has nothing to say when none is running.
const k8sBoundary = "Read through the answering replica's own ServiceAccount; with every replica down this path cannot answer and kubectl is the only reader."

// K8sOperations reads alarmd's own workload from the Kubernetes API: its
// Pods, the events on it, and a bounded log tail. Any replica answers, the
// entry one by default, so a crashing replica can be read from a healthy one.
func K8sOperations(reader *k8sread.Reader) []Operation {
	pod := Field{Type: "string", Description: "alarmd 的 Pod 名。", Source: "k8s.pods pods[].name", Pattern: "^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$", MinLength: 1, MaxLength: 253}
	container := Field{Type: "string", Description: "容器名；省略时取 Pod 唯一的容器，多个容器时取 alarmd。", Source: "k8s.pods pods[].containers[].name", Pattern: "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", MinLength: 1, MaxLength: 63}
	minLines, maxLines := int64(1), int64(k8sread.MaxLogLines)
	maxScan, maxSince := int64(k8sread.MaxScanLines), int64(k8sread.MaxLogSinceSeconds)
	substring := Field{Type: "string", MinLength: 1, MaxLength: k8sread.MaxLogFilterBytes}
	limits := map[string]any{"max_pods": k8sread.MaxPods, "max_events": k8sread.MaxEvents, "max_replica_sets": k8sread.MaxReplicaSets,
		"max_log_lines": k8sread.MaxLogLines, "max_log_bytes": k8sread.MaxLogBytes,
		"max_scan_bytes": k8sread.MaxScanBytes, "max_log_line_bytes": k8sread.MaxLogLineBytes, "verbs": "GET only", "scope": "the Deployment this replica belongs to"}
	ops := []Operation{
		{ID: "k8s.pods", Summary: "读取 alarmd 自己的 Deployment 状态与各 Pod 的阶段、就绪、重启次数和上次退出原因。", Fields: map[string]Field{}, OutputSchema: SchemaOf(k8sread.PodsResult{}),
			Run: func(ctx context.Context, _ Params) Outcome {
				result, err := reader.Pods(ctx)
				out := k8sOutcome(result, err)
				if err == nil && result.Truncated {
					out.Complete = false
					out.Limitations = append(out.Limitations, "More Pods match than the bound; the list is cut.")
				}
				return out
			}},
		{ID: "k8s.events", Summary: "读取 alarmd 的 Deployment、ReplicaSet 与 Pod 上的 Kubernetes 事件，新的在前；可只看一个 Pod。", Fields: map[string]Field{"pod": pod}, OutputSchema: SchemaOf(k8sread.EventsResult{}),
			Run: func(ctx context.Context, p Params) Outcome {
				result, err := reader.Events(ctx, p.String("pod"))
				out := k8sOutcome(result, err)
				if err != nil {
					return out
				}
				if len(result.Failed) > 0 {
					out.Complete = false
					out.Limitations = append(out.Limitations, "Events of the objects in result.failed could not be read; no event for them is not none happened.")
				}
				if result.Truncated {
					out.Complete = false
					out.Limitations = append(out.Limitations, "More events than the bound; the oldest are cut.")
				}
				return out
			}},
		{ID: "k8s.logs", Summary: "读取 alarmd 某个 Pod 容器日志的末尾若干行；previous=true 读上一次运行（崩溃前）的日志；contains 按子串过滤：日志边读边扫、只返回命中的行，扫描量不受返回上限约束；配 since_seconds 时从那一刻起扫到最新，扫不完会写明停在哪一行。", Fields: map[string]Field{
			"pod": pod, "container": container,
			"previous": {Type: "boolean", Description: "读上一次运行的日志，即崩溃或被杀之前的那一次。"},
			"lines":    {Type: "integer", Description: "返回的行数上限，默认 200；过滤时是命中行里最新的这么多行。", Minimum: &minLines, Maximum: &maxLines},
			"contains": {Type: "array", Items: &substring, MaxItems: k8sread.MaxLogFilters, UniqueItems: true,
				Description: "只保留含任一子串的行，比如 stage 名、原因码、QG 标识。"},
			"scan_lines":    {Type: "integer", Description: "过滤且不给 since_seconds 时扫描的末尾行数，默认 20000；给了 since_seconds 时默认扫整个窗口。", Minimum: &minLines, Maximum: &maxScan},
			"since_seconds": {Type: "integer", Description: "只读最近这么多秒的日志；过滤时从这一刻扫到最新。", Minimum: &minLines, Maximum: &maxSince},
		}, Required: []string{"pod"}, OutputSchema: SchemaOf(k8sread.LogResult{}), Examples: []Params{{"pod": "bk-monitor-alarmd-trigger-5bdb679ddf-abcde", "previous": true},
			{"pod": "bk-monitor-alarmd-trigger-5bdb679ddf-abcde", "contains": []any{"schedule_cutover"}, "since_seconds": int64(3600)}},
			Run: func(ctx context.Context, p Params) Outcome {
				return logOutcome(reader.Logs(ctx, logRequestOf(p)))
			}},
	}
	for i := range ops {
		ops[i].EvidenceScope = "deployment_workload"
		ops[i].Limits = limits
	}
	return ops
}

// k8sOutcome carries a named failure as the channel's failure, code for
// code: an empty list is only ever the answer of a read that happened.
func k8sOutcome(value any, err error) Outcome {
	if err != nil {
		var named *k8sread.Error
		if errors.As(err, &named) {
			return Outcome{Error: &Failure{Code: "k8s_" + named.Code, Message: named.Error()}, Limitations: []string{k8sBoundary}}
		}
		return Outcome{Error: &Failure{Code: "k8s_" + k8sread.CodeAPIError, Message: err.Error()}, Limitations: []string{k8sBoundary}}
	}
	return Outcome{Value: value, Complete: true, Limitations: []string{k8sBoundary}}
}

// logRequestOf is the log read the parameters ask for.
func logRequestOf(p Params) k8sread.LogRequest {
	var contains []string
	if values, ok := p["contains"].([]any); ok {
		for _, value := range values {
			if text, ok := value.(string); ok {
				contains = append(contains, text)
			}
		}
	}
	return k8sread.LogRequest{Pod: p.String("pod"), Container: p.String("container"), Previous: p.Bool("previous"),
		Lines: p.Int("lines", k8sread.DefaultLogLines), Contains: contains, ScanLines: p.Int("scan_lines", 0), SinceSeconds: p.Int("since_seconds", 0)}
}

// logOutcome is a log read as the channel answers it: partial, and saying
// why, when lines were left out or the scan stopped short of the end.
func logOutcome(result k8sread.LogResult, err error) Outcome {
	out := k8sOutcome(result, err)
	if err == nil && result.Truncated {
		out.Complete = false
		if len(result.Contains) > 0 {
			out.Limitations = append(out.Limitations, "More lines matched than returned; the newest are kept. Narrow the substrings or the window for older ones.")
		} else {
			out.Limitations = append(out.Limitations, "The log reached the byte bound; fewer lines than asked, the oldest dropped.")
		}
	}
	if err == nil && result.ScanComplete != nil && !*result.ScanComplete {
		out.Complete = false
		out.Limitations = append(out.Limitations, "The scan stopped at the "+result.ScanStopped+" after the line stamped "+result.ScanTo+
			"; nothing after it was read. Ask again with since_seconds reaching back only to that time to scan the rest.")
	}
	if err == nil && len(result.Contains) > 0 && result.MatchedLines == 0 {
		out.Limitations = append(out.Limitations, "No matching line in the scanned part (scan_from to scan_to); that is not proof there was none outside it.")
	}
	return out
}
