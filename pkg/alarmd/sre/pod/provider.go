// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
)

type Provider struct {
	options  Options
	redactor *redactor
	// keyMu guards the fingerprint key (fingerprintKey): the key, where it
	// compares, and when the deployment's was last asked for.
	keyMu    sync.Mutex
	key      []byte
	keyScope string
	keyAsked time.Time
	// shellless is the containers found without /bin/sh.
	shellless shelllessMemo
}

// The provider's per-request bounds: how long one execution may run (what the
// channel's two-minute operation ceiling leaves once the checks before and
// after, the start and the termination grace are kept back), how much one answer may carry
// (the channel sets it from the pod's memory; DefaultOutputBytes until it
// does), how much stdin one execution takes, and how many targets one page
// lists. There is no bound on how many requests run at once.
const (
	// ExecOperationCeiling is the channel's operation ceiling the whole run
	// fits in (obchannel.MaxExecutionTimeout; a test keeps the two equal).
	ExecOperationCeiling = 2 * time.Minute
	// MaxExecTimeout is the command's share: 120 - (3 + 4 + 4 + 3) = 106 s.
	// It was 110 s, and a command run that long had its termination record
	// and final check cut by the operation's deadline (timeout audit D08).
	MaxExecTimeout     = ExecOperationCeiling - (2*VerificationTimeout + StartupAllowance + TerminationGrace)
	DefaultOutputBytes = 64 << 20
	MaxOutputCeiling   = 256 << 20
	MaxStdinBytes      = 1 << 20
	MaxPodsPerPage     = 500
	// MaxLogLines and MaxLogWindow bound how far back one log read scans.
	MaxLogLines  = 100000
	MaxLogWindow = 7 * 24 * time.Hour
)

// DefaultNamespace is the namespace a request that names none reads.
func (p *Provider) DefaultNamespace() string { return p.options.DefaultNamespace }

// ModuleLabel is the label key modules are read from.
func (p *Provider) ModuleLabel() string { return p.options.ModuleLabel }

func (p *Provider) Limits() Limits {
	return Limits{DefaultTimeoutMS: p.options.DefaultTimeout.Milliseconds(), MaxTimeoutMS: p.options.MaxTimeout.Milliseconds(), MaxOutputBytes: p.options.MaxOutputBytes, MaxStdinBytes: p.options.MaxStdinBytes, MaxPods: p.options.MaxPods}
}

