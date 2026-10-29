// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/core"
	profilingStorage "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/profiling/storage"
)

const defaultProfilingMockFile = "./internal/profiling/testdata/active_profiling_events.json"

var (
	profilingMockFile        string
	profilingMockMaxDuration time.Duration
	profilingMockWindowDelay time.Duration
)

func init() {
	cmd := &cobra.Command{
		Use:   "run_profiling_mock",
		Short: "run the Active Profiling flow with local mock Events",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runProfilingMock(profilingMockFile, profilingMockMaxDuration, profilingMockWindowDelay, os.Stdout)
		},
	}
	cmd.Flags().StringVarP(&profilingMockFile, "file", "f", defaultProfilingMockFile, "mock Event JSON file")
	cmd.Flags().DurationVar(&profilingMockMaxDuration, "max-duration", time.Second, "maximum collection window")
	cmd.Flags().DurationVar(&profilingMockWindowDelay, "delay", time.Second, "late Event grace period")
	rootCmd.AddCommand(cmd)
}

type profilingMockResult struct {
	BeforeExpiry profilingStorage.MockSnapshot `json:"before_expiry"`
	AfterExpiry  profilingStorage.MockSnapshot `json:"after_expiry"`
}

func runProfilingMock(filePath string, maxDuration, delay time.Duration, output io.Writer) error {
	if filePath == "" {
		return fmt.Errorf("mock Event file path is required")
	}
	if maxDuration <= 0 || delay <= 0 {
		return fmt.Errorf("max-duration and delay must be positive")
	}
	payload, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read mock Event file: %w", err)
	}
	events, err := core.DecodeMessage(payload)
	if err != nil {
		return fmt.Errorf("decode mock Events: %w", err)
	}
	if len(events) == 0 {
		return fmt.Errorf("mock Event file contains no supported Events")
	}

	var executionKey core.ExecutionKey
	var clockStart time.Time
	var latestStart time.Time
	keyInitialized := false
	hasStartedEvent := false
	for _, event := range events {
		key := core.ExecutionKey{
			TenantID:    event.TenantID,
			BizID:       event.BizID,
			TaskID:      event.TaskID,
			ScheduledAt: event.ScheduledAt,
		}
		if keyInitialized && key != executionKey {
			return fmt.Errorf("mock Event file must contain exactly one execution")
		}
		if !keyInitialized {
			executionKey = key
			keyInitialized = true
		}
		if event.Name != core.EventStarted {
			continue
		}
		hasStartedEvent = true
		if clockStart.IsZero() {
			clockStart = time.UnixMilli(event.ScheduledAt)
		}
		if latestStart.IsZero() || event.Content.StartedAt > latestStart.UnixMilli() {
			latestStart = time.UnixMilli(event.Content.StartedAt)
		}
	}
	if !hasStartedEvent {
		return fmt.Errorf("mock Event file needs at least one started Event")
	}
	if !latestStart.After(clockStart) {
		latestStart = clockStart
	}

	clock := &profilingMockClock{now: clockStart}
	store := profilingStorage.NewMockStore()
	processor, err := core.NewProcessor(store, core.Options{
		WindowMaxDuration: maxDuration,
		WindowDelay:       delay,
		Now:               clock.Now,
	})
	if err != nil {
		return err
	}
	if err := processor.HandleMessage(context.Background(), payload); err != nil {
		return fmt.Errorf("process mock Event message: %w", err)
	}
	before, ok := store.Snapshot(executionKey)
	if !ok {
		return fmt.Errorf("mock execution was not created")
	}

	// Advance virtual time beyond the latest start-based window and its grace period.
	clock.Advance(latestStart.Sub(clockStart) + maxDuration + delay + time.Millisecond)
	if err := processor.SweepUnknownNow(context.Background()); err != nil {
		return fmt.Errorf("close expired mock windows: %w", err)
	}
	after, ok := store.Snapshot(executionKey)
	if !ok {
		return fmt.Errorf("mock execution disappeared after expiry")
	}

	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(profilingMockResult{BeforeExpiry: before, AfterExpiry: after}); err != nil {
		return fmt.Errorf("write mock run result: %w", err)
	}
	return nil
}

type profilingMockClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *profilingMockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *profilingMockClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
