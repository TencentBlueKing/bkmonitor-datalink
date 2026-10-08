// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package allinone_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/redisclient"
	"linkd/internal/shieldcheck"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
)

// armShieldHints 只把本测试创建的子告警下一次定时检查推迟一小时，证明解除由真实事件提示触发。
// 改动在正式 fingerprint lease 内以元数据 CAS 提交，等待旧扫描轮次结束后才结束主告警。
func (h *policyHarness) armShieldHints(tenant string, ids ...string) {
	h.t.Helper()
	h.until("shield hint subscription ready", func() bool {
		var snapshot taskstate.Snapshot
		h.call(http.MethodGet, "/api/v1/control-plane/tasks", nil, &snapshot)
		for _, task := range snapshot.Tasks {
			if task.ID == "shield-hints" {
				for _, step := range task.Steps {
					if step.ID == "subscription" && step.LastSuccess != nil && step.ErrorCode == "" && !step.Running {
						return true
					}
				}
			}
		}
		return false
	})
	cfg, err := config.Load(h.configPath, config.Overrides{})
	if err != nil {
		h.t.Fatal(err)
	}
	runtime, err := storeassembly.OpenExisting(h.ctx, *cfg.Storage, 4)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() {
		if err := runtime.Close(); err != nil {
			h.t.Error(err)
		}
	}()
	options := cfg.Storage.Redis.ClientOptions()
	options.ContextTimeoutEnabled = true
	client, err := redisclient.New(options)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	current := runtime.Repository.GetAlert
	if reader, ok := runtime.Repository.(store.LifecycleAlertStore); ok {
		current = reader.GetAlertCurrent
	}
	for _, id := range ids {
		observed, err := current(h.ctx, tenant, id)
		if err != nil {
			h.t.Fatal(err)
		}
		locker, err := scheduler.NewRedisLocker(client, cfg.Lifecycle.ForSource(cfg.Dispatch.WithDefaults().Deployment, observed.Alert.EventSourceID).SchedulerConfig())
		if err != nil {
			h.t.Fatal(err)
		}
		var lease scheduler.Lease
		h.until("fixture obtains child fingerprint lease", func() bool {
			lease, err = locker.Acquire(h.ctx, scheduler.CorrelationKey(tenant, observed.Alert.EventSourceID, observed.Alert.Fingerprint))
			if errors.Is(err, scheduler.ErrLockBusy) {
				return false
			}
			if err != nil {
				h.t.Fatal(err)
			}
			return true
		})
		func() {
			defer func() {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(h.ctx), 2*time.Second)
				defer cancel()
				if err := locker.Release(ctx, lease); err != nil {
					h.t.Error(err)
				}
			}()
			row, err := current(h.ctx, tenant, id)
			if err != nil {
				h.t.Fatal(err)
			}
			next := row.Alert.Clone()
			at := time.Now().UTC().Add(time.Hour)
			next.Shield.NextCheckAt = &at
			saved, err := runtime.Repository.CompareAndSetAlert(h.ctx, tenant, id, row.Version, next)
			if err != nil || saved.Alert.Revision != row.Alert.Revision {
				h.t.Fatal("fixture must only change timer metadata", err)
			}
		}()
	}
	barrier := time.Now()
	h.until("prior timer scan completed", func() bool {
		var snapshot taskstate.Snapshot
		h.call(http.MethodGet, "/api/v1/control-plane/tasks", nil, &snapshot)
		for _, task := range snapshot.Tasks {
			if task.ID == "shield-check" && task.Execution.FinishedAt != nil && task.Execution.FinishedAt.After(barrier) && !task.Execution.Running {
				return true
			}
		}
		return false
	})
	for _, id := range ids {
		row, err := current(h.ctx, tenant, id)
		if err != nil || row.Alert.Shield.NextCheckAt == nil || time.Until(*row.Alert.Shield.NextCheckAt) < 30*time.Minute {
			h.t.Fatal("timer fixture changed", err)
		}
	}
}

func (h *policyHarness) assertHintUnshield(tenant, id string) {
	h.t.Helper()
	h.assertUnshieldedWithoutAdmission(id)
	var latest struct {
		Check *shieldcheck.Check `json:"check"`
	}
	defer func() {
		if h.t.Failed() && latest.Check != nil {
			c := latest.Check
			h.t.Logf("last diagnostic trigger=%s outcome=%s error=%s changed=%v revisions=%d/%d", c.Trigger, c.Report.Outcome, c.ErrorCode, c.Report.Changed, c.Report.ObservedRevision, c.Report.ResultRevision)
		}
	}()
	h.untilWithin("hint diagnostic persisted", 15*time.Second, func() bool {
		h.call(http.MethodGet, "/api/v1/policy-runtime/shield/alerts/"+url.PathEscape(id)+"/check?bk_tenant_id="+url.QueryEscape(tenant), nil, &latest)
		return latest.Check != nil && latest.Check.Trigger == "hint" && latest.Check.Report.Outcome == "changed" && latest.Check.Report.Changed && latest.Check.RequestID == ""
	})
}
