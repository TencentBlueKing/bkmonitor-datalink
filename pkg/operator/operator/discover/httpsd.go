// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package discover

import (
	"context"
	"fmt"

	httpsd "github.com/prometheus/prometheus/discovery/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

const (
	discoverTypeHttpSd = "httpsd"
)

type HttpSdParams struct {
	*BaseParams
	SDConfig *httpsd.SDConfig
}

type HttpSdDiscover struct {
	*BaseDiscover
	sdConfig *httpsd.SDConfig
}

func NewHttpSdDiscover(ctx context.Context, checkFn define.CheckFunc, params *HttpSdParams) Discover {
	return &HttpSdDiscover{
		BaseDiscover: NewBaseDiscover(ctx, checkFn, params.BaseParams),
		sdConfig:     params.SDConfig,
	}
}

func (d *HttpSdDiscover) Type() string {
	return discoverTypeHttpSd
}

func (d *HttpSdDiscover) UK() string {
	return fmt.Sprintf("%s:%s", d.Type(), d.BaseParams.Name)
}

func (d *HttpSdDiscover) Reload() error {
	d.Stop()
	return d.Start()
}

func (d *HttpSdDiscover) Start() error {
	d.PreStart()
	RegisterHttpSdDiscover(d.Name(), d.sdConfig)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.loopHandleTargetGroup()
	}()
	return nil
}
