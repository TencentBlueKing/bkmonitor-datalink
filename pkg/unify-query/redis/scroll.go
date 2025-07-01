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
	"encoding/json"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

type SessionObject struct {
	QueryTs        string         `json:"query_ts"`
	Status         string         `json:"status"`
	CreateAt       time.Time      `json:"create_at"`
	LastAccessAt   time.Time      `json:"last_access_at"`
	ScrollTimeout  time.Duration  `json:"scroll_timeout"`
	LockTimeout    time.Duration  `json:"lock_timeout"`
	MaxSlice       int            `json:"max_slice"`
	Limit          int            `json:"limit"`
	Index          int            `json:"index"`
	QueryReference QueryReference `json:"query_reference"`
}

type QueryReference map[string]*RTState

func (s *SessionObject) IsQueryReferenceEmpty() bool {
	if s.QueryReference == nil {
		return true
	}
	return len(s.QueryReference) == 0
}

type RTState struct {
	Query       *metadata.Query `json:"query"`
	Type        string          `json:"type"`
	Offset      int64           `json:"offset"`
	ScrollIDs   []string        `json:"scroll_ids"`
	HasMoreData bool            `json:"has_more_data"`
	SliceStates []SliceState    `json:"slice_states"`
}

func (r *RTState) PickSlices(maxRunningCount, maxFailedCount int) ([]SliceState, error) {
	if r == nil {
		return nil, fmt.Errorf("RTState is nil")
	}

	remainSlices, filteredSlices := r.collectRTSlices([]string{SliceStatusRunning, SliceStatusFailed}, filterRunningAndFailedSlicesCallback)
	if filteredSlices != nil && len(filteredSlices) > maxFailedCount {
		return nil, fmt.Errorf("too many failed slices: %d, max allowed: %d", len(filteredSlices), maxFailedCount)
	}
	if len(remainSlices) < maxRunningCount {
		var lastOne SliceState
		if len(remainSlices) > 0 {
			lastOne = remainSlices[len(remainSlices)-1]
		} else {
			lastOne = SliceState{
				SliceID:     -1,
				StartOffset: 0,
				EndOffset:   1000,
				Size:        1000,
				Status:      SliceStatusRunning,
				MaxRetries:  3,
			}
		}
		r.SliceStates = append(remainSlices, r.makeUpSlices(lastOne, maxRunningCount-len(remainSlices))...)
	}

	return r.SliceStates, nil

}

func (r *RTState) makeUpSlices(lastOne SliceState, count int) []SliceState {
	if count <= 0 || len(r.SliceStates) >= count {
		return r.SliceStates
	}

	if r.SliceStates == nil {
		r.SliceStates = make([]SliceState, 0, count)
	}
	startOffset := lastOne.EndOffset

	for i := len(r.SliceStates); i < count; i++ {
		r.SliceStates = append(r.SliceStates, SliceState{
			SliceID:     i,
			StartOffset: startOffset,
			EndOffset:   startOffset + lastOne.Size,
			Status:      SliceStatusRunning,
			Size:        lastOne.Size,
			ErrorMsg:    "",
			RetryCount:  0,
			MaxRetries:  lastOne.MaxRetries,
		})
		startOffset += lastOne.Size
	}
	return r.SliceStates
}

var filterRunningAndFailedSlicesCallback = func(r SliceState) bool {
	if r.Status == SliceStatusRunning {
		return true
	}
	if r.Status == SliceStatusFailed && r.RetryCount < r.MaxRetries {
		return true
	}
	return false
}

