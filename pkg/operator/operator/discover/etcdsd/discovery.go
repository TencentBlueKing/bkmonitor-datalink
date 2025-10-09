// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etcdsd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path"
	"time"

	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	promconfig "github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/discovery/refresh"
	"github.com/prometheus/prometheus/discovery/targetgroup"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	DefaultSDConfig = SDConfig{
		RefreshInterval:  model.Duration(60 * time.Second),
		HTTPClientConfig: promconfig.DefaultHTTPClientConfig,
	}

	failuresCount = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "prometheus_sd_etcd_failures_total",
			Help: "Number of ETCD service discovery refresh failures.",
		},
	)
)

func init() {
	discovery.RegisterConfig(&SDConfig{})
}

type SDConfig struct {
	HTTPClientConfig promconfig.HTTPClientConfig `yaml:",inline"`
	RefreshInterval  model.Duration              `yaml:"refresh_interval,omitempty"`
	PrefixKeys       []string                    `yaml:"prefix_keys"`
	Endpoints        []string                    `yaml:"endpoints"`
	EnableIPFilter   bool                        `yaml:"enable_ip_filter"`
	IPFilter         func(string) bool           `yaml:"-"`
}

func (*SDConfig) Name() string {
	return "etcd"
}

func (c *SDConfig) SetDirectory(dir string) {
	c.HTTPClientConfig.SetDirectory(dir)
}

func (c *SDConfig) UnmarshalYAML(unmarshal func(any) error) error {
	*c = DefaultSDConfig
	type plain SDConfig
	return unmarshal((*plain)(c))
}

func (c *SDConfig) NewDiscoverer(opts discovery.DiscovererOptions) (discovery.Discoverer, error) {
	return NewDiscovery(c, opts.Logger, opts.HTTPClientOptions)
}

const etcdSDURLLabel = model.MetaLabelPrefix + "url"

type Discovery struct {
	*refresh.Discovery
	tgLastLength int
	sdConfig     *SDConfig
	client       *clientv3.Client
}

func NewDiscovery(conf *SDConfig, logger log.Logger, _ []promconfig.HTTPClientOption) (*Discovery, error) {
	if logger == nil {
		logger = log.NewNopLogger()
	}

	d := &Discovery{sdConfig: conf}
	d.Discovery = refresh.NewDiscovery(
		logger,
		"etcd",
		time.Duration(conf.RefreshInterval),
		d.Refresh,
	)
	return d, nil
}

type Service struct {
	Addr     string            `json:"addr"`
	TenantID string            `json:"tenant_id"`
	Metadata map[string]string `json:"metadata"`
}

func (s Service) Labels() model.LabelSet {
	lbs := make(map[model.LabelName]model.LabelValue)
	lbs["tenant_id"] = model.LabelValue(s.TenantID)
	for k, v := range s.Metadata {
		lbs[model.LabelName(k)] = model.LabelValue(v)
	}
	return lbs
}

func (d *Discovery) getClient() (*clientv3.Client, error) {
	if d.client != nil {
		return d.client, nil
	}

	cli, err := clientv3.New(clientv3.Config{
		Endpoints: d.sdConfig.Endpoints,
	})
	if err != nil {
		return nil, err
	}

	d.client = cli
	return d.client, nil
}

func (d *Discovery) resolveServices(ctx context.Context, prefixKey string) ([]Service, error) {
	cli, err := d.getClient()
	if err != nil {
		return nil, err
	}

	// 先遍历 prefixKey 列出所有的实例
	// 再通过 IP 过滤具体的 service
	rsp, err := cli.Get(ctx, prefixKey, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	// 获取所有的 key 并判断 ip 是否为需要采集
	var values []string
	for i := 0; i < len(rsp.Kvs); i++ {
		kv := rsp.Kvs[i]
		if kv == nil {
			continue
		}

		key := string(kv.Key)
		host, _, err := net.SplitHostPort(path.Base(key))
		if err != nil {
			continue
		}

		val := string(kv.Value)
		if d.sdConfig.EnableIPFilter && d.sdConfig.IPFilter != nil {
			if d.sdConfig.IPFilter(host) {
				values = append(values, val)
			}
		} else {
			values = append(values, val)
		}
	}

	// 获取 key 具体 values 解析 labels
	var services []Service
	for _, val := range values {
		var svc Service
		if err := json.Unmarshal([]byte(val), &svc); err != nil {
			logger.Warnf("etcdsd unmarshal failed: %v", err)
			continue
		}
		services = append(services, svc)
	}
	return services, nil
}

func (d *Discovery) refresh(ctx context.Context, prefixKey string) ([]*targetgroup.Group, error) {
	services, err := d.resolveServices(ctx, prefixKey)
	if err != nil {
		failuresCount.Inc()
		return nil, err
	}

	var targetGroups []*targetgroup.Group
	for _, service := range services {
		targetGroups = append(targetGroups, &targetgroup.Group{
			Targets: []model.LabelSet{{
				model.AddressLabel: model.LabelValue(service.Addr),
			}},
			Labels: service.Labels(),
		})
	}

	for i, tg := range targetGroups {
		tg.Source = urlSource(prefixKey, i)
		if tg.Labels == nil {
			tg.Labels = model.LabelSet{}
		}
		tg.Labels[etcdSDURLLabel] = model.LabelValue(prefixKey)
	}

	// 告知上层存在删除事件
	l := len(targetGroups)
	for i := l; i < d.tgLastLength; i++ {
		targetGroups = append(targetGroups, &targetgroup.Group{Source: urlSource(prefixKey, i)})
	}
	d.tgLastLength = l

	return targetGroups, nil
}

func (d *Discovery) Refresh(ctx context.Context) ([]*targetgroup.Group, error) {
	var ret []*targetgroup.Group
	for _, prefixKey := range d.sdConfig.PrefixKeys {
		tgs, err := d.refresh(ctx, prefixKey)
		if err != nil {
			return nil, err
		}
		ret = append(ret, tgs...)
	}
	return ret, nil
}

func (d *Discovery) Stop() {
	if d.client != nil {
		d.client.Close()
	}
}

func urlSource(url string, i int) string {
	return fmt.Sprintf("%s:%d", url, i)
}
