// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpd

import (
	"context"
	"fmt"

	"github.com/elastic/beats/libbeat/common/transport/tlscommon"
	"github.com/pkg/errors"
	promconfig "github.com/prometheus/common/config"
	promhttpsd "github.com/prometheus/prometheus/discovery/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/logx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
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

func New(ctx context.Context, checkFn define.CheckFunc, opts *Options) *Discover {
	d := &Discover{
		BaseDiscover: discover.NewBaseDiscover(ctx, checkFn, opts.CommonOptions),
		opts:         opts,
	}

	d.SetUK(fmt.Sprintf("%s:%s", d.Type(), opts.Name))
	d.SetHelper(discover.Helper{
		AccessBasicAuth:   d.accessBasicAuth,
		AccessBearerToken: d.accessBearerToken,
		AccessTlsConfig:   d.accessTLSConfig,
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

func (d *Discover) Start() error {
	d.PreStart()

	err := shareddiscovery.Register(d.UK(), func() (*shareddiscovery.SharedDiscovery, error) {
		discovery, err := promhttpsd.NewDiscovery(d.opts.SDConfig, logx.New(TypeHttpSd), nil)
		if err != nil {
			return nil, errors.Wrap(err, d.Type())
		}
		return shareddiscovery.New(d.UK(), discovery), nil
	})
	if err != nil {
		return err
	}

	go d.LoopHandle()
	return nil
}

func (d *Discover) accessBasicAuth() (string, string, error) {
	auth := d.opts.HTTPClientConfig.BasicAuth
	if auth != nil {
		return auth.Username, string(auth.Password), nil
	}
	return "", "", nil
}

func (d *Discover) accessBearerToken() (string, error) {
	bearerToken := d.opts.HTTPClientConfig.BearerToken
	return string(bearerToken), nil
}

func (d *Discover) accessTLSConfig() (*tlscommon.Config, error) {
	cfg := d.opts.HTTPClientConfig.TLSConfig
	if len(cfg.CAFile) == 0 && len(cfg.KeyFile) == 0 && len(cfg.CertFile) == 0 {
		return nil, nil
	}

	tlsConfig := &tlscommon.Config{
		CAs: []string{cfg.CAFile},
	}

	tlsConfig.Certificate.Certificate = cfg.CertFile
	tlsConfig.Certificate.Key = cfg.KeyFile
	return tlsConfig, nil
}
