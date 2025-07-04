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

var GetSliceKey = func(queryTsKey string) string {
	return fmt.Sprintf("%s%s", SliceKeyPrefix, queryTsKey)
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

var generalKey = func(keyPrefix string, queryTs any, userName string) (string, error) {
	keyStruct := map[string]any{
		"prefix":   keyPrefix,
		"queryTs":  queryTs,
		"username": userName,
	}

	queryBytes, err := json.Marshal(keyStruct)
	if err != nil {
		log.Errorf(
			context.Background(),
			"failed to marshal session key: %v",
			err,
		)
		return "", fmt.Errorf("failed to marshal session key: %v", err)
	}
	return string(queryBytes), err
}

var scrollSessionKey = func(queryTs any, userName string) (string, error) {
	return generalKey(SessionKeyPrefix, queryTs, userName)
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

var ScrollGetOrCreateSession = func(ctx context.Context, sessionKey string, forceClear bool) (*ScrollSession, error) {
	client := globalInstance.client
	if client == nil {
		return nil, fmt.Errorf("redis client not available")
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
			if updateErr := scrollUpdateSession(ctx, sessionKey, &session); updateErr != nil {
				log.Warnf(
					ctx,
					"[redis] failed to update session access time: %v",
					updateErr,
				)
			}
			return &session, nil
		}
	}

	session := &ScrollSession{
		Key:          sessionKey,
		CreateAt:     time.Now(),
		LastAccessAt: time.Now(),
		Index:        0,
	}

	if err := scrollUpdateSession(ctx, sessionKey, session); err != nil {
		return nil, fmt.Errorf("failed to save new session: %v", err)
	}

	return session, nil
}

var scrollUpdateSession = func(ctx context.Context, key string, session *ScrollSession) error {
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

var ScrollUpdateSession = func(ctx context.Context, sessionKey string, session *ScrollSession) error {
	return scrollUpdateSession(ctx, sessionKey, session)
}

var ScrollDeleteSession = func(ctx context.Context, sessionKey string) error {
	log.Debugf(ctx, "[redis] delete session %s", sessionKey)
	client := globalInstance.client
	if client == nil {
		return fmt.Errorf("redis client not available")
	}

	return client.Del(ctx, sessionKey).Err()
}

var ScrollUpdateSlice = func(ctx context.Context, sliceKey string, slice SliceState) error {
	client := globalInstance.client
	if client == nil {
		return fmt.Errorf("redis client not available")
	}

	slice.LastAccessAt = time.Now()
	sliceBytes, err := json.Marshal(slice)
	if err != nil {
		return err
	}

	return client.Set(ctx, sliceKey, sliceBytes, slice.ScrollTimeOut).Err()
}

var CheckIsScrollAllDone = func(ctx context.Context, queryTs any, userName string) bool {
	key, err := scrollSessionKey(queryTs, userName)
	if err != nil {
		log.Warnf(ctx, "[redis] failed to generate session key: %v", err)
		return false
	}
	client := globalInstance.client
	if client == nil {
		log.Warnf(ctx, "[redis] redis client not available")
		return false
	}

	result := client.Get(ctx, key)
	if result.Err() != nil {
		log.Warnf(ctx, "[redis] failed to get session: %v", result.Err())
		return false
	}

	var session ScrollSession
	if err := json.Unmarshal([]byte(result.Val()), &session); err != nil {
		log.Warnf(ctx, "[redis] failed to unmarshal session: %v", err)
		return false
	}

	slices, err := session.getAllSlices(ctx)
	if err != nil {
		log.Warnf(ctx, "[redis] failed to get all slices: %v", err)
		return false
	}
	allDone := true
	for _, slice := range slices {
		if slice.Status != SliceStatusDone {
			allDone = false
			break
		}
	}
	if allDone {
		log.Debugf(ctx, "[redis] all slices done for session %s", key)
	} else {
		log.Debugf(ctx, "[redis] not all slices done for session %s", key)
	}
	return allDone
}
