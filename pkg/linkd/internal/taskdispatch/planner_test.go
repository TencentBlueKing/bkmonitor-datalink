// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskdispatch

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func fixture() (State, eventsource.Release, time.Time) {
	now := time.Now()
	s := newState()
	for i := range 8 {
		id := fmt.Sprint(i)
		s.Workers[id] = Worker{ID: id, Roles: []string{"cleaner", "lifecycle"}, Labels: map[string]string{"pool": "a"}, Seen: now, MaxTasks: 16}
	}
	spec := config.EventSource{EventSourceID: "source", Enabled: true}.WithDefaults()
	r := eventsource.Release{ID: "source", Version: 1, Spec: spec}
	s.Metadata[r.ID] = UpdateMetadata(Metadata{}, spec, "topic-id", 3, nil, now)
	return s, r, now
}

func count(s State, role string) int {
	n := 0
	for _, task := range s.Tasks {
		if task.Role == role && task.Phase != "stopped" {
			n++
		}
	}
	return n
}

func TestPartitionCapAndExpansion(t *testing.T) {
	s, r, now := fixture()
	Reconcile(&s, []eventsource.Release{r}, now)
	if count(s, "cleaner") != 3 || count(s, "lifecycle") != 8 {
		t.Fatal("wrong replica cap")
	}
	epochs := map[string]int64{}
	for id, task := range s.Tasks {
		epochs[id] = task.Epoch
	}
	s.Metadata[r.ID] = UpdateMetadata(s.Metadata[r.ID], r.Spec, "topic-id", 5, nil, now)
	Reconcile(&s, []eventsource.Release{r}, now)
	if count(s, "cleaner") != 5 {
		t.Fatal("did not scale")
	}
	for id, epoch := range epochs {
		if s.Tasks[id].Epoch != epoch {
			t.Fatal("healthy assignment replaced")
		}
	}
	seen := map[string]bool{}
	for _, task := range s.Tasks {
		key := task.Worker + task.Source + task.Role
		if seen[key] {
			t.Fatal("duplicate worker source role")
		}
		seen[key] = true
	}
}

func TestProbeFailureDoesNotExpandOrClear(t *testing.T) {
	s, r, now := fixture()
	Reconcile(&s, []eventsource.Release{r}, now)
	s.Metadata[r.ID] = UpdateMetadata(s.Metadata[r.ID], r.Spec, "", 0, errors.New("offline"), now)
	Reconcile(&s, []eventsource.Release{r}, now)
	if count(s, "cleaner") != 3 {
		t.Fatal("probe error changed count")
	}
	for _, task := range s.Tasks {
		if task.Role == "cleaner" && task.Phase == "stopping" {
			t.Fatal("error revoked source")
		}
	}
	m := UpdateMetadata(s.Metadata[r.ID], r.Spec, "different", 5, nil, now)
	if m.Partitions != 3 || m.Error == "" {
		t.Fatal("topic recreate accepted")
	}
	m = UpdateMetadata(s.Metadata[r.ID], r.Spec, "topic-id", 2, nil, now)
	if m.Partitions != 3 || m.Error == "" {
		t.Fatal("partition decrease accepted")
	}
}

func TestLabelsZeroAndStopHandshake(t *testing.T) {
	s, r, now := fixture()
	w := s.Workers["0"]
	w.Explicit = true
	s.Workers["0"] = w
	Reconcile(&s, []eventsource.Release{r}, now)
	for _, task := range s.Tasks {
		if task.Worker == "0" {
			t.Fatal("explicit worker received empty selector")
		}
	}
	zero := 0
	r.Spec.Scheduling.Cleaner.Replicas.Number = &zero
	r.Spec.Scheduling.Lifecycle.Selector = map[string]string{"pool": "b"}
	Reconcile(&s, []eventsource.Release{r}, now)
	for _, task := range s.Tasks {
		if task.Phase != "stopping" {
			t.Fatal("zero/selector did not revoke")
		}
	}
	n := len(s.Tasks)
	Reconcile(&s, []eventsource.Release{r}, now.Add(time.Second))
	if len(s.Tasks) != n {
		t.Fatal("started before stopping")
	}
}

func TestInitialUnknownMetadataBlocksOnlyCleaner(t *testing.T) {
	s, r, now := fixture()
	s.Metadata = map[string]Metadata{}
	Reconcile(&s, []eventsource.Release{r}, now)
	if count(s, "cleaner") != 0 || count(s, "lifecycle") != 8 {
		t.Fatal("unknown metadata handling")
	}
}

func TestFailedConnectionChangeKeepsOldReleaseUntilProbeSucceeds(t *testing.T) {
	s, r, now := fixture()
	Reconcile(&s, []eventsource.Release{r}, now)
	r.Version = 2
	r.Spec.Storage.Kafka.FetchMaxWaitMilliseconds = 200
	s.Metadata[r.ID] = UpdateMetadata(s.Metadata[r.ID], r.Spec, "", 0, errors.New("probe failed"), now)
	Reconcile(&s, []eventsource.Release{r}, now)
	for _, task := range s.Tasks {
		if task.Role == "cleaner" && (task.Phase == "stopping" || task.Version != 1) {
			t.Fatal("failed preflight disrupted old flow")
		}
	}
	s.Metadata[r.ID] = UpdateMetadata(s.Metadata[r.ID], r.Spec, "topic-id", 3, nil, now)
	Reconcile(&s, []eventsource.Release{r}, now)
	for _, task := range s.Tasks {
		if task.Role == "cleaner" && task.Phase != "stopping" {
			t.Fatal("successful preflight did not request switch")
		}
	}
}

func TestExpiredSlotMovesOnlyAfterFullLeaseAndMargin(t *testing.T) {
	s, r, now := fixture()
	Reconcile(&s, []eventsource.Release{r}, now)
	var original Task
	for _, task := range s.Tasks {
		if task.Role == "cleaner" {
			original = task
			break
		}
	}
	w := s.Workers[original.Worker]
	w.Seen = now.Add(-20 * time.Second)
	s.Workers[original.Worker] = w
	Reconcile(&s, []eventsource.Release{r}, now)
	if s.Tasks[original.ID].Epoch != original.Epoch || s.Tasks[original.ID].Phase != "stopping" {
		t.Fatal("moved before authorization expired")
	}
	future := original.Expires.Add(SafetyMargin + time.Millisecond)
	for id, w := range s.Workers {
		if id != original.Worker {
			w.Seen = future
			s.Workers[id] = w
		}
	}
	for id, task := range s.Tasks {
		if task.Worker != original.Worker {
			task.Expires = future.Add(LeaseTTL)
			s.Tasks[id] = task
		}
	}
	Reconcile(&s, []eventsource.Release{r}, future)
	replacement := s.Tasks[original.ID]
	if replacement.Epoch <= original.Epoch || replacement.Worker == original.Worker || len(replacement.Retired) == 0 {
		t.Fatal("safe timeout takeover did not occur")
	}
}

func TestWorkerBudgetDoesNotSilentlyChangeDesiredCount(t *testing.T) {
	s, r, now := fixture()
	for id, w := range s.Workers {
		w.MaxConcurrency = 1
		s.Workers[id] = w
	}
	Reconcile(&s, []eventsource.Release{r}, now)
	if len(s.Tasks) != 0 {
		t.Fatal("exceeded process budget")
	}
	if s.Statuses[0].Target != 3 {
		t.Fatal("capacity silently reduced desired count")
	}
}