func (r *RTState) collectRTSlices(status []string, filterCb func(r SliceState) bool) ([]SliceState, []SliceState) {
	if r == nil || r.SliceStates == nil {
		return nil, nil
	}

	statusSet := set.New[string]()
	for _, s := range status {
		statusSet.Add(s)
	}

	remainSliceStates := make([]SliceState, 0)
	filteredSliceStates := make([]SliceState, 0)
	for _, slice := range r.SliceStates {
		if filterCb != nil && !filterCb(slice) {
			remainSliceStates = append(remainSliceStates, slice)
			continue
		}
		if statusSet.Existed(slice.Status) {
			filteredSliceStates = append(filteredSliceStates, slice)
		} else {
			remainSliceStates = append(remainSliceStates, slice)
		}
	}
	return remainSliceStates, filteredSliceStates
}

type SliceState struct {
	SliceID     int    `json:"slice_id"`
	StartOffset int64  `json:"start_offset"`
	EndOffset   int64  `json:"end_offset"`
	Size        int64  `json:"size"`        // slice数据量 (从Query.Limit获取)
	Status      string `json:"status"`      // slice状态: "running", "done", "failed"
	ErrorMsg    string `json:"error_msg"`   // 错误信息 (仅当status为failed时)
	RetryCount  int    `json:"retry_count"` // 重试次数
	MaxRetries  int    `json:"max_retries"` // 最大重试次数 (默认3次)
}

// ScrollResponse scroll查询响应结构
type ScrollResponse struct {
	QueryTs     string           `json:"query_ts"`     // 会话标识
	Total       int64            `json:"total"`        // 本次返回数据量
	List        []map[string]any `json:"list"`         // 数据列表
	HasMore     bool             `json:"has_more"`     // 是否还有更多数据
	SessionInfo SessionInfo      `json:"session_info"` // 会话信息
}

// SessionInfo 会话状态信息
type SessionInfo struct {
	Index    int    `json:"index"`     // 当前请求次数
	MaxSlice int    `json:"max_slice"` // 并发slice数
	Size     int    `json:"size"`      // 每次数据量
	Status   string `json:"status"`    // 会话状态
}

// ScrollError scroll专用错误类型
type ScrollError struct {
	Code    string `json:"code"`    // 错误代码
	Message string `json:"message"` // 错误信息
	Retry   bool   `json:"retry"`   // 是否可重试
}

func (e *ScrollError) Error() string {
	return e.Message
}

type SliceResult struct {
	SliceID  int   `json:"slice_id"`
	RowCount int   `json:"row_count"`
	Error    error `json:"error"`
}

type SliceQuery struct {
	SliceID int    `json:"slice_id"`
	Offset  int64  `json:"offset"`
	Limit   int    `json:"limit"`
	Action  string `json:"action"`
}

const (
	SessionStatusRunning = "RUNNING"
	SessionStatusFailed  = "FAILED"
)

const (
	SliceStatusRunning = "running"
	SliceStatusDone    = "done"
	SliceStatusFailed  = "failed"
)

const (
	ErrorCodeConcurrentRequest = "CONCURRENT_REQUEST"
	ErrorCodeSessionExpired    = "SESSION_EXPIRED"
	ErrorCodeLockTimeout       = "LOCK_TIMEOUT"
	ErrorCodeQueryFailed       = "QUERY_FAILED"
)

const (
	ScrollMax = 10
)

const (
	SessionKeyPrefix = "scroll:session:"
	LockKeyPrefix    = "scroll:lock:"
)

func GetSessionKey(queryTsKey string) string {
	return SessionKeyPrefix + queryTsKey
}

func GetLockKey(queryTsKey string) string {
	return LockKeyPrefix + queryTsKey
}

var ScrollGenerateQueryTsKey = func(queryTs interface{}, username string) (string, error) {
	log.Debugf(context.TODO(), "[redis] generate query ts key with username: %s", username)

	// 创建包含queryTs和username的键映射
	keyMap := map[string]interface{}{
		"queryTs":  queryTs,
		"username": username,
	}

	// 使用JSON序列化生成唯一标识
	queryBytes, err := json.Marshal(keyMap)
	if err != nil {
		return "", err
	}
	return string(queryBytes), nil
}

