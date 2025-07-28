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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func isRedisNilError(err error) bool {
	return errors.Is(err, redis.Nil)
}

const (
	clearCacheField = "clear_cache"
)

var (
	ErrorOfSessionAlreadyLocked = fmt.Errorf("failed to acquire lock, already locked")
	ErrorOfInvalidLockType      = fmt.Errorf("invalid lock type")
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
		return ErrorOfSessionAlreadyLocked
	}

	return nil
}

func ScrollReleaseRedisLock(ctx context.Context, lock interface{}) error {
	lockKey, ok := lock.(string)
	if !ok {
		return ErrorOfInvalidLockType
	}

	return Client().Del(ctx, lockKey).Err()
}

func ScrollGetOrCreateSession(ctx context.Context, sessionKey string, forceClear bool, timeout time.Duration, maxSlice int, limit int) (*ScrollSession, error) {
	if forceClear {
		if err := globalInstance.client.Del(ctx, sessionKey).Err(); err != nil {
			return nil, err
		}
	}

	result := globalInstance.client.Get(ctx, sessionKey)

	if result.Err() != nil {
		if isRedisNilError(result.Err()) {
			session := NewScrollSession(maxSlice, timeout, limit)
			if err := scrollUpdateSession(ctx, sessionKey, session); err != nil {
				return nil, err
			}
			return session, nil
		}
		return nil, result.Err()
	}

	var session *ScrollSession
	if err := json.Unmarshal([]byte(result.Val()), &session); err != nil {
		return nil, err
	}

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

	if err = Client().Set(ctx, sessionKey, sessionBytes, session.ScrollTimeout).Err(); err != nil {
		return err
	}

	return nil
}

func ScrollProcessSliceResult(ctx context.Context, sessionKey string, session *ScrollSession, connect, tableID string, sliceIdx int, tsDbType string, size int64, options metadata.ResultTableOptions) (err error) {
	ctx, span := trace.NewSpan(ctx, "scroll-process-slice-result")
	defer span.End(&err)
	isSliceDone := size == 0
	span.Set("key", fmt.Sprintf("%s:%s:%d", connect, tableID, sliceIdx))
	span.Set("size", size)
	if isSliceDone {
		session.RemoveScrollID(connect, tableID, sliceIdx)
		session.MarkSliceDone(connect, tableID, sliceIdx)
	} else {
		switch tsDbType {
		case consul.ElasticsearchStorageType:
			if options != nil {
				if opt := options.GetOption(tableID, connect); opt != nil && opt.ScrollID != "" {
					session.SetScrollID(connect, tableID, opt.ScrollID, sliceIdx)
				}
			}
		case consul.BkSqlStorageType:
			// 保留一个标记,doris scroll还没有取完
			session.SetScrollID(connect, tableID, "", sliceIdx)
		}
	}
	nextScrollID, hasMoreData := session.HasMoreData(tsDbType)
	span.Set("nextScrollID", nextScrollID)

	if !hasMoreData {
		session.Status = SessionStatusDone
	}

	err = UpdateSession(ctx, sessionKey, session)
	return err
}
