// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventgen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ManagedOptions 只包含控制面连接和轮询参数，任务与 Kafka 凭据由内部接口提供。
type ManagedOptions struct {
	Logger       *slog.Logger
	URL          string
	Token        string
	PollInterval time.Duration
	SessionID    string
}

type managedRun struct {
	engine            *Engine
	run               Run
	revision          int64
	lastError         string
	requestedRevision int64
}

// RunManaged 注册新进程并执行当前会话的任务；关闭时先收尾并回报最终状态。
func RunManaged(ctx context.Context, o ManagedOptions) error {
	if o.URL == "" || o.Token == "" {
		return fmt.Errorf("control-plane URL and worker token are required")
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 3 * time.Second
	}
	if o.SessionID == "" {
		o.SessionID = NewRunID()
	}
	client := &http.Client{Timeout: 10 * time.Second}
	runs := map[string]*managedRun{}
	first := true
	lastSuccess := time.Now()
	ticker := time.NewTicker(o.PollInterval)
	defer ticker.Stop()
	poll := func(call context.Context) error {
		reports := []Run{}
		for _, r := range runs {
			if r.engine != nil {
				r.run.Status = r.engine.Snapshot()
			}
			r.run.Status.RequestedRevision = r.requestedRevision
			if r.lastError != "" {
				r.run.Status.Error = r.lastError
			}
			reports = append(reports, r.run)
		}
		assignments, err := pollManaged(call, client, o, Heartbeat{SessionID: o.SessionID, NewSession: first, Reports: reports})
		if err != nil {
			return err
		}
		first = false
		lastSuccess = time.Now()
		for key, r := range runs {
			if terminalState(r.run.Status.State) {
				delete(runs, key)
			}
		}
		for _, a := range assignments {
			key := objectKey(a.Run.TenantID, a.Run.ID)
			local := runs[key]
			if local == nil {
				if a.Run.SessionID != o.SessionID {
					continue
				}
				if a.Run.Command.Action == "stop" || a.Run.Command.Action == "force_stop" {
					stopped := a.Run
					stopped.Status.State = "stopped"
					stopped.AppliedOperation = a.Run.Command.OperationID
					runs[key] = &managedRun{run: stopped, requestedRevision: a.Plan.Revision}
					continue
				}
				publisher, err := NewKafkaPublisher(a.Sources, a.Plan.Config.defaults().Limits)
				if err != nil {
					failed := a.Run
					failed.Status = Status{State: "failed", RequestedRevision: a.Plan.Revision, Error: "Kafka producer initialization failed"}
					failed.AppliedOperation = a.Run.Command.OperationID
					runs[key] = &managedRun{run: failed, requestedRevision: a.Plan.Revision}
					continue
				}
				start := time.Now().UTC()
				if a.Plan.Config.StartAt.After(start) {
					start = a.Plan.Config.StartAt
				}
				engine, err := New(a.Plan.Config, a.Run.ID, a.Plan.Revision, start, publisher, o.Logger)
				if err != nil {
					publisher.Close()
					failed := a.Run
					failed.Status = Status{State: "failed", RequestedRevision: a.Plan.Revision, Error: "eventgen configuration initialization failed"}
					failed.AppliedOperation = a.Run.Command.OperationID
					runs[key] = &managedRun{run: failed, requestedRevision: a.Plan.Revision}
					continue
				}
				local = &managedRun{engine: engine, run: a.Run, revision: a.Plan.Revision}
				local.run.AppliedOperation = a.Run.Command.OperationID
				runs[key] = local
				if a.Run.Command.Action == "pause" {
					_ = engine.Control("pause")
				}
				go func() { _ = engine.Run(ctx) }()
			}
			local.requestedRevision = a.Plan.Revision
			if local.engine == nil {
				continue
			}
			if a.Plan.Revision != local.revision {
				var err error
				if RateOnly(local.engine.programConfig(), a.Plan.Config) {
					err = local.engine.Apply(a.Plan.Config, a.Plan.Revision)
				} else {
					var publisher *KafkaPublisher
					publisher, err = NewKafkaPublisher(a.Sources, a.Plan.Config.defaults().Limits)
					if err == nil {
						err = local.engine.applyPublisher(a.Plan.Config, a.Plan.Revision, publisher)
						if err != nil {
							publisher.Close()
						}
					}
				}
				if err != nil {
					local.lastError = "configuration could not be applied"
				} else {
					local.revision = a.Plan.Revision
					local.lastError = ""
				}
			}
			if a.Run.Command.OperationID != local.run.AppliedOperation {
				if err := local.engine.Control(a.Run.Command.Action); err != nil {
					local.lastError = err.Error()
				} else {
					local.lastError = ""
				}
				local.run.AppliedOperation = a.Run.Command.OperationID
			}
		}
		return nil
	}
	for {
		if err := poll(ctx); err != nil && time.Since(lastSuccess) > 60*time.Second {
			for _, r := range runs {
				if r.engine != nil {
					_ = r.engine.Control("stop")
				}
			}
		}
		select {
		case <-ctx.Done():
			for _, r := range runs {
				if r.engine != nil {
					_ = r.engine.Control("stop")
				}
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
			defer cancel()
			for _, r := range runs {
				if r.engine == nil {
					continue
				}
				select {
				case <-r.engine.done:
				case <-cleanup.Done():
					_ = r.engine.Control("force_stop")
				}
			}
			finalCtx, stopFinal := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stopFinal()
			reports := []Run{}
			for _, r := range runs {
				if r.engine != nil {
					select {
					case <-r.engine.done:
					case <-finalCtx.Done():
					}
					r.run.Status = r.engine.Snapshot()
				}
				reports = append(reports, r.run)
			}
			_, _ = pollManaged(finalCtx, client, o, Heartbeat{SessionID: o.SessionID, Reports: reports})
			return nil
		case <-ticker.C:
		}
	}
}

func pollManaged(ctx context.Context, client *http.Client, o ManagedOptions, h Heartbeat) ([]Assignment, error) {
	body, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.URL, "/")+"/internal/eventgen/heartbeat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+o.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("eventgen heartbeat HTTP %d", response.StatusCode)
	}
	var assignments []Assignment
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<20))
	decoder.UseNumber()
	err = decoder.Decode(&assignments)
	return assignments, err
}

func (e *Engine) programConfig() Config { e.mu.Lock(); defer e.mu.Unlock(); return e.program.Config }
