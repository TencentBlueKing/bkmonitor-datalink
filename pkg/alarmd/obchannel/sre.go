// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/rollout"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/uq"
)

// SREAction is one execution or management call, as its audit record and
// its count name it: who (the session and the request), what, where, and how
// it ended. An execution records the digest of its argv and stdin and the
// stdin's length, never their text.
type SREAction struct {
	Op         string `json:"op"`
	Namespace  string `json:"namespace"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Result     string `json:"result"`
	Code       string `json:"code,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	SessionID  string `json:"session_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	// At is when the action ended.
	At time.Time `json:"at"`
	// Exec is what a pod.exec run said about itself, without its output.
	Exec *SREExec `json:"exec,omitempty"`
}

// The results an SREAction ends in, closed: done, the target replaced since
// it was read, refused by the apiserver's authorization, refused before any
// effect (an invalid or unsupported request, a busy provider, a command that
// could not start), an apiserver or transport failure, and an execution
// whose remote end is unknown.
const (
	SREResultDone          = "done"
	SREResultTargetChanged = "target_changed"
	SREResultForbidden     = "forbidden"
	SREResultRefused       = "refused"
	SREResultFailed        = "failed"
	SREResultUnknown       = "unknown"
)

// SREActionOps and SREActionResults are the closed label values of the
// action count: six operations by six results.
// The image-change operations: the change a caller asks for, the revert the
// driver writes when the new image does not become ready, and the receipt
// read.
const (
	OpSetImage = "workload.set_image"
	OpRevert   = "workload.revert"
	OpRollout  = "workload.rollout"
)

var (
	SREActionOps     = []string{"pod.exec", pod.OpDeletePod, pod.OpRestartWorkload, pod.OpScaleWorkload, pod.OpReadSecret, pod.OpReadConfigMap, OpSetImage, OpRevert}
	SREActionResults = []string{SREResultDone, SREResultTargetChanged, SREResultForbidden, SREResultRefused, SREResultFailed, SREResultUnknown}
)

// sreResult is the result an action's error ends it in.
func sreResult(err error) (string, string) {
	if err == nil {
		return SREResultDone, ""
	}
	var change *rollout.Error
	if errors.As(err, &change) {
		switch change.Code {
		case rollout.CodeTargetChanged:
			return SREResultTargetChanged, change.Code
		case rollout.CodeInvalid:
			return SREResultRefused, change.Code
		case rollout.CodeForbidden:
			return SREResultForbidden, change.Code
		}
		return SREResultFailed, change.Code
	}
	var named *pod.Error
	if !errors.As(err, &named) {
		return SREResultFailed, "pod_provider_failed"
	}
	switch named.Code {
	case pod.CodeTargetChanged:
		return SREResultTargetChanged, named.Code
	case pod.CodeForbidden:
		return SREResultForbidden, named.Code
	case pod.RemoteUnknown:
		return SREResultUnknown, named.Code
	case pod.CodeInvalidRequest, pod.CodeNotInScope, pod.CodeTargetNotRunning, pod.CodeCommandStartFailed, pod.CodeTimeoutUnsupported, pod.CodeBusy, pod.CodeNotFound:
		return SREResultRefused, named.Code
	}
	return SREResultFailed, named.Code
}

// Caller is who asked for an operation: the admitted session and the
// request. The channel puts it on the operation's context.
type Caller struct {
	SessionID string
	RequestID string
}

type callerKey struct{}

// WithCaller carries caller to the operation run under ctx.
func WithCaller(ctx context.Context, caller Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, caller)
}

// CallerFrom is the caller ctx carries; zero when none.
func CallerFrom(ctx context.Context) Caller {
	caller, _ := ctx.Value(callerKey{}).(Caller)
	return caller
}

// SREOptions is what the SRE operations are built from. Audit hears every
// execution and management call once, after it ends; nil hears nothing.
type SREOptions struct {
	Pods      *pod.Provider
	Queries   *uq.Provider
	Scrubber  *redact.Scrubber
	PodReason string
	// Rollouts changes images and reads their receipts; nil leaves both
	// operations unavailable, for RolloutReason.
	Rollouts      *rollout.Driver
	RolloutReason string
	Audit         func(SREAction)
	// Receipts answers sre.receipts; nil leaves it unavailable, for
	// ReceiptsReason. Writing them is the audit's.
	Receipts       *SREReceipts
	ReceiptsReason string
	Now            func() time.Time
}