func New(options Options) (*Provider, error) {
	if options.Client == nil {
		return nil, invalid("a Kubernetes client is required")
	}
	if options.ModuleLabel == "" {
		options.ModuleLabel = DefaultModuleLabel
	}
	if len(validation.IsQualifiedName(options.ModuleLabel)) != 0 {
		return nil, invalid("module label is not a label key")
	}
	if options.DefaultNamespace != "" && len(validation.IsDNS1123Label(options.DefaultNamespace)) != 0 {
		return nil, invalid("default namespace is not a namespace name")
	}
	if options.DefaultTimeout == 0 {
		options.DefaultTimeout = 30 * time.Second
	}
	if options.MaxTimeout == 0 {
		options.MaxTimeout = MaxExecTimeout
	}
	if options.MaxOutputBytes == 0 {
		options.MaxOutputBytes = DefaultOutputBytes
	}
	if options.MaxStdinBytes == 0 {
		options.MaxStdinBytes = MaxStdinBytes
	}
	if options.MaxPods == 0 {
		options.MaxPods = MaxPodsPerPage
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.DefaultTimeout <= 0 || options.DefaultTimeout > options.MaxTimeout || options.MaxTimeout > MaxExecTimeout || options.MaxOutputBytes < 1 || options.MaxOutputBytes > MaxOutputCeiling || options.MaxStdinBytes < 1 || options.MaxStdinBytes > MaxStdinBytes || options.MaxPods < 1 || options.MaxPods > MaxPodsPerPage {
		return nil, invalid("provider budgets exceed supported bounds")
	}
	options.LiteralSecrets = append([]string(nil), options.LiteralSecrets...)
	options.FingerprintKey = append([]byte(nil), options.FingerprintKey...)
	if len(options.FingerprintKey) > 0 && options.FingerprintScope == "" {
		options.FingerprintScope = FingerprintScopeDeployment
	}
	return &Provider{options: options, redactor: newRedactor(options.LiteralSecrets)}, nil
}

// Where fingerprints compare (Options.FingerprintScope).
const (
	FingerprintScopeDeployment = "deployment"
	FingerprintScopeProcess    = "process"
)

// fingerprintKeyRetry is how long a process that had to make its own key
// waits before asking for the deployment's again: a runtime store that did
// not answer at the first fingerprint is asked again, at most this often,
// until it does.
const fingerprintKeyRetry = time.Minute

// fingerprintKey is the key fingerprints are made under and where they
// compare: the configured key; else the deployment's, asked for at the first
// fingerprint and again at most every fingerprintKeyRetry while it has not
// come; else one this process made. The source is asked under the lock, so
// a slow store holds fingerprinting back for one bounded read a minute.
func (p *Provider) fingerprintKey() ([]byte, string) {
	p.keyMu.Lock()
	defer p.keyMu.Unlock()
	if len(p.options.FingerprintKey) > 0 {
		return p.options.FingerprintKey, p.options.FingerprintScope
	}
	if p.keyScope == FingerprintScopeDeployment {
		return p.key, p.keyScope
	}
	if source := p.options.FingerprintKeySource; source != nil {
		if now := p.options.Now(); p.keyAsked.IsZero() || now.Sub(p.keyAsked) >= fingerprintKeyRetry {
			p.keyAsked = now
			if key := source(); len(key) > 0 {
				p.key, p.keyScope = key, FingerprintScopeDeployment
				return p.key, p.keyScope
			}
		}
	}
	if len(p.key) == 0 {
		p.key, p.keyScope = make([]byte, 32), FingerprintScopeProcess
		_, _ = rand.Read(p.key)
	}
	return p.key, p.keyScope
}

// fingerprints is one answer's fingerprinting. It takes the key at the
// answer's first fingerprint and keeps it, so every fingerprint in one answer
// is made under one key and the scope the answer reports is that key's.
type fingerprints struct {
	p     *Provider
	key   []byte
	scope string
}

func (p *Provider) newFingerprints() *fingerprints { return &fingerprints{p: p} }

func (f *fingerprints) take() {
	if f.key == nil {
		f.key, f.scope = f.p.fingerprintKey()
	}
}

// Scope is where this answer's fingerprints compare.
func (f *fingerprints) Scope() string {
	f.take()
	return f.scope
}

// sum is a value as it is shown when it may not be: HMAC-SHA256 under the
// answer's key, the first 16 hex digits. It tells two equal values and a
// changed one apart and gives nothing else.
func (f *fingerprints) sum(value []byte) string {
	f.take()
	mac := hmac.New(sha256.New, f.key)
	mac.Write(value)
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

func invalid(message string) error { return &Error{Code: CodeInvalidRequest, Message: message} }
func denied(message string) error  { return &Error{Code: CodeNotInScope, Message: message} }
func apiError(err error) error {
	if err == nil {
		return nil
	}
	var known *Error
	if errors.As(err, &known) {
		return known
	}
	code := CodeAPIError
	switch {
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		code = CodeForbidden
	case apierrors.IsNotFound(err):
		code = CodeNotFound
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = CodeCanceled
	}
	return &Error{Code: code, Message: "Kubernetes evidence request did not succeed" + statusWords(err)}
}

// statusScrubber clears the common credential shapes from an apiserver's
// words; maxStatusWords bounds them.
var statusScrubber = redact.New()

const maxStatusWords = 512

// statusWords is an apiserver refusal's own reason and message, scrubbed and
// bounded, so the reader is told why ("previous terminated container ... not
// found"); an error that is no apiserver status adds nothing.
func statusWords(err error) string {
	var status apierrors.APIStatus
	if !errors.As(err, &status) {
		return ""
	}
	words := strings.TrimSpace(status.Status().Message)
	if reason := string(status.Status().Reason); reason != "" && words != "" {
		words = reason + ": " + words
	} else if reason != "" {
		words = reason
	}
	if words == "" {
		return ""
	}
	words = statusScrubber.Text(words)
	if len(words) > maxStatusWords {
		// Cut on a character boundary: a webhook's denial may be in Chinese.
		words = strings.ToValidUTF8(words[:maxStatusWords], "") + "..."
	}
	return ": " + words
}

// acquire admits a call whose caller is still there. How many run at once
// is the channel's admission, by the observation memory line; the provider
// keeps no count of its own, which refused reads the channel had admitted.
func (p *Provider) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, apiError(err)
	}
	return func() {}, nil
}
func (p *Provider) readContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, p.options.MaxTimeout)
}

