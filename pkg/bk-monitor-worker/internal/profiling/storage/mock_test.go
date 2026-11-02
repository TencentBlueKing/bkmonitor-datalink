// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/models"
	profilingwindow "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/window"
)

// MockStore is an in-memory Store for local end-to-end runs. It uses the same
// event merge, window deadline and execution aggregation functions as MySQLStore.
type MockStore struct {
	mu            sync.RWMutex
	nextExecution int
	nextHost      int
	executions    map[core.ExecutionKey]*mockExecution
}

type mockExecution struct {
	row              executionRow
	scheduleType     string
	taskName         string
	creator          string
	collectionMethod string
	collectionType   string
	hosts            map[int]models.ActiveProfilingHostExecution
	aggregate        executionAggregate
	createdAt        time.Time
	updatedAt        time.Time
}

// MockSnapshot is a detached view of a mock execution and its host rows.
type MockSnapshot struct {
	Execution models.ActiveProfilingExecution       `json:"execution"`
	Hosts     []models.ActiveProfilingHostExecution `json:"hosts"`
}

// NewMockStore creates an empty in-memory Store.
func NewMockStore() *MockStore {
	return &MockStore{executions: make(map[core.ExecutionKey]*mockExecution)}
}

// ApplyEvent mirrors MySQLStore.ApplyEvent's state transitions without SQL or
// external services. The shared helpers keep the mock aligned with production.
func (s *MockStore) ApplyEvent(
	ctx context.Context,
	event core.Event,
	nowMS int64,
	policy core.WindowPolicy,
) error {
	return s.ApplyEvents(ctx, []core.Event{event}, nowMS, policy)
}

// ApplyEvents shares the production event state derivation and keeps only mock
// row creation and in-memory ID allocation local to this store.
func (s *MockStore) ApplyEvents(ctx context.Context, events []core.Event, nowMS int64, policy core.WindowPolicy) error {
	if len(events) == 0 {
		return nil
	}
	batches, err := groupEventsByExecution(events)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	now := time.UnixMilli(nowMS)
	s.mu.Lock()
	defer s.mu.Unlock()
	nextExecutionID := s.nextExecution
	nextHostID := s.nextHost
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			return err
		}

		storedExecution, exists := s.executions[batch.key]
		var execution *mockExecution
		if !exists {
			firstEvent := batch.events[0]
			initialConfig, err := validJSON(firstEvent.Content.Config, `{}`)
			if err != nil {
				return fmt.Errorf("validate mock Event config: %w", err)
			}
			nextExecutionID++
			execution = &mockExecution{
				row: executionRow{
					ID:          nextExecutionID,
					Key:         batch.key,
					ScheduledAt: batch.key.ScheduledAt,
					Config:      json.RawMessage(initialConfig),
				},
				scheduleType:     firstEvent.ScheduleType,
				taskName:         firstEvent.TaskName,
				creator:          firstEvent.Creator,
				collectionMethod: firstEvent.CollectionMethod,
				collectionType:   firstEvent.CollectionType,
				hosts:            make(map[int]models.ActiveProfilingHostExecution),
				createdAt:        now,
				updatedAt:        now,
			}
		} else {
			execution = cloneMockExecution(storedExecution)
		}

		changes, err := prepareExecutionEventChanges(
			execution.row, batch.events, execution.hosts, now, nowMS, policy,
		)
		if err != nil {
			return fmt.Errorf("prepare mock execution event changes: %w", err)
		}
		aggregate, err := aggregateExecution(execution.row, changes.hostStates)
		if err != nil {
			return err
		}
		for i := range changes.hosts {
			host := changes.hosts[i]
			if host.ID == 0 {
				nextHostID++
				host.ID = nextHostID
			}
			changes.hosts[i] = host
			changes.hostStates[host.BkHostID] = host
		}

		execution.hosts = changes.hostStates
		execution.row.Config = json.RawMessage(changes.config)
		execution.aggregate = aggregate
		execution.updatedAt = now
		s.executions[batch.key] = execution
		s.nextExecution = nextExecutionID
		s.nextHost = nextHostID
	}
	return nil
}