// ScrollAcquireRedisLock 获取Redis分布式锁
var ScrollAcquireRedisLock = func(ctx context.Context, lockKey string, timeout time.Duration) (interface{}, error) {
	log.Debugf(ctx, "[redis] acquire lock %s", lockKey)
	client := globalInstance.client
	if client == nil {
		return nil, fmt.Errorf("redis client not available")
	}

	// 使用SET NX EX命令实现分布式锁
	result := client.SetNX(ctx, lockKey, "locked", timeout)
	if result.Err() != nil {
		return nil, result.Err()
	}

	if !result.Val() {
		return nil, fmt.Errorf("failed to acquire lock, already locked")
	}

	return lockKey, nil
}

var ScrollReleaseRedisLock = func(ctx context.Context, lock interface{}) error {
	log.Debugf(ctx, "[redis] release lock")
	client := globalInstance.client
	if client == nil {
		return fmt.Errorf("redis client not available")
	}

	lockKey, ok := lock.(string)
	if !ok {
		return fmt.Errorf("invalid lock type")
	}

	return client.Del(ctx, lockKey).Err()
}

var ScrollClearRedisSession = func(ctx context.Context, sessionKey string) error {
	log.Debugf(ctx, "[redis] clear session %s", sessionKey)
	client := globalInstance.client
	if client == nil {
		return fmt.Errorf("redis client not available")
	}

	return client.Del(ctx, sessionKey).Err()
}

var ScrollGetOrCreateSession = func(ctx context.Context, sessionKey string, queryTsJSON string) (*SessionObject, error) {
	log.Debugf(ctx, "[redis] get or create session %s", sessionKey)
	client := globalInstance.client
	if client == nil {
		return nil, fmt.Errorf("redis client not available")
	}
	result := client.Get(ctx, sessionKey)
	if result.Err() == nil {
		var session SessionObject
		if err := json.Unmarshal([]byte(result.Val()), &session); err == nil {
			return &session, nil
		}
	}

	session := &SessionObject{
		QueryTs:        queryTsJSON,
		Status:         SessionStatusRunning,
		CreateAt:       time.Now(),
		LastAccessAt:   time.Now(),
		Index:          0,
		QueryReference: make(map[string]*RTState),
	}

	if err := ScrollUpdateSession(ctx, sessionKey, session); err != nil {
		return nil, err
	}

	return session, nil
}

var ScrollUpdateSession = func(ctx context.Context, sessionKey string, session *SessionObject) error {
	log.Debugf(ctx, "[redis] update session %s", sessionKey)
	client := globalInstance.client
	if client == nil {
		return fmt.Errorf("redis client not available")
	}

	sessionBytes, err := json.Marshal(session)
	if err != nil {
		return err
	}

	// 设置过期时间为1小时
	return client.Set(ctx, sessionKey, sessionBytes, time.Hour).Err()
}

var ScrollDeleteSession = func(ctx context.Context, sessionKey string) error {
	log.Debugf(ctx, "[redis] delete session %s", sessionKey)
	client := globalInstance.client
	if client == nil {
		return fmt.Errorf("redis client not available")
	}

	return client.Del(ctx, sessionKey).Err()
}

var ScrollCheckSessionHasMore = func(session *SessionObject) bool {
	if session == nil || session.QueryReference == nil {
		return false
	}

	for _, rtState := range session.QueryReference {
		if rtState.HasMoreData {
			return true
		}
	}
	return false
}

func MarshalSessionObject(session *SessionObject) (string, error) {
	data, err := json.Marshal(session)
	if err != nil {
		return "", fmt.Errorf("failed to marshal session object: %v", err)
	}
	return string(data), nil
}

func UnmarshalSessionObject(data string) (*SessionObject, error) {
	var session SessionObject
	err := json.Unmarshal([]byte(data), &session)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal session object: %v", err)
	}
	return &session, nil
}
