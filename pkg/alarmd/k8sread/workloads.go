// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package k8sread

import (
	"context"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The workload read's bounds. A namespace answers at most this much whatever
// it holds, and says by name when it held more.
const (
	// MaxNamespaces bounds the namespaces one read covers: its own, the ones
	// its dependencies' addresses name, and the ones the deployment lists.
	MaxNamespaces = 8
	// MaxNamespacePods and MaxNamespaceReplicaSets are the page each list asks
	// for. A pod or ReplicaSet object is a few kilobytes, so a page stays well
	// inside the response bound before it is decoded.
	MaxNamespacePods        = 200
	MaxNamespaceReplicaSets = 200
	// MaxWorkloads bounds the workloads one namespace reports, newest rollout
	// first.
	MaxWorkloads = 100
	// MaxRollouts bounds the ReplicaSets one Deployment reports, newest first.
	MaxRollouts = 3
	// DefaultWindowHours and MaxWindowHours bound the window a rollout is
	// reported as recent in.
	DefaultWindowHours = 24
	MaxWindowHours     = 7 * 24
)

// How a namespace came to be read: this process's own; derived from the
// address of a dependency it connects to; or listed by the deployment
// (ALARMD_OBSERVE_NAMESPACES, from the chart's list of the namespaces its
// dependencies run in, each with a Role granting the read).
const (
	OriginOwn        = "own"
	OriginDerived    = "derived"
	OriginConfigured = "configured"
	// OriginShortName is a dependency addressed by a bare Service name, placed
	// in this process's own namespace because that is where a Pod's resolver
	// looks it up first. It is an assumption the address supports, not one it
	// proves: a hostAliases entry or the node's search domains can send the
	// name elsewhere, and the origin says so rather than reading like a
	// namespace the address names.
	OriginShortName = "short_name"
)

// ReasonNoNamespace is why a dependency adds no namespace: its address is not
// an in-cluster Service name, so which namespace serves it cannot be read
// from the address.
const ReasonNoNamespace = "address_has_no_namespace"

// ReasonConfiguredInvalid is why a listed namespace was not read: it is not
// a namespace name. It is reported rather than skipped, so a typo in the list
// does not read as a namespace with no workloads.
const ReasonConfiguredInvalid = "configured_namespace_invalid"

// RolloutBasisReplicaSet and RolloutBasisPod say what a workload's rollout
// time was read from: a Deployment's newest ReplicaSet, or, for a workload
// that keeps no ReplicaSets, its newest Pod.
const (
	RolloutBasisReplicaSet = "replicaset"
	RolloutBasisPod        = "pod"
)

// Dependency is an address alarmd already connects to, named by what it is
// (uq, linkd_console, redis:<role>, kafka). The workload read derives a
// namespace from it and nothing else: it adds no configuration and connects
// to nothing new.
type Dependency struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// NamespaceOrigin is one reason a namespace was read.
type NamespaceOrigin struct {
	Kind       string `json:"kind"`
	Dependency string `json:"dependency,omitempty"`
	Address    string `json:"address,omitempty"`
}

// UnresolvedDependency is a dependency whose address names no namespace. It
// is listed, not dropped: a dependency the read could not place is not a
// dependency with no workloads.
type UnresolvedDependency struct {
	Dependency string `json:"dependency"`
	Address    string `json:"address"`
	Reason     string `json:"reason"`
}

// ReadFailure is one list that failed, by its named code.
type ReadFailure struct {
	Resource string `json:"resource"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// ContainerImage is one container of a pod template or a Pod, by name and the
// image it runs. Init containers are marked: a migration usually runs in one.
type ContainerImage struct {
	Container string `json:"container"`
	Image     string `json:"image"`
	Init      bool   `json:"init,omitempty"`
}

// ImageChange is one container whose image differs between a Deployment's
// newest rollout and the one before it. From is empty for a container the
// newer rollout added, To for one it removed.
type ImageChange struct {
	Container string `json:"container"`
	From      string `json:"from"`
	To        string `json:"to"`
	Init      bool   `json:"init,omitempty"`
}

// Rollout is one ReplicaSet of a Deployment: one rollout of it.
type Rollout struct {
	ReplicaSet string           `json:"replicaset"`
	Revision   string           `json:"revision,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	Desired    int32            `json:"desired"`
	Ready      int32            `json:"ready"`
	Images     []ContainerImage `json:"images"`
}

// PodSummary is a workload's Pods as they stand.
type PodSummary struct {
	Total    int `json:"total"`
	Ready    int `json:"ready"`
	Restarts int `json:"restarts"`
	// NewestStartedAt is the latest start among them.
	NewestStartedAt *time.Time `json:"newest_started_at,omitempty"`
}

// Workload is one workload of a namespace: a Deployment, StatefulSet,
// DaemonSet or Job found from its Pods' and ReplicaSets' owners, or a Pod
// that has none.
type Workload struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// LastRolloutAt is when the workload last rolled out, read as
	// RolloutBasis says; InWindow says it falls in the read's window. For a
	// Deployment it is the creation of the ReplicaSet with the highest
	// revision, so a rollback - which re-uses an older ReplicaSet under the
	// next revision - shows that ReplicaSet's original creation, not the
	// moment of the rollback.
	LastRolloutAt *time.Time `json:"last_rollout_at,omitempty"`
	RolloutBasis  string     `json:"rollout_basis,omitempty"`
	InWindow      bool       `json:"in_window"`
	// Images are what the newest rollout runs (its ReplicaSet's template, or
	// the newest Pod's containers).
	Images []ContainerImage `json:"images"`
	// Changed is the image change of the newest rollout against the one
	// before it, for a Deployment with at least two ReplicaSets on record.
	Changed  []ImageChange `json:"changed,omitempty"`
	Rollouts []Rollout     `json:"rollouts,omitempty"`
	Pods     PodSummary    `json:"pods"`
}

