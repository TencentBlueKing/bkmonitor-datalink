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
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// dataID is one DataID resource as bk-monitor-operator's are: its usage
// labels on the object, its business in spec.labels.
func dataID(name, usage, isCommon, isSystem, business string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "monitoring.bk.tencent.com/v1beta1", "kind": "DataID",
		"metadata": map[string]any{"name": name, "labels": map[string]any{"usage": usage, "isCommon": isCommon, "isSystem": isSystem}},
		"spec":     map[string]any{"dataID": 1, "labels": map[string]any{"bcs_cluster_id": "BCS-K8S-00000", "bk_biz_id": business}},
	}}
}

type probeCall struct{ space, promQL string }

func spacesUnder(items []unstructured.Unstructured, listErr error, has map[string]bool, at *time.Time) (*trendSpaces, *[]probeCall, *int) {
	var calls []probeCall
	lists := 0
	spaces := &trendSpaces{
		list: func(context.Context) ([]unstructured.Unstructured, error) {
			lists++
			return items, listErr
		},
		probe: func(_ context.Context, space, promQL string) (bool, error) {
			calls = append(calls, probeCall{space, promQL})
			return has[space+"|"+strings.SplitN(promQL, "(", 3)[1]], nil
		},
		namespace: "alarmd-ns", now: func() time.Time { return *at },
	}
	return spaces, &calls, &lists
}

// The cluster registered with two platforms has two common custom-metric
// DataIDs: each business is asked, in order, and the space that has the
// metric is the one. The container space is derived the same way from the
// system k8s metric DataIDs; a DataID of a ServiceMonitor of its own, or
// for events, or with no positive business, is no candidate.
func TestTheTrendSpacesAreDerivedFromTheOperatorsDataIDs(t *testing.T) {
	at := time.Unix(1789006380, 0)
	items := []unstructured.Unstructured{
		dataID("custommetricdataid", "metric", "true", "false", "106"),
		dataID("platform-a-custommetricdataid", "metric", "true", "false", "2"),
		dataID("platform-a-k8smetricdataid", "metric", "true", "true", "2"),
		dataID("platform-a-k8seventdataid", "event", "true", "true", "5"),
		dataID("exporter-9", "metric", "false", "false", "9"),
		dataID("space-scoped", "metric", "true", "false", "-4"),
	}
	has := map[string]bool{"bkcc__2|alarmd_build_info)": true, "bkcc__2|container_memory_working_set_bytes{namespace=\"alarmd-ns\"})": true}
	spaces, calls, _ := spacesUnder(items, nil, has, &at)
	got := spaces.get(context.Background())
	self, container := got[fleet.TrendSpaceSelf], got[fleet.TrendSpaceContainer]
	if self.State != fleet.TrendSpaceResolved || self.Space != "bkcc__2" || strings.Join(self.Candidates, ",") != "bkcc__106,bkcc__2" {
		t.Fatalf("self space = %+v, want bkcc__2 of the two candidates", self)
	}
	if container.State != fleet.TrendSpaceResolved || container.Space != "bkcc__2" || strings.Join(container.Candidates, ",") != "bkcc__2" {
		t.Fatalf("container space = %+v, want bkcc__2, the system DataID's", container)
	}
	if len(*calls) != 3 || (*calls)[0].space != "bkcc__106" || (*calls)[1].space != "bkcc__2" {
		t.Fatalf("probes = %+v, want 106 then 2 for self, then 2 for containers", *calls)
	}
	// The container space is the one with this cluster's container data for
	// the namespace; whether alarmd's own container has series is the
	// charts' answer, so a node the collector skips does not unresolve it.
	if probe := (*calls)[2].promQL; probe != `count(container_memory_working_set_bytes{namespace="alarmd-ns"})` {
		t.Fatalf("container probe = %s, want this deployment's namespace alone", probe)
	}
}

