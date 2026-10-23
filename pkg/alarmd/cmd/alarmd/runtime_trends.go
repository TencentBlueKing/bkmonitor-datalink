// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kubeclient"
)

// The trends read this deployment's numbers back from the platform's
// collection of them, in the space the platform files them in. Which space
// that is was decided where the collection was wired, and the wiring says
// so: bk-monitor-operator's DataID resources carry the business each kind
// of data lands in. A ServiceMonitor without a DataID of its own lands in
// the common custom-metric one; container metrics in the system k8s metric
// one. A cluster registered with more than one platform has one of each
// per platform, so every candidate business is asked, and the first space
// that has the metric is the one.

// dataIDResource is bk-monitor-operator's DataID, cluster-scoped.
var dataIDResource = schema.GroupVersionResource{Group: "monitoring.bk.tencent.com", Version: "v1beta1", Resource: "dataids"}

// trendContainer is the alarmd container's name in the workloads the chart
// renders.
const trendContainer = "alarmd"

// How long a derived space answers: a resolved one for ten minutes, an
// unresolved one for five, since the reason is usually a collection that is
// absent rather than one just starting, and each derivation probes every
// candidate.
const (
	trendSpaceResolvedFor   = 10 * time.Minute
	trendSpaceUnresolvedFor = 5 * time.Minute
)

// trendProbeWindow is how far back a candidate space is asked for the
// metric that says it is the one.
const trendProbeWindow = 10 * time.Minute

// trendSpaceDeriveTimeout bounds one derivation: the DataID list and every
// candidate's probe.
const trendSpaceDeriveTimeout = 30 * time.Second

// The DataID list is read in pages, up to a cap; an install with more is a
// named cut, not a silent one.
const (
	dataIDPage  = 500
	dataIDPages = 10
)

type dataIDLister func(ctx context.Context) ([]unstructured.Unstructured, error)

// spaceProbe says whether a space has samples of an expression lately.
type spaceProbe func(ctx context.Context, space, promQL string) (bool, error)

// trendSpaces derives the two spaces and keeps them for their answer time.
type trendSpaces struct {
	list      dataIDLister
	probe     spaceProbe
	namespace string
	now       func() time.Time

	mu     sync.Mutex
	at     time.Time
	ttl    time.Duration
	spaces map[string]fleet.TrendSpace
}

// trendKind is a trend space: the DataID label that says a DataID carries
// it, the expression whose samples say a candidate is the one, and what no
// candidate having them means.
type trendKind struct {
	key, isSystem, probe, absent string
}

// spaceKinds is the two trend spaces. The container probe is the
// namespace's container data, not alarmd's own container's: whether that
// has series is the charts' answer, so a node the collector skips is said
// on the charts rather than unresolving the space.
func (spaces *trendSpaces) spaceKinds() []trendKind {
	return []trendKind{
		{fleet.TrendSpaceSelf, "false", fleet.SelfSpaceProbe,
			"no candidate space has samples of " + fleet.SelfSpaceProbe + " in the last 10 minutes; most likely alarmd's ServiceMonitor is not collected into this platform's metadata"},
		{fleet.TrendSpaceContainer, "true", fmt.Sprintf(`count(container_memory_working_set_bytes{namespace=%q})`, spaces.namespace),
			fmt.Sprintf("no candidate space has container data for namespace %q in the last 10 minutes", spaces.namespace)},
	}
}

