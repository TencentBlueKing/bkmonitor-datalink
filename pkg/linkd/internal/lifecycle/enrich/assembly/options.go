// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package assembly

import "linkd/internal/lifecycle/enrich"

type routerOptions struct{ observer enrich.Observer }

// RouterOption 配置 Router 创建的 Processor Chain。
type RouterOption func(*routerOptions)

// WithEnrichObserver 注入所有 configured Chain 共用的 Processor 观察器。
func WithEnrichObserver(observer enrich.Observer) RouterOption {
	return func(options *routerOptions) {
		if observer != nil {
			options.observer = observer
		}
	}
}
