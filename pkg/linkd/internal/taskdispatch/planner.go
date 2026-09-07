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
	"fmt"
	"sort"
	"time"

	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/kafkaclient"
)

// Reconcile 保留已有匹配 owner，再补足目标；撤销中的 assignment 仍占位置。
// Kafka 分片元数据失败时只允许保留/撤销，不能新增 assignment。
func Reconcile(st *State, releases []eventsource.Release, now time.Time) {
	reconcileBudgets(st, releases, now, config.DefaultCleanerRuntimeConfig(), (config.LifecycleConfig{}).WithDefaults())
}

func reconcileBudgets(st *State, releases []eventsource.Release, now time.Time, cleanerDefaults config.CleanerRuntimeConfig, lifecycleDefaults config.LifecycleConfig) {
	for id, t := range st.Tasks {
		if t.Phase != "stopped" && !now.Before(t.Expires.Add(SafetyMargin)) {
			t.Phase = "stopped"
			t.Retired = append(t.Retired, ConsumerName(t))
			t.Error = "authorization expired"
			w := st.Workers[t.Worker]
			w.CooldownUntil = now.Add(60 * time.Second)
			st.Workers[t.Worker] = w
			st.Tasks[id] = t
		}
	}
	for id, w := range st.Workers {
		if now.Sub(w.Seen) > 10*time.Minute {
			active := false
			for _, t := range st.Tasks {
				if t.Worker == id && t.Phase != "stopped" {
					active = true
				}
			}
			if !active {
				delete(st.Workers, id)
			}
		}
	}
	st.Statuses = nil
	sourceSet := map[string]bool{}
	subscriptionOwners := map[string]string{}
	for _, rel := range releases {
		for _, t := range st.Tasks {
			if t.Source == rel.ID && t.Role == "cleaner" && t.Phase != "stopped" {
				key := subscriptionIdentity(rel.Spec)
				if owner := subscriptionOwners[key]; owner == "" || rel.ID < owner {
					subscriptionOwners[key] = rel.ID
				}
			}
		}
	}

	for _, rel := range releases {
		s := rel.Spec
		sourceSet[rel.ID] = true
		for _, role := range []string{"cleaner", "lifecycle"} {
			eligible := []string{}
			for id, w := range st.Workers {
				if !w.Draining && !now.Before(w.StableAfter) && !now.Before(w.CooldownUntil) && now.Sub(w.Seen) < 10*time.Second && matches(w, s, role) {
					eligible = append(eligible, id)
				}
			}
			sort.Strings(eligible)
			target := placement(s, role).Replicas.Limit(len(eligible))
			if !s.Enabled || rel.Deleted {
				target = 0
			}
			status := Status{Source: rel.ID, Role: role, Matching: len(eligible), Target: target}
			allowNew := true
			subscriptionConflict := false
			if role == "cleaner" {
				m := st.Metadata[rel.ID]
				status.Metadata = &m
				key := subscriptionIdentity(s)
				owner := subscriptionOwners[key]
				subscriptionConflict = owner != "" && owner != rel.ID
				if owner == "" && s.Enabled && !rel.Deleted {
					subscriptionOwners[key] = rel.ID
				}
				if m.Digest != digest(s.Storage) || m.Partitions == 0 {
					allowNew = false
					status.Reason = "waiting for Kafka metadata"
				} else {
					if target > m.Partitions {
						status.Reason = "limited by Kafka partitions"
					}
					target = min(target, m.Partitions)
					if m.Error != "" {
						allowNew = false
						status.Reason = m.Error
					}
				}
			}
			if subscriptionConflict {
				allowNew = false
				status.Reason = "subscription is owned by another source"
			}
			status.Target = target
			active := []string{}
			for id, t := range st.Tasks {
				if t.Source == rel.ID && t.Role == role && t.Phase != "stopped" {
					active = append(active, id)
				}
			}
			sort.Strings(active)
			// 停用整个来源时先撤销 Cleaner，避免停止 Lifecycle 后仍有新的接入。
			delayStop := false
			if role == "lifecycle" && (!s.Enabled || rel.Deleted) {
				for _, t := range st.Tasks {
					if t.Source == rel.ID && t.Role == "cleaner" && t.Phase != "stopped" {
						delayStop = true
					}
				}
			}
			retained := 0
			used := map[string]bool{}
			for _, id := range active {
				t := st.Tasks[id]
				w := st.Workers[t.Worker]
				used[t.Worker] = true
				valid := !subscriptionConflict && matches(w, s, role) && !w.Draining && t.Digest == executionDigest(s)
				// 单次失联不迁走。到达失联容错窗口后撤销，仍等待停止或完整授权到期。
				if now.Sub(w.Seen) >= 10*time.Second {
					valid = false
				}
				revoke := !valid || retained >= target
				if !allowNew && !subscriptionConflict && placement(s, role).Replicas.Limit(1) > 0 && s.Enabled && !rel.Deleted && matches(w, s, role) && !w.Draining && now.Sub(w.Seen) < 10*time.Second && retained < target {
					revoke = false
				}
				if delayStop {
					revoke = false
				}
				if revoke && t.Phase != "stopping" {
					t.Phase = "stopping"
					st.Tasks[id] = t
				}
				if t.Phase != "stopping" {
					retained++
				}
				if t.Phase == "running" {
					status.Running++
				}
			}
			live := len(active)
			for _, wid := range eligible {
				if !allowNew || live >= target {
					break
				}
				if used[wid] {
					continue
				}
				w := st.Workers[wid]
				load, totalWorkers, totalBytes := 0, 0, int64(0)
				costWorkers, costBytes := lifecycleDefaults.Concurrency, int64(lifecycleDefaults.Signal.MaxInflightMessages)*int64(lifecycleDefaults.Signal.MaxMessageBytes)
				if role == "cleaner" {
					runtime := s.Cleaner.RuntimeConfig(cleanerDefaults)
					costWorkers = runtime.WorkerCount
					costBytes = int64(runtime.MaxInflightBytes)
				}
				for _, t := range st.Tasks {
					if t.Worker == wid && t.Phase != "stopped" {
						load++
						totalWorkers += t.Concurrency
						totalBytes += t.InflightBytes
					}
				}
				maxWorkers, maxBytes := w.MaxConcurrency, w.MaxInflightBytes
				if maxWorkers == 0 {
					maxWorkers = 128
				}
				if maxBytes == 0 {
					maxBytes = 256 << 20
				}
				if load >= w.MaxTasks || totalWorkers+costWorkers > maxWorkers || totalBytes+costBytes > maxBytes {
					continue
				}
				slot := 0
				for {
					key := taskID(rel.ID, role, slot)
					old, ok := st.Tasks[key]
					if !ok || old.Phase == "stopped" {
						if now.Before(old.RetryAfter) {
							break
						}
						if len(old.Retired) > 1000 {
							status.Reason = "retired consumer history requires cleanup"
							break
						}
						st.NextEpoch++
						st.Tasks[key] = Task{Concurrency: costWorkers, InflightBytes: costBytes, ID: key, Source: rel.ID, Role: role, Slot: slot, Epoch: st.NextEpoch, Worker: wid, Version: rel.Version, Digest: executionDigest(s), Phase: "preparing", Expires: now.Add(LeaseTTL), Retired: old.Retired, Failures: old.Failures}
						used[wid] = true
						live++
						break
					}
					slot++
				}
			}
			if live < target && status.Reason == "" {
				status.Reason = fmt.Sprintf("capacity pending: %d", target-live)
			}
			st.Statuses = append(st.Statuses, status)
		}
	}
	for id, t := range st.Tasks {
		if !sourceSet[t.Source] && t.Phase != "stopped" {
			t.Phase = "stopping"
			st.Tasks[id] = t
		}
	}
}

func subscriptionIdentity(s config.EventSource) string {
	brokers, _ := kafkaclient.NormalizeBrokers(s.Storage.Kafka.Brokers)
	sort.Strings(brokers)
	return digest(struct {
		Brokers      []string
		Topic, Group string
	}{brokers, s.Storage.Kafka.Topic, s.Storage.Kafka.ConsumerGroup})
}
