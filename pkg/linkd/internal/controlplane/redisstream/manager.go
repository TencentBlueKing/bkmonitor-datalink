// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redisstream

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// Config 定义单个 Redis Stream 管理任务的目标和资源边界。
type Config struct {
	// Stream 是受管 Redis Stream key。
	Stream string
	// ExpectedGroup 是 Linkd Lifecycle 必须存在的 Consumer Group。
	ExpectedGroup string
	// ReconcileInterval 是采集和裁剪周期。
	ReconcileInterval time.Duration
	// OperationTimeout 限制单轮 Redis 操作总时间。
	OperationTimeout time.Duration
	// MaxEntries 是触发安全裁剪的软长度上限。
	MaxEntries int64
	// TrimBatchSize 限制单轮裁剪检查和删除的条目数。
	TrimBatchSize int64
}

// Snapshot 是一轮采集得到的低基数 Stream 状态。
type Snapshot struct {
	// Exists 表示 Stream key 当前存在。
	Exists bool
	// ExpectedGroupPresent 表示配置的 Lifecycle Consumer Group 当前存在。
	ExpectedGroupPresent bool
	// Length 是 Stream 当前条目数。
	Length int64
	// EntriesAdded 是当前 Stream 自创建以来累计添加条目数。
	EntriesAdded int64
	// MemoryBytes 是 MEMORY USAGE 返回的当前字节数。
	MemoryBytes int64
	// Groups 是 Stream 当前 Consumer Group 数。
	Groups int64
	// Consumers 是全部 Consumer Group 的 Consumer 总数。
	Consumers int64
	// Pending 是全部 Consumer Group 的 PEL 条目总数。
	Pending int64
	// MaxLag 是各 Consumer Group 未投递条目数的最大值；-1 表示 Redis 无法计算。
	MaxLag int64
	// OldestEntryAgeSeconds 是最老 Stream entry 按自动生成 ID 计算的年龄。
	OldestEntryAgeSeconds float64
	// OldestPendingAgeSeconds 是全部 PEL 中最小 Stream ID 对应条目的年龄。
	OldestPendingAgeSeconds float64
	// MaxEntries 是配置的软长度上限。
	MaxEntries int64
	// EntriesAboveConfiguredMax 是当前长度超过软上限的条目数。
	EntriesAboveConfiguredMax int64
	// TrimRequired 表示当前长度已经超过软上限。
	TrimRequired bool
	// TrimSafe 表示本轮采集形成了不会删除未确认消息的安全裁剪边界。
	TrimSafe bool
}

// Observer 接收 Stream 当前状态和每轮管理结果。实现不得改变裁剪结果或错误语义。
type Observer interface {
	ObserveSnapshot(ctx context.Context, snapshot Snapshot)
	ReconcileFinished(ctx context.Context, outcome string, duration time.Duration, trimmed int64)
}

type noopObserver struct{}

func (noopObserver) ObserveSnapshot(context.Context, Snapshot) {}

func (noopObserver) ReconcileFinished(context.Context, string, time.Duration, int64) {
}

type redisClient interface {
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
	XInfoStream(ctx context.Context, key string) *redis.XInfoStreamCmd
	XInfoGroups(ctx context.Context, key string) *redis.XInfoGroupsCmd
	XPendingExt(ctx context.Context, args *redis.XPendingExtArgs) *redis.XPendingExtCmd
	MemoryUsage(ctx context.Context, key string, samples ...int) *redis.IntCmd
	XTrimMinIDApprox(ctx context.Context, key, minID string, limit int64) *redis.IntCmd
}

// Manager 周期采集 Stream/Consumer Group/PEL 状态，并在超过软上限时裁剪安全前缀。
// 安全边界取所有 Consumer Group 的最小未确认 ID；无 Pending 时取 last-delivered-id 的后继。
// 因此未读或 Pending 消息不会为满足长度目标而被删除，实际长度允许暂时超过 MaxEntries。
type Manager struct {
	client   redisClient
	config   Config
	observer Observer
	now      func() time.Time
}

// NewManager 创建 Redis Stream 管理器；构造过程不访问 Redis。
func NewManager(client redis.UniversalClient, config Config, observer Observer) (*Manager, error) {
	return newManager(client, config, observer, time.Now)
}

