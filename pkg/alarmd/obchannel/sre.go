// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/node"
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
	Op          string `json:"op"`
	Namespace   string `json:"namespace"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	UID         string `json:"uid,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Result      string `json:"result"`
	Code        string `json:"code,omitempty"`
	DurationMS  int64  `json:"duration_ms"`
	SessionID   string `json:"session_id,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
	ExecutionID string `json:"execution_id,omitempty"`
	// Operator is the admin user the session was issued to; empty with
	// IssuedVia admin_key, the deployment administrator. Both empty on a
	// receipt from before 0.4.
	Operator  string `json:"operator,omitempty"`
	IssuedVia string `json:"issued_via,omitempty"`
	// Entry is the caller's entry when it was not the session's own
	// (Caller.Entry).
	Entry string `json:"entry,omitempty"`
	// At is when the action ended.
	At time.Time `json:"at"`
	// Effect is the list the receipt was kept in, change, exec or access;
	// set on read, not stored.
	Effect string `json:"effect,omitempty"`
	// DryRun marks an action that made every check and wrote nothing
	// (configmap.edit_text with dry_run): it is not a change.
	DryRun bool `json:"dry_run,omitempty"`
	// Exec is what a pod.exec run said about itself, without its output.
	Exec *SREExec `json:"exec,omitempty"`
	// Argv is an execution's command line, redacted and capped
	// (pod.AuditArgv), for the sre_action line only: the stored receipt keeps
	// the script's digest and never the command line.
	Argv string `json:"-"`
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
	SREActionOps     = []string{OpNodeExec, OpNodeGet, OpNodeCancel, OpNodeEnded, "pod.exec", pod.OpDeletePod, pod.OpRestartWorkload, pod.OpScaleWorkload, pod.OpReadSecret, pod.OpReadConfigMap, OpSetImage, OpRevert, pod.OpListResources, pod.OpDryRun, pod.OpApply, pod.OpDelete, pod.OpPatch, pod.OpEditConfigMapText}
	SREActionResults = []string{SREResultDone, SREResultTargetChanged, SREResultForbidden, SREResultRefused, SREResultFailed, SREResultUnknown}
)

// sreResult is the result an action's error ends it in.
func sreResult(err error) (string, string) {
	if err == nil {
		return SREResultDone, ""
	}
	var nodeError *node.Error
	if errors.As(err, &nodeError) {
		switch nodeError.Code {
		case "target_changed":
			return SREResultTargetChanged, nodeError.Code
		case "forbidden":
			return SREResultForbidden, nodeError.Code
		case "unknown":
			return SREResultUnknown, nodeError.Code
		case "invalid_request", "invalid_params", "invalid_execution_id", "execution_expired", "execution_conflict":
			return SREResultRefused, nodeError.Code
		}
		return SREResultFailed, nodeError.Code
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
	case pod.CodeInvalidRequest, pod.CodeNotInScope, pod.CodeTargetNotRunning, pod.CodeCommandStartFailed, pod.CodeTimeoutUnsupported, pod.CodeNotFound, pod.CodeKindNotInstalled:
		return SREResultRefused, named.Code
	}
	return SREResultFailed, named.Code
}

// Caller is who asked for an operation: the admitted session and the
// request. The channel puts it on the operation's context.
type Caller struct {
	SessionID string
	RequestID string
	// Operator and IssuedVia are the session's: who it was issued to and
	// through which route (cliauth.IssuedViaFront or IssuedViaAdminKey).
	Operator  string
	IssuedVia string
	// Entry is the address the request came in on when it was not the one
	// the session was issued through: EntryAlarmd, the deployment's own
	// address, which the CLI falls back to when the admin cannot reach
	// alarmd. Empty is the session's entry.
	Entry string
}

// EntryHeader carries the entry a CLI request took (Caller.Entry). The admin
// forwards a fixed list of headers and this is not on it, so it is never
// seen on a request that came through the admin.
const EntryHeader = "X-Alarmd-Entry"

// EntryAlarmd is the one entry a request names: alarmd's own address.
const EntryAlarmd = "alarmd"

// EntryOf is the entry r names, or empty for any other value.
func EntryOf(r *http.Request) string {
	if r.Header.Get(EntryHeader) == EntryAlarmd {
		return EntryAlarmd
	}
	return ""
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
	Nodes      NodeExecutions
	NodeReason string
	Pods       *pod.Provider
	Queries    *uq.Provider
	Scrubber   *redact.Scrubber
	PodReason  string
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
		action.Operator, action.IssuedVia, action.Entry = caller.Operator, caller.IssuedVia, caller.Entry
		options.Audit(action)
	}
	text := Field{Type: "string", MinLength: 1, MaxLength: 256}
	nonnegative, positive := int64(0), int64(1)
	maxTimeout, maxOutput, maxTail, maxSince, maxLimit, maxReplicas := pod.MaxExecTimeout.Milliseconds(), int64(MaxResponseBytes()), int64(100000), int64(7*86400), int64(pod.MaxPodsPerPage), int64(pod.MaxReplicas)
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
	podLimits := map[string]any{"max_timeout_ms": maxTimeout, "max_output_bytes": maxOutput, "max_stdin_bytes": pod.MaxStdinBytes, "max_targets_per_page": pod.MaxPodsPerPage,
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
		{ID: pod.OpListResources, Summary: "列出或读取一种 Kubernetes 资源（Node、Namespace、Pod、Service、Endpoints、EndpointSlice、Event、Deployment、StatefulSet、DaemonSet、ReplicaSet、Job、CronJob、Ingress、HorizontalPodAutoscaler、ConfigMap、Secret、ServiceAccount、PersistentVolumeClaim、Role、RoleBinding、ClusterRole、ClusterRoleBinding，ValidatingWebhookConfiguration、MutatingWebhookConfiguration（集群级，只给元数据与各 webhook 的规则、选择器、失败策略与调用目标，不给 caBundle），以及自定义资源 ServiceMonitor、PodMonitor、BkLogConfig、DataID（集群级，bk-monitor-operator 的数据落在哪个业务））：不填 namespace 时命名空间级的种类按整个集群列；name 读一个对象；label_selector、field_selector 原样交给 apiserver（如 spec.nodeName=节点名 按节点列 Pod，app.kubernetes.io/managed-by=Helm 列 Helm 管理的对象）；paths 只取给出的点分路径。每个对象都带 Helm 的归属标签与注解（meta.helm.sh/release-name、release-namespace），投影时也带，按它们可列出一个 release 拥有什么。Secret 只给元数据和每个键的字节数与指纹（部署内密钥的 HMAC，16 位十六进制，能比较是否同值、是否变过，不能还原），从不给值；ConfigMap 只给元数据和每个键的字节数，值用 configmap.get 读。其他种类去掉 managedFields 与 last-applied 注解，凭据名的注解整值遮蔽，凭据名的 env 值遮蔽并附指纹，所有字符串经过脱敏；对 Secret 的引用（secretName、secretKeyRef、envFrom、imagePullSecrets）原样保留。集群没装的自定义资源答 kind_not_installed。列 Secret 每次都留审计记录。",
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
				started := now()
				v, err := pods.ListResources(ctx, request)
				if v.Kind == "Secret" {
					audit(ctx, started, SREAction{Op: pod.OpListResources, Namespace: v.Namespace, Kind: "Secret", Name: secretNames(v), Detail: fmt.Sprintf("secrets %d, no values", v.Count)}, err)
				}
				out := wrapPod(v, err, err == nil && v.Continue == "" && !v.Truncated && len(v.OversizedItems) == 0)
				out.Limitations = []string{"A list across the cluster is paged: a cluster-wide Pod list can take several pages at the output bound, so follow continue until it is empty.",
					"A Secret comes back as metadata and each key's size and fingerprint, never a value; a ConfigMap as metadata and each key's size, its values through configmap.get."}
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
				out.Summary = listSummary(v)
				if len(v.OversizedItems) > 0 {
					out.Limitations = append(out.Limitations, "Some objects were over the output bound alone and came back as their identity only (oversized_items); read them by name with paths.")
				}
				return out
			}},
		{ID: pod.OpDryRun, Summary: "把一组清单（YAML 文档或 JSON 对象，如 chart 渲染出的）逐个以服务端 apply、dryRun=All 交给 apiserver：准入 webhook、schema、配额与冲突都照常检查，什么都不落库。每个对象答 would_create / would_update / unchanged / denied（webhook、校验、配额、无权限）/ manager_conflict（字段归属冲突，不是拒绝）/ not_checked（集群不认这个种类、没有名字或命名空间、没拿到应答）/ rbac_not_dry_run（Role、ClusterRole 与绑定不送：apiserver 即使 dry run 也做 RBAC 提权检查，通过它要 bind 与 escalate，通道不持有），拒绝时原样带 apiserver 的 reason、code、message 与 causes（Secret 自己的值被遮蔽）；would_update 带 changed_paths：apply 前后逐层比较出的每个变化字段路径（映射按键、列表按下标，只在一侧有的键与长度变化的列表各算一条；managedFields、resourceVersion、generation 与 status 不算），Secret 只给键名（data.KEY），值在本进程内比较、从不回传；服务端 apply 不删其它字段管理者写入而清单里没有的字段，这类回退看不见。clean 为真表示没有 denied、manager_conflict 与 not_checked。dryRun=All 写死在代码里，没有参数能关掉。命名空间级对象没写命名空间时用 namespace 参数；不给就不检查。每次调用都留审计记录，写明对象清单。", EvidenceScope: "sre_dry_run", ExecutionPool: "sre", ExecutionTimeout: MaxExecutionTimeout, Availability: podAvailable, RequestMaxBytes: EnvelopeMaxBytes,
			Limits:   map[string]any{"max_objects": pod.MaxDryRunObjects, "max_manifest_bytes": pod.MaxDryRunBytes, "dry_run": "All (fixed)", "field_manager": "alarmd-dry-run", "force": true},
			Fields:   map[string]Field{"manifests": {Type: "string", MinLength: 1, MaxLength: pod.MaxDryRunBytes, Source: "helm template 的输出或任意清单"}, "namespace": text},
			Required: []string{"manifests"}, OutputSchema: SchemaOf(pod.DryRunReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				v, err := pods.DryRun(ctx, pod.DryRunRequest{Manifests: p.String("manifests"), Namespace: p.String("namespace")})
				audit(ctx, started, SREAction{Op: pod.OpDryRun, Namespace: p.String("namespace"), Kind: "objects", Name: dryRunNames(v), Detail: dryRunCounts(v)}, err)
				out := wrapPod(v, err, err == nil && v.ResumeFrom == nil)
				if err == nil && !v.Clean {
					out.Summary = "dry run not clean: " + dryRunCounts(v)
				}
				if err == nil && v.ResumeFrom != nil {
					out.Limitations = append(out.Limitations, fmt.Sprintf("The operation's time ran out between objects: objects from index %d were not checked (not_attempted); the verdicts above stand. Dry-run the rest by sending the manifests from that object on.", *v.ResumeFrom))
				}
				return out
			}},
		{ID: pod.OpApply, Summary: "把一组清单（YAML 文档或 JSON 对象，至多 1000 个）逐个以服务端 apply 写入集群（字段管理者 alarmd-sre，真写，不是 dry run）：每个对象答 created / updated / unchanged / conflict（字段归属冲突，force=true 接管这些字段）/ denied（准入、校验、配额、无权限，原样带 apiserver 的话）/ not_checked / not_granted_kind（chart 授权之外的种类）/ rbac_not_writable（Role、ClusterRole 与绑定不写），并给出改前改后的字段差异（按 k8s.list 的遮蔽显示，Secret 只显示键与指纹，从不显示值）；对象带 meta.helm.sh/release-name 时写明 helm_release：下一次 helm upgrade 会改回或冲突。命名空间级对象没写命名空间时用 namespace 参数。注意：本环境 CLI 到 alarmd 若是明文 HTTP，Secret 的值会以明文经过集群网络。每次调用都留审计记录，写明对象清单。", Effect: "exec", EvidenceScope: "sre_manage", ExecutionPool: "exec", ExecutionTimeout: MaxExecutionTimeout, Availability: podAvailable, RequestMaxBytes: EnvelopeMaxBytes,
			Limits:   map[string]any{"max_objects": pod.MaxApplyObjects, "max_manifest_bytes": pod.MaxDryRunBytes, "field_manager": "alarmd-sre"},
			Fields:   map[string]Field{"manifests": {Type: "string", MinLength: 1, MaxLength: pod.MaxDryRunBytes, Source: "要写入的清单；Secret 从本地文件经 --input @文件 传入"}, "namespace": text, "force": {Type: "boolean"}},
			Required: []string{"manifests"}, OutputSchema: SchemaOf(pod.WriteReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				force, _ := p["force"].(bool)
				v, err := pods.Apply(ctx, pod.ApplyRequest{Manifests: p.String("manifests"), Namespace: p.String("namespace"), Force: force})
				audit(ctx, started, SREAction{Op: pod.OpApply, Namespace: p.String("namespace"), Kind: "objects", Name: writeNames(v), Detail: writeDetail(v)}, err)
				out := wrapPod(v, err, err == nil && v.ResumeFrom == nil)
				if err == nil && v.ResumeFrom != nil {
					out.Limitations = append(out.Limitations, fmt.Sprintf("The operation's time ran out between objects: objects from index %d were not applied (not_attempted, nothing sent). Applying the same manifests again is safe: objects already applied answer unchanged.", *v.ResumeFrom))
				}
				return out
			}},
		{ID: pod.OpDelete, Summary: "删除一个对象：按 api_version、kind、namespace、name 与读到的 uid（必填，来自先前的 k8s.list），给了 resource_version 时也须仍是它；依赖对象后台删除。答 deleted / gone（已不存在）/ precondition_failed（已不是读到的那个）/ denied / not_granted_kind / rbac_not_writable / protected_namespace（kube-system、kube-public、kube-node-lease、default 与本进程所在命名空间不删）；对象带 helm 归属时写明 helm_release。每次调用都留审计记录。", Effect: "exec", EvidenceScope: "sre_manage", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: podAvailable,
			Fields:   map[string]Field{"api_version": text, "kind": text, "namespace": text, "name": text, "uid": {Type: "string", MinLength: 1, MaxLength: 128, Source: "k8s.list items[].metadata.uid"}, "resource_version": {Type: "string", MinLength: 1, MaxLength: 64}},
			Required: []string{"api_version", "kind", "name", "uid"}, OutputSchema: SchemaOf(pod.WriteReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				v, err := pods.DeleteObject(ctx, pod.ObjectDeleteRequest{APIVersion: p.String("api_version"), Kind: p.String("kind"), Namespace: p.String("namespace"),
					Name: p.String("name"), UID: p.String("uid"), ResourceVersion: p.String("resource_version")})
				audit(ctx, started, SREAction{Op: pod.OpDelete, Namespace: p.String("namespace"), Kind: p.String("kind"), Name: p.String("name"), UID: p.String("uid"), Detail: writeDetail(v)}, err)
				return wrapPod(v, err, err == nil)
			}},
		{ID: pod.OpPatch, Summary: "对一个对象做 RFC 6902 JSON Patch：按 api_version、kind、namespace、name 与读到的 uid（必填）定位，op 只允许 test / remove / replace / add；服务端在最前面加一条对 uid 的 test，apiserver 整份原子执行，任何一条 test 不符就什么都不改（test_failed，带 apiserver 原话）。按下标删列表元素时先 test 整条元素，同一列表从大到小删；注解键里的斜杠写成 ~1。答 updated / unchanged / test_failed / precondition_failed（uid 已不是读到的那个，补丁不发）/ gone / denied / not_granted_kind / rbac_not_writable，带改前改后字段差异（按 k8s.list 遮蔽，Secret 只显示键与指纹）与 will_roll（Pod 模板变了，工作负载会滚动）；对象带 helm 归属时写明 helm_release。用来删 helm 不拥有的手工字段（服务端 apply 删不掉其它管理者的字段）。每次调用都留审计记录，写明操作数与路径，不写值。", Effect: "exec", EvidenceScope: "sre_manage", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, RequestMaxBytes: EnvelopeMaxBytes,
			Fields: map[string]Field{"api_version": text, "kind": text, "namespace": text, "name": text, "uid": {Type: "string", MinLength: 1, MaxLength: 128, Source: "k8s.list items[].metadata.uid"},
				"patch": {Type: "array", MinItems: 1, Items: &Field{Type: "object", Properties: map[string]Field{"op": {Type: "string", Enum: []string{"test", "remove", "replace", "add"}},
					"path": {Type: "string", MinLength: 1, MaxLength: 4096}}, Required: []string{"op", "path"}, AdditionalProperties: true}}},
			Required: []string{"api_version", "kind", "name", "uid", "patch"}, OutputSchema: SchemaOf(pod.WriteReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				var ops []pod.PatchOp
				encoded, err := json.Marshal(p["patch"])
				if err == nil {
					err = json.Unmarshal(encoded, &ops)
				}
				if err != nil {
					return Outcome{Error: &Failure{Code: "invalid_request", Message: "patch must be an array of RFC 6902 operations"}}
				}
				v, err := pods.PatchObject(ctx, pod.ObjectPatchRequest{APIVersion: p.String("api_version"), Kind: p.String("kind"), Namespace: p.String("namespace"),
					Name: p.String("name"), UID: p.String("uid"), Patch: ops})
				audit(ctx, started, SREAction{Op: pod.OpPatch, Namespace: p.String("namespace"), Kind: p.String("kind"), Name: p.String("name"), UID: p.String("uid"),
					Detail: writeDetail(v) + "; " + patchDetail(ops)}, err)
				return wrapPod(v, err, err == nil)
			}},
		{ID: "pod.get", Summary: "读取一个 Pod 的容器身份、运行状态与 owner 链；首次可省略 UID，后续使用返回身份。", EvidenceScope: "sre_pod_target", ExecutionPool: "sre", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits, Fields: map[string]Field{"target": inspectTarget}, Required: []string{"target"}, OutputSchema: SchemaOf(pod.Observation{}),
			Run: func(ctx context.Context, p Params) Outcome {
				v, err := pods.Inspect(ctx, pod.TargetRequest{Target: podTarget(p)})
				return wrapPod(v, err, err == nil)
			}},
		{ID: "pod.logs", Summary: "读取指定 Pod/container 身份的有界日志，执行前后复核容器变化；init 容器同样可读（pod.discover 的结果里标 init: true），previous 读它上一次运行的日志。contains、since_time、until_time 在服务端逐行过滤，只回传匹配的行，并给出扫描与匹配行数；tail_lines 最多 100000 行，回看最多 7 天。", EvidenceScope: "sre_pod_logs", ExecutionPool: "sre", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits, Fields: map[string]Field{"target": target, "tail_lines": {Type: "integer", Minimum: &positive, Maximum: &maxTail}, "since_seconds": {Type: "integer", Minimum: &positive, Maximum: &maxSince}, "previous": {Type: "boolean"}, "output_bytes": {Type: "integer", Minimum: &positive, Maximum: &maxOutput},
			"contains":   {Type: "string", MinLength: 1, MaxLength: 1024, Description: "keep only lines holding this text, scanned server-side"},
			"since_time": {Type: "string", MaxLength: 64, Description: "RFC 3339; lines written at or after it"},
			"until_time": {Type: "string", MaxLength: 64, Description: "RFC 3339; lines written at or before it"}}, Required: []string{"target"}, OutputSchema: SchemaOf(pod.LogsReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				request := pod.LogsRequest{Target: podTarget(p), TailLines: int64(p.Int("tail_lines", 0)), SinceSeconds: int64(p.Int("since_seconds", 0)), Previous: p.Bool("previous"), OutputBytes: int64(p.Int("output_bytes", 0)), Contains: p.String("contains")}
				for key, into := range map[string]*time.Time{"since_time": &request.SinceTime, "until_time": &request.UntilTime} {
					if value := p.String(key); value != "" {
						at, err := time.Parse(time.RFC3339Nano, value)
						if err != nil {
							return Outcome{Error: &Failure{Code: "invalid_input", Message: key + " is not an RFC 3339 time"}}
						}
						*into = at
					}
				}
				v, err := pods.Logs(ctx, request)
				out := wrapPod(v, err, err == nil && !v.Truncated && !v.TargetChanged && v.EvidenceScope == "identity_verified_before_and_after")
				if v.ObservedAfter == nil {
					out.Limitations = append(out.Limitations, "Post-read target identity verification did not complete.")
				}
				return out
			}},
		{ID: "pod.exec", Summary: "在目标容器运行非 TTY argv/stdin，返回远端执行回执；请求不会自动重试，每次调用都留审计记录。", Effect: "exec", EvidenceScope: "sre_pod_exec", ExecutionPool: "exec", ExecutionTimeout: MaxExecutionTimeout, Availability: podAvailable, Limits: podLimits,
			Fields: map[string]Field{"target": target, "argv": {Type: "array", MinItems: 1, MaxItems: 128, Items: &Field{Type: "string", MaxLength: 32768}}, "stdin": {Type: "string", Source: "stdin", MaxLength: pod.MaxStdinBytes}, "timeout_ms": {Type: "integer", Minimum: &positive, Maximum: &maxTimeout}, "output_bytes": {Type: "integer", Minimum: &positive, Maximum: &maxOutput}}, Required: []string{"target", "argv"}, OutputSchema: SchemaOf(pod.ExecReceipt{}),
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
				v, err := pods.Exec(ctx, pod.ExecRequest{RequestID: CallerFrom(ctx).RequestID, Target: target, Argv: argv, Stdin: stdin, TimeoutMS: int64(p.Int("timeout_ms", 0)), OutputBytes: int64(p.Int("output_bytes", 0))})
				audit(ctx, started, SREAction{Op: "pod.exec", Namespace: target.Namespace, Kind: "Pod", Name: target.Pod + "/" + target.Container, UID: target.UID, Argv: pods.AuditArgv(argv),
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
		{ID: pod.OpReadSecret, Summary: "读一个 Secret（任意命名空间，不指定时是本进程所在的命名空间）：返回类型、标签、注解（去掉 last-applied，凭据名的整值遮蔽）、属主引用、创建时间，以及每个键的字节数与指纹（部署内密钥的 HMAC，16 位十六进制；同值同指纹，能比较是否同值、是否变过，不能还原，也不能拿别处算的常见值表去对），从不返回值。fingerprint_scope 为 process 时指纹只在应答进程内可比。每次调用都留审计记录。", Effect: "exec", EvidenceScope: "sre_secret", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits,
			Fields: map[string]Field{"namespace": text, "name": text}, Required: []string{"name"}, OutputSchema: SchemaOf(pod.SecretReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				v, err := pods.ReadSecret(ctx, pod.SecretRequest{Namespace: p.String("namespace"), Name: p.String("name")})
				audit(ctx, started, SREAction{Op: pod.OpReadSecret, Namespace: v.Namespace, Kind: "Secret", Name: v.Name, UID: v.UID, Detail: fmt.Sprintf("keys %d, no values", len(v.Keys))}, err)
				return wrapPod(v, err, err == nil && !v.Truncated)
			}},
		{ID: pod.OpReadConfigMap, Summary: "读一个 ConfigMap（任意命名空间，不指定时是本进程所在的命名空间）：每个键给出值的字节数与指纹；值照原样显示，只遮蔽名字或形状像凭据的部分（value_contract=config_values_blacklist_v1）——YAML/JSON 文件逐个叶子判、其他文本（含注释）逐行判：键名或行里的词像凭据（password、secret、token、key、iv、crypt、sign、cert、salt 等）的，其值或其后的内容整段遮蔽；形状像凭据的片段不论键名就地遮蔽（PEM、JWT、16 字节以上像密钥的 base64/hex、URL 里的 user:pass、Authorization/Bearer 值、已知前缀的令牌），遮蔽处写成 <redacted bytes=N fingerprint=F>，masked 是遮蔽的个数；其余（主机、地址、端口、路径、Corefile、hosts 文件等）照原样显示。每次调用都留审计记录，只记键数。", Effect: "exec", EvidenceScope: "sre_configmap", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits,
			Fields: map[string]Field{"namespace": text, "name": text}, Required: []string{"name"}, OutputSchema: SchemaOf(pod.ConfigMapReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				v, err := pods.ReadConfigMap(ctx, pod.ConfigMapRequest{Namespace: p.String("namespace"), Name: p.String("name")})
				audit(ctx, started, SREAction{Op: pod.OpReadConfigMap, Namespace: v.Namespace, Kind: "ConfigMap", Name: v.Name, UID: v.UID, Detail: fmt.Sprintf("keys %d", len(v.Keys))}, err)
				return wrapPod(v, err, err == nil && !v.Truncated)
			}},
		{ID: pod.OpEditConfigMapText, Summary: "在一个 ConfigMap 键里把一段 configmap.get 显示出来的文字换成新文字，用来改整份配置文件（config.yaml、environment.yaml）里的地址这类普通值：键整份读回来是遮蔽的，没法整份写回。" +
			"按 namespace、name、key 定位，expected_fingerprint 填 configmap.get 给这个键的指纹（键变过或换了指纹密钥就拒绝）。old_text 必须在这个键的显示结果里恰好出现一次，" +
			"并且只在显示出来的地方改：落在遮蔽值里、跨遮蔽值、落在注释里的都不算，所以不能拿来试探遮蔽内容；new_text 改完后也必须照常显示，形如密钥或落到凭据名下会被遮蔽的拒绝（密钥走 Secret）。" +
			"old_text、new_text 各一行。服务端用一次 JSON Patch 写入，同时 test uid、resourceVersion 与键的原值，apiserver 整份原子执行，期间有人改过就什么都不改（test_failed）。" +
			"回执只给出现次数、改动所在行号、改前改后指纹和改后这一行的显示结果，从不给值；dry_run 只做全部检查不写。拒绝时的错误码（与其它 Pod 类操作同样带 pod_ 前缀）：pod_key_not_found、pod_fingerprint_mismatch、" +
			"pod_new_text_not_plain、pod_old_text_not_shown_once、pod_edit_not_shown_exactly。每次调用都留审计记录，写明键、行号与结果，不写值。", Effect: "exec", EvidenceScope: "sre_manage", ExecutionPool: "exec", ExecutionTimeout: 30 * time.Second, Availability: podAvailable, Limits: podLimits,
			Fields: map[string]Field{"namespace": text, "name": text, "key": text,
				"old_text":             {Type: "string", MinLength: 1, MaxLength: 4096, Description: "one line of text configmap.get shows exactly once in the key"},
				"new_text":             {Type: "string", MaxLength: 4096, Description: "its replacement, one line, which must itself be shown in clear"},
				"expected_fingerprint": {Type: "string", MinLength: 1, MaxLength: 64, Source: "configmap.get keys[].fingerprint"},
				"dry_run":              {Type: "boolean", Description: "make every check and write nothing"}},
			Required: []string{"name", "key", "old_text", "new_text", "expected_fingerprint"}, OutputSchema: SchemaOf(pod.ConfigMapEditReceipt{}),
			Run: func(ctx context.Context, p Params) Outcome {
				started := now()
				v, err := pods.EditConfigMapText(ctx, pod.ConfigMapEditRequest{Namespace: p.String("namespace"), Name: p.String("name"), Key: p.String("key"),
					OldText: p.String("old_text"), NewText: p.String("new_text"), ExpectedFingerprint: p.String("expected_fingerprint"), DryRun: p.Bool("dry_run")})
				audit(ctx, started, SREAction{Op: pod.OpEditConfigMapText, Namespace: v.Namespace, Kind: "ConfigMap", Name: v.Name, UID: v.UID, Detail: editDetail(v), DryRun: p.Bool("dry_run")}, err)
				return wrapPod(v, err, err == nil)
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
				Image: p.String("image"), SessionID: caller.SessionID, RequestID: caller.RequestID, Operator: caller.Operator, IssuedVia: caller.IssuedVia})
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
	maxQueryTimeout := uq.MaxTimeout.Milliseconds()
	maxTracePages := int64(uq.MaxTracePages)
	queryResponseBytes := uq.DefaultResponseBytes
	if queries != nil {
		queryResponseBytes = queries.ResponseBytes()
	}
	uqLimits := map[string]any{"wire_contract": "structured_query_ts_v1", "max_timeout_ms": uq.MaxTimeout.Milliseconds(), "max_window_seconds": int64(uq.MaxWindow / time.Second), "max_native_request_bytes": uq.MaxBodyBytes, "max_native_response_bytes": queryResponseBytes, "pagination": "explicit_native_cursor_or_offset_in_body; one_page_per_invocation", "authority": "deployment_operator_readonly; page_user_authorization_is_separate", "redaction_contract": redact.Contract, "log_content": "status/count/digest only before SaaS topic masking; content and cursors omitted"}
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
		ID: "uq.query", Summary: "按登记出口和端点转发原生 UQ JSON 查询，保留实际出口、scope、下游状态与响应。", EvidenceScope: "sre_uq_native_query", ExecutionPool: "sre", ExecutionTimeout: uq.MaxTimeout + 5*time.Second, Availability: queryAvailable, Limits: uqLimits,
		Fields: map[string]Field{"egress": text, "endpoint": {Type: "string", Source: "uq.egresses endpoints[].path", MinLength: 1, MaxLength: 256}, "tenant": text, "space": text, "body": {Type: "object", AdditionalProperties: true}, "timeout_ms": {Type: "integer", Minimum: &nonnegative, Maximum: &maxQueryTimeout}}, Required: []string{"egress", "endpoint", "tenant", "space", "body"}, OutputSchema: SchemaOf(uq.Receipt{}),
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
	traceLimits := map[string]any{"wire_contract": "bkapm_raw_span_v1", "max_timeout_ms": uq.MaxTimeout.Milliseconds(), "max_window_seconds": int64(uq.MaxWindow / time.Second), "max_native_request_bytes": uq.MaxBodyBytes, "max_native_response_bytes": queryResponseBytes, "max_rows": uq.MaxTraceRows, "window_time_unit": "s", "span_time_unit": "us", "pagination": "explicit_native_offset; one_page_per_invocation", "authority": "deployment_operator_readonly; page_user_authorization_is_separate", "redaction_contract": redact.Contract, "scope": "registered_application_and_exact_table; no cross-application or retention expansion"}
	ops = append(ops, Operation{ID: "uq.trace", Summary: "按登记应用与实际结果表转发原生 bkapm Span search/trace_id 查询，保留原始 Span、微秒时间和单页完整性；pages 一次连续读至多 10 页并合成一份应答。", EvidenceScope: "sre_uq_native_trace", ExecutionPool: "sre", ExecutionTimeout: MaxExecutionTimeout, Availability: traceAvailable, Limits: traceLimits,
		Fields: map[string]Field{"egress": text, "target": {Type: "string", Source: "uq.egresses trace_targets[].name", MinLength: 1, MaxLength: 256}, "body": {Type: "object", AdditionalProperties: true}, "timeout_ms": {Type: "integer", Minimum: &nonnegative, Maximum: &maxQueryTimeout}, "pages": {Type: "integer", Minimum: &positive, Maximum: &maxTracePages, Description: "read this many consecutive pages of the same query as one answer"}}, Required: []string{"egress", "target", "body"}, OutputSchema: SchemaOf(uq.Receipt{}),
		Run: func(ctx context.Context, p Params) Outcome {
			body, _ := p["body"].(map[string]any)
			v, err := queries.TracePages(ctx, uq.TraceRequest{Egress: p.String("egress"), Target: p.String("target"), Body: body, TimeoutMS: int64(p.Int("timeout_ms", 0))}, p.Int("pages", 1))
			return wrapQuery(v, err)
		},
	})
	ops = append(ops, Operation{ID: OpTraceTopFailures, Summary: "列出一个登记 trace 目标里失败最多的服务端接口：按服务、路由、HTTP 状态与业务码分组，给次数、首末次时间、耗时 P50/P95、至多 3 个示例 trace_id 与一条错误原文（脱敏、截断）。失败指 SERVER span 被标失败或 HTTP ≥500（include_4xx 时 ≥400），条件下推给 UQ；至多扫 10 页×1000 条，扫不完时标 sampled，total 到上限 10000 时写 total_at_least。end 可回看 7 天内某个时刻（UTC，以 Z 结尾）。示例 trace_id 用 uq.trace 下钻。", EvidenceScope: "sre_uq_native_trace", ExecutionPool: "sre", ExecutionTimeout: MaxExecutionTimeout, Availability: traceAvailable,
		Limits: map[string]any{"max_pages": uq.TopFailuresPages, "page_rows": uq.MaxTraceRows, "max_groups": uq.TopFailuresGroups, "window": "a duration from 1m to the retention, such as 90m, 6h or 3d", "retention_days": int(uq.TraceRetention / (24 * time.Hour)), "message_bytes": topFailureMessageBytes, "redaction_contract": redact.Contract},
		Fields: map[string]Field{"egress": text, "target": {Type: "string", Source: "uq.egresses trace_targets[].name", MinLength: 1, MaxLength: 256}, "window": {Type: "string", MaxLength: 16},
			"end": {Type: "string", MaxLength: 64, Description: "RFC 3339 UTC"}, "service": {Type: "string", MaxLength: 256}, "include_4xx": {Type: "boolean"}},
		Required: []string{"egress", "target"}, OutputSchema: SchemaOf(uq.TopFailures{}),
		Run: func(ctx context.Context, p Params) Outcome {
			include4xx, _ := p["include_4xx"].(bool)
			v, err := queries.TopFailures(ctx, uq.TopFailuresRequest{Egress: p.String("egress"), Target: p.String("target"), Window: p.String("window"),
				End: p.String("end"), Service: p.String("service"), Include4xx: include4xx})
			for i := range v.Groups {
				v.Groups[i].Message = boundedText(scrubber.Text(v.Groups[i].Message), topFailureMessageBytes)
				for j, id := range v.Groups[i].SampleTraceIDs {
					v.Groups[i].SampleTraceIDs[j] = scrubber.Text(id)
				}
			}
			out := Outcome{Value: v, Complete: err == nil && !v.Sampled, Limitations: v.Limitations}
			if err != nil {
				var named *uq.Error
				if errors.As(err, &named) {
					out.Error = &Failure{Code: named.Code, Message: named.Message}
				} else {
					out.Error = &Failure{Code: "uq_provider_failed", Message: "Native query did not complete."}
				}
			}
			return out
		},
	})
	maxLogTop := int64(uq.LogCountMaxTop)
	ops = append(ops, Operation{ID: OpLogCount, Summary: "按字段统计一个日志结果表在一段时间里的条数，不回传日志正文：一次聚合查询（count_over_time 覆盖整个窗口、按 by 的字段求和），只回每组的字段值（脱敏、截到 256 字节）与条数，按条数降序取前 top 组，total 是全部组的合计。query_string 原样下推用于筛选（Doris 存储按内容查写 log:*关键字*），应答里只回它的长度。不能按正文字段（log、message、content、msg、body 及 *.log、*_log、*.message、*_message、*.body）分组。窗口任意时长，至多 30 天。",
		EvidenceScope: "sre_uq_log_count", ExecutionPool: "sre", ExecutionTimeout: uq.MaxTimeout + 5*time.Second, Availability: queryAvailable,
		Limits: map[string]any{"max_top": uq.LogCountMaxTop, "default_top": uq.LogCountDefaultTop, "value_bytes": uq.LogCountValueBytes, "max_window_seconds": int64(uq.MaxWindow / time.Second), "redaction_contract": redact.Contract, "content": "field values and counts only; no log text"},
		Fields: map[string]Field{"egress": text, "tenant": text, "space": text,
			"table_id":     {Type: "string", MinLength: 1, MaxLength: 256, Description: "<business>_bklog.<name>"},
			"by":           {Type: "array", MinItems: 1, Items: &Field{Type: "string", MinLength: 1, MaxLength: 256}},
			"query_string": {Type: "string", MaxLength: 8192},
			"window":       {Type: "string", MaxLength: 16},
			"end":          {Type: "string", MaxLength: 64, Description: "RFC 3339 UTC"},
			"top":          {Type: "integer", Minimum: &positive, Maximum: &maxLogTop}},
		Required: []string{"egress", "tenant", "space", "table_id", "by"}, OutputSchema: SchemaOf(uq.LogCounts{}),
		Run: func(ctx context.Context, p Params) Outcome {
			var by []string
			if values, ok := p["by"].([]any); ok {
				for _, value := range values {
					by = append(by, value.(string))
				}
			}
			v, err := queries.LogCount(ctx, uq.LogCountRequest{Egress: p.String("egress"), Tenant: p.String("tenant"), Space: p.String("space"),
				TableID: p.String("table_id"), By: by, QueryString: p.String("query_string"), Window: p.String("window"), End: p.String("end"), Top: p.Int("top", 0)})
			for i := range v.Groups {
				for key, value := range v.Groups[i].Values {
					v.Groups[i].Values[key] = boundedText(scrubber.Text(value), uq.LogCountValueBytes)
				}
			}
			out := Outcome{Value: v, Complete: err == nil && !v.Truncated && !v.Partial, Limitations: v.Limitations}
			if err != nil {
				var named *uq.Error
				if errors.As(err, &named) {
					out.Error = &Failure{Code: named.Code, Message: named.Message}
				} else {
					out.Error = &Failure{Code: "uq_provider_failed", Message: "Native query did not complete."}
				}
			}
			return out
		},
	})
	maxSearchLines, maxSearchLineBytes, maxSearchTotalBytes := int64(uq.LogSearchMaxLines), int64(uq.LogSearchMaxLineBytes), int64(uq.LogSearchMaxTotalBytes)
	ops = append(ops, Operation{ID: OpLogSearch, Summary: "读一个日志结果表在 UTC 时间窗里的日志正文，一次调用一页：按 query_string 筛选（原样下推，Doris 存储按内容查写 log:*关键字*），order 为 desc（新的在前，默认）或 asc；每行给时间、少数元数据字段与正文，正文与字段值按脱敏合同逐行脱敏后再截到 line_bytes。行数、单行字节、整页字节与时间窗都有上限，触到哪个就在应答里写明（stopped_at、truncated_lines、redacted_lines、limitations）；next_cursor 是 UQ 自己的翻页位置（search_after 或 offset），原样传回读下一页，窗口随游标固定。与 pod.logs 同一脱敏合同，平台日志主题的脱敏规则不在此执行。",
		EvidenceScope: "sre_uq_log_search", ExecutionPool: "sre", ExecutionTimeout: uq.MaxTimeout + 5*time.Second, Availability: queryAvailable,
		Limits: map[string]any{"max_lines": uq.LogSearchMaxLines, "default_lines": uq.LogSearchDefaultLines, "max_line_bytes": uq.LogSearchMaxLineBytes, "default_line_bytes": uq.LogSearchDefaultLineBytes,
			"max_total_bytes": uq.LogSearchMaxTotalBytes, "default_total_bytes": uq.LogSearchDefaultTotalBytes, "max_window_seconds": int64(uq.MaxWindow / time.Second), "default_window": "1h",
			"max_cursor_bytes": uq.LogSearchMaxCursorBytes, "pagination": "native UQ search_after or offset cursor; one page per invocation", "redaction_contract": redact.Contract,
			"content":   "log text and allowed metadata fields, each redacted under the contract before it is cut; the platform's log topic desensitization is not applied, as for pod.logs",
			"authority": "deployment_operator_readonly; page_user_authorization_not_replayed"},
		Fields: map[string]Field{"egress": text, "tenant": text, "space": text,
			"table_id":     {Type: "string", MinLength: 1, MaxLength: 256, Description: "<business>_bklog.<name>"},
			"query_string": {Type: "string", MaxLength: 8192},
			"window":       {Type: "string", MaxLength: 16, Description: "a duration from 1m to 30d ending at end; omit with cursor"},
			"end":          {Type: "string", MaxLength: 64, Description: "RFC 3339 UTC; omit with cursor"},
			"order":        {Type: "string", Enum: []string{"desc", "asc"}, Description: "desc reads newest first (default); keep it the same across pages"},
			"lines":        {Type: "integer", Minimum: &positive, Maximum: &maxSearchLines},
			"line_bytes":   {Type: "integer", Minimum: &positive, Maximum: &maxSearchLineBytes},
			"total_bytes":  {Type: "integer", Minimum: &positive, Maximum: &maxSearchTotalBytes},
			"cursor":       {Type: "string", MaxLength: uq.LogSearchMaxCursorBytes, Description: "next_cursor of the previous page"}},
		Required: []string{"egress", "tenant", "space", "table_id"}, OutputSchema: SchemaOf(uq.LogPage{}),
		Run: func(ctx context.Context, p Params) Outcome {
			v, err := queries.LogSearch(ctx, uq.LogSearchRequest{Egress: p.String("egress"), Tenant: p.String("tenant"), Space: p.String("space"),
				TableID: p.String("table_id"), QueryString: p.String("query_string"), Window: p.String("window"), End: p.String("end"), Order: p.String("order"),
				Lines: p.Int("lines", 0), LineBytes: p.Int("line_bytes", 0), TotalBytes: p.Int("total_bytes", 0), Cursor: p.String("cursor"), Redact: scrubber.Text})
			v.RedactionContract = redact.Contract
			out := Outcome{Value: v, Complete: err == nil && !v.Partial && v.TruncatedLines == 0, Limitations: v.Limitations}
			if err != nil {
				var named *uq.Error
				if errors.As(err, &named) {
					out.Error = &Failure{Code: named.Code, Message: named.Message}
				} else {
					out.Error = &Failure{Code: "uq_provider_failed", Message: "Native query did not complete."}
				}
			}
			return out
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
	ops = append(ops, Operation{ID: OpSREReceipts, Summary: "读取 SRE 动作（执行、管理、改镜像与回退）的回执，最新的在前；可按时间、命名空间、对象名、request_id、execution_id 与 effect 过滤，带分页。回执分三类分开保存，一类再多也挤不掉另一类：change（可能改了状态的动作：改镜像、回退、apply、patch、delete、删 Pod、重启、扩缩、改 ConfigMap；dry run 与生效前被拒的不算）、exec（经通道的命令：节点执行及其取消、终态，pod.exec）与 access（读取：执行输出、Secret、ConfigMap、资源列表，以及 dry run 与生效前被拒的尝试）。",
		EvidenceScope: "sre_receipts", ExecutionPool: "sre", ExecutionTimeout: 10 * time.Second, Availability: receiptsAvailable,
		Limits: map[string]any{"kept": MaxSREReceipts, "kept_change": MaxSREChangeReceipts, "kept_exec": MaxSREExecReceipts, "kept_access": MaxSREAccessReceipts,
			"receipt_bytes_change": SREChangeReceiptBytes, "receipt_bytes_exec": SREExecReceiptBytes, "receipt_bytes_access": SREAccessReceiptBytes, "retention_seconds": int64(SREReceiptRetention / time.Second), "max_page": maxSREReceiptsPage,
			"content": "who, what, where and how each action ended; a pod.exec receipt carries its run's facts and its script digest, never its argv, stdin or output"},
		Fields: map[string]Field{"since": timestamp, "until": timestamp, "namespace": text, "name": text, "request_id": text, "execution_id": text,
			"effect": {Type: "string", Enum: []string{SREEffectChange, SREEffectExec, SREEffectAccess}},
			"limit":  {Type: "integer", Minimum: &positive, Maximum: &maxReceiptsPage}, "offset": {Type: "integer", Minimum: &nonnegative, Maximum: &maxReceiptsOffset}},
		OutputSchema: SchemaOf(SREReceiptPage{}),
		Run: func(ctx context.Context, p Params) Outcome {
			filter := SREReceiptFilter{Namespace: p.String("namespace"), Name: p.String("name"), RequestID: p.String("request_id"), ExecutionID: p.String("execution_id"), Effect: p.String("effect"), Offset: p.Int("offset", 0), Limit: p.Int("limit", 0)}
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
				for _, key := range []string{"since", "until", "namespace", "name", "request_id", "execution_id", "effect", "limit"} {
					if value, present := p[key]; present {
						next[key] = value
					}
				}
				out.Next = []Call{{Operation: OpSREReceipts, Params: next, Reason: "读取同一过滤条件的下一页。"}}
			}
			return out
		},
	})
	return append(ops, nodeOperations(options, audit)...)
}

// listSummary is a list page's one line: how many it holds and whether, and
// how many, objects are past it, so a reader does not take a first page for
// the whole list.
func listSummary(v pod.ResourceReceipt) string {
	switch {
	case v.Truncated:
		return fmt.Sprintf("本页 %d 个对象；超出应答上限漏了 %d 个，按 next_call 用更小的页再读。", v.Count, v.ItemsOmitted)
	case v.Continue != "" && v.RemainingItems != nil:
		pages := (*v.RemainingItems + int64(max(1, v.Count)) - 1) / int64(max(1, v.Count))
		return fmt.Sprintf("本页 %d 个对象；还有 %d 个、约 %d 页，按 next_call 续读。", v.Count, *v.RemainingItems, pages)
	case v.Continue != "":
		return fmt.Sprintf("本页 %d 个对象；还有更多页（apiserver 未给总数），按 next_call 续读直至 continue 为空。", v.Count)
	}
	return fmt.Sprintf("共 %d 个对象，已全部列出。", v.Count)
}

func podTarget(p Params) pod.Target {
	value, _ := json.Marshal(p["target"])
	var target pod.Target
	_ = json.Unmarshal(value, &target)
	return target
}

// dryRunNames is the objects one dry run asked about, as its audit line names
// them: kind/namespace/name, at most secretAuditNames, then how many more.
func dryRunNames(receipt pod.DryRunReceipt) string {
	names := make([]string, 0, min(len(receipt.Objects), secretAuditNames+1))
	for index, object := range receipt.Objects {
		if index == secretAuditNames {
			names = append(names, fmt.Sprintf("+%d more", len(receipt.Objects)-secretAuditNames))
			break
		}
		names = append(names, object.Kind+"/"+object.Namespace+"/"+object.Name)
	}
	return strings.Join(names, ",")
}

// writeNames is the objects one apply wrote or tried to, as its audit line
// names them, at most secretAuditNames, then how many more.
func writeNames(receipt pod.WriteReceipt) string {
	names := make([]string, 0, min(len(receipt.Objects), secretAuditNames+1))
	for index, object := range receipt.Objects {
		if index == secretAuditNames {
			names = append(names, fmt.Sprintf("+%d more", len(receipt.Objects)-secretAuditNames))
			break
		}
		names = append(names, object.Kind+"/"+object.Namespace+"/"+object.Name)
	}
	return strings.Join(names, ",")
}

// writeDetail is a write's outcomes by count, whether it forced ownership,
// and the helm releases it touched.
// patchDetail is what a patch's audit record says of it: how many
// operations and their ops and paths, never a value.
// editDetail is an edit's audit line: the key, the lines changed and the
// outcome, never a value.
func editDetail(receipt pod.ConfigMapEditReceipt) string {
	detail := fmt.Sprintf("key %s; lines %v; outcome %s", receipt.Key, receipt.ChangedLines, receipt.Outcome)
	if receipt.DryRun {
		detail += "; dry_run"
	}
	return detail
}

func patchDetail(ops []pod.PatchOp) string {
	parts := make([]string, 0, len(ops))
	for _, op := range ops {
		parts = append(parts, op.Op+" "+op.Path)
	}
	return fmt.Sprintf("%d ops: %s", len(ops), strings.Join(parts, ", "))
}

func writeDetail(receipt pod.WriteReceipt) string {
	var parts []string
	for _, outcome := range pod.WriteOutcomes {
		if count := receipt.Counts[outcome]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", outcome, count))
		}
	}
	if receipt.Force {
		parts = append(parts, "force")
	}
	releases := map[string]bool{}
	for _, object := range receipt.Objects {
		if object.HelmRelease != "" && !releases[object.HelmRelease] {
			releases[object.HelmRelease] = true
			parts = append(parts, "helm_release "+object.HelmRelease)
		}
	}
	if len(parts) == 0 {
		return "no objects"
	}
	return strings.Join(parts, ", ")
}

// dryRunCounts is a dry run's verdicts by count, in their fixed order.
func dryRunCounts(receipt pod.DryRunReceipt) string {
	var parts []string
	for _, verdict := range pod.DryRunVerdicts {
		if count := receipt.Counts[verdict]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", verdict, count))
		}
	}
	if len(parts) == 0 {
		return "no objects"
	}
	return strings.Join(parts, ", ")
}

// secretAuditNames bounds the Secret names one listing's audit line carries.
const secretAuditNames = 20

// secretNames is the Secrets a listing returned, as its audit line names
// them: namespace/name, at most secretAuditNames, then how many more.
func secretNames(receipt pod.ResourceReceipt) string {
	names := make([]string, 0, min(len(receipt.Items), secretAuditNames+1))
	for index, raw := range receipt.Items {
		if index == secretAuditNames {
			names = append(names, fmt.Sprintf("+%d more", len(receipt.Items)-secretAuditNames))
			break
		}
		var item struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
		}
		_ = json.Unmarshal(raw, &item)
		names = append(names, item.Metadata.Namespace+"/"+item.Metadata.Name)
	}
	return strings.Join(names, ",")
}

// OpTraceTopFailures lists a trace target's most failing server routes.
const OpTraceTopFailures = "trace.top_failures"

// OpLogCount counts a log table's lines by fields, without the log text.
const OpLogCount = "log.count"

// OpLogSearch reads one page of a log table's lines, redacted, with UQ's own cursor.
const OpLogSearch = "log.search"

// topFailureMessageBytes bounds a group's message for its reader.
const topFailureMessageBytes = 200

// boundedText cuts text to at most limit bytes on a character boundary.
func boundedText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "") + "..."
}
