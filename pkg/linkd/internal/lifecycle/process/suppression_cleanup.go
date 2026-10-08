// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"errors"
	"time"

	"linkd/internal/config"
	policystore "linkd/internal/policy/storage"
	"linkd/internal/suppressioncleanup"
)

// configuredCleanupRecorder 让低频人工关闭/合并终态只在真正清理时打开记录存储。
// 普通屏蔽检查也共用处理器，但不会为未发生的终态清理创建连接或初始化集合。
type configuredCleanupRecorder struct {
	storage    config.StorageConfig
	deployment string
}

func (r configuredCleanupRecorder) Run(ctx context.Context, c suppressioncleanup.Cause, run func(context.Context) (suppressioncleanup.Result, error)) (err error) {
	docs, err := policystore.Open(ctx, r.storage, r.deployment)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, docs.Close()) }()
	journal, err := suppressioncleanup.NewJournal(docs, time.Now)
	if err != nil {
		return err
	}
	return journal.Run(ctx, c, run)
}
