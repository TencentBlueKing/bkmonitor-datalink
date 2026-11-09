// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"context"
	"errors"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	SessionKeyPrefix    = "scroll:session:"
	ScrollLockKeyPrefix = "scroll:lock:"
)

const (
	DefaultSliceMaxFailedNum = 3
	// scrollStateTimeout 分别限制完成状态保存和解锁的耗时。
	scrollStateTimeout = 2 * time.Second
)

const (
	StatusPending   = "pending"
	StatusFailed    = "failed"
	StatusCompleted = "completed"
)

var (
	ErrScrollSessionLocked   = errors.New("scroll session is already locked")
	ErrScrollSessionLockLost = errors.New("scroll session lock expired or changed owner")
	ErrScrollSessionTenant   = errors.New("scroll session belongs to another tenant")
)

type SliceStatus struct {
	SliceKey     string                     `json:"slice_key"`
	SliceMax     int                        `json:"slice_max"`
	ScrollID     string                     `json:"scroll_id"`
	Offset       int                        `json:"offset"`
	Status       string                     `json:"status"`
	FailedNum    int                        `json:"failed_num"`
	MaxFailedNum int                        `json:"max_failed_num"`
	Limit        int                        `json:"limit"`
	ScrollTarget *ElasticsearchScrollTarget `json:"scroll_target,omitempty"`
}

// ElasticsearchScrollTarget 随分片保存清理所需的存储、来源和空间信息，供后续请求恢复原路由。
// 这里只保存路由标识；连接地址和鉴权凭据由存储层按当前配置获取。
type ElasticsearchScrollTarget struct {
	StorageID  string `json:"storage_id"`
	SourceType string `json:"source_type"`
	SpaceUID   string `json:"space_uid"`
	TenantID   string `json:"tenant_id"`
}

func (s *SliceStatus) Done() bool {
	return s.Status == StatusCompleted || s.Status == StatusFailed
}

type ScrollSession struct {
	SessionKey          string        `json:"session_key"`
	LockKey             string        `json:"lock_key"`
	LastAccessAt        time.Time     `json:"-"`
	ScrollWindowTimeout time.Duration `json:"scroll_window_timeout"`
	ScrollLockTimeout   time.Duration `json:"scroll_lock_timeout"`
	MaxSlice            int           `json:"max_slice"`
	SliceMaxFailedNum   int           `json:"slice_max_failed_num"`
	Limit               int           `json:"limit"`

	Cache    bool    `json:"cache"`
	TenantID *string `json:"tenant_id,omitempty"`

	SlicesMap map[string]*SliceStatus `json:"slices_map"`

	mu        sync.RWMutex
	lockToken string
}

func (s *ScrollSession) Slice(key string) *SliceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slice, ok := s.SlicesMap[key]; ok {
		return slice
	}

	slice := &SliceStatus{
		SliceKey:     key,
		SliceMax:     s.MaxSlice,
		MaxFailedNum: s.SliceMaxFailedNum,
		Limit:        s.Limit,
		Status:       StatusPending,
	}
	s.SlicesMap[key] = slice
	return slice
}

func (s *ScrollSession) SliceLength() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.MaxSlice < 2 {
		return 1
	}

	return s.MaxSlice
}

// Slices 返回全部已保存分片的独立快照，供清理逻辑在释放锁后遍历。
// ScrollTarget 也需要复制，避免修改快照时影响会话中的路由信息。
func (session *ScrollSession) Slices() []SliceStatus {
	session.mu.RLock()
	defer session.mu.RUnlock()

	slices := make([]SliceStatus, 0, len(session.SlicesMap))
	for _, slice := range session.SlicesMap {
		snapshot := *slice
		if slice.ScrollTarget != nil {
			target := *slice.ScrollTarget
			snapshot.ScrollTarget = &target
		}
		slices = append(slices, snapshot)
	}
	return slices
}

func (s *ScrollSession) UpdateSliceStatus(key string, value *SliceStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 重试次数超过先定之后，直接算失败
	if value.FailedNum > s.SliceMaxFailedNum {
		value.Status = StatusFailed
	}

	s.SlicesMap[key] = value
	s.LastAccessAt = time.Now()
}

func (s *ScrollSession) lock(ctx context.Context) error {
	token := uuid.NewString()
	locked, err := Client().SetNX(ctx, s.LockKey, token, s.ScrollLockTimeout).Result()
	if err != nil {
		return err
	}
	if !locked {
		return ErrScrollSessionLocked
	}
	s.lockToken = token
	return nil
}

func (s *ScrollSession) unlock(ctx context.Context) error {
	// 锁过期后可能已被下一页取得，只允许当前持有者释放。
	err := Client().Eval(ctx, `
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
end
return 0`, []string{s.LockKey}, s.lockToken).Err()
	s.lockToken = ""
	return err
}

func (s *ScrollSession) release(ctx context.Context) {
	unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scrollStateTimeout)
	defer cancel()
	if err := s.unlock(unlockCtx); err != nil {
		_ = metadata.NewMessage(metadata.MsgRedisLock, "redis unlock failed").Error(ctx, err)
	}
}

func (s *ScrollSession) Clear(ctx context.Context) error {
	return Client().Del(ctx, s.SessionKey).Err()
}