// SREOperations runs on the channel. These operations do not accept a Worker
// target and are never reachable through the internal Worker evidence RPC.
func SREOperations(options SREOptions) []Operation {
	pods, queries, scrubber, podReason := options.Pods, options.Queries, options.Scrubber, options.PodReason
	if scrubber == nil {
		scrubber = redact.New()
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	audit := func(ctx context.Context, started time.Time, action SREAction, err error) {
		if options.Audit == nil {
			return
		}
		caller := CallerFrom(ctx)
		action.Result, action.Code = sreResult(err)
		ended := now()
		action.DurationMS, action.At = ended.Sub(started).Milliseconds(), ended.UTC()
		action.SessionID, action.RequestID = caller.SessionID, caller.RequestID
		options.Audit(action)
	}
	text := Field{Type: "string", MinLength: 1, MaxLength: 256}
	nonnegative, positive := int64(0), int64(1)
	maxTimeout, maxOutput, maxTail, maxSince, maxLimit, maxReplicas := int64(30000), int64(512<<10), int64(1000), int64(86400), int64(32), int64(pod.MaxReplicas)
	target := Field{Type: "object", Properties: map[string]Field{"namespace": text, "pod": text, "uid": text, "container": text, "container_id": {Type: "string", MinLength: 1, MaxLength: 512}, "image_id": {Type: "string", MinLength: 1, MaxLength: 1024}}, Required: []string{"namespace", "pod", "uid", "container", "container_id", "image_id"}}
	inspectTarget := target
	inspectTarget.Required = []string{"namespace", "pod"}
	deleteTarget := target
	deleteTarget.Required = []string{"namespace", "pod", "uid"}
	workloadFields := map[string]Field{"namespace": text, "kind": {Type: "string", Enum: []string{"Deployment", "StatefulSet", "DaemonSet"}}, "name": text, "uid": text}
	podAvailable := func() Availability {
		if pods == nil {
			reason := podReason
			if reason == "" {
				reason = "pod_provider_unavailable: this process is not running in a cluster"
			}
			return Availability{Reason: reason}
		}
		return Availability{Available: true}
	}
	podLimits := map[string]any{"max_timeout_ms": maxTimeout, "max_output_bytes": maxOutput, "max_stdin_bytes": 32 << 10, "max_targets_per_page": 32,
		"scope": "one namespace per request (this process's own when none is named) and its label selector; owner chain and container identity verified",
		"retry": "explicit_only; canceled or unknown executions must be inspected before retry", "non_tty": true,
		"supervisor": pod.TimeoutContract + ": the target's /bin/sh runs the command in its own process group (setsid, else job control; " +
			"supervision says whether it got one) with a sleep and kill timer at its deadline, " +
			"rounded up to whole seconds; without /bin/sh or sleep the command runs unsupervised (remote_unsupervised, unsupervised_reason) " +
			"and only this side's stream deadline bounds it"}
	maxResourceLimit := int64(pod.MaxResourceLimit)
	k8sListLimits := map[string]any{"max_output_bytes": maxOutput, "max_objects_per_page": pod.MaxResourceLimit, "default_objects_per_page": pod.DefaultResourceLimit,
		"max_paths": pod.MaxResourcePaths, "kinds": pod.ResourceKinds(),
		"scope":     "the whole cluster when namespace is left out, else that namespace; the kinds the channel's ClusterRole grants get and list on",
		"redaction": pod.RedactionContract + "; managedFields and the last-applied annotation dropped, credential-named annotations and env values masked whole; Secret and ConfigMap names in references kept"}
	podLimits["verification_timeout_ms"] = pod.VerificationTimeout.Milliseconds()
	podLimits["termination_grace_ms"] = pod.TerminationGrace.Milliseconds()
	podLimits["startup_allowance_ms"] = pod.StartupAllowance.Milliseconds()
	if pods != nil {
		podLimits["default_namespace"] = pods.DefaultNamespace()
		podLimits["module_label"] = pods.ModuleLabel()
		podLimits["provider_limits"] = pods.Limits()
	}
	wrapPod := func(value any, err error, complete bool) Outcome {
		out := Outcome{Value: value, Complete: complete, Limitations: []string{"Owner chain and container identity are checked through the channel ServiceAccount; arbitrary application content requires its domain masking."}}
		if err != nil {
			out.Complete = false
			var named *pod.Error
			if errors.As(err, &named) {
				out.Error = &Failure{Code: "pod_" + named.Code, Message: named.Message}
			} else {
				out.Error = &Failure{Code: "pod_provider_failed", Message: "Pod provider did not complete."}
			}
		}
		return out
	}
	workloadRequest := func(p Params) pod.WorkloadRequest {
		request := pod.WorkloadRequest{Namespace: p.String("namespace"), Kind: p.String("kind"), Name: p.String("name"), UID: p.String("uid")}
		if request.Namespace == "" && pods != nil {
			request.Namespace = pods.DefaultNamespace()
		}
		return request
	}
	ops := []Operation{
		{ID: "pod.targets", Summary: "在一个命名空间（不指定时是本进程所在的命名空间）里按标签选择器发现 Pod 与容器，返回 UID、containerID、imageID 和 owner 链。", EvidenceScope: "sre_pod_targets", ExecutionPool: "sre", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits,
			Fields: map[string]Field{"namespace": text, "selector": {Type: "string", MaxLength: 1024}, "release": text, "module": text, "workload": text, "continue": {Type: "string", MaxLength: 4096}, "limit": {Type: "integer", Minimum: &positive, Maximum: &maxLimit}}, OutputSchema: SchemaOf(pod.DiscoverReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				v, err := pods.Discover(ctx, pod.DiscoverRequest{Namespace: p.String("namespace"), Selector: p.String("selector"), Release: p.String("release"), Module: p.String("module"), Workload: p.String("workload"), Continue: p.String("continue"), Limit: p.Int("limit", 0)})
				out := wrapPod(v, err, v.Continue == "")
				if v.Continue != "" {
					next := Params{"continue": v.Continue}
					for _, key := range []string{"namespace", "selector", "release", "module", "workload", "limit"} {
						if value, present := p[key]; present {
							next[key] = value
						}
					}
					out.Next = []Call{{Operation: "pod.targets", Params: next, Reason: "读取同一目标范围的下一页，直至 continuation 为空。"}}
				}
				return out
			}},
		{ID: pod.OpListResources, Summary: "列出或读取一种 Kubernetes 资源（Node、Namespace、Pod、Service、Endpoints、Event、Deployment、StatefulSet、DaemonSet、ReplicaSet、Job、CronJob、Ingress、HorizontalPodAutoscaler）：不填 namespace 时命名空间级的种类按整个集群列；name 读一个对象；label_selector、field_selector 原样交给 apiserver（如 spec.nodeName=节点名 按节点列 Pod）；paths 只取给出的点分路径。输出去掉 managedFields 与 last-applied 注解，凭据名的注解与 env 值整值遮蔽，所有字符串经过脱敏。Secret 与 ConfigMap 不在此读。",
			EvidenceScope: "sre_k8s_read", ExecutionPool: "sre", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: k8sListLimits,
			Fields: map[string]Field{"kind": {Type: "string", MinLength: 1, MaxLength: 64}, "namespace": text, "name": {Type: "string", MinLength: 1, MaxLength: 253},
				"label_selector": {Type: "string", MaxLength: 1024}, "field_selector": {Type: "string", MaxLength: 1024},
				"paths":    {Type: "array", MaxItems: pod.MaxResourcePaths, Items: &Field{Type: "string", MinLength: 1, MaxLength: 256}},
				"continue": {Type: "string", MaxLength: 4096}, "limit": {Type: "integer", Minimum: &positive, Maximum: &maxResourceLimit}},
			Required: []string{"kind"}, OutputSchema: SchemaOf(pod.ResourceReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				request := pod.ResourceRequest{Kind: p.String("kind"), Namespace: p.String("namespace"), Name: p.String("name"),
					LabelSelector: p.String("label_selector"), FieldSelector: p.String("field_selector"), Continue: p.String("continue"), Limit: p.Int("limit", 0)}
				if values, ok := p["paths"].([]any); ok {
					for _, value := range values {
						request.Paths = append(request.Paths, value.(string))
					}
				}
				v, err := pods.ListResources(ctx, request)
				out := wrapPod(v, err, err == nil && v.Continue == "" && !v.Truncated && len(v.OversizedItems) == 0)
				out.Limitations = []string{"A list across the cluster is paged: a cluster-wide Pod list can take several pages at the output bound, so follow continue until it is empty.",
					"Secret and ConfigMap content is not read here; secret.get and configmap.get read them with their masking."}
				if err != nil {
					return out
				}
				next := Params{}
				for _, key := range []string{"kind", "namespace", "label_selector", "field_selector", "paths", "limit"} {
					if value, present := p[key]; present {
						next[key] = value
					}
				}
				switch {
				case v.Truncated:
					next["limit"] = max(1, v.Count)
					if v.ReadWith != "" {
						next["continue"] = v.ReadWith
					}
					out.Next = []Call{{Operation: pod.OpListResources, Params: next, Reason: fmt.Sprintf("这一页超出应答上限，漏了 %d 个对象；用同一续读位置、每页 %d 个再读。", v.ItemsOmitted, max(1, v.Count))}}
				case v.Continue != "":
					next["continue"] = v.Continue
					out.Next = []Call{{Operation: pod.OpListResources, Params: next, Reason: "读取下一页，直至 continue 为空。"}}
				}
				if len(v.OversizedItems) > 0 {
					out.Limitations = append(out.Limitations, "Some objects were over the output bound alone and came back as their identity only (oversized_items); read them by name with paths.")
				}
				return out
			}},
		{ID: "pod.get", Summary: "读取一个 Pod 的容器身份、运行状态与 owner 链；首次可省略 UID，后续使用返回身份。", EvidenceScope: "sre_pod_target", ExecutionPool: "sre", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits, Fields: map[string]Field{"target": inspectTarget}, Required: []string{"target"}, OutputSchema: SchemaOf(pod.Observation{}),
			Run: func(ctx context.Context, p Params) Outcome {
				v, err := pods.Inspect(ctx, pod.TargetRequest{Target: podTarget(p)})
				return wrapPod(v, err, err == nil)
			}},
		{ID: "pod.logs", Summary: "读取指定 Pod/container 身份的有界日志，执行前后复核容器变化。", EvidenceScope: "sre_pod_logs", ExecutionPool: "sre", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits, Fields: map[string]Field{"target": target, "tail_lines": {Type: "integer", Minimum: &positive, Maximum: &maxTail}, "since_seconds": {Type: "integer", Minimum: &positive, Maximum: &maxSince}, "previous": {Type: "boolean"}, "output_bytes": {Type: "integer", Minimum: &positive, Maximum: &maxOutput}}, Required: []string{"target"}, OutputSchema: SchemaOf(pod.LogsReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				v, err := pods.Logs(ctx, pod.LogsRequest{Target: podTarget(p), TailLines: int64(p.Int("tail_lines", 0)), SinceSeconds: int64(p.Int("since_seconds", 0)), Previous: p.Bool("previous"), OutputBytes: int64(p.Int("output_bytes", 0))})
				out := wrapPod(v, err, err == nil && !v.Truncated && !v.TargetChanged && v.EvidenceScope == "identity_verified_before_and_after")
				if v.ObservedAfter == nil {
					out.Limitations = append(out.Limitations, "Post-read target identity verification did not complete.")
				}
				return out
			}},
		{ID: "pod.exec", Summary: "在目标容器运行非 TTY argv/stdin，返回远端执行回执；请求不会自动重试，每次调用都留审计记录。", Effect: "exec", EvidenceScope: "sre_pod_exec", ExecutionPool: "exec", ExecutionTimeout: 45 * time.Second, Availability: podAvailable, Limits: podLimits,
			Fields: map[string]Field{"target": target, "argv": {Type: "array", MinItems: 1, MaxItems: 128, Items: &Field{Type: "string", MaxLength: 32768}}, "stdin": {Type: "string", Source: "stdin", MaxLength: 32768}, "timeout_ms": {Type: "integer", Minimum: &positive, Maximum: &maxTimeout}, "output_bytes": {Type: "integer", Minimum: &positive, Maximum: &maxOutput}}, Required: []string{"target", "argv"}, OutputSchema: SchemaOf(pod.ExecReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				argv := []string{}
				if values, ok := p["argv"].([]any); ok {
					for _, value := range values {
						argv = append(argv, value.(string))
					}
				}
				stdin := p.String("stdin")
				target := podTarget(p)
				v, err := pods.Exec(ctx, pod.ExecRequest{Target: target, Argv: argv, Stdin: stdin, TimeoutMS: int64(p.Int("timeout_ms", 0)), OutputBytes: int64(p.Int("output_bytes", 0))})
				audit(ctx, started, SREAction{Op: "pod.exec", Namespace: target.Namespace, Kind: "Pod", Name: target.Pod + "/" + target.Container, UID: target.UID,
					Detail: fmt.Sprintf("script %s stdin_bytes %d remote %s", v.ScriptDigest, len(stdin), v.RemoteState),
					Exec: &SREExec{RemoteState: v.RemoteState, Supervision: v.Supervision, UnsupervisedReason: v.UnsupervisedReason, ExitCode: v.ExitCode,
						DeadlineRaced: v.DeadlineRaced, StreamCut: v.StreamCut, Truncated: v.Truncated, ScriptDigest: v.ScriptDigest,
						DeclaredTimeoutMS: v.DeclaredTimeoutMS, EnforcedTimeoutSeconds: v.EnforcedTimeoutSeconds, BootstrapExitCode: v.BootstrapExitCode,
						BootstrapClass: v.BootstrapClass, StartFailure: v.StartFailure}}, err)
				out := wrapPod(v, err, err == nil && !v.Truncated && !v.TargetChanged && v.RemoteState == pod.RemoteCompleted && v.EvidenceScope == "identity_verified_before_and_after")
				if v.ObservedAfter == nil && v.RemoteState != pod.RemoteNotStarted {
					out.Limitations = append(out.Limitations, "Remote execution receipt is retained; post-execution target identity verification did not complete.")
				}
				if v.RemoteState == pod.RemoteUnknown {
					out.Limitations = append(out.Limitations, "Remote process termination is unconfirmed. Do not automatically repeat this execution.")
				}
				if v.RemoteState == pod.RemoteUnsupervised {
					out.Limitations = append(out.Limitations, "Nothing in the target could end this command ("+v.UnsupervisedReason+"); only this side's stream deadline bounded it.")
				}
				return out
			}},
		{ID: pod.OpDeletePod, Summary: "删除一个 Pod 交由控制器重建；按发现时的 UID 删除，Pod 已被替换时不删。每次调用都留审计记录。", Effect: "exec", EvidenceScope: "sre_manage", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits,
			Fields: map[string]Field{"target": deleteTarget, "owner_uid": text}, Required: []string{"target"}, OutputSchema: SchemaOf(pod.ActionReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				target := podTarget(p)
				v, err := pods.DeletePod(ctx, pod.DeleteRequest{Target: target, OwnerUID: p.String("owner_uid")})
				audit(ctx, started, SREAction{Op: pod.OpDeletePod, Namespace: target.Namespace, Kind: "Pod", Name: target.Pod, UID: target.UID}, err)
				return wrapPod(v, err, err == nil)
			}},
		{ID: pod.OpRestartWorkload, Summary: "滚动重启一个 Deployment/StatefulSet/DaemonSet（与 kubectl rollout restart 相同），按发现时的 UID 写入。每次调用都留审计记录。", Effect: "exec", EvidenceScope: "sre_manage", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits,
			Fields: workloadFields, Required: []string{"kind", "name", "uid"}, OutputSchema: SchemaOf(pod.ActionReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				request := workloadRequest(p)
				v, err := pods.RestartWorkload(ctx, request)
				audit(ctx, started, SREAction{Op: pod.OpRestartWorkload, Namespace: request.Namespace, Kind: request.Kind, Name: request.Name, UID: request.UID, Detail: "restarted_at " + v.RestartedAt}, err)
				return wrapPod(v, err, err == nil)
			}},
		{ID: pod.OpScaleWorkload, Summary: "设置一个 Deployment/StatefulSet 的副本数，回执写明原副本数；按发现时的 UID 写入。每次调用都留审计记录。", Effect: "exec", EvidenceScope: "sre_manage", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits,
			Fields: map[string]Field{"namespace": text, "kind": {Type: "string", Enum: []string{"Deployment", "StatefulSet"}}, "name": text, "uid": text, "replicas": {Type: "integer", Minimum: &nonnegative, Maximum: &maxReplicas}}, Required: []string{"kind", "name", "uid", "replicas"}, OutputSchema: SchemaOf(pod.ActionReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				request := workloadRequest(p)
				replicas := int32(p.Int("replicas", -1))
				request.Replicas = &replicas
				v, err := pods.ScaleWorkload(ctx, request)
				detail := fmt.Sprintf("replicas %d", replicas)
				if v.PreviousReplicas != nil {
					detail = fmt.Sprintf("replicas %d -> %d", *v.PreviousReplicas, replicas)
				}
				audit(ctx, started, SREAction{Op: pod.OpScaleWorkload, Namespace: request.Namespace, Kind: request.Kind, Name: request.Name, UID: request.UID, Detail: detail}, err)
				return wrapPod(v, err, err == nil)
			}},
		{ID: pod.OpReadSecret, Summary: "读白名单里的一个 Secret：默认只返回键名、每个值的字节数和 sha256，不返回值；reveal=true 才返回原值（仍经脱敏），回执和审计都记下要了原值。不在白名单的 Secret 由 apiserver 拒绝。", Effect: "exec", EvidenceScope: "sre_secret", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits,
			Fields: map[string]Field{"namespace": text, "name": text, "reveal": {Type: "boolean"}}, Required: []string{"name"}, OutputSchema: SchemaOf(pod.SecretReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				v, err := pods.ReadSecret(ctx, pod.SecretRequest{Namespace: p.String("namespace"), Name: p.String("name"), Reveal: p.Bool("reveal")})
				detail := "values not revealed"
				if v.Revealed {
					detail = "values revealed on request"
				}
				audit(ctx, started, SREAction{Op: pod.OpReadSecret, Namespace: v.Namespace, Kind: "Secret", Name: v.Name, UID: v.UID, Detail: detail}, err)
				out := wrapPod(v, err, err == nil && !v.Truncated)
				if v.Revealed {
					out.Limitations = append(out.Limitations, "Secret values were returned on request; they passed the output redaction, which masks only credentials it recognizes.")
				}
				return out
			}},
		{ID: pod.OpReadConfigMap, Summary: "读一个 ConfigMap（任意命名空间，不指定时是本进程所在的命名空间）：每个值都经过脱敏，名字像凭据的键（password、secret、token 等）整值遮蔽；各模块渲染进 ConfigMap 的明文口令因此不会原样返回。每次调用都留审计记录。", Effect: "exec", EvidenceScope: "sre_configmap", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits,
			Fields: map[string]Field{"namespace": text, "name": text}, Required: []string{"name"}, OutputSchema: SchemaOf(pod.ConfigMapReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				v, err := pods.ReadConfigMap(ctx, pod.ConfigMapRequest{Namespace: p.String("namespace"), Name: p.String("name")})
				audit(ctx, started, SREAction{Op: pod.OpReadConfigMap, Namespace: v.Namespace, Kind: "ConfigMap", Name: v.Name, UID: v.UID, Detail: fmt.Sprintf("keys %d", len(v.Keys))}, err)
				return wrapPod(v, err, err == nil && !v.Truncated)
			}},
	}
	rollouts := options.Rollouts
	rolloutAvailable := func() Availability {
		if rollouts == nil {
			reason := options.RolloutReason
			if reason == "" {
				reason = "rollout_unavailable: this process is not running in a cluster"
			}
			return Availability{Reason: reason}
		}
		return Availability{Available: true}
	}
	rolloutLimits := map[string]any{"kind": "Deployment", "receipt_ttl_hours": int64(rollout.RecordTTL / time.Hour), "lease_ms": rollout.LeaseDuration.Milliseconds(),
		"takeover_scan_ms": rollout.DefaultScan.Milliseconds(), "max_image_bytes": rollout.MaxImageBytes,
		"failure": "the Deployment's ProgressDeadlineExceeded, or no progress for progressDeadlineSeconds + 30 s since the last progress; a paused Deployment is never reverted",
		"revert":  "automatic, only while the image is still the one written; someone else's image is left as it is (superseded)"}
	wrapRollout := func(value any, err error) Outcome {
		out := Outcome{Value: value, Complete: err == nil}
		var named *rollout.Error
		if errors.As(err, &named) {
			out.Error = &Failure{Code: "rollout_" + named.Code, Message: named.Message}
		} else if err != nil {
			out.Error = &Failure{Code: "rollout_failed", Message: "The image change did not complete."}
		}
		return out
	}
	ops = append(ops, Operation{ID: OpSetImage, Summary: "把一个 Deployment 的一个容器换成另一个镜像，并盯滚动到结束：新镜像起不来（Deployment 自己判定超过进展期限，或进展停了超过期限 30 s）就自动换回原镜像。回执先写进 Redis 再改镜像，立即返回 receipt_id；改的是本服务自己的 Deployment 时，被替换的进程交出回执，由其他进程接着盯。用 workload.rollout 读结果。能改哪些 Deployment 由通道 ServiceAccount 的集群级角色决定（与 workload.restart 同一个 patch 权限，用户裁定的集群范围管理）；apiserver 拒绝时返回 rollout_forbidden。每次调用和每次自动回退都留审计记录。",
		Effect: "exec", EvidenceScope: "sre_manage", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: rolloutAvailable, Limits: rolloutLimits,
		Fields:   map[string]Field{"namespace": text, "name": text, "uid": text, "container": text, "image": {Type: "string", MinLength: 1, MaxLength: rollout.MaxImageBytes}},
		Required: []string{"name", "uid", "container", "image"}, OutputSchema: SchemaOf(rollout.Receipt{}),
		Run: func(ctx context.Context, p Params) Outcome {
			started := now()
			caller := CallerFrom(ctx)
			namespace := p.String("namespace")
			if namespace == "" && pods != nil {
				namespace = pods.DefaultNamespace()
			}
			v, err := rollouts.Start(ctx, rollout.Request{Namespace: namespace, Name: p.String("name"), UID: p.String("uid"), Container: p.String("container"),
				Image: p.String("image"), SessionID: caller.SessionID, RequestID: caller.RequestID})
			audit(ctx, started, SREAction{Op: OpSetImage, Namespace: namespace, Kind: "Deployment", Name: p.String("name"), UID: p.String("uid"),
				Detail: "receipt " + v.ID + " container " + p.String("container") + ": " + v.Previous + " -> " + p.String("image") + " " + v.State}, err)
			out := wrapRollout(v, err)
			if v.ID != "" {
				out.Next = append(out.Next, Call{Operation: OpRollout, Params: Params{"receipt_id": v.ID}, Reason: "读这次换镜像的滚动进度与结果。"})
			}
			return out
		}}, Operation{ID: OpRollout, Summary: "按 receipt_id，或按 namespace+name 取这个 Deployment 最近一张回执：原镜像、目标镜像、状态（pending/rolling_out/reverting 进行中；unchanged/not_applied/complete/reverted/revert_failed/superseded/target_changed 结束），以及 Deployment 现在的镜像、副本进度、是否暂停和由哪个进程在盯；进行中却没人持有时 lease_expired 为真。",
		EvidenceScope: "sre_manage", Availability: rolloutAvailable, Limits: rolloutLimits,
		Fields: map[string]Field{"receipt_id": text, "namespace": text, "name": text}, OutputSchema: SchemaOf(rollout.View{}),
		Run: func(ctx context.Context, p Params) Outcome {
			namespace := p.String("namespace")
			if namespace == "" && pods != nil {
				namespace = pods.DefaultNamespace()
			}
			if p.String("receipt_id") == "" && p.String("name") == "" {
				return Outcome{Error: &Failure{Code: "invalid_input", Message: "receipt_id, or the deployment's name, is required"}}
			}
			v, err := rollouts.Read(ctx, p.String("receipt_id"), namespace, p.String("name"))
			return wrapRollout(v, err)
		}})
	queryAvailable := func() Availability {
		if queries == nil || len(queries.Egresses()) == 0 {
			return Availability{Reason: "uq_provider_unconfigured: no UQ endpoint is configured (phase_two.access.uq_endpoint) and no sre.uq.egresses are registered"}
		}
		return Availability{Available: true}
	}
	uqLimits := map[string]any{"wire_contract": "structured_query_ts_v1", "max_timeout_ms": maxTimeout, "max_window_seconds": int64(uq.MaxWindow / time.Second), "max_native_request_bytes": uq.MaxBodyBytes, "max_native_response_bytes": uq.MaxResponseBytes, "pagination": "explicit_native_cursor_or_offset_in_body; one_page_per_invocation", "authority": "deployment_operator_readonly; page_user_authorization_is_separate", "redaction_contract": redact.Contract, "log_content": "status/count/digest only before SaaS topic masking; content and cursors omitted"}
	wrapQuery := func(v uq.Receipt, err error) Outcome {
		if v.Native != nil {
			v.Native = scrubber.JSON(v.Native)
		}
		v.TraceID = scrubber.Text(v.TraceID)
		if v.TracePage != nil {
			v.TracePage.RequestedID = scrubber.Text(v.TracePage.RequestedID)
		}
		out := Outcome{Value: v, Complete: v.Complete, Limitations: v.Limitations}
		if err != nil {
			var named *uq.Error
			if errors.As(err, &named) {
				out.Error = &Failure{Code: named.Code, Message: named.Message}
			} else {
				out.Error = &Failure{Code: "uq_provider_failed", Message: "Native query did not complete."}
			}
		}
		return out
	}
	ops = append(ops, Operation{ID: "uq.egresses", Summary: "读取部署登记的 UQ 出口、端点、tenant/space 范围与时间单位合同。", EvidenceScope: "sre_uq_egresses", Availability: queryAvailable, Fields: map[string]Field{}, Limits: uqLimits, OutputSchema: SchemaOf([]uq.Egress{}), Run: func(context.Context, Params) Outcome { return Outcome{Value: queries.Egresses(), Complete: true} }}, Operation{
		ID: "uq.query", Summary: "按登记出口和端点转发原生 UQ JSON 查询，保留实际出口、scope、下游状态与响应。", EvidenceScope: "sre_uq_native_query", ExecutionPool: "sre", ExecutionTimeout: 35 * time.Second, Availability: queryAvailable, Limits: uqLimits,
		Fields: map[string]Field{"egress": text, "endpoint": {Type: "string", Source: "uq.egresses endpoints[].path", MinLength: 1, MaxLength: 256}, "tenant": text, "space": text, "body": {Type: "object", AdditionalProperties: true}, "timeout_ms": {Type: "integer", Minimum: &nonnegative, Maximum: &maxTimeout}}, Required: []string{"egress", "endpoint", "tenant", "space", "body"}, OutputSchema: SchemaOf(uq.Receipt{}),
		Run: func(ctx context.Context, p Params) Outcome {
			body, _ := p["body"].(map[string]any)
			v, err := queries.Query(ctx, uq.Request{Egress: p.String("egress"), Endpoint: p.String("endpoint"), Tenant: p.String("tenant"), Space: p.String("space"), Body: body, TimeoutMS: int64(p.Int("timeout_ms", 0))})
			return wrapQuery(v, err)
		},
	})
	traceAvailable := func() Availability {
		if queries != nil {
			for _, egress := range queries.Egresses() {
				if len(egress.TraceTargets) != 0 {
					return Availability{Available: true}
				}
			}
		}
		return Availability{Reason: "uq_trace_unconfigured: register exact sre.uq.egresses trace_targets from the application datasource mapping"}
	}
	traceLimits := map[string]any{"wire_contract": "bkapm_raw_span_v1", "max_timeout_ms": maxTimeout, "max_window_seconds": int64(uq.MaxWindow / time.Second), "max_native_request_bytes": uq.MaxBodyBytes, "max_native_response_bytes": uq.MaxResponseBytes, "max_rows": uq.MaxTraceRows, "window_time_unit": "s", "span_time_unit": "us", "pagination": "explicit_native_offset; one_page_per_invocation", "authority": "deployment_operator_readonly; page_user_authorization_is_separate", "redaction_contract": redact.Contract, "scope": "registered_application_and_exact_table; no cross-application or retention expansion"}
	ops = append(ops, Operation{ID: "uq.trace", Summary: "按登记应用与实际结果表转发原生 bkapm Span search/trace_id 查询，保留原始 Span、微秒时间和单页完整性。", EvidenceScope: "sre_uq_native_trace", ExecutionPool: "sre", ExecutionTimeout: 35 * time.Second, Availability: traceAvailable, Limits: traceLimits,
		Fields: map[string]Field{"egress": text, "target": {Type: "string", Source: "uq.egresses trace_targets[].name", MinLength: 1, MaxLength: 256}, "body": {Type: "object", AdditionalProperties: true}, "timeout_ms": {Type: "integer", Minimum: &nonnegative, Maximum: &maxTimeout}}, Required: []string{"egress", "target", "body"}, OutputSchema: SchemaOf(uq.Receipt{}),
		Run: func(ctx context.Context, p Params) Outcome {
			body, _ := p["body"].(map[string]any)
			v, err := queries.Trace(ctx, uq.TraceRequest{Egress: p.String("egress"), Target: p.String("target"), Body: body, TimeoutMS: int64(p.Int("timeout_ms", 0))})
			return wrapQuery(v, err)
		},
	})
	receiptsAvailable := func() Availability {
		if options.Receipts == nil {
			reason := options.ReceiptsReason
			if reason == "" {
				reason = "receipts_unavailable: this channel keeps no shared receipt store"
			}
			return Availability{Reason: reason}
		}
		return Availability{Available: true}
	}
	timestamp := Field{Type: "string", MinLength: 1, MaxLength: 64, Description: "RFC 3339 time"}
	maxReceiptsPage, maxReceiptsOffset := int64(maxSREReceiptsPage), int64(MaxSREReceipts)
	ops = append(ops, Operation{ID: OpSREReceipts, Summary: "读取 SRE 动作（执行、管理、改镜像与回退）的回执，最新的在前；可按时间、命名空间、对象名与 request_id 过滤，带分页。",
		EvidenceScope: "sre_receipts", ExecutionPool: "sre", ExecutionTimeout: 10 * time.Second, Availability: receiptsAvailable,
		Limits: map[string]any{"kept": MaxSREReceipts, "retention_seconds": int64(SREReceiptRetention / time.Second), "max_page": maxSREReceiptsPage,
			"content": "who, what, where and how each action ended; a pod.exec receipt carries its run's facts and its script digest, never its argv, stdin or output"},
		Fields: map[string]Field{"since": timestamp, "until": timestamp, "namespace": text, "name": text, "request_id": text,
			"limit": {Type: "integer", Minimum: &positive, Maximum: &maxReceiptsPage}, "offset": {Type: "integer", Minimum: &nonnegative, Maximum: &maxReceiptsOffset}},
		OutputSchema: SchemaOf(SREReceiptPage{}),
		Run: func(ctx context.Context, p Params) Outcome {
			filter := SREReceiptFilter{Namespace: p.String("namespace"), Name: p.String("name"), RequestID: p.String("request_id"), Offset: p.Int("offset", 0), Limit: p.Int("limit", 0)}
			for key, into := range map[string]*time.Time{"since": &filter.Since, "until": &filter.Until} {
				if value := p.String(key); value != "" {
					at, err := time.Parse(time.RFC3339Nano, value)
					if err != nil {
						return Outcome{Error: &Failure{Code: "sre_receipts_invalid_time", Message: key + " is not an RFC 3339 time"}}
					}
					*into = at
				}
			}
			page, err := options.Receipts.List(ctx, filter)
			if err != nil {
				return Outcome{Error: &Failure{Code: "sre_receipts_unreadable", Message: "the receipt store did not answer"}}
			}
			out := Outcome{Value: page, Complete: page.NextOffset == 0}
			if page.NextOffset > 0 {
				next := Params{"offset": page.NextOffset}
				for _, key := range []string{"since", "until", "namespace", "name", "request_id", "limit"} {
					if value, present := p[key]; present {
						next[key] = value
					}
				}
				out.Next = []Call{{Operation: OpSREReceipts, Params: next, Reason: "读取同一过滤条件的下一页。"}}
			}
			return out
		},
	})
	return ops
}

func podTarget(p Params) pod.Target {
	value, _ := json.Marshal(p["target"])
	var target pod.Target
	_ = json.Unmarshal(value, &target)
	return target
}
