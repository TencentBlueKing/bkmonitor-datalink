// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package polarissd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/grafana/regexp"
	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	promconfig "github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/discovery/refresh"
	"github.com/prometheus/prometheus/discovery/targetgroup"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	DefaultSDConfig = SDConfig{
		RefreshInterval:  model.Duration(60 * time.Second),
		HTTPClientConfig: promconfig.DefaultHTTPClientConfig,
	}
	userAgent        = "Blueking/Operator"
	matchContentType = regexp.MustCompile(`^(?i:application\/json(;\s*charset=("utf-8"|utf-8))?)$`)

	failuresCount = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "prometheus_sd_polaris_failures_total",
			Help: "Number of Polaris service discovery refresh failures.",
		})
)

func init() {
	discovery.RegisterConfig(&SDConfig{})
}

type SDConfig struct {
	HTTPClientConfig promconfig.HTTPClientConfig `yaml:",inline"`
	RefreshInterval  model.Duration              `yaml:"refresh_interval,omitempty"`
	Namespace        string                      `yaml:"namespace"`
	Service          string                      `yaml:"service"`
	MetadataSelector map[string]string           `yaml:"metadata_selector"`
}

func (*SDConfig) Name() string {
	return "polaris"
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

const polarisSDURLLabel = model.MetaLabelPrefix + "url"

type Discovery struct {
	*refresh.Discovery
	client          *http.Client
	refreshInterval time.Duration
	tgLastLength    int
	sdConfig        *SDConfig
	consumer        polaris.ConsumerAPI
}

func NewDiscovery(conf *SDConfig, logger log.Logger, clientOpts []promconfig.HTTPClientOption) (*Discovery, error) {
	if logger == nil {
		logger = log.NewNopLogger()
	}
	client, err := promconfig.NewClientFromConfig(conf.HTTPClientConfig, "polaris", clientOpts...)
	if err != nil {
		return nil, err
	}
	client.Timeout = time.Duration(conf.RefreshInterval)

	d := &Discovery{
		sdConfig:        conf,
		client:          client,
		refreshInterval: time.Duration(conf.RefreshInterval),
	}
	d.Discovery = refresh.NewDiscovery(
		logger,
		"polaris",
		time.Duration(conf.RefreshInterval),
		d.Refresh,
	)
	return d, nil
}

func (d *Discovery) consumerAPI() (polaris.ConsumerAPI, error) {
	if d.consumer != nil {
		return d.consumer, nil
	}

	if len(configs.G().PolarisAddress) == 0 {
		return nil, errors.New("no polaris address found")
	}

	cfg := api.NewConfiguration()
	cfg.GetGlobal().GetServerConnector().SetAddresses(configs.G().PolarisAddress)

	consumer, err := polaris.NewConsumerAPIByConfig(cfg)
	if err != nil {
		return nil, err
	}

	d.consumer = consumer
	return d.consumer, nil
}

func (d *Discovery) resolveInstances() ([]string, error) {
	consumer, err := d.consumerAPI()
	if err != nil {
		return nil, err
	}

	req := &polaris.GetAllInstancesRequest{}
	req.Namespace = d.sdConfig.Namespace
	req.Service = d.sdConfig.Service

	rsp, err := consumer.GetAllInstances(req)
	if err != nil {
		return nil, err
	}

	var instances []string
	for _, inst := range rsp.Instances {
		hostPort := fmt.Sprintf("%s:%d", inst.GetHost(), inst.GetPort())

		// 只取健康的实例
		if !inst.IsHealthy() {
			logger.Infof("%s found unhealthy instance %s", d.uid(), hostPort)
			continue
		}

		if len(d.sdConfig.MetadataSelector) == 0 {
			instances = append(instances, fmt.Sprintf("http://%s", hostPort))
			continue
		}

		meta := inst.GetMetadata()
		for sk, sv := range d.sdConfig.MetadataSelector {
			mv, ok := meta[sk]
			if ok && mv == sv {
				instances = append(instances, fmt.Sprintf("http://%s", hostPort))
			}
		}
	}
	return instances, nil
}

func (d *Discovery) refresh(ctx context.Context, url string) ([]*targetgroup.Group, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Prometheus-Refresh-Interval-Seconds", strconv.FormatFloat(d.refreshInterval.Seconds(), 'f', -1, 64))

	rsp, err := d.client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer func() {
		io.Copy(io.Discard, rsp.Body)
		rsp.Body.Close()
	}()

	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP status %s", rsp.Status)
	}

	if !matchContentType.MatchString(strings.TrimSpace(rsp.Header.Get("Content-Type"))) {
		return nil, fmt.Errorf("unsupported content type %q", rsp.Header.Get("Content-Type"))
	}

	b, err := io.ReadAll(rsp.Body)
	if err != nil {
		return nil, err
	}

	var targetGroups []*targetgroup.Group
	if err := json.Unmarshal(b, &targetGroups); err != nil {
		return nil, err
	}

	for i, tg := range targetGroups {
		if tg == nil {
			err = errors.New("nil target group item found")
			return nil, err
		}

		tg.Source = urlSource(url, i)
		if tg.Labels == nil {
			tg.Labels = model.LabelSet{}
		}
		tg.Labels[polarisSDURLLabel] = model.LabelValue(url)
	}

	// 告知上层存在删除事件
	l := len(targetGroups)
	for i := l; i < d.tgLastLength; i++ {
		targetGroups = append(targetGroups, &targetgroup.Group{Source: urlSource(url, i)})
	}
	d.tgLastLength = l

	return targetGroups, nil
}

func (d *Discovery) Refresh(ctx context.Context) ([]*targetgroup.Group, error) {
	instances, err := d.resolveInstances()
	if err != nil {
		failuresCount.Inc()
		return nil, err
	}

	if len(instances) == 0 {
		return nil, fmt.Errorf("no instances resolved: %s/%s", d.sdConfig.Namespace, d.sdConfig.Service)
	}
	logger.Debugf("%s found instances %v", d.uid(), instances)

	var ret []*targetgroup.Group
	for _, inst := range instances {
		tgs, err := d.refresh(ctx, inst)
		if err != nil {
			failuresCount.Inc()
			return nil, err
		}
		ret = append(ret, tgs...)
	}
	return ret, nil
}

func (d *Discovery) Stop() {
	if d.consumer != nil {
		d.consumer.Destroy()
	}
}

func (d *Discovery) uid() string {
	return fmt.Sprintf("polaris:%s/%s", d.sdConfig.Namespace, d.sdConfig.Service)
}

func urlSource(url string, i int) string {
	return fmt.Sprintf("%s:%d", url, i)
}