// Reset 在持有会话锁时开始一次新下载，clear_cache 与续页共用同一把锁。
func (s *ScrollSession) Reset(ctx context.Context) error {
	s.SlicesMap = make(map[string]*SliceStatus)
	s.Cache = false
	err := s.Update(ctx)
	if err != nil {
		s.release(ctx)
	}
	return err
}

func (s *ScrollSession) Start(ctx context.Context) (err error) {
	// GetOrCreateScrollSession 已持有锁；直接构造的会话在每页开始时取得锁。
	if s.lockToken != "" {
		return nil
	}
	err = s.lock(ctx)
	if err != nil {
		return metadata.NewMessage(metadata.MsgRedisLock,
			"redis lock failed",
		).Error(ctx, err)
	}
	defer func() {
		if err != nil {
			s.release(ctx)
		}
	}()

	// 取得锁以后读取，防止使用上一请求完成并清理游标前的 pending 快照。
	data, err := Client().Get(ctx, s.SessionKey).Bytes()
	if errors.Is(err, goRedis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = s.UnmarshalBinary(data); err != nil {
		return err
	}
	s.Cache = true
	return nil
}

func (s *ScrollSession) Update(ctx context.Context) error {
	data, err := s.MarshalBinary()
	if err != nil {
		return err
	}
	// 保存与所有权检查在 Redis 内原子执行，过期请求不能覆盖下一持有者的状态。
	updated, err := Client().Eval(ctx, `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
    return 0
end
if tonumber(ARGV[3]) > 0 then
    redis.call('PSETEX', KEYS[2], ARGV[3], ARGV[2])
else
    redis.call('SET', KEYS[2], ARGV[2])
end
return 1`, []string{s.LockKey, s.SessionKey}, s.lockToken, data, s.ScrollWindowTimeout.Milliseconds()).Int()
	if err != nil {
		return err
	}
	if updated == 0 {
		return ErrScrollSessionLockLost
	}
	return nil
}

// Stop 在每页查询退出时保存会话，再清理完成会话的 ES 游标，最后释放锁。
func (s *ScrollSession) Stop(ctx context.Context, clearScroll ClearScrollFunc) error {
	defer s.release(ctx)

	updateCtx := ctx
	if s.Done() {
		// EOF 后客户端可能已经断开；完成状态必须保存成功，才能删除缓存引用的游标。
		var cancel context.CancelFunc
		updateCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), scrollStateTimeout)
		defer cancel()
	}
	if err := s.Update(updateCtx); err != nil {
		return err
	}
	s.clearCompletedScrolls(ctx, clearScroll)
	return nil
}

func (s *ScrollSession) MarshalBinary() ([]byte, error) {
	return json.Marshal(s)
}

func (s *ScrollSession) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, s)
}

// Done 以所有分片完成或耗尽重试次数为会话结束条件。
// FailedNum 恰好达到上限时，分片可能仍为 pending，因此不能用各分片的 Done 代替。
func (s *ScrollSession) Done() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.SlicesMap) == 0 {
		return false
	}

	for _, sliceValue := range s.SlicesMap {
		if sliceValue.Status != StatusCompleted && sliceValue.FailedNum < s.SliceMaxFailedNum {
			return false
		}
	}
	return true
}

func NewScrollSession(queryTsStr string, scrollTimeout, scrollLockTimeout time.Duration, maxSlice, sliceMaxFailedNum, limit int) *ScrollSession {
	session := &ScrollSession{
		SessionKey:          SessionKeyPrefix + queryTsStr,
		LockKey:             ScrollLockKeyPrefix + queryTsStr,
		LastAccessAt:        time.Now(),
		ScrollWindowTimeout: scrollTimeout,
		ScrollLockTimeout:   scrollLockTimeout,
		MaxSlice:            maxSlice,
		SliceMaxFailedNum:   sliceMaxFailedNum,
		Limit:               limit,
		SlicesMap:           make(map[string]*SliceStatus),
	}

	return session
}

func GetOrCreateScrollSession(ctx context.Context, queryTsStr string, scrollWindowTimeout, scrollLockTimeout string, maxSlice, Limit int) (*ScrollSession, error) {
	scrollWindowTimeoutDuration, err := time.ParseDuration(scrollWindowTimeout)
	if err != nil {
		return nil, err
	}
	scrollLockTimeoutDuration, err := time.ParseDuration(scrollLockTimeout)
	if err != nil {
		return nil, err
	}

	session := NewScrollSession(queryTsStr, scrollWindowTimeoutDuration, scrollLockTimeoutDuration, maxSlice, DefaultSliceMaxFailedNum, Limit)
	if err := session.Start(ctx); err != nil {
		return nil, err
	}
	user := metadata.GetUser(ctx)
	tenantID := user.TenantID
	if session.Cache && !session.Done() {
		if session.TenantID != nil && *session.TenantID != tenantID {
			session.release(ctx)
			return nil, ErrScrollSessionTenant
		}
		// 空间已包含在查询正文中。旧版未保存租户，首次续页时补充，游标和进度保持不变。
		session.TenantID = &tenantID
		log.Debugf(ctx, "session cache")
		return session, nil
	}
	session.TenantID = &tenantID

	// 完成会话再次请求时开始新下载；状态替换与读取都在同一把锁内。
	if err = session.Reset(ctx); err != nil {
		return nil, err
	}

	log.Debugf(ctx, "session new")
	return session, nil
}