func (spaces *trendSpaces) get(context.Context) map[string]fleet.TrendSpace {
	spaces.mu.Lock()
	defer spaces.mu.Unlock()
	at := spaces.now()
	if spaces.spaces != nil && at.Sub(spaces.at) < spaces.ttl {
		return spaces.spaces
	}
	// Derived on its own bounded context: the answer is cached for every
	// caller, so one caller leaving must not make it unresolved.
	ctx, cancel := context.WithTimeout(context.Background(), trendSpaceDeriveTimeout)
	defer cancel()
	derived := map[string]fleet.TrendSpace{}
	resolved := true
	items, err := spaces.list(ctx)
	for _, kind := range spaces.spaceKinds() {
		var space fleet.TrendSpace
		if err != nil {
			space = fleet.TrendSpace{State: fleet.TrendSpaceUnresolved, Reason: "the operator's DataID resources could not be read: " + err.Error()}
		} else {
			space = spaces.derive(ctx, items, kind)
		}
		derived[kind.key] = space
		resolved = resolved && space.State == fleet.TrendSpaceResolved
	}
	spaces.spaces, spaces.at, spaces.ttl = derived, at, trendSpaceResolvedFor
	if !resolved {
		spaces.ttl = trendSpaceUnresolvedFor
	}
	return derived
}

// derive is one space: the businesses of the common metric DataIDs of the
// kind, in order, each asked for the probe's samples; the first that has
// them.
func (spaces *trendSpaces) derive(ctx context.Context, items []unstructured.Unstructured, kind trendKind) fleet.TrendSpace {
	isSystem, probe := kind.isSystem, kind.probe
	businesses := map[string]bool{}
	for _, item := range items {
		labels := item.GetLabels()
		if labels["usage"] != "metric" || labels["isCommon"] != "true" || labels["isSystem"] != isSystem {
			continue
		}
		business, _, _ := unstructured.NestedString(item.Object, "spec", "labels", "bk_biz_id")
		if id, err := strconv.Atoi(business); err == nil && id > 0 {
			businesses[business] = true
		}
	}
	candidates := make([]string, 0, len(businesses))
	for business := range businesses {
		candidates = append(candidates, "bkcc__"+business)
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return fleet.TrendSpace{State: fleet.TrendSpaceUnresolved,
			Reason: fmt.Sprintf("no DataID with usage=metric, isCommon=true, isSystem=%s names a business", isSystem)}
	}
	var failures []string
	for _, candidate := range candidates {
		found, err := spaces.probe(ctx, candidate, probe)
		if err != nil {
			failures = append(failures, candidate+": "+err.Error())
			continue
		}
		if found {
			return fleet.TrendSpace{Space: candidate, State: fleet.TrendSpaceResolved, Candidates: candidates}
		}
	}
	reason := kind.absent
	if len(failures) > 0 {
		reason += "; " + strings.Join(failures, "; ")
	}
	return fleet.TrendSpace{State: fleet.TrendSpaceUnresolved, Reason: reason, Candidates: candidates}
}

// trendRange adapts the query client the evaluation path uses.
type trendRange struct{ client *accessuq.Client }

func (provider trendRange) Range(ctx context.Context, space, promQL string, start, end time.Time, step time.Duration) (fleet.SeriesRange, error) {
	result, err := provider.client.Range(ctx, accessuq.RangeRequest{PromQL: promQL, SpaceUID: space, Start: start, End: end, Step: step})
	if err != nil {
		return fleet.SeriesRange{}, err
	}
	lines := make([]fleet.SeriesLine, 0, len(result.Series))
	for _, series := range result.Series {
		points := make([]fleet.SeriesPoint, 0, len(series.Points))
		for _, point := range series.Points {
			points = append(points, fleet.SeriesPoint{AtUnixMilli: point.AtUnixMilli, Value: point.Value})
		}
		lines = append(lines, fleet.SeriesLine{Labels: series.Labels, Points: points})
	}
	return fleet.SeriesRange{Lines: lines, Code: result.Code, Message: result.Message, Partial: result.Partial}, nil
}

// probeWith asks a space for an expression's samples over the probe window.
func probeWith(provider fleet.RangeProvider, now func() time.Time) spaceProbe {
	return func(ctx context.Context, space, promQL string) (bool, error) {
		at := now()
		result, err := provider.Range(ctx, space, promQL, at.Add(-trendProbeWindow), at, time.Minute)
		if err != nil {
			return false, err
		}
		// A refusal (a code) comes with no samples, and is no.
		for _, line := range result.Lines {
			if len(line.Points) > 0 {
				return true, nil
			}
		}
		return false, nil
	}
}

