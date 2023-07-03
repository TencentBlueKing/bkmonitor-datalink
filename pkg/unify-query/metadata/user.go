// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"strings"
)

// User
type User struct {
	Key      string
	Source   string
	Name     string
	Role     string
	SpaceUid string
}

// SetUser
func SetUser(ctx context.Context, key, spaceUid string) {
	user := &User{
		Key:      key,
		SpaceUid: spaceUid,
	}
	arr := strings.Split(key, ":")
	if len(arr) > 0 {
		user.Source = arr[0]
		if len(arr) > 1 {
			user.Name = arr[1]
		}
	}
	md.set(ctx, UserKey, user)
}

// GetUser
func GetUser(ctx context.Context) *User {
	r, ok := md.get(ctx, UserKey)
	if ok {
		if v, ok := r.(*User); ok {
			return v
		}
	}
	return &User{}
}
