// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package shard

import (
	"context"
	"fmt"
	"time"

	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/offline-data-archive/trace"
)

func (d *Shard) Unique() string {
	k := fmt.Sprintf(
		"%s|%d|%d",
		d.Meta.String(), d.Spec.Start.UnixNano(), d.Spec.End.UnixNano(),
	)
	return k
}

func (d *Shard) CodeName() string {
	if v, ok := codeName[d.Status.Code]; ok {
		return v
	} else {
		return ""
	}
}

func (d *Shard) renewal(ctx context.Context, key string,
	renewalLock func(ctx context.Context, key string) (bool, error)) {
	// 续期逻辑

	// 每10秒检查一下是否应该续期
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// 上下文关闭的时候，也需要停止续期
			d.Log.Debugf(ctx, "renewal quit, the context is done,  renewal quit, %s", d.Unique())
			return

		case <-ticker.C:
			success, err := renewalLock(ctx, key)
			// 如果本次续期失败，则静静等下一次的续期周期
			if err != nil {
				d.Log.Errorf(ctx, "renewal locked error, %s", err)
				continue
			}
			// 如果锁续期失败
			if !success {
				d.Log.Errorf(ctx, "renewal locked failed, %s", d.Unique())
				continue
			}

			d.Log.Debugf(ctx, "renewal locked success, %s", d.Unique())
		}
	}

}

func (d *Shard) Run(
	ctx context.Context,
	action Action,
	distributedLock func(ctx context.Context, key, val string) (string, error),
	renewalLock func(ctx context.Context, key string) (bool, error),
	update func(ctx context.Context, key string, shard *Shard) error,
) error {

	var (
		err  error
		span oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "shard-run")
	if span != nil {
		defer span.End()
	}

	if action == nil {
		action = &BaseAction{}
	}

	trace.InsertStringIntoSpan("shard-key", d.Unique(), span)
	trace.InsertStringIntoSpan("shard-meta", d.Meta.String(), span)
	trace.InsertStringIntoSpan("shard-data", fmt.Sprintf("%+v", d), span)

	actionFunc := GetAction(action, d)

	if actionFunc == nil {
		d.Log.Infof(ctx, "actionFunc not register, shard Status code :%s", d.Status.Code)
		return nil
	}

	if distributedLock == nil {
		distributedLock = func(ctx context.Context, key, val string) (string, error) {
			return d.Spec.Source.Name, nil
		}
	}
	if update == nil {
		update = func(ctx context.Context, key string, shard *Shard) error {
			return nil
		}
	}

	if renewalLock == nil {
		renewalLock = func(ctx context.Context, key string) (bool, error) {
			return true, nil
		}
	}

	// 获得分布式锁
	dl, err := distributedLock(ctx, d.Unique(), d.Spec.Source.Name)
	if err != nil {
		return err
	}

	// 启动一个临时的协层进行续期
	go d.renewal(ctx, d.Unique(), renewalLock)

	if dl != d.Spec.Source.Name {
		return nil
	}

	oldStatus := d.CodeName()
	d.Log.Infof(ctx, "run with %s: %s", d.CodeName(), d.Unique())
	err = actionFunc(ctx, d)

	if err != nil {
		d.Log.Errorf(ctx, err.Error())
		d.Status.Message = err.Error()
	} else {
		d.Status.Message = ""
	}

	if d.CodeName() != oldStatus {
		d.Log.Infof(ctx, "shard code change: %s => %s", oldStatus, d.CodeName())
	}

	trace.InsertStringIntoSpan("shard-status-code-name", d.CodeName(), span)
	trace.InsertStringIntoSpan("shard-status-message", d.Status.Message, span)

	// 更新 shard 状态
	return update(ctx, d.Unique(), d)
}
