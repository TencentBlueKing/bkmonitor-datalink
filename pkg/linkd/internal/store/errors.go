// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import "errors"

var (
	// ErrNotFound 表示当前租户作用域内不存在目标对象。
	ErrNotFound = errors.New("store object not found")
	// ErrIdentityConflict 表示相同业务身份已保存了不同内容。
	ErrIdentityConflict = errors.New("store identity conflict")
	// ErrVersionConflict 表示 CAS 使用的版本令牌已经过期。
	ErrVersionConflict = errors.New("store version conflict")
	// ErrInvalidTransition 表示请求违反 Event 或 Alert 的状态转换约束。
	ErrInvalidTransition = errors.New("store invalid transition")
	// ErrInvalidCursor 表示分页 cursor 无法解析或不属于当前查询。
	ErrInvalidCursor = errors.New("store invalid cursor")
	// ErrInvalidArgument 表示调用参数不满足存储契约。
	ErrInvalidArgument = errors.New("store invalid argument")
)
