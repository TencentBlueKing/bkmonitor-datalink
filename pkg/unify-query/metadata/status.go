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

// Status
type Status struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SetStatus
func SetStatus(ctx context.Context, code, message string) {
	if code != "" {
		status := &Status{
			Code:    code,
			Message: message,
		}
		md.set(ctx, StatusKey, status)
	}
}

// GetStatus
func GetStatus(ctx context.Context) *Status {
	r, ok := md.get(ctx, StatusKey)
	if ok {
		if v, ok := r.(*Status); ok {
			return v
		}
	}
	return nil
}
