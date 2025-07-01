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
		log.Errorf(
			context.Background(),
			"failed to marshal queryTs key: %v",
			err,
		)
		return "", fmt.Errorf("failed to marshal queryTs key: %v", err)
	}
	return string(queryBytes), nil
}

var GetSessionKey = func(queryTsKey string) string {
	return fmt.Sprintf("%s%s", SessionKeyPrefix, queryTsKey)
}

var GetLockKey = func(queryTsKey string) string {
	return fmt.Sprintf("%s%s", LockKeyPrefix, queryTsKey)
}

var ScrollAcquireRedisLock = func(ctx context.Context, lockKey string, timeout time.Duration) (string, error) {
	client := globalInstance.client
	if client == nil {
		return "", fmt.Errorf("redis client not available")
	}

	result := client.SetNX(ctx, lockKey, "locked", timeout)
	if result.Err() != nil {
		return "", result.Err()
	}

	if !result.Val() {
		return "", fmt.Errorf("failed to acquire lock, already locked")
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

var ScrollGetOrCreateSession = func(ctx context.Context, sessionKey string, forceClear bool, timeout time.Duration, maxSlice int, limit int) (ScrollSession, error) {
	client := globalInstance.client
	if client == nil {
		return ScrollSession{}, fmt.Errorf("redis client not available")
	}
	if forceClear {
		if err := client.Del(ctx, sessionKey).Err(); err != nil {
			log.Warnf(ctx, "[redis] failed to clear session: %v", err)
		}
	}

	result := client.Get(ctx, sessionKey)
	if result.Err() == nil {
		var session ScrollSession
		if err := json.Unmarshal([]byte(result.Val()), &session); err == nil {
			session.LastAccessAt = time.Now()
			if updateErr := scrollUpdateSession(ctx, sessionKey, session); updateErr != nil {
				log.Warnf(
					ctx,
					"[redis] failed to update session access time: %v",
					updateErr,
				)
			}
			return session, nil
		}
	}

	session := ScrollSession{
		Key:            sessionKey,
		CreateAt:       time.Now(),
		LastAccessAt:   time.Now(),
		Index:          0,
		MaxSlice:       maxSlice,
		Limit:          limit,
		ScrollTimeout:  timeout,
		Status:         SessionStatusRunning,
		ScrollIDs:      make(map[string][]string),
		ScrollIDStatus: make(map[string]bool),
	}

	if err := scrollUpdateSession(ctx, sessionKey, session); err != nil {
		return ScrollSession{}, fmt.Errorf("failed to save new session: %v", err)
	}

	return session, nil
}

var scrollUpdateSession = func(ctx context.Context, key string, session ScrollSession) error {
	log.Debugf(ctx, "[redis] update session %s", key)
	client := globalInstance.client
	if client == nil {
		return fmt.Errorf("redis client not available")
	}

	session.LastAccessAt = time.Now()
	sessionBytes, err := json.Marshal(session)
	if err != nil {
		return err
	}

	return client.Set(ctx, key, sessionBytes, session.ScrollTimeout).Err()
}

var UpdateSession = func(ctx context.Context, sessionKey string, session ScrollSession) error {
	log.Debugf(ctx, "[redis] UpdateSession: session %s", sessionKey)
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
		log.Errorf(ctx, "[redis] Failed to update session %s: %v", sessionKey, err)
	} else {
		log.Debugf(ctx, "[redis] Successfully updated session %s", sessionKey)
	}

	return err
}

var ScrollProcessSliceResult = func(ctx context.Context, session *ScrollSession, connect, tableID, scrollID string, sliceIdx int, tsDbType string, size int64, options metadata.ResultTableOptions) error {
	isDone := size == 0

	if tsDbType == consul.ElasticsearchStorageType {
		log.Debugf(ctx, "[DEBUG] Processing ES slice result: connect=%s, tableID=%s, scrollID=%s, sliceIdx=%d, size=%d", connect, tableID, scrollID, sliceIdx, size)
		log.Debugf(ctx, "[DEBUG] Session ScrollIDs before processing: %+v", session.ScrollIDs)
		log.Debugf(ctx, "[DEBUG] Session ScrollIDStatus before processing: %+v", session.ScrollIDStatus)

		// 如果当前scrollID不为空，标记为完成（因为我们已经使用了它）
		if scrollID != "" {
			session.MarkScrollIDDone(connect, tableID, scrollID, sliceIdx)
			log.Debugf(ctx, "[DEBUG] Marked current scrollID %s as done for %s|%s|%d", scrollID, connect, tableID, sliceIdx)
		}

		// 处理新的scrollID
		if options != nil {
			resultOption := options.GetOption(tableID, connect)
			if resultOption != nil && resultOption.ScrollID != "" {
				newScrollID := resultOption.ScrollID
				log.Debugf(ctx, "[DEBUG] Found new scrollID in result: %s", newScrollID)

				// 只有当返回的scrollID不为空且与当前scrollID不同时才添加
				if newScrollID != scrollID {
					session.AddScrollID(connect, tableID, newScrollID, sliceIdx)
					log.Debugf(ctx, "[DEBUG] Added new scrollID %s for %s|%s|%d", newScrollID, connect, tableID, sliceIdx)
				} else {
					log.Debugf(ctx, "[DEBUG] New scrollID %s is same as current, not adding", newScrollID)
				}
			} else {
				log.Debugf(ctx, "[DEBUG] No new scrollID found in result options")
				if isDone {
					log.Debugf(ctx, "[DEBUG] Query returned empty result, slice %d completed", sliceIdx)
				}
			}
		} else {
			log.Debugf(ctx, "[DEBUG] Options is nil")
		}

		log.Debugf(ctx, "[DEBUG] Session ScrollIDs after processing: %+v", session.ScrollIDs)
		log.Debugf(ctx, "[DEBUG] Session ScrollIDStatus after processing: %+v", session.ScrollIDStatus)

		// 检查ES是否所有scrollID都处理完成
		hasMoreData := session.HasMoreData("elasticsearch")
		log.Debugf(ctx, "[DEBUG] HasMoreData check result: %t", hasMoreData)
		if !hasMoreData {
			session.Status = SessionStatusDone
			log.Debugf(ctx, "[DEBUG] All ES scrollIDs processed, setting session status to done")
		}
	} else {
		// Doris场景：遇到空结果直接标记为完成
		if isDone {
			log.Debugf(ctx, "[DEBUG] Doris query completed for %s|%s (empty result, marking session as done)", connect, tableID)
			session.Status = SessionStatusDone
		} else {
			log.Debugf(ctx, "[DEBUG] Doris query for %s|%s still has data", connect, tableID)
		}
	}

	return nil
}
