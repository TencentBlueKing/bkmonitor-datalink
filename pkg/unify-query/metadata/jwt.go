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
)

const (
	AppCodeKey  = "app.app_code"
	UserNameKey = "user.username"
)

// JwtPayLoad jwt payload 结构体
type JwtPayLoad map[string]any

// SetJwtPayLoad 写入
func SetJwtPayLoad(ctx context.Context, payLoad JwtPayLoad) {
	if md != nil {
		md.set(ctx, JwtPayLoadKey, payLoad)
	}
}

// GetJwtPayLoad 获取
func GetJwtPayLoad(ctx context.Context) JwtPayLoad {
	if md != nil {
		r, ok := md.get(ctx, JwtPayLoadKey)
		if ok {
			if v, ok := r.(JwtPayLoad); ok {
				return v
			}
		}
	}
	return JwtPayLoad{}
}

func (j JwtPayLoad) AppCode() string {
	if v, ok := j[AppCodeKey]; ok {
		if vs, ok := v.(string); ok {
			return vs
		}
	}
	return ""
}

func (j JwtPayLoad) UserName() string {
	if v, ok := j[UserNameKey]; ok {
		if vs, ok := v.(string); ok {
			return vs
		}
	}
	return ""
}