// NamespaceWorkloads is one namespace's section of the read.
type NamespaceWorkloads struct {
	Namespace string            `json:"namespace"`
	Origins   []NamespaceOrigin `json:"origins"`
	// Failures are the lists that failed. A namespace with a failure reports
	// what the other list gave, and is not complete.
	Failures  []ReadFailure `json:"failures,omitempty"`
	Workloads []Workload    `json:"workloads"`
	// The lists and the workloads each say when they held more than their
	// bound.
	PodsTruncated        bool `json:"pods_truncated,omitempty"`
	ReplicaSetsTruncated bool `json:"replicasets_truncated,omitempty"`
	WorkloadsTruncated   bool `json:"workloads_truncated,omitempty"`
}

// WorkloadsResult is the whole read.
type WorkloadsResult struct {
	WindowHours int                    `json:"window_hours"`
	ReadAt      time.Time              `json:"read_at"`
	Namespaces  []NamespaceWorkloads   `json:"namespaces"`
	Unresolved  []UnresolvedDependency `json:"unresolved,omitempty"`
	// NamespacesTruncated names the namespaces past MaxNamespaces that were
	// not read.
	NamespacesTruncated []string `json:"namespaces_truncated,omitempty"`
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// NamespaceOfAddress reads the namespace an in-cluster Service address names:
// <service>.<namespace>.svc, optionally followed by the cluster domain, and
// the <pod>.<service>.<namespace>.svc form of a headless Service. The address
// may be a URL, host:port or a bare host. Anything else - an IP, an external
// name, a short <service>.<namespace> that could be either - names none.
// What follows svc is not checked against the cluster's own domain, so an
// external name with a label "svc" in it is taken for one: the cost is one
// more LIST, answered by name (usually forbidden), and one of MaxNamespaces.
func NamespaceOfAddress(address string) (string, bool) {
	host, ok := addressHost(address)
	if !ok {
		return "", false
	}
	labels := strings.Split(host, ".")
	for i := 2; i < len(labels); i++ {
		if labels[i] != "svc" {
			continue
		}
		if namespace := labels[i-1]; dnsLabel.MatchString(namespace) && dnsLabel.MatchString(labels[i-2]) {
			return namespace, true
		}
		return "", false
	}
	return "", false
}

// IsShortServiceName says whether an address names a Service by its bare
// name: one DNS label, no dots. A Pod's resolver looks such a name up in its
// own namespace first (<name>.<namespace>.svc under the cluster domain), so
// the Service it reaches runs where this process runs. localhost is not a
// Service.
func IsShortServiceName(address string) bool {
	host, ok := addressHost(address)
	return ok && host != "localhost" && !strings.Contains(host, ".") && dnsLabel.MatchString(host)
}

// addressHost is the lower-cased host of a URL, host:port or bare host,
// without a trailing dot; false for an empty host or an IP.
func addressHost(address string) (string, bool) {
	host := strings.TrimSpace(address)
	if strings.Contains(host, "://") {
		parsed, err := url.Parse(host)
		if err != nil {
			return "", false
		}
		host = parsed.Hostname()
	} else if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || net.ParseIP(host) != nil {
		return "", false
	}
	return host, true
}

type replicaSetObject struct {
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		Replicas *int32 `json:"replicas"`
		// Only the containers' names and images are decoded from the pod
		// template: its env and everything else pass the decoder unread.
		Template struct {
			Spec templateContainers `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas int32 `json:"readyReplicas"`
	} `json:"status"`
}

type templateContainers struct {
	InitContainers []namedImage `json:"initContainers"`
	Containers     []namedImage `json:"containers"`
}

type namedImage struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

func (c templateContainers) images() []ContainerImage {
	images := make([]ContainerImage, 0, len(c.InitContainers)+len(c.Containers))
	for _, container := range c.InitContainers {
		images = append(images, ContainerImage{Container: container.Name, Image: container.Image, Init: true})
	}
	for _, container := range c.Containers {
		images = append(images, ContainerImage{Container: container.Name, Image: container.Image})
	}
	return images
}

type workloadPod struct {
	Metadata objectMeta `json:"metadata"`
	// Names and images only, as for the ReplicaSet template.
	Spec   templateContainers `json:"spec"`
	Status struct {
		StartTime  *time.Time `json:"startTime"`
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
		ContainerStatuses []struct {
			RestartCount int `json:"restartCount"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

// ownNamespace is the namespace this process runs in, from the
// ServiceAccount's namespace file.
func (r *Reader) ownNamespace() (string, error) {
	raw, err := os.ReadFile(r.options.NamespacePath)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return "", &Error{Code: CodeNoServiceAccount, Resource: "namespace", Message: "no ServiceAccount namespace is mounted"}
	}
	return strings.TrimSpace(string(raw)), nil
}

// Workloads reads the workloads of this process's own namespace, of the
// namespaces its dependencies' addresses name, and of the namespaces the
// deployment lists: every Deployment, StatefulSet, DaemonSet, Job and bare
// Pod, newest rollout first, with what each newest rollout changed. A
// namespace named by more than one of them is read once, with every origin.
//
// It is read only when asked: two LISTs per namespace (pods, replicasets),
// nothing periodic. A namespace whose list fails reports the named failure in
// its section, and the others are read all the same; a read that did not
// happen is never reported as a namespace with no workloads. The namespace
// file missing - a process outside Kubernetes - is the whole read's failure.
func (r *Reader) Workloads(ctx context.Context, dependencies []Dependency, configured []string, windowHours int) (WorkloadsResult, error) {
	if windowHours <= 0 {
		windowHours = DefaultWindowHours
	}
	if windowHours > MaxWindowHours {
		windowHours = MaxWindowHours
	}
	own, err := r.ownNamespace()
	if err != nil {
		return WorkloadsResult{}, err
	}
	now := r.options.Now()
	result := WorkloadsResult{WindowHours: windowHours, ReadAt: now.UTC(), Namespaces: []NamespaceWorkloads{}}
	sections := map[string]*NamespaceWorkloads{}
	order := []string{}
	add := func(namespace string, origin NamespaceOrigin) {
		if section, known := sections[namespace]; known {
			section.Origins = append(section.Origins, origin)
			return
		}
		if len(order) == MaxNamespaces {
			for _, skipped := range result.NamespacesTruncated {
				if skipped == namespace {
					return
				}
			}
			result.NamespacesTruncated = append(result.NamespacesTruncated, namespace)
			return
		}
		sections[namespace] = &NamespaceWorkloads{Namespace: namespace, Origins: []NamespaceOrigin{origin}, Workloads: []Workload{}}
		order = append(order, namespace)
	}
	add(own, NamespaceOrigin{Kind: OriginOwn})
	for _, dependency := range dependencies {
		namespace, ok := NamespaceOfAddress(dependency.Address)
		if !ok && IsShortServiceName(dependency.Address) {
			// A bare Service name resolves in this process's own namespace:
			// the dependency is read there, and named on that section, rather
			// than listed as an address that names none.
			add(own, NamespaceOrigin{Kind: OriginShortName, Dependency: dependency.Name, Address: dependency.Address})
			continue
		}
		if !ok {
			result.Unresolved = append(result.Unresolved, UnresolvedDependency{Dependency: dependency.Name, Address: dependency.Address, Reason: ReasonNoNamespace})
			continue
		}
		add(namespace, NamespaceOrigin{Kind: OriginDerived, Dependency: dependency.Name, Address: dependency.Address})
	}
	for _, namespace := range configured {
		namespace = strings.TrimSpace(namespace)
		if namespace == "" {
			continue
		}
		if !dnsLabel.MatchString(namespace) {
			result.Unresolved = append(result.Unresolved, UnresolvedDependency{Dependency: OriginConfigured, Address: namespace, Reason: ReasonConfiguredInvalid})
			continue
		}
		add(namespace, NamespaceOrigin{Kind: OriginConfigured})
	}
	window := now.Add(-time.Duration(windowHours) * time.Hour)
	for _, namespace := range order {
		section := sections[namespace]
		r.readNamespace(ctx, section, window)
		result.Namespaces = append(result.Namespaces, *section)
	}
	return result, nil
}

func (r *Reader) readNamespace(ctx context.Context, section *NamespaceWorkloads, window time.Time) {
	core, apps := r.paths(Scope{Namespace: section.Namespace})
	var pods struct {
		Items    []workloadPod `json:"items"`
		Metadata struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
	}
	var replicaSets struct {
		Items    []replicaSetObject `json:"items"`
		Metadata struct {
			Continue string `json:"continue"`
		} `json:"metadata"`
	}
	fail := func(resource string, err error) {
		failure := ReadFailure{Resource: resource, Code: CodeAPIError, Message: err.Error()}
		if named, ok := err.(*Error); ok {
			failure.Code, failure.Message = named.Code, named.Error()
		}
		section.Failures = append(section.Failures, failure)
	}
	if err := r.getJSON(ctx, "pods", core+"/pods", url.Values{"limit": {strconv.Itoa(MaxNamespacePods)}}, &pods); err != nil {
		fail("pods", err)
	}
	if err := r.getJSON(ctx, "replicasets", apps+"/replicasets", url.Values{"limit": {strconv.Itoa(MaxNamespaceReplicaSets)}}, &replicaSets); err != nil {
		fail("replicasets", err)
	}
	section.PodsTruncated = pods.Metadata.Continue != ""
	section.ReplicaSetsTruncated = replicaSets.Metadata.Continue != ""
	section.Workloads, section.WorkloadsTruncated = composeWorkloads(pods.Items, replicaSets.Items, window)
}

type workloadKey struct{ kind, name string }

// composeWorkloads groups Pods and ReplicaSets under the workload that owns
// them, newest rollout first, bounded by MaxWorkloads.
func composeWorkloads(pods []workloadPod, replicaSets []replicaSetObject, window time.Time) ([]Workload, bool) {
	deploymentOf := map[string]string{}
	rollouts := map[string][]replicaSetObject{}
	for _, rs := range replicaSets {
		if deployment := rs.Metadata.controller("Deployment"); deployment != "" {
			deploymentOf[rs.Metadata.Name] = deployment
			rollouts[deployment] = append(rollouts[deployment], rs)
		}
	}
	workloads := map[workloadKey]*Workload{}
	newestPod := map[workloadKey]workloadPod{}
	at := func(key workloadKey) *Workload {
		if workload, ok := workloads[key]; ok {
			return workload
		}
		workload := &Workload{Kind: key.kind, Name: key.name, Images: []ContainerImage{}}
		workloads[key] = workload
		return workload
	}
	for deployment := range rollouts {
		at(workloadKey{"Deployment", deployment})
	}
	for _, pod := range pods {
		key := podWorkload(pod.Metadata, deploymentOf)
		workload := at(key)
		workload.Pods.Total++
		for _, condition := range pod.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				workload.Pods.Ready++
			}
		}
		for _, status := range pod.Status.ContainerStatuses {
			workload.Pods.Restarts += status.RestartCount
		}
		if started := pod.Status.StartTime; started != nil && (workload.Pods.NewestStartedAt == nil || started.After(*workload.Pods.NewestStartedAt)) {
			startedAt := *started
			workload.Pods.NewestStartedAt = &startedAt
		}
		if current, seen := newestPod[key]; !seen || pod.Metadata.CreationTimestamp.After(current.Metadata.CreationTimestamp) {
			newestPod[key] = pod
		}
	}
	for key, workload := range workloads {
		if key.kind == "Deployment" && len(rollouts[key.name]) > 0 {
			describeRollouts(workload, rollouts[key.name])
		} else if pod, ok := newestPod[key]; ok {
			created := pod.Metadata.CreationTimestamp
			workload.LastRolloutAt, workload.RolloutBasis = &created, RolloutBasisPod
			workload.Images = pod.Spec.images()
		}
		workload.InWindow = workload.LastRolloutAt != nil && !workload.LastRolloutAt.Before(window)
	}
	out := make([]Workload, 0, len(workloads))
	for _, workload := range workloads {
		out = append(out, *workload)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i].LastRolloutAt, out[j].LastRolloutAt
		switch {
		case left != nil && right != nil && !left.Equal(*right):
			return left.After(*right)
		case (left == nil) != (right == nil):
			return left != nil
		case out[i].Kind != out[j].Kind:
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > MaxWorkloads {
		return out[:MaxWorkloads], true
	}
	return out, false
}

// podWorkload is the workload a Pod belongs to: its ReplicaSet's Deployment,
// its StatefulSet, DaemonSet or Job, a ReplicaSet no Deployment owns, or the
// Pod itself when nothing owns it.
func podWorkload(meta objectMeta, deploymentOf map[string]string) workloadKey {
	if rs := meta.controller("ReplicaSet"); rs != "" {
		if deployment, ok := deploymentOf[rs]; ok {
			return workloadKey{"Deployment", deployment}
		}
		return workloadKey{"ReplicaSet", rs}
	}
	for _, kind := range []string{"StatefulSet", "DaemonSet", "Job"} {
		if name := meta.controller(kind); name != "" {
			return workloadKey{kind, name}
		}
	}
	return workloadKey{"Pod", meta.Name}
}

// describeRollouts fills a Deployment from its ReplicaSets: the newest by
// revision (then creation) is its current rollout, the one before it what
// that rollout changed from.
func describeRollouts(workload *Workload, replicaSets []replicaSetObject) {
	sort.Slice(replicaSets, func(i, j int) bool {
		left, right := revisionOf(replicaSets[i]), revisionOf(replicaSets[j])
		if left != right {
			return left > right
		}
		return replicaSets[i].Metadata.CreationTimestamp.After(replicaSets[j].Metadata.CreationTimestamp)
	})
	newest := replicaSets[0]
	created := newest.Metadata.CreationTimestamp
	workload.LastRolloutAt, workload.RolloutBasis = &created, RolloutBasisReplicaSet
	workload.Images = newest.Spec.Template.Spec.images()
	if len(replicaSets) > 1 {
		workload.Changed = imageChanges(replicaSets[1].Spec.Template.Spec.images(), workload.Images)
	}
	for i, rs := range replicaSets {
		if i == MaxRollouts {
			break
		}
		rollout := Rollout{ReplicaSet: rs.Metadata.Name, Revision: rs.Metadata.Annotations.Revision,
			CreatedAt: rs.Metadata.CreationTimestamp, Ready: rs.Status.ReadyReplicas, Images: rs.Spec.Template.Spec.images()}
		if rs.Spec.Replicas != nil {
			rollout.Desired = *rs.Spec.Replicas
		}
		workload.Rollouts = append(workload.Rollouts, rollout)
	}
}

func revisionOf(rs replicaSetObject) int64 {
	revision, err := strconv.ParseInt(rs.Metadata.Annotations.Revision, 10, 64)
	if err != nil {
		return 0
	}
	return revision
}

// imageChanges lists the containers whose image differs between two
// rollouts, in the newer rollout's order and then the removed ones.
func imageChanges(before, after []ContainerImage) []ImageChange {
	key := func(image ContainerImage) string { return strconv.FormatBool(image.Init) + "/" + image.Container }
	previous := map[string]ContainerImage{}
	for _, image := range before {
		previous[key(image)] = image
	}
	var changes []ImageChange
	seen := map[string]bool{}
	for _, image := range after {
		seen[key(image)] = true
		if old, ok := previous[key(image)]; !ok || old.Image != image.Image {
			changes = append(changes, ImageChange{Container: image.Container, From: old.Image, To: image.Image, Init: image.Init})
		}
	}
	for _, image := range before {
		if !seen[key(image)] {
			changes = append(changes, ImageChange{Container: image.Container, From: image.Image, Init: image.Init})
		}
	}
	return changes
}
