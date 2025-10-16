// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package noop

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// NoopBackend 此 backend 类型不对数据做任何处理
type NoopBackend struct {
	*define.BaseBackend
	*define.ProcessorMonitor
	ctx context.Context
}

// NewNoopBackend :
func NewNoopBackend(ctx context.Context, name string) (*NoopBackend, error) {
	backend := &NoopBackend{
		BaseBackend: define.NewBaseBackend(name),
		ctx:         ctx,
	}

	return backend, nil
}

func (b *NoopBackend) SetETLRecordFields(f *define.ETLRecordFields) {}

// Push : raw data from payload
func (b *NoopBackend) Push(d define.Payload, killChan chan<- error) {}

func init() {
	define.RegisterBackend("argus", func(ctx context.Context, name string) (define.Backend, error) {
		if config.FromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "config is empty")
		}
		if config.ShipperConfigFromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "shipper config is empty")
		}
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewNoopBackend(ctx, pipeConfig.FormatName(name))
	})
}
