// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/suppressioncheck"
)

// suppressionOwnerLease 与 Lifecycle 使用同一个部署、来源和 correlation key，不能另建管理专属 owner 锁。
func suppressionOwnerLease(cfg config.Config, client redis.UniversalClient) suppressioncheck.OwnerLease {
	return func(ctx context.Context, tenant, source, fingerprint string, run func() error) (err error) {
		if cfg.Lifecycle == nil {
			return errors.New("lifecycle not configured")
		}
		c := cfg.Lifecycle.ForSource(cfg.Dispatch.WithDefaults().Deployment, source).SchedulerConfig()
		c.LockTTL = max(c.LockTTL, 30*time.Second)
		l, err := scheduler.NewRedisLocker(client, c)
		if err != nil {
			return err
		}
		lease, err := l.Acquire(ctx, mailbox.CorrelationKey(tenant, source, fingerprint))
		if err != nil {
			return err
		}
		defer func() {
			release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			err = errors.Join(err, l.Release(release, lease))
		}()
		return run()
	}
}
