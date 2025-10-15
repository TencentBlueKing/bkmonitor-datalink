// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpsd

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	promconfig "github.com/prometheus/common/config"
	promhttpsd "github.com/prometheus/prometheus/discovery/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/logx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/commonconfigs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover/shareddiscovery"
)

const (
	TypeHttpSd = "httpsd"
)

type Options struct {
	*discover.CommonOptions

	SDConfig         *promhttpsd.SDConfig
	HTTPClientConfig promconfig.HTTPClientConfig
}

type Discover struct {
	*discover.BaseDiscover

	opts *Options
}

var _ discover.Discover = (*Discover)(nil)

func New(ctx context.Context, opts *Options) *Discover {
	d := &Discover{
		BaseDiscover: discover.NewBaseDiscover(ctx, opts.CommonOptions),
		opts:         opts,
	}

	d.SetUK(fmt.Sprintf("%s:%s", d.Type(), opts.Name))
	d.SetHelper(discover.Helper{
		AccessBasicAuth:   commonconfigs.WrapHttpAccessBasicAuth(opts.HTTPClientConfig),
		AccessBearerToken: commonconfigs.WrapHttpAccessBearerToken(opts.HTTPClientConfig),
		AccessTlsConfig:   commonconfigs.WrapHttpAccessTLSConfig(opts.HTTPClientConfig),
	})
	return d
}

func (d *Discover) Type() string {
	return TypeHttpSd
}

func (d *Discover) Reload() error {
	d.Stop()
	return d.Start()
}

func (d *Discover) Stop() {
	d.BaseDiscover.Stop()
	shareddiscovery.Unregister(d.UK())
}

type WrapDiscovery struct {
	*promhttpsd.Discovery
}

func (WrapDiscovery) Stop() {}

func (d *Discover) Start() error {
	d.PreStart()

	err := shareddiscovery.Register(d.UK(), func() (*shareddiscovery.SharedDiscovery, error) {
		discovery, err := promhttpsd.NewDiscovery(d.opts.SDConfig, logx.New(TypeHttpSd), nil)
		if err != nil {
			return nil, errors.Wrap(err, d.Type())
		}
		return shareddiscovery.New(d.UK(), &WrapDiscovery{discovery}), nil
	})
	if err != nil {
		return err
	}

	go d.LoopHandle()
	return nil
}