// MarkUnknown mirrors MySQLStore.MarkUnknown for due host windows.
func (s *MockStore) MarkUnknown(ctx context.Context, nowMS int64, limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("unknown scan limit must be positive")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	type dueExecution struct {
		firstDue  int64
		execution *mockExecution
	}
	var due []dueExecution
	for _, execution := range s.executions {
		var firstDue int64
		for _, host := range execution.hosts {
			if host.Status != core.StatusRunning || host.UnknownAt == nil {
				continue
			}
			if _, expired := profilingwindow.FromDeadline(*host.UnknownAt).ExpireAt(nowMS); !expired {
				continue
			}
			if firstDue == 0 || *host.UnknownAt < firstDue {
				firstDue = *host.UnknownAt
			}
		}
		if firstDue != 0 {
			due = append(due, dueExecution{firstDue: firstDue, execution: execution})
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].firstDue < due[j].firstDue })
	if len(due) > limit {
		due = due[:limit]
	}

	changed := 0
	updatedAt := time.UnixMilli(nowMS)
	for _, candidate := range due {
		execution := candidate.execution
		for hostID, host := range execution.hosts {
			if host.Status != core.StatusRunning || host.UnknownAt == nil {
				continue
			}
			window, expired := profilingwindow.FromDeadline(*host.UnknownAt).ExpireAt(nowMS)
			if !expired {
				continue
			}
			host.Status = core.StatusUnknown
			host.UnknownAt = window.Deadline()
			host.UpdatedAt = updatedAt
			execution.hosts[hostID] = host
			changed++
		}
		aggregate, err := aggregateExecution(execution.row, execution.hosts)
		if err != nil {
			return changed, err
		}
		execution.aggregate = aggregate
		execution.updatedAt = updatedAt
	}
	return changed, nil
}

// Snapshot returns a detached execution and host-row view for assertions and
// local demo output.
func (s *MockStore) Snapshot(key core.ExecutionKey) (MockSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	execution, ok := s.executions[key]
	if !ok {
		return MockSnapshot{}, false
	}
	model := models.ActiveProfilingExecution{
		ID:               execution.row.ID,
		BkTenantID:       key.TenantID,
		BkBizID:          key.BizID,
		ScheduleType:     execution.scheduleType,
		TaskID:           key.TaskID,
		ScheduledAt:      key.ScheduledAt,
		TaskName:         execution.taskName,
		Creator:          execution.creator,
		CollectionMethod: execution.collectionMethod,
		CollectionType:   execution.collectionType,
		ExecutionStatus:  execution.aggregate.status,
		StatusSummary:    json.RawMessage(execution.aggregate.statusSummary),
		TargetCount:      execution.aggregate.targetCount,
		StartedAt:        execution.aggregate.startedAt,
		FinishedAt:       cloneInt64(execution.aggregate.finishedAt),
		Config:           append(json.RawMessage(nil), execution.row.Config...),
		CreatedAt:        execution.createdAt,
		UpdatedAt:        execution.updatedAt,
	}
	hosts := make([]models.ActiveProfilingHostExecution, 0, len(execution.hosts))
	for _, host := range execution.hosts {
		hosts = append(hosts, cloneHost(host))
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].BkHostID < hosts[j].BkHostID })
	return MockSnapshot{Execution: model, Hosts: hosts}, true
}

func cloneHost(host models.ActiveProfilingHostExecution) models.ActiveProfilingHostExecution {
	host.StartedAt = cloneInt64(host.StartedAt)
	host.FinishedAt = cloneInt64(host.FinishedAt)
	host.UnknownAt = cloneInt64(host.UnknownAt)
	host.Processes = append(json.RawMessage(nil), host.Processes...)
	return host
}

func cloneMockExecution(execution *mockExecution) *mockExecution {
	cloned := *execution
	cloned.row.Config = append(json.RawMessage(nil), execution.row.Config...)
	cloned.hosts = make(map[int]models.ActiveProfilingHostExecution, len(execution.hosts))
	for hostID, host := range execution.hosts {
		cloned.hosts[hostID] = cloneHost(host)
	}
	cloned.aggregate.finishedAt = cloneInt64(execution.aggregate.finishedAt)
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var _ core.Store = (*MockStore)(nil)
