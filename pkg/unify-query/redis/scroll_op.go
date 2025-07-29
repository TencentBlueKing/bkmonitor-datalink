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
	"fmt"
	"time"

	redis "github.com/go-redis/redis/v8"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func isRedisNilError(err error) bool {
	return errors.Is(err, redis.Nil)
}

const (
	clearCacheField = "clear_cache"
)

func ScrollGenerateQueryTsKey(queryTs any, userName string) (string, error) {
	queryTsMap, err := cast.ToStringMapE(queryTs)
	if err != nil {
		return "", err
	}
	if _, ok := queryTsMap[clearCacheField]; ok {
		delete(queryTsMap, clearCacheField)
	}

	keyData := map[string]any{
		"queryTs":  queryTsMap,
		"username": userName,
	}

	jsonBytes, err := json.StableMarshal(keyData)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}

func GetSessionKey(queryTsKey string) string {
	return fmt.Sprintf("%s%s", SessionKeyPrefix, queryTsKey)
}

func GetLockKey(queryTsKey string) string {
	return fmt.Sprintf("%s%s", LockKeyPrefix, queryTsKey)
}

func ScrollAcquireRedisLock(ctx context.Context, lockKey string, timeout time.Duration) error {
	result := Client().SetNX(ctx, lockKey, "locked", timeout)
	if result.Err() != nil {
		return result.Err()
	}

	if !result.Val() {
		return fmt.Errorf("failed to acquire lock, already locked")
	}

	return nil
}

func ScrollReleaseRedisLock(ctx context.Context, lock interface{}) error {
	lockKey, ok := lock.(string)
	if !ok {
		return fmt.Errorf("invalid lock type")
	}

	return Client().Del(ctx, lockKey).Err()
}

func ScrollGetOrCreateSession(ctx context.Context, sessionKey string, forceClear bool, timeout time.Duration, maxSlice int, limit int) (*ScrollSession, error) {
	if forceClear {
		if err := Client().Del(ctx, sessionKey).Err(); err != nil {
			return nil, err
		}
	}

	result := Client().Get(ctx, sessionKey)
	if result.Err() == nil {
		var session *ScrollSession
		if err := json.Unmarshal([]byte(result.Val()), &session); err == nil {
			session.LastAccessAt = time.Now()
			if err = scrollUpdateSession(ctx, sessionKey, session); err != nil {
				return nil, err
			}
			return session, nil
		}
	} else if !isRedisNilError(result.Err()) {
		return nil, result.Err()
	}

	session := NewScrollSession(maxSlice, timeout, limit)

	if err := scrollUpdateSession(ctx, sessionKey, session); err != nil {
		return nil, err
	}

	return session, nil
}

func scrollUpdateSession(ctx context.Context, key string, session *ScrollSession) error {
	session.LastAccessAt = time.Now()
	sessionBytes, err := json.Marshal(session)
	if err != nil {
		return err
	}

	return Client().Set(ctx, key, sessionBytes, session.ScrollTimeout).Err()
}

func UpdateSession(ctx context.Context, sessionKey string, session *ScrollSession) error {
	session.LastAccessAt = time.Now()
	sessionBytes, err := json.Marshal(session)
	if err != nil {
		return err
	}

	err = Client().Set(ctx, sessionKey, sessionBytes, session.ScrollTimeout).Err()
	if err != nil {
		return err
	}

	return nil
}

func ScrollProcessSliceResult(ctx context.Context, slice SliceInfo, sessionKey string, session *ScrollSession, connect, tableID string, sliceIdx int, tsDbType string, size int64, options metadata.ResultTableOptions) error {
	isSliceDone := size == 0
	var newScrollID string
	if options != nil {
		if opt := options.GetOption(tableID, connect); opt != nil {
			newScrollID = opt.ScrollID
		}
	}

	if isSliceDone {
		session.RemoveScrollID(slice, connect, tableID, sliceIdx)
	} else {
		session.AddScrollId(connect, tableID, newScrollID, sliceIdx)
	}
	isAfterSlice := slice.ScrollID != ""
	if isAfterSlice && !session.HasMoreData(tsDbType) {
		session.Status = SessionStatusDone
	}

	return UpdateSession(ctx, sessionKey, session)
}
