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
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
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
	HasMoreData bool            `json:"has_more_data"`
	SliceStates SliceStates     `json:"slice_states"`
}

type SliceStates []SliceState

func (r *RTState) PickSlices(maxRunningCount, maxFailedCount int) (SliceStates, error) {
	if r == nil {
		return nil, fmt.Errorf("RTState is nil")
	}

	activeSlices := r.getActiveSlices()

	failedCount := r.countFailedSlices(activeSlices)
	if failedCount > maxFailedCount {
		return nil, fmt.Errorf("too many failed slices: %d, max allowed: %d", failedCount, maxFailedCount)
	}

	if len(activeSlices) < maxRunningCount {
		activeSlices = r.ensureSliceCount(activeSlices, maxRunningCount)
	}

	r.SliceStates = activeSlices
	return r.SliceStates, nil
}

func (r *RTState) getActiveSlices() SliceStates {
	if r.SliceStates == nil {
		return nil
	}

	var activeSlices SliceStates
	for _, slice := range r.SliceStates {
		if r.isSliceActive(slice) {
			activeSlices = append(activeSlices, slice)
		}
	}
	return activeSlices
}

func (r *RTState) isSliceActive(slice SliceState) bool {
	if slice.Status == SliceStatusRunning {
		return true
	}
	if slice.Status == SliceStatusFailed && slice.RetryCount < slice.MaxRetries {
		return true
	}
	return false
}

func (r *RTState) countFailedSlices(slices SliceStates) int {
	count := 0
	for _, slice := range slices {
		if slice.Status == SliceStatusFailed {
			count++
		}
	}
	return count
}

func (r *RTState) ensureSliceCount(currentSlices SliceStates, targetCount int) SliceStates {
	if len(currentSlices) >= targetCount {
		return currentSlices
	}

	template := r.getSliceTemplate(currentSlices)

	for len(currentSlices) < targetCount {
		newSlice := r.createNextSlice(template, len(currentSlices))
		currentSlices = append(currentSlices, newSlice)
	}

	return currentSlices
}

func (r *RTState) getSliceTemplate(slices SliceStates) SliceState {
	if len(slices) > 0 {
		maxSlice := slices[0]
		for _, slice := range slices {
			if slice.SliceID > maxSlice.SliceID {
				maxSlice = slice
			}
		}
		return maxSlice
	}

	return SliceState{
		SliceID:     -1,
		StartOffset: 0,
		EndOffset:   1000,
		Size:        1000,
		Status:      SliceStatusRunning,
		MaxRetries:  3,
	}
}

func (r *RTState) createNextSlice(template SliceState, currentIndex int) SliceState {
	newSlice := SliceState{
		SliceID:     currentIndex,
		StartOffset: template.EndOffset + int64(currentIndex-template.SliceID-1)*template.Size,
		EndOffset:   template.EndOffset + int64(currentIndex-template.SliceID)*template.Size,
		Size:        template.Size,
		Status:      SliceStatusRunning,
		ErrorMsg:    "",
		RetryCount:  0,
		MaxRetries:  template.MaxRetries,
		ScrollID:    "",
		ConnectInfo: template.ConnectInfo,
	}

	if template.SliceID == -1 {
		newSlice.SliceID = currentIndex
		newSlice.StartOffset = int64(currentIndex) * template.Size
		newSlice.EndOffset = int64(currentIndex+1) * template.Size
	}

	return newSlice
}

type SliceState struct {
	SliceID     int    `json:"slice_id"`
	StartOffset int64  `json:"start_offset"`
	EndOffset   int64  `json:"end_offset"`
	Size        int64  `json:"size"`         // slice数据量 (从Query.Limit获取)
	Status      string `json:"status"`       // slice状态: "running", "done", "failed"
	ErrorMsg    string `json:"error_msg"`    // 错误信息 (仅当status为failed时)
	RetryCount  int    `json:"retry_count"`  // 重试次数
	MaxRetries  int    `json:"max_retries"`  // 最大重试次数 (默认3次)
	ScrollID    string `json:"scroll_id"`    // ES scroll ID for this slice
	ConnectInfo string `json:"connect_info"` // 连接信息，支持不同connect的tableID
}

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
	SessionKeyPrefix = "scroll:session:"
	LockKeyPrefix    = "scroll:lock:"
)

func GetSessionKey(queryTsKey string) string {
	return SessionKeyPrefix + queryTsKey
}

func GetLockKey(queryTsKey string) string {
	return LockKeyPrefix + queryTsKey
}

type QueryTsKey struct {
	QueryTs  interface{} `json:"queryTs"`
	Username string      `json:"username"`
}

var ScrollGenerateQueryTsKey = func(queryTs interface{}, username string) (string, error) {
	log.Debugf(context.TODO(), "[redis] generate query ts key with username: %s", username)

	keyStruct := QueryTsKey{
		QueryTs:  queryTs,
		Username: username,
	}

	queryBytes, err := json.Marshal(keyStruct)
	if err != nil {
		return "", err
	}
	return string(queryBytes), nil
}

var ScrollAcquireRedisLock = func(ctx context.Context, lockKey string, timeout time.Duration) (interface{}, error) {
	log.Debugf(ctx, "[redis] acquire lock %s", lockKey)
	client := globalInstance.client
	if client == nil {
		return nil, fmt.Errorf("redis client not available")
	}

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
			session.LastAccessAt = time.Now()
			if updateErr := ScrollUpdateSession(ctx, sessionKey, &session); updateErr != nil {
				log.Warnf(ctx, "[redis] failed to update session access time: %v", updateErr)
			}
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
		return nil, fmt.Errorf("failed to save new session: %v", err)
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

	return client.Set(ctx, sessionKey, sessionBytes, session.ScrollTimeout).Err()
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