// namespace is the one namespace a Pod read works in: the one it names, or
// this process's own. A listing across the cluster is k8s.list's, page by
// page, each page bounded by its limit (ListResources).
func (p *Provider) namespace(value string) (string, error) {
	if value == "" {
		value = p.options.DefaultNamespace
	}
	if value == "" {
		return "", invalid("namespace is required: this process does not know its own namespace")
	}
	if len(validation.IsDNS1123Label(value)) != 0 {
		return "", invalid("namespace must be one Kubernetes namespace name")
	}
	return value, nil
}

func (p *Provider) Discover(ctx context.Context, request DiscoverRequest) (DiscoverReceipt, error) {
	result := DiscoverReceipt{Targets: []Observation{}, ObservedAt: p.options.Now().UTC()}
	release, err := p.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	namespace, err := p.namespace(request.Namespace)
	if err != nil {
		return result, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = p.options.MaxPods
	}
	if limit < 1 || limit > p.options.MaxPods || len(request.Continue) > 4096 {
		return result, invalid("invalid discovery limit or continuation")
	}
	selector, err := labels.Parse(request.Selector)
	if err != nil || len(request.Selector) > 1024 {
		return result, invalid("selector is not a Kubernetes label selector")
	}
	for _, index := range []struct{ key, value string }{{ReleaseLabel, request.Release}, {p.options.ModuleLabel, request.Module}} {
		if index.value == "" {
			continue
		}
		requirement, err := labels.NewRequirement(index.key, selection.Equals, []string{index.value})
		if err != nil {
			return result, invalid("release/module is not a label value")
		}
		selector = selector.Add(*requirement)
	}
	pods, err := p.options.Client.ListPods(ctx, namespace, metav1.ListOptions{LabelSelector: selector.String(), Limit: int64(limit), Continue: request.Continue})
	if err != nil {
		return result, apiError(err)
	}
	result.Continue = pods.Continue
	if len(pods.Items) > limit {
		return result, &Error{Code: CodeAPIError, Message: "API ignored the discovery limit"}
	}
	// One discovery reads each owner once: replicas of a workload share
	// their ReplicaSet and Deployment. Nothing is kept past the request.
	owners := map[string]OwnerResource{}
	for i := range pods.Items {
		obj := &pods.Items[i]
		if request.Workload != "" && !mayLeadTo(obj.OwnerReferences, request.Workload) {
			continue
		}
		observations, err := p.observe(ctx, obj, "", owners)
		if err != nil {
			var failure *Error
			if errors.As(err, &failure) && failure.Code == CodeNotInScope {
				result.Rejected++
				continue
			}
			return result, err
		}
		for _, observation := range observations {
			if request.Workload == "" || request.Workload == observation.Workload.Name {
				result.Targets = append(result.Targets, observation)
			}
		}
	}
	sort.Slice(result.Targets, func(i, j int) bool {
		a, b := result.Targets[i].Target, result.Targets[j].Target
		return a.Pod < b.Pod || a.Pod == b.Pod && a.Container < b.Container
	})
	return result, nil
}

