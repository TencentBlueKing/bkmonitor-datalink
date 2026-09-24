// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"testing"
	"time"
)

// The sentence reads installed over expected and the revision, names the
// first two lagging Workers with why -- not connected, or the failure word
// the Worker last reported, or that it has not answered -- and counts the
// rest; a replica that does not lead says only that, and a Leader with no
// receivers yet says that.
func TestTheViewStreamLineNamesTheLaggingWorkersAndWhy(t *testing.T) {
	for name, test := range map[string]struct {
		facts *ViewStreamFacts
		want  string
	}{
		"nil":        {nil, ""},
		"not leader": {&ViewStreamFacts{Leading: false, Installed: 3, Expected: 4}, "非 leader，不服务视图流"},
		"leader with nobody expected yet": {&ViewStreamFacts{Leading: true, Revision: 3, Sessions: 2},
			"视图流：尚无接收方（版本 3，2 条连接）"},
		"everyone installed": {&ViewStreamFacts{Leading: true, Revision: 12, Expected: 64, Installed: 64},
			"视图已装载 64/64，版本 12"},
		"two lagging, one unconnected, one with a failure word": {&ViewStreamFacts{Leading: true, Revision: 12, Expected: 64, Installed: 62,
			Lagging: []ViewStreamLagging{{WorkerID: "w17", Connected: false}, {WorkerID: "w23", Connected: true, Failure: "DELTA_DIGEST_MISMATCH"}}},
			"视图已装载 62/64，版本 12；落后：w17（未连接）、w23（DELTA_DIGEST_MISMATCH）"},
		"a connected worker that has not answered": {&ViewStreamFacts{Leading: true, Revision: 5, Expected: 4, Installed: 3,
			Lagging: []ViewStreamLagging{{WorkerID: "w02", Connected: true}}},
			"视图已装载 3/4，版本 5；落后：w02（未回执）"},
		"five lagging names two and counts all": {&ViewStreamFacts{Leading: true, Revision: 12, Expected: 64, Installed: 59,
			Lagging: []ViewStreamLagging{{WorkerID: "w01"}, {WorkerID: "w02"}, {WorkerID: "w03"}, {WorkerID: "w04"}, {WorkerID: "w05"}}},
			"视图已装载 59/64，版本 12；落后：w01（未连接）、w02（未连接） 等 5 个"},
		// The installed Workers' objects: silent when every one probed and
		// found its objects; the missing count over the Workers that probed;
		// and the Workers that could not probe, by name -- "cannot tell",
		// said apart from "nothing missing", which is what a silent clause
		// would claim for them. Two of the three cells give a number the
		// third does not, so a clause that read Missing alone fails both.
		"everyone probed, nothing missing": {&ViewStreamFacts{Leading: true, Revision: 8, Expected: 4, Installed: 4,
			Objects: ViewStreamObjects{Probed: 4, UnprobedWorkers: []string{}}},
			"视图已装载 4/4，版本 8"},
		"objects missing on the workers that probed": {&ViewStreamFacts{Leading: true, Revision: 8, Expected: 4, Installed: 4,
			Objects: ViewStreamObjects{Probed: 4, Missing: 7, UnprobedWorkers: []string{}}},
			"视图已装载 4/4，版本 8；缺对象 7（4 个副本探到）"},
		"one worker could not probe, named": {&ViewStreamFacts{Leading: true, Revision: 8, Expected: 4, Installed: 4,
			Objects: ViewStreamObjects{Probed: 3, Unprobed: 1, UnprobedWorkers: []string{"w03"}}},
			"视图已装载 4/4，版本 8；1 个副本未探到对象（w03）"},
		"three could not probe, two named, and one lagging after": {&ViewStreamFacts{Leading: true, Revision: 8, Expected: 5, Installed: 4,
			Objects: ViewStreamObjects{Probed: 1, Unprobed: 3, Missing: 2, UnprobedWorkers: []string{"w01", "w02", "w03"}},
			Lagging: []ViewStreamLagging{{WorkerID: "w05", Connected: true}}},
			"视图已装载 4/5，版本 8；缺对象 2（1 个副本探到）；3 个副本未探到对象（w01、w02）；落后：w05（未回执）"},
	} {
		if got := ViewStreamLine(test.facts); got != test.want {
			t.Fatalf("%s: line = %q, want %q", name, got, test.want)
		}
	}
}