func newManager(client redisClient, config Config, observer Observer, now func() time.Time) (*Manager, error) {
	if client == nil {
		return nil, fmt.Errorf("create redis stream manager: client is required")
	}
	if strings.TrimSpace(config.Stream) == "" || strings.TrimSpace(config.ExpectedGroup) == "" {
		return nil, fmt.Errorf("create redis stream manager: stream and expected group are required")
	}
	if config.ReconcileInterval < time.Second || config.OperationTimeout <= 0 ||
		config.OperationTimeout >= config.ReconcileInterval || config.MaxEntries < 1 || config.TrimBatchSize < 1 {
		return nil, fmt.Errorf("create redis stream manager: invalid resource limits")
	}
	if observer == nil {
		observer = noopObserver{}
	}
	if now == nil {
		return nil, fmt.Errorf("create redis stream manager: clock is required")
	}
	return &Manager{client: client, config: config, observer: observer, now: now}, nil
}

// Run 立即执行一轮采集和裁剪，随后按固定周期持续运行。
// 任一轮 Redis 操作失败都会返回，由控制面任务监督器统一取消并重启其他管理任务。
func (m *Manager) Run(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("run redis stream manager: context must not be nil")
	}
	if err := m.runCycle(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(m.config.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := m.runCycle(ctx); err != nil {
				return err
			}
		}
	}
}

func (m *Manager) runCycle(ctx context.Context) error {
	cycleCtx, cancel := context.WithTimeout(ctx, m.config.OperationTimeout)
	defer cancel()
	return m.ReconcileOnce(cycleCtx)
}

// ReconcileOnce 采集一次 Redis 状态，并在长度超过 MaxEntries 时有界裁剪已确认前缀。
func (m *Manager) ReconcileOnce(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("reconcile redis stream: context must not be nil")
	}
	startedAt := time.Now()
	trimmed := int64(0)
	snapshot, boundary, safeToTrim, err := m.inspect(ctx)
	if err == nil && snapshot.TrimRequired && safeToTrim {
		trimmed, err = m.client.XTrimMinIDApprox(
			ctx,
			m.config.Stream,
			boundary,
			m.config.TrimBatchSize,
		).Result()
		if err != nil {
			err = fmt.Errorf("trim redis stream %q before %q: %w", m.config.Stream, boundary, err)
		} else if trimmed > 0 {
			snapshot, _, _, err = m.inspect(ctx)
		}
	}
	if err != nil {
		m.observer.ReconcileFinished(ctx, "failed", time.Since(startedAt), trimmed)
		return err
	}
	m.observer.ObserveSnapshot(ctx, snapshot)
	m.observer.ReconcileFinished(ctx, "succeeded", time.Since(startedAt), trimmed)
	return nil
}

func (m *Manager) inspect(ctx context.Context) (Snapshot, string, bool, error) {
	snapshot := Snapshot{MaxEntries: m.config.MaxEntries, MaxLag: -1}
	exists, err := m.client.Exists(ctx, m.config.Stream).Result()
	if err != nil {
		return snapshot, "", false, fmt.Errorf("inspect redis stream %q existence: %w", m.config.Stream, err)
	}
	if exists == 0 {
		snapshot.MaxLag = 0
		return snapshot, "", false, nil
	}
	snapshot.Exists = true

	info, err := m.client.XInfoStream(ctx, m.config.Stream).Result()
	if err != nil {
		return snapshot, "", false, fmt.Errorf("inspect redis stream %q: %w", m.config.Stream, err)
	}
	snapshot.Length = info.Length
	snapshot.TrimRequired = info.Length > m.config.MaxEntries
	snapshot.EntriesAdded = info.EntriesAdded
	snapshot.OldestEntryAgeSeconds = streamIDAgeSeconds(m.now(), info.FirstEntry.ID)
	if info.Length > m.config.MaxEntries {
		snapshot.EntriesAboveConfiguredMax = info.Length - m.config.MaxEntries
	}
	memoryBytes, err := m.client.MemoryUsage(ctx, m.config.Stream).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return snapshot, "", false, fmt.Errorf("inspect redis stream %q memory: %w", m.config.Stream, err)
	}
	snapshot.MemoryBytes = memoryBytes

	groups, err := m.client.XInfoGroups(ctx, m.config.Stream).Result()
	if err != nil {
		return snapshot, "", false, fmt.Errorf("inspect redis stream %q groups: %w", m.config.Stream, err)
	}
	snapshot.Groups = int64(len(groups))
	if len(groups) == 0 {
		snapshot.MaxLag = 0
	}
	var boundary string
	safeToTrim := len(groups) > 0
	unknownLag := false
	for _, group := range groups {
		snapshot.Consumers += group.Consumers
		snapshot.Pending += group.Pending
		if group.Lag < 0 {
			unknownLag = true
		} else if group.Lag > snapshot.MaxLag {
			snapshot.MaxLag = group.Lag
		}
		if group.Name == m.config.ExpectedGroup {
			snapshot.ExpectedGroupPresent = true
		}

		candidate, pendingID, ok, candidateErr := m.groupTrimBoundary(ctx, group)
		if candidateErr != nil {
			return snapshot, "", false, candidateErr
		}
		if !ok {
			safeToTrim = false
			continue
		}
		if pendingID != "" {
			age := streamIDAgeSeconds(m.now(), pendingID)
			if age > snapshot.OldestPendingAgeSeconds {
				snapshot.OldestPendingAgeSeconds = age
			}
		}
		if boundary == "" || compareStreamIDs(candidate, boundary) < 0 {
			boundary = candidate
		}
	}
	if unknownLag {
		snapshot.MaxLag = -1
	}
	if !snapshot.ExpectedGroupPresent {
		safeToTrim = false
	}
	snapshot.TrimSafe = safeToTrim && boundary != ""
	return snapshot, boundary, snapshot.TrimSafe, nil
}