func (p *Provider) Inspect(ctx context.Context, request TargetRequest) (Observation, error) {
	release, err := p.acquire(ctx)
	if err != nil {
		return Observation{}, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	return p.resolve(ctx, request.Target, false)
}

func validateTarget(target Target, strict bool) error {
	if len(validation.IsDNS1123Label(target.Namespace)) != 0 || len(validation.IsDNS1123Subdomain(target.Pod)) != 0 || len(validation.IsDNS1123Label(target.Container)) != 0 {
		return invalid("namespace, pod and container are required Kubernetes names")
	}
	if strict && (target.UID == "" || target.ContainerID == "" || target.ImageID == "") {
		return invalid("pod UID, container ID and image ID from discovery are required")
	}
	return nil
}
func (p *Provider) resolve(ctx context.Context, target Target, strict bool) (Observation, error) {
	if err := validateTarget(target, strict); err != nil {
		return Observation{}, err
	}
	obj, err := p.options.Client.GetPod(ctx, target.Namespace, target.Pod)
	if err != nil {
		return Observation{}, apiError(err)
	}
	observations, err := p.observe(ctx, obj, target.Container, nil)
	if err != nil {
		return Observation{}, err
	}
	observed := observations[0]
	if target.UID != "" && target.UID != observed.Target.UID || target.ContainerID != "" && target.ContainerID != observed.Target.ContainerID || target.ImageID != "" && target.ImageID != observed.Target.ImageID {
		return observed, &Error{Code: CodeTargetChanged, Message: "pod or container identity differs from discovery"}
	}
	return observed, nil
}

// observe reads a Pod's containers with the owner chain verified against
// the live resources. The release and module are the Pod's own labels.
func (p *Provider) observe(ctx context.Context, obj *corev1.Pod, container string, owners map[string]OwnerResource) ([]Observation, error) {
	chain, _, err := p.ownerChain(ctx, obj, owners)
	if err != nil {
		return nil, err
	}
	root := chain[len(chain)-1]
	var observations []Observation
	// Init containers are targets too, each with its own status: a stuck or
	// failing init is read by its logs like any container.
	type candidate struct {
		spec     corev1.Container
		statuses []corev1.ContainerStatus
		init     bool
	}
	var candidates []candidate
	for _, spec := range obj.Spec.Containers {
		candidates = append(candidates, candidate{spec, obj.Status.ContainerStatuses, false})
	}
	for _, spec := range obj.Spec.InitContainers {
		candidates = append(candidates, candidate{spec, obj.Status.InitContainerStatuses, true})
	}
	for _, each := range candidates {
		spec := each.spec
		if container != "" && container != spec.Name {
			continue
		}
		observation := Observation{Init: each.init, Target: Target{Namespace: obj.Namespace, Pod: obj.Name, UID: string(obj.UID), Container: spec.Name}, Release: obj.Labels[ReleaseLabel], Module: obj.Labels[p.options.ModuleLabel], Workload: root, OwnerChain: chain, Image: spec.Image, Phase: string(obj.Status.Phase), Conditions: []Condition{}, NodeName: obj.Spec.NodeName, ObservedAt: p.options.Now().UTC()}
		if obj.DeletionTimestamp != nil {
			deletedAt := obj.DeletionTimestamp.Time.UTC()
			observation.DeletionTimestamp = &deletedAt
		}
		for i, condition := range obj.Status.Conditions {
			if i >= 16 {
				break
			}
			observation.Conditions = append(observation.Conditions, Condition{Type: boundedStatus(string(condition.Type)), Status: string(condition.Status), Reason: boundedStatus(condition.Reason)})
		}
		for _, status := range each.statuses {
			if status.Name == spec.Name {
				observation.Target.ContainerID = status.ContainerID
				observation.Target.ImageID = status.ImageID
				observation.Ready = status.Ready
				observation.Running = status.State.Running != nil
				observation.RestartCount = status.RestartCount
				if status.State.Waiting != nil {
					observation.WaitingReason = boundedStatus(status.State.Waiting.Reason)
				}
				observation.CurrentTermination = termination(status.State.Terminated)
				observation.LastTermination = termination(status.LastTerminationState.Terminated)
				break
			}
		}
		observations = append(observations, observation)
	}
	if len(observations) == 0 {
		return nil, denied("container is not a container or init container of this pod")
	}
	return observations, nil
}
func boundedStatus(value string) string {
	if len(value) > 128 {
		return value[:128]
	}
	return value
}
func termination(status *corev1.ContainerStateTerminated) *Termination {
	if status == nil {
		return nil
	}
	return &Termination{Reason: boundedStatus(status.Reason), ExitCode: status.ExitCode, Signal: status.Signal, StartedAt: status.StartedAt.Time.UTC(), FinishedAt: status.FinishedAt.Time.UTC()}
}
func controller(owners []metav1.OwnerReference) (metav1.OwnerReference, error) {
	var selected metav1.OwnerReference
	count := 0
	for _, owner := range owners {
		if owner.Controller != nil && *owner.Controller {
			selected = owner
			count++
		}
	}
	if count != 1 || selected.UID == "" {
		return selected, denied("pod owner chain has no unique controller identity")
	}
	return selected, nil
}
func rootKind(kind string) bool {
	return kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet" || kind == "CronJob" || kind == "Job"
}

// mayLeadTo says whether a Pod's controller could have workload at the root
// of its chain: a StatefulSet, DaemonSet, Job or bare ReplicaSet is the name
// itself, a Deployment's ReplicaSet and a CronJob's Job are the name and a
// dash. It only spares the owner reads of Pods that cannot match; those that
// can are still checked through the whole chain.
func mayLeadTo(owners []metav1.OwnerReference, workload string) bool {
	ref, err := controller(owners)
	if err != nil {
		return true
	}
	return ref.Name == workload || strings.HasPrefix(ref.Name, workload+"-")
}

// ownerChain reads a Pod's owners up to its workload. owners, when not nil,
// holds those this request has read already, by kind, name and UID.
func (p *Provider) ownerChain(ctx context.Context, obj *corev1.Pod, owners map[string]OwnerResource) ([]Workload, []map[string]string, error) {
	refs := obj.OwnerReferences
	var chain []Workload
	var ownerLabels []map[string]string
	childKind := "Pod"
	childLabels := obj.Labels
	for depth := 0; depth < 3; depth++ {
		ref, err := controller(refs)
		if err != nil {
			return nil, nil, err
		}
		valid := childKind == "Pod" && (ref.Kind == "ReplicaSet" || ref.Kind == "StatefulSet" || ref.Kind == "DaemonSet" || ref.Kind == "Job") || childKind == "ReplicaSet" && ref.Kind == "Deployment" || childKind == "Job" && ref.Kind == "CronJob"
		if !valid {
			return nil, nil, denied("unsupported workload owner chain")
		}
		key := obj.Namespace + "/" + ref.APIVersion + "/" + ref.Kind + "/" + ref.Name + "/" + string(ref.UID)
		owner, read := owners[key]
		if !read {
			owner, err = p.options.Client.GetOwner(ctx, obj.Namespace, ref)
			if err != nil {
				return nil, nil, apiError(err)
			}
			if owners != nil {
				owners[key] = owner
			}
		}
		if owner.Namespace != obj.Namespace || owner.Name != ref.Name || owner.Kind != ref.Kind || owner.UID != string(ref.UID) {
			return nil, nil, denied("owner reference identity does not match the live resource")
		}
		if ref.Kind != "CronJob" {
			if owner.Selector == nil {
				return nil, nil, denied("workload has no selector")
			}
			selector, err := metav1.LabelSelectorAsSelector(owner.Selector)
			if err != nil || selector.Empty() || !selector.Matches(labels.Set(childLabels)) || !selector.Matches(labels.Set(obj.Labels)) {
				return nil, nil, denied("workload selector does not match its pod/owner chain")
			}
		}
		chain = append(chain, Workload{Kind: owner.Kind, Name: owner.Name, UID: owner.UID})
		ownerLabels = append(ownerLabels, owner.Labels)
		if rootKind(ref.Kind) && len(owner.Owners) == 0 {
			return chain, ownerLabels, nil
		}
		refs = owner.Owners
		childKind = owner.Kind
		childLabels = owner.Labels
	}
	return nil, nil, denied("owner chain exceeded the supported depth")
}

func (p *Provider) outputLimit(value int64) (int64, error) {
	if value == 0 {
		value = p.options.MaxOutputBytes
	}
	if value < 1 || value > p.options.MaxOutputBytes {
		return 0, invalid("output budget exceeds provider limit")
	}
	return value, nil
}
func (p *Provider) postObserve(ctx context.Context, target Target) (*Observation, bool, string) {
	// Cancellation can prevent final verification; do not silently turn it into
	// an unbounded detached API call.
	if ctx.Err() != nil {
		return nil, false, "post_observation_unavailable"
	}
	ctx, cancel := context.WithTimeout(ctx, VerificationTimeout)
	defer cancel()
	observed, err := p.resolve(ctx, target, true)
	if err != nil {
		var failure *Error
		if errors.As(err, &failure) && (failure.Code == CodeTargetChanged || failure.Code == CodeNotFound || failure.Code == CodeNotInScope) {
			if observed.Target.Pod != "" {
				return &observed, true, "target_changed"
			}
			return nil, true, "target_changed"
		}
		return nil, false, "post_observation_unavailable"
	}
	return &observed, false, "identity_verified_before_and_after"
}

func (p *Provider) Logs(ctx context.Context, request LogsRequest) (LogsReceipt, error) {
	result := LogsReceipt{Target: request.Target, Previous: request.Previous, ObservedAt: p.options.Now().UTC(), EvidenceScope: "identity_verified_before", RedactionContract: RedactionContract}
	release, err := p.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	if request.TailLines == 0 {
		request.TailLines = 200
	}
	filtered := request.Contains != "" || !request.SinceTime.IsZero() || !request.UntilTime.IsZero()
	if request.TailLines < 1 || request.TailLines > MaxLogLines || request.SinceSeconds < 0 || request.SinceSeconds > int64(MaxLogWindow/time.Second) ||
		(request.SinceSeconds > 0 && !request.SinceTime.IsZero()) || (!request.UntilTime.IsZero() && request.UntilTime.Before(request.SinceTime)) {
		return result, invalid("log window exceeds supported bounds")
	}
	limit, err := p.outputLimit(request.OutputBytes)
	if err != nil {
		return result, err
	}
	if _, err = p.resolve(ctx, request.Target, true); err != nil {
		return result, err
	}
	// Ask for one sentinel byte beyond the returned budget so an API-side cap
	// cannot turn a truncated log into an apparently complete response.
	apiLimit := limit + 1
	options := corev1.PodLogOptions{Container: request.Target.Container, TailLines: &request.TailLines, Previous: request.Previous, LimitBytes: &apiLimit}
	if request.SinceSeconds > 0 {
		options.SinceSeconds = &request.SinceSeconds
	}
	if filtered {
		// The scan is bounded by its lines and the read's deadline, not by
		// bytes: only the kept lines are held.
		options.LimitBytes = nil
		options.Timestamps = !request.UntilTime.IsZero()
		if !request.SinceTime.IsZero() {
			since := metav1.NewTime(request.SinceTime)
			options.SinceTime = &since
		}
	}
	stream, err := p.options.Client.OpenLogs(ctx, request.Target.Namespace, request.Target.Pod, options)
	if err != nil {
		return result, apiError(err)
	}
	defer stream.Close()
	var data []byte
	var readErr error
	if filtered {
		data, result.ScannedLines, result.MatchedLines, readErr = scanLogs(stream, request, limit)
	} else {
		data, readErr = io.ReadAll(io.LimitReader(stream, limit+1))
	}
	result.Truncated = int64(len(data)) > limit
	if result.Truncated {
		data = data[:limit]
	}
	var redactedCut bool
	result.Output, redactedCut = p.redactor.bounded(string(data), limit)
	result.Truncated = result.Truncated || redactedCut
	result.ObservedAfter, result.TargetChanged, result.EvidenceScope = p.postObserve(ctx, request.Target)
	if readErr != nil {
		return result, apiError(readErr)
	}
	if result.TargetChanged {
		return result, &Error{Code: CodeTargetChanged, Message: "target identity changed while logs were read"}
	}
	return result, nil
}

// scanLogs keeps the lines of a log stream that the request's filters let
// through, one past limit bytes at most so the caller sees the cut; it says
// how many lines it scanned and kept. With UntilTime the stream carries
// each line's timestamp, which decides and is then dropped.
func scanLogs(stream io.Reader, request LogsRequest, limit int64) ([]byte, int64, int64, error) {
	reader := bufio.NewReaderSize(stream, 64<<10)
	var kept bytes.Buffer
	var scanned, matched int64
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			scanned++
			text := line
			keep := true
			if !request.UntilTime.IsZero() {
				stamp, rest, found := strings.Cut(text, " ")
				at, parseErr := time.Parse(time.RFC3339Nano, stamp)
				if found && parseErr == nil {
					text = rest
					keep = !at.After(request.UntilTime)
				}
			}
			if keep && request.Contains != "" {
				keep = strings.Contains(text, request.Contains)
			}
			if keep {
				matched++
				kept.WriteString(text)
				if int64(kept.Len()) > limit {
					return kept.Bytes(), scanned, matched, nil
				}
			}
		}
		if err == io.EOF {
			return kept.Bytes(), scanned, matched, nil
		}
		if err != nil {
			return kept.Bytes(), scanned, matched, err
		}
	}
}

// String keeps a formatted Provider to its two plain settings: printed with
// %v, the struct would carry the redactor's literal credentials.
func (p *Provider) String() string {
	return fmt.Sprintf("pod provider (default namespace %q, module label %q)", p.options.DefaultNamespace, p.options.ModuleLabel)
}
