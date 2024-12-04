// Copyright 2021 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
	promconfig "github.com/prometheus/common/config"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/discovery"
	"github.com/prometheus/prometheus/discovery/refresh"
	"github.com/prometheus/prometheus/discovery/targetgroup"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	// DefaultSDConfig is the default HTTP SD configuration.
	DefaultSDConfig = SDConfig{
		RefreshInterval:  model.Duration(60 * time.Second),
		HTTPClientConfig: promconfig.DefaultHTTPClientConfig,
	}
	userAgent        = "Blueking/Operator"
	matchContentType = regexp.MustCompile(`^(?i:application\/json(;\s*charset=("utf-8"|utf-8))?)$`)

	failuresCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "prometheus_sd_polaris_failures_total",
			Help: "Number of Polaris service discovery refresh failures.",
		})
)

func init() {
	discovery.RegisterConfig(&SDConfig{})
	prometheus.MustRegister(failuresCount)
}

// SDConfig is the configuration for HTTP based discovery.
type SDConfig struct {
	HTTPClientConfig promconfig.HTTPClientConfig `yaml:",inline"`
	RefreshInterval  model.Duration              `yaml:"refresh_interval,omitempty"`
	Namespace        string                      `yaml:"namespace"`
	Service          string                      `yaml:"service"`
}

// Name returns the name of the Config.
func (*SDConfig) Name() string { return "polaris" }

// SetDirectory joins any relative file paths with dir.
func (c *SDConfig) SetDirectory(dir string) {
	c.HTTPClientConfig.SetDirectory(dir)
}

// UnmarshalYAML implements the yaml.Unmarshaler interface.
func (c *SDConfig) UnmarshalYAML(unmarshal func(interface{}) error) error {
	*c = DefaultSDConfig
	type plain SDConfig
	return unmarshal((*plain)(c))
}

// NewDiscoverer returns a Discoverer for the Config.
func (c *SDConfig) NewDiscoverer(opts discovery.DiscovererOptions) (discovery.Discoverer, error) {
	return NewDiscovery(c, opts.Logger, opts.HTTPClientOptions)
}

const polarisSDURLLabel = model.MetaLabelPrefix + "url"

// Discovery provides service discovery functionality based
// on HTTP endpoints that return target groups in JSON format.
type Discovery struct {
	*refresh.Discovery
	client          *http.Client
	refreshInterval time.Duration
	tgLastLength    int
	sdConfig        *SDConfig
	consumer        polaris.ConsumerAPI
}

// NewDiscovery returns a new HTTP discovery for the given config.
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
		refreshInterval: time.Duration(conf.RefreshInterval), // Stored to be sent as headers.
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

	cfg := api.NewConfiguration()
	cfg.GetGlobal().GetServerConnector().SetAddresses(configs.G().PolarisAddress)

	consumer, err := polaris.NewConsumerAPIByConfig(cfg)
	if err != nil {
		return nil, err
	}

	d.consumer = consumer
	return d.consumer, nil
}

func (d *Discovery) resolve() ([]string, error) {
	consumer, err := d.consumerAPI()
	if err != nil {
		return nil, err
	}

	req := &polaris.GetAllInstancesRequest{}
	req.Namespace = d.sdConfig.Namespace
	req.Service = d.sdConfig.Service

	response, err := consumer.GetAllInstances(req)
	if err != nil {
		return nil, err
	}

	var address []string
	for _, inst := range response.Instances {
		// 只取健康的实例
		if !inst.IsHealthy() {
			logger.Infof("%s found unhealthy instance %s:%d", d.uid(), inst.GetHost(), inst.GetPort())
			continue
		}
		addr := fmt.Sprintf("http://%s:%d", inst.GetHost(), inst.GetPort())
		address = append(address, addr)
	}
	return address, nil
}

func (d *Discovery) refresh(ctx context.Context, url string) ([]*targetgroup.Group, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Prometheus-Refresh-Interval-Seconds", strconv.FormatFloat(d.refreshInterval.Seconds(), 'f', -1, 64))

	resp, err := d.client.Do(req.WithContext(ctx))
	if err != nil {
		failuresCount.Inc()
		return nil, err
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		failuresCount.Inc()
		return nil, fmt.Errorf("server returned HTTP status %s", resp.Status)
	}

	if !matchContentType.MatchString(strings.TrimSpace(resp.Header.Get("Content-Type"))) {
		failuresCount.Inc()
		return nil, fmt.Errorf("unsupported content type %q", resp.Header.Get("Content-Type"))
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		failuresCount.Inc()
		return nil, err
	}

	var targetGroups []*targetgroup.Group

	if err := json.Unmarshal(b, &targetGroups); err != nil {
		failuresCount.Inc()
		return nil, err
	}

	for i, tg := range targetGroups {
		if tg == nil {
			failuresCount.Inc()
			err = errors.New("nil target group item found")
			return nil, err
		}

		tg.Source = urlSource(url, i)
		if tg.Labels == nil {
			tg.Labels = model.LabelSet{}
		}
		tg.Labels[polarisSDURLLabel] = model.LabelValue(url)
	}

	// Generate empty updates for sources that disappeared.
	l := len(targetGroups)
	for i := l; i < d.tgLastLength; i++ {
		targetGroups = append(targetGroups, &targetgroup.Group{Source: urlSource(url, i)})
	}
	d.tgLastLength = l

	return targetGroups, nil
}

func (d *Discovery) Refresh(ctx context.Context) ([]*targetgroup.Group, error) {
	urls, err := d.resolve()
	if err != nil {
		return nil, err
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("no urls resolved: %s/%s", d.sdConfig.Namespace, d.sdConfig.Service)
	}
	logger.Debugf("%s found %d urls", d.uid(), len(urls))

	var ret []*targetgroup.Group
	for _, url := range urls {
		tgs, err := d.refresh(ctx, url)
		if err != nil {
			return nil, err
		}
		ret = append(ret, tgs...)
	}
	return ret, nil
}

func (d *Discovery) uid() string {
	return fmt.Sprintf("polaris:%s/%s", d.sdConfig.Namespace, d.sdConfig.Service)
}

// urlSource returns a source ID for the i-th target group per URL.
func urlSource(url string, i int) string {
	return fmt.Sprintf("%s:%d", url, i)
}
