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

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/actiondelivery"
	actionstore "linkd/internal/actiondelivery/storage"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	projectionlock "linkd/internal/projection/redislock"
)

// openActionRecorder 仅连接既有任务集合；client 生命周期由装配方管理，不读取任何远端凭据。
func openActionRecorder(ctx context.Context, storage config.StorageConfig, deployment string, client redis.UniversalClient) (*actiondelivery.Recorder, func() error, error) {
	tasks, err := actionstore.OpenExisting(ctx, storage, deployment)
	if err != nil {
		return nil, nil, err
	}
	locker, err := projectionlock.New(client, deployment)
	if err != nil {
		return nil, nil, errors.Join(err, tasks.Close())
	}
	recorder, err := actiondelivery.NewRecorder(tasks, locker, time.Now)
	if err != nil {
		return nil, nil, errors.Join(err, tasks.Close())
	}
	return recorder, tasks.Close, nil
}

// configuredActionRecorder 供人工关闭/合并/屏蔽等低频控制入口使用，只有确实存在持久意图时才打开任务连接。
// 入队仅引用已保存的目标版本，不以当前来源或部署凭据是否仍存在决定是否保留原动作。
type configuredActionRecorder struct {
	storage    config.StorageConfig
	deployment string
	client     redis.UniversalClient
}

func (r configuredActionRecorder) RecordAction(ctx context.Context, alert domain.Alert, intent domain.AlertActionIntent) (err error) {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	recorder, closeTasks, err := openActionRecorder(call, r.storage, r.deployment, r.client)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, closeTasks()) }()
	return recorder.RecordAction(call, alert, intent)
}

var _ lifecycle.ActionRecorder = (*actiondelivery.Recorder)(nil)

var _ lifecycle.ActionRecorder = configuredActionRecorder{}
