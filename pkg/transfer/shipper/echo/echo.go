// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package echo

import (
	"context"
	"fmt"

	client "github.com/influxdata/influxdb/client/v2"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/shipper"
)

// EchoBackend 标准输出 backend, 测试使用
type EchoBackend struct {
	*define.BaseBackend
	*define.ProcessorMonitor
	ctx    context.Context
	enable bool
}

// NewEchoBackend :
func NewEchoBackend(ctx context.Context, name string) (*EchoBackend, error) {
	backend := &EchoBackend{
		BaseBackend: define.NewBaseBackend(name),
		ctx:         ctx,
		enable:      shipper.ShipperEchoEnable,
	}

	return backend, nil
}

func (b *EchoBackend) SetETLRecordFields(f *define.ETLRecordFields) {}

// Push : raw data from payload
func (b *EchoBackend) Push(d define.Payload, killChan chan<- error) {
	if !b.enable {
		return
	}

	var message []byte

	err := d.To(&message)
	if err != nil {
		logging.Warnf("%v load %#v error %v", b, d, err)
		return
	}

	// print to stdout
	fmt.Println(string(message))
}

// WritePoint : point data from influxdb
func (b *EchoBackend) WritePoint(point *client.Point) {
	if !b.enable {
		return
	}

	fields, _ := point.Fields()
	fmt.Println(point.Time(), point.Name(), point.Tags(), fields)
}

func init() {
	define.RegisterBackend("echo", func(ctx context.Context, name string) (define.Backend, error) {
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
		return NewEchoBackend(ctx, pipeConfig.FormatName(name))
	})
}
