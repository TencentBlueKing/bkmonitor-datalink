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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

var ScrollGenerateQueryTsKey = func(queryTs any, userName string) (string, error) {
	keyStruct := map[string]any{
		"queryTs":  queryTs,
		"username": userName,
	}

	queryBytes, err := json.Marshal(keyStruct)
	if err != nil {
		return "", fmt.Errorf("failed to marshal queryTs key: %v", err)
	}
	return string(queryBytes), nil
}

func GetSessionKey(queryTsKey string) string {
	return fmt.Sprintf("%s%s", SessionKeyPrefix, queryTsKey)
}

func GetLockKey(queryTsKey string) string {
	return fmt.Sprintf("%s%s", LockKeyPrefix, queryTsKey)
}

func ScrollAcquireRedisLock(ctx context.Context, lockKey string, timeout time.Duration) (string, error) {
	client := globalInstance.client

	result := client.SetNX(ctx, lockKey, "locked", timeout)
	if result.Err() != nil {
		return "", result.Err()
	}

	if !result.Val() {
		return "", fmt.Errorf("failed to acquire lock, already locked")
	}

	return lockKey, nil
}

func ScrollReleaseRedisLock(ctx context.Context, lock interface{}) error {
	client := globalInstance.client

	lockKey, ok := lock.(string)
	if !ok {
		return fmt.Errorf("invalid lock type")
	}

	return client.Del(ctx, lockKey).Err()
}

func ScrollGetOrCreateSession(ctx context.Context, sessionKey string, forceClear bool, timeout time.Duration, maxSlice int, limit int) (ScrollSession, error) {
	client := globalInstance.client
	if client == nil {
		return ScrollSession{}, fmt.Errorf("redis client not available")
	}
	if forceClear {
		if err := client.Del(ctx, sessionKey).Err(); err != nil {
			log.Warnf(ctx, "failed to clear session: %v", err)
		}
	}

	result := client.Get(ctx, sessionKey)
	if result.Err() == nil {
		var session ScrollSession
		if err := json.Unmarshal([]byte(result.Val()), &session); err == nil {
			session.LastAccessAt = time.Now()
			if err = scrollUpdateSession(ctx, sessionKey, session); err != nil {
				log.Warnf(
					ctx,
					"failed to update session access time: %v",
					err,
				)
			}
			return session, nil
		}
	} else {
		return ScrollSession{}, fmt.Errorf("failed to get session: %v", result.Err())
	}

	session := ScrollSession{
		Key:           sessionKey,
		CreateAt:      time.Now(),
		LastAccessAt:  time.Now(),
		Index:         0,
		MaxSlice:      maxSlice,
		Limit:         limit,
		ScrollTimeout: timeout,
		Status:        SessionStatusRunning,
		ScrollIDs:     make(map[SliceStatus]string),
		SliceStatus:   make(map[SliceStatus]struct{}),
	}

	if err := scrollUpdateSession(ctx, sessionKey, session); err != nil {
		return ScrollSession{}, fmt.Errorf("failed to save new session: %v", err)
	}

	return session, nil
}

func scrollUpdateSession(ctx context.Context, key string, session ScrollSession) error {
	client := globalInstance.client

	session.LastAccessAt = time.Now()
	sessionBytes, err := json.Marshal(session)
	if err != nil {
		return err
	}

	return client.Set(ctx, key, sessionBytes, session.ScrollTimeout).Err()
}

func UpdateSession(ctx context.Context, sessionKey string, session ScrollSession) error {
	if globalInstance == nil {
		return fmt.Errorf("redis instance not initialized")
	}
	client := globalInstance.client
	if client == nil {
		return fmt.Errorf("redis client not available")
	}

	session.LastAccessAt = time.Now()
	sessionBytes, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to marshal session: %v", err)
	}

	err = client.Set(ctx, sessionKey, sessionBytes, session.ScrollTimeout).Err()
	if err != nil {
		return fmt.Errorf("failed to update session: %v", err)
	}

	return err
}

func ScrollProcessSliceResult(ctx context.Context, session *ScrollSession, connect, tableID, scrollID string, sliceIdx int, tsDbType string, size int64, options metadata.ResultTableOptions) error {
	isDone := size == 0

	if isDone {
		session.Status = SessionStatusDone
	}

	if tsDbType == consul.ElasticsearchStorageType {
		newScrollID := ""
		if options != nil {
			resultOption := options.GetOption(tableID, connect)
			if resultOption != nil && resultOption.ScrollID != "" {
				newScrollID = resultOption.ScrollID
				log.Debugf(ctx, "Found new scrollID in result: %s", newScrollID)
			}
		}

		if newScrollID != "" {
			session.SetScrollID(connect, tableID, newScrollID, sliceIdx)
			log.Debugf(ctx, "Updated scrollID for slice %d: %s", sliceIdx, newScrollID)
		} else if isDone {
			session.RemoveScrollID(connect, tableID, sliceIdx)
			session.MarkSliceDone(connect, tableID, sliceIdx)
			log.Debugf(ctx, "slice %d completed for %s|%s (empty result and no new scrollID)", sliceIdx, connect, tableID)
		}

		hasMoreData := session.HasMoreData(consul.ElasticsearchStorageType)
		if !hasMoreData {
			session.Status = SessionStatusDone
			log.Debugf(ctx, "All slices completed, session marked as done")
		}
	}

	return nil
}