func (m *Manager) groupTrimBoundary(
	ctx context.Context,
	group redis.XInfoGroup,
) (candidate, pendingID string, ok bool, err error) {
	if group.Pending == 0 {
		candidate, err = nextStreamID(group.LastDeliveredID)
		if err != nil {
			return "", "", false, fmt.Errorf(
				"inspect redis stream %q group %q last delivered id: %w",
				m.config.Stream,
				group.Name,
				err,
			)
		}
		return candidate, "", true, nil
	}
	pending, err := m.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: m.config.Stream,
		Group:  group.Name,
		Start:  "-",
		End:    "+",
		Count:  1,
	}).Result()
	if err != nil {
		return "", "", false, fmt.Errorf(
			"inspect redis stream %q group %q oldest pending: %w",
			m.config.Stream,
			group.Name,
			err,
		)
	}
	if len(pending) == 0 {
		// Pending 可能在 XINFO 与 XPENDING 之间刚好被确认。本轮不裁剪，下一轮重读完整状态，
		// 避免根据一次跨命令竞态推断出过于激进的边界。
		return "", "", false, nil
	}
	if _, _, err := parseStreamID(pending[0].ID); err != nil {
		return "", "", false, fmt.Errorf(
			"inspect redis stream %q group %q pending id: %w",
			m.config.Stream,
			group.Name,
			err,
		)
	}
	return pending[0].ID, pending[0].ID, true, nil
}

func streamIDAgeSeconds(now time.Time, id string) float64 {
	milliseconds, _, err := parseStreamID(id)
	if err != nil || milliseconds == 0 || milliseconds > math.MaxInt64 {
		return 0
	}
	age := now.Sub(time.UnixMilli(int64(milliseconds)))
	if age < 0 {
		return 0
	}
	return age.Seconds()
}

func nextStreamID(id string) (string, error) {
	milliseconds, sequence, err := parseStreamID(id)
	if err != nil {
		return "", err
	}
	if sequence == ^uint64(0) {
		if milliseconds == ^uint64(0) {
			return "", fmt.Errorf("stream id %q has no successor", id)
		}
		milliseconds++
		sequence = 0
	} else {
		sequence++
	}
	return strconv.FormatUint(milliseconds, 10) + "-" + strconv.FormatUint(sequence, 10), nil
}

func compareStreamIDs(left, right string) int {
	leftMilliseconds, leftSequence, leftErr := parseStreamID(left)
	rightMilliseconds, rightSequence, rightErr := parseStreamID(right)
	if leftErr != nil || rightErr != nil {
		return strings.Compare(left, right)
	}
	if leftMilliseconds < rightMilliseconds || leftMilliseconds == rightMilliseconds && leftSequence < rightSequence {
		return -1
	}
	if leftMilliseconds == rightMilliseconds && leftSequence == rightSequence {
		return 0
	}
	return 1
}

func parseStreamID(id string) (uint64, uint64, error) {
	parts := strings.Split(id, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid stream id %q", id)
	}
	milliseconds, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid stream id %q: %w", id, err)
	}
	sequence, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid stream id %q: %w", id, err)
	}
	return milliseconds, sequence, nil
}

var _ Observer = noopObserver{}