// inClusterDataIDs lists the DataIDs with this process's ServiceAccount;
// out of a cluster every list says so.
func inClusterDataIDs() dataIDLister {
	var once sync.Once
	var client dynamic.Interface
	var setup error
	return func(ctx context.Context) ([]unstructured.Unstructured, error) {
		once.Do(func() {
			config, err := kubeclient.InCluster()
			if err != nil {
				setup = fmt.Errorf("not in a cluster: %w", err)
				return
			}
			client, setup = dynamic.NewForConfig(config)
		})
		if setup != nil {
			return nil, setup
		}
		return pagedDataIDs(ctx, func(ctx context.Context, token string) ([]unstructured.Unstructured, string, error) {
			list, err := client.Resource(dataIDResource).List(ctx, metav1.ListOptions{Limit: dataIDPage, Continue: token})
			if err != nil {
				return nil, "", err
			}
			return list.Items, list.GetContinue(), nil
		})
	}
}

// fleetTrends is the trends read, or nil without a query client: the route
// is then absent, which says there is nothing to read with.
func fleetTrends(client *accessuq.Client, namespace string, now func() time.Time) *fleet.Trends {
	if client == nil {
		return nil
	}
	provider := trendRange{client: client}
	spaces := &trendSpaces{list: inClusterDataIDs(), probe: probeWith(provider, now), namespace: namespace, now: now}
	// The shared read's time is its parts' own bounds added: the space
	// derivation, the node read, and one range query, the lines running side
	// by side.
	return &fleet.Trends{Range: provider, Spaces: spaces.get, Scope: fleet.TrendScope{Namespace: namespace, Container: trendContainer},
		Node: cachedNode(inClusterPodNode(namespace)), ComputeTimeout: trendSpaceDeriveTimeout + podNodeReadTimeout + accessuq.RangeDeadline}
}

var podResource = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// podNodeReadTimeout bounds the read of this replica's own Pod.
const podNodeReadTimeout = 3 * time.Second

// inClusterPodNode reads the node of this replica's own Pod, named by its
// hostname, with this process's ServiceAccount.
func inClusterPodNode(namespace string) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		// Its own short bound: the read runs inside the charts' shared
		// budget, which a hanging API call must not use up.
		ctx, cancel := context.WithTimeout(ctx, podNodeReadTimeout)
		defer cancel()
		name, err := os.Hostname()
		if err != nil {
			return "", err
		}
		config, err := kubeclient.InCluster()
		if err != nil {
			return "", err
		}
		client, err := dynamic.NewForConfig(config)
		if err != nil {
			return "", err
		}
		pod, err := client.Resource(podResource).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		node, _, _ := unstructured.NestedString(pod.Object, "spec", "nodeName")
		return node, nil
	}
}

// cachedNode keeps the first node a read answers; a failed or empty read
// says nothing and is tried again on the next chart read.
func cachedNode(read func(ctx context.Context) (string, error)) func(ctx context.Context) string {
	var mu sync.Mutex
	known := ""
	return func(ctx context.Context) string {
		mu.Lock()
		defer mu.Unlock()
		if known == "" {
			if node, err := read(ctx); err == nil {
				known = node
			}
		}
		return known
	}
}

// pagedDataIDs reads every page, up to dataIDPages of them; more is an
// error that says so, never a silent cut.
func pagedDataIDs(ctx context.Context, page func(ctx context.Context, token string) ([]unstructured.Unstructured, string, error)) ([]unstructured.Unstructured, error) {
	var items []unstructured.Unstructured
	token := ""
	for count := 0; count < dataIDPages; count++ {
		read, next, err := page(ctx, token)
		if err != nil {
			return nil, err
		}
		items = append(items, read...)
		if token = next; token == "" {
			return items, nil
		}
	}
	return nil, fmt.Errorf("more than %d DataIDs; the candidates would be cut", dataIDPage*dataIDPages)
}
