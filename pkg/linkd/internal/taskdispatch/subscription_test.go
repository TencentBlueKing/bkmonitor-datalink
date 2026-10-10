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
	"testing"

	"linkd/internal/config"
	"linkd/internal/eventsource"
)

func TestSubscriptionUpdateReplacesStoppedTasksAndRefreshesTopicIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*config.KafkaStorageConfig)
	}{
		{"topic", func(k *config.KafkaStorageConfig) { k.Topic = "new-topic" }},
		{"group", func(k *config.KafkaStorageConfig) { k.ConsumerGroup = "new-group" }},
		{"brokers", func(k *config.KafkaStorageConfig) { k.Brokers = []string{"new-broker:9092"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, old, now := fixture()
			for id := range state.Workers {
				if id != "0" {
					delete(state.Workers, id)
				}
			}
			Reconcile(&state, []eventsource.Release{old}, now)
			if len(state.Tasks) != 2 {
				t.Fatalf("old tasks = %d", len(state.Tasks))
			}
			oldEpochs := map[string]int64{}
			for id, task := range state.Tasks {
				task.Phase = "running"
				state.Tasks[id] = task
				oldEpochs[id] = task.Epoch
			}
			next := old
			next.Version, next.Spec.Version = 2, 2
			tc.edit(&next.Spec.Storage.Kafka)
			// 新订阅必须重新取得元数据，不能继承旧 topic 的身份/分片数。
			state.Metadata[old.ID] = UpdateMetadata(state.Metadata[old.ID], next.Spec, "new-topic-id", 1, nil, now)
			if m := state.Metadata[old.ID]; m.Error != "" || m.TopicID != "new-topic-id" || m.Partitions != 1 {
				t.Fatalf("old metadata retained: %+v", m)
			}
			Reconcile(&state, []eventsource.Release{next}, now)
			Reconcile(&state, []eventsource.Release{next}, now)
			for id, task := range state.Tasks {
				if task.Phase != "stopping" || task.Version != 1 || task.Epoch != oldEpochs[id] {
					t.Fatalf("task replaced before stopping: %+v", task)
				}
				task.Phase = "stopped"
				task.Retired = append(task.Retired, ConsumerName(task))
				state.Tasks[id] = task
			}
			Reconcile(&state, []eventsource.Release{next}, now)
			for id, task := range state.Tasks {
				if task.Phase != "preparing" || task.Version != 2 || task.Epoch <= oldEpochs[id] || len(task.Retired) != 1 {
					t.Fatalf("new task did not retain retirement history: %+v", task)
				}
			}
		})
	}
}