// Each way a space cannot be derived is named: the DataIDs unreadable, no
// DataID of the kind, or no candidate with the metric.
func TestAnUnresolvedSpaceSaysWhy(t *testing.T) {
	at := time.Unix(1789006380, 0)
	spaces, _, _ := spacesUnder(nil, errors.New("dataids.monitoring.bk.tencent.com is forbidden"), nil, &at)
	if self := spaces.get(context.Background())[fleet.TrendSpaceSelf]; self.State != fleet.TrendSpaceUnresolved ||
		!strings.Contains(self.Reason, "could not be read") || !strings.Contains(self.Reason, "forbidden") {
		t.Fatalf("unreadable DataIDs: %+v", self)
	}
	spaces, _, _ = spacesUnder([]unstructured.Unstructured{dataID("k8smetricdataid", "metric", "true", "true", "2")}, nil, nil, &at)
	if self := spaces.get(context.Background())[fleet.TrendSpaceSelf]; self.State != fleet.TrendSpaceUnresolved ||
		!strings.Contains(self.Reason, "isSystem=false") {
		t.Fatalf("no custom-metric DataID: %+v", self)
	}
	spaces, _, _ = spacesUnder([]unstructured.Unstructured{dataID("custommetricdataid", "metric", "true", "false", "7")}, nil, nil, &at)
	if self := spaces.get(context.Background())[fleet.TrendSpaceSelf]; self.State != fleet.TrendSpaceUnresolved ||
		!strings.Contains(self.Reason, "no candidate space has samples of count(alarmd_build_info)") || len(self.Candidates) != 1 ||
		!strings.Contains(self.Reason, "; most likely alarmd's ServiceMonitor is not collected into this platform's metadata") {
		t.Fatalf("no candidate with the metric: %+v", self)
	}
	spaces, _, _ = spacesUnder([]unstructured.Unstructured{dataID("k8smetricdataid", "metric", "true", "true", "7")}, nil, nil, &at)
	if container := spaces.get(context.Background())[fleet.TrendSpaceContainer]; container.State != fleet.TrendSpaceUnresolved ||
		!strings.Contains(container.Reason, `no candidate space has container data for namespace "alarmd-ns"`) {
		t.Fatalf("no candidate with the namespace's container data: %+v", container)
	}
}

// A resolved pair answers for ten minutes; an unresolved one for five: the
// reason is usually a collection that is absent, not one just starting, and
// each derivation probes every candidate.
func TestDerivedSpacesAnswerForTheirTime(t *testing.T) {
	at := time.Unix(1789006380, 0)
	items := []unstructured.Unstructured{dataID("c", "metric", "true", "false", "2"), dataID("k", "metric", "true", "true", "2")}
	has := map[string]bool{"bkcc__2|alarmd_build_info)": true, "bkcc__2|container_memory_working_set_bytes{namespace=\"alarmd-ns\"})": true}
	spaces, _, lists := spacesUnder(items, nil, has, &at)
	spaces.get(context.Background())
	at = at.Add(trendSpaceResolvedFor - time.Second)
	spaces.get(context.Background())
	if *lists != 1 {
		t.Fatalf("listed %d times within ten minutes of a resolved pair, want once", *lists)
	}
	at = at.Add(time.Second)
	spaces.get(context.Background())
	if *lists != 2 {
		t.Fatalf("listed %d times at ten minutes, want again", *lists)
	}
	unresolved, _, unresolvedLists := spacesUnder(nil, errors.New("forbidden"), nil, &at)
	unresolved.get(context.Background())
	at = at.Add(trendSpaceUnresolvedFor - time.Second)
	unresolved.get(context.Background())
	at = at.Add(time.Second)
	unresolved.get(context.Background())
	if *unresolvedLists != 2 || trendSpaceUnresolvedFor != 5*time.Minute {
		t.Fatalf("an unresolved pair listed %d times over %v, want twice over five minutes", *unresolvedLists, trendSpaceUnresolvedFor)
	}
}

// A candidate is the one when the provider has samples of the probe in the
// window; a refusal or an empty answer is not.
func TestAProbeIsSamplesNotAnAnswer(t *testing.T) {
	at := time.Unix(1789006380, 0)
	answers := map[string]fleet.SeriesRange{
		"bkcc__1": {Lines: []fleet.SeriesLine{{Points: []fleet.SeriesPoint{{AtUnixMilli: 1, Value: 1}}}}},
		"bkcc__2": {Code: "SPACE_IS_NOT_EXISTS"},
		"bkcc__3": {Lines: []fleet.SeriesLine{{Points: []fleet.SeriesPoint{}}}},
	}
	provider := rangeFunc(func(space string) (fleet.SeriesRange, error) { return answers[space], nil })
	probe := probeWith(provider, func() time.Time { return at })
	for space, want := range map[string]bool{"bkcc__1": true, "bkcc__2": false, "bkcc__3": false} {
		if got, err := probe(context.Background(), space, "count(x)"); err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", space, got, err, want)
		}
	}
}

