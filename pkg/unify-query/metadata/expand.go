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

// SetExpand
func SetExpand(ctx context.Context, expand *VmExpand) {
	md.set(ctx, ExpandKey, expand)
}

// GetExpand
func GetExpand(ctx context.Context) *VmExpand {
	var v *VmExpand
	r, ok := md.get(ctx, ExpandKey)
	if ok {
		if v, ok = r.(*VmExpand); ok {
			return v
		}
	}
	return nil
}

// SetCheckPreviewMetricQL 仅用于 check 直查路径：写入待序列化的 VM MetricQL 预览，供 victoriaMetrics.Instance.GetRequestBody(ctx) 读取。
func SetCheckPreviewMetricQL(ctx context.Context, metricQL string) {
	md.set(ctx, CheckPreviewMetricQLKey, metricQL)
}

// GetCheckPreviewMetricQL 读取 check VM 预览 MetricQL；未设置时返回空字符串。
func GetCheckPreviewMetricQL(ctx context.Context) string {
	r, ok := md.get(ctx, CheckPreviewMetricQLKey)
	if !ok {
		return ""
	}
	if s, ok := r.(string); ok {
		return s
	}
	return ""
}