// The aggregate carries the Leader's account: a snapshot that leads displaces
// one that does not however new, a newer Leader displaces an older one, and
// with no Leader among the counted replicas the newest non-Leader's "not
// leading" stands so the page can say no Leader serves the stream. The
// sentence travels as published; the route does not recompose it.
func TestTheVerdictRouteCarriesTheLeadersViewStream(t *testing.T) {
	snapshots := healthySnapshots()
	leader := &ViewStreamFacts{At: now.Add(-2 * time.Minute), Leading: true, Revision: 12, Expected: 64, Installed: 62, Sessions: 63,
		Lagging: []ViewStreamLagging{{WorkerID: "w17"}, {WorkerID: "w23", Connected: true, Failure: "DELTA_DIGEST_MISMATCH"}}}
	leader.Line = ViewStreamLine(leader)
	follower := &ViewStreamFacts{At: now.Add(-time.Minute), Leading: false, Line: ViewStreamLine(&ViewStreamFacts{})}
	snapshots[0].ViewStream = follower
	snapshots[1].ViewStream = leader
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	stream, _ := health["view_stream"].(map[string]any)
	if stream == nil || stream["leading"] != true || stream["installed"] != 62.0 || stream["expected"] != 64.0 || stream["revision"] != 12.0 ||
		stream["line"] != "视图已装载 62/64，版本 12；落后：w17（未连接）、w23（DELTA_DIGEST_MISMATCH）" {
		t.Fatalf("view_stream = %v, want the Leader's account over the newer follower's", health["view_stream"])
	}
	if health["view_stream_replica"] != snapshots[1].Replica {
		t.Fatalf("view_stream_replica = %v, want %s", health["view_stream_replica"], snapshots[1].Replica)
	}
	lagging, _ := stream["lagging"].([]any)
	if len(lagging) != 2 {
		t.Fatalf("lagging = %v, want both Workers", stream["lagging"])
	}

	// A newer Leader displaces an older one.
	newer := *leader
	newer.At, newer.Installed, newer.Lagging = now.Add(-30*time.Second), 64, nil
	newer.Line = ViewStreamLine(&newer)
	snapshots[0].ViewStream = &newer
	handler = handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health = get(t, handler, "/api/health")
	if stream, _ := health["view_stream"].(map[string]any); stream["installed"] != 64.0 || health["view_stream_replica"] != snapshots[0].Replica {
		t.Fatalf("view_stream = %v replica %v, want the newer Leader's", health["view_stream"], health["view_stream_replica"])
	}
	// Nobody lagging is an empty list on the wire, not null: the copy the
	// aggregate makes must stay a list when there is nothing to copy.
	if stream, _ := health["view_stream"].(map[string]any); stream["lagging"] == nil {
		t.Fatalf("view_stream.lagging = null with nobody lagging: %v", health["view_stream"])
	} else if list, ok := stream["lagging"].([]any); !ok || len(list) != 0 {
		t.Fatalf("view_stream.lagging = %v, want an empty list", stream["lagging"])
	}

	// No Leader among the counted replicas: the newest follower's account.
	snapshots[0].ViewStream, snapshots[1].ViewStream = follower, &ViewStreamFacts{At: now.Add(-3 * time.Minute), Leading: false}
	handler = handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health = get(t, handler, "/api/health")
	if stream, _ := health["view_stream"].(map[string]any); stream == nil || stream["leading"] != false || stream["line"] != "非 leader，不服务视图流" || health["view_stream_replica"] != snapshots[0].Replica {
		t.Fatalf("view_stream with no Leader = %v replica %v, want the newest follower's 'not leading'", health["view_stream"], health["view_stream_replica"])
	}

	// Nobody published: absent.
	snapshots[0].ViewStream, snapshots[1].ViewStream = nil, nil
	handler = handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health = get(t, handler, "/api/health")
	if _, present := health["view_stream"]; present {
		t.Fatalf("view_stream present with nobody publishing: %v", health["view_stream"])
	}
}