type rangeFunc func(space string) (fleet.SeriesRange, error)

func (f rangeFunc) Range(_ context.Context, space, _ string, _, _ time.Time, _ time.Duration) (fleet.SeriesRange, error) {
	return f(space)
}

// The DataIDs are read page by page; an install past the cap is said, not
// cut.
func TestTheDataIDsAreReadPageByPage(t *testing.T) {
	pages := 0
	items, err := pagedDataIDs(context.Background(), func(_ context.Context, token string) ([]unstructured.Unstructured, string, error) {
		pages++
		next := map[string]string{"": "p2", "p2": "p3", "p3": ""}[token]
		return []unstructured.Unstructured{dataID("d-"+token, "metric", "true", "false", "2")}, next, nil
	})
	if err != nil || len(items) != 3 || pages != 3 {
		t.Fatalf("items %d pages %d err %v, want 3 of each", len(items), pages, err)
	}
	_, err = pagedDataIDs(context.Background(), func(context.Context, string) ([]unstructured.Unstructured, string, error) {
		return nil, "more", nil
	})
	if err == nil || !strings.Contains(err.Error(), "the candidates would be cut") {
		t.Fatalf("err %v, want the cut named", err)
	}
}

// The spaces are derived on their own context: a caller that has already
// left does not leave every space unresolved for the minute after.
func TestDerivingTheSpacesDoesNotUseTheCallersContext(t *testing.T) {
	at := time.Unix(1789006380, 0)
	items := []unstructured.Unstructured{dataID("c", "metric", "true", "false", "2"), dataID("k", "metric", "true", "true", "2")}
	spaces := &trendSpaces{
		list: func(ctx context.Context) ([]unstructured.Unstructured, error) { return items, ctx.Err() },
		probe: func(ctx context.Context, _, _ string) (bool, error) {
			return ctx.Err() == nil, ctx.Err()
		},
		namespace: "alarmd-ns", now: func() time.Time { return at },
	}
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	got := spaces.get(gone)
	if got[fleet.TrendSpaceSelf].State != fleet.TrendSpaceResolved || got[fleet.TrendSpaceContainer].State != fleet.TrendSpaceResolved {
		t.Fatalf("spaces %+v, want both resolved despite the caller's cancelled context", got)
	}
}

// The node this replica runs on is read from its own Pod once it answers;
// a failed read says nothing and is tried again on the next chart read.
func TestTheReplicasNodeIsReadOnceItAnswers(t *testing.T) {
	reads := 0
	failing := true
	node := cachedNode(func(context.Context) (string, error) {
		reads++
		if failing {
			return "", errors.New("pods is forbidden")
		}
		return "node-7", nil
	})
	if got := node(context.Background()); got != "" {
		t.Fatalf("a failed read said %q", got)
	}
	failing = false
	if got := node(context.Background()); got != "node-7" || reads != 2 {
		t.Fatalf("after a failure: %q after %d reads, want node-7 read again", got, reads)
	}
	if got := node(context.Background()); got != "node-7" || reads != 2 {
		t.Fatalf("an answered node was read again: %q after %d reads", got, reads)
	}
}

// The trends' shared read has its parts' own bounds added: the space
// derivation, the node read, and one range query, the lines running side by
// side.
func TestTheTrendsReadHasItsPartsTime(t *testing.T) {
	client, err := accessuq.NewClient("http://uq.invalid", "alarmd-test", &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	trends := fleetTrends(client, "alarmd-test", time.Now)
	if want := trendSpaceDeriveTimeout + podNodeReadTimeout + accessuq.RangeDeadline; trends.ComputeTimeout != want {
		t.Fatalf("shared read %v, want the parts' %v", trends.ComputeTimeout, want)
	}
}
