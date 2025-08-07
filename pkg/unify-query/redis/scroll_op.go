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
	"time"
)

const (
	SessionKeyPrefix    = "scroll:session:"
	ScrollLockKeyPrefix = "scroll:lock:"
)

func ClearScrollSession(ctx context.Context, sessionKeySuffix string) error {
	sessionKey := SessionKeyPrefix + sessionKeySuffix
	return Client().Del(ctx, sessionKey).Err()
}

func GetOrCreateScrollSession(ctx context.Context, sessionKeySuffix string, maxSliceCount int, scrollTimeout string, sliceLimit int) (session *ScrollSession, err error) {
	session, exist, err := checkScrollSession(ctx, sessionKeySuffix)
	if err != nil {
		return nil, err
	}
	if !exist {
		session, err = createScrollSession(ctx, sessionKeySuffix, maxSliceCount, scrollTimeout, sliceLimit)
		if err != nil {
			return
		}
	}
	return
}

func createScrollSession(ctx context.Context, sessionKeySuffix string, maxSlice int, scrollTimeoutStr string, limit int) (*ScrollSession, error) {
	scrollTimeout, err := time.ParseDuration(scrollTimeoutStr)
	if err != nil {
		return nil, err
	}
	session := NewScrollSession(sessionKeySuffix, maxSlice, scrollTimeout, limit)
	err = Client().Set(ctx, SessionKeyPrefix+sessionKeySuffix, session, scrollTimeout).Err()
	if err != nil {
		return nil, err
	}
	return session, nil
}

func checkScrollSession(ctx context.Context, key string) (*ScrollSession, bool, error) {
	session := &ScrollSession{}
	err := Client().Get(ctx, SessionKeyPrefix+key).Scan(session)
	if err != nil {
		if !IsEntryNil(err) {
			return nil, false, err
		}
		return nil, false, nil
	}
	return session, true, nil
}
