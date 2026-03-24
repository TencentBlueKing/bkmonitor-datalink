// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scraper

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/elastic/beats/libbeat/common/transport/tlscommon"
	"github.com/elastic/beats/libbeat/outputs"
	"github.com/elastic/beats/libbeat/outputs/transport"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

type ModuleConfig struct {
	Tasks []struct {
		Module TaskConfig `yaml:"module"`
	} `yaml:"tasks"`
}

type TaskConfig struct {
	Hosts       []string          `yaml:"hosts"`
	MetricsPath string            `yaml:"metrics_path"`
	BearerFile  string            `yaml:"bearer_file"`
	Query       url.Values        `yaml:"query"`
	Ssl         *tlscommon.Config `yaml:"ssl"`
	Username    string            `yaml:"username"`
	Password    string            `yaml:"password"`
	ProxyURL    string            `yaml:"proxy_url"`
	Headers     map[string]string `yaml:"headers"`
	Timeout     time.Duration     `yaml:"timeout"`
}

type Scraper struct {
	client *http.Client
	config TaskConfig
}

func (c *Scraper) doRequest(ctx context.Context, host string) (*http.Response, error) {
	u := host + c.config.MetricsPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, &bytes.Buffer{})
	if err != nil {
		return nil, err
	}
	for k, v := range c.config.Headers {
		req.Header.Set(k, v)
	}
	if len(c.config.Query) > 0 {
		req.URL.RawQuery = c.config.Query.Encode()
	}

	return c.client.Do(req)
}

func (c *Scraper) StringCh(ctx context.Context) chan string {
	ch := make(chan string, 1)
	go func() {
		defer close(ch)
		for _, host := range c.config.Hosts {
			resp, err := c.doRequest(ctx, host)
			if err != nil {
				ch <- fmt.Sprintf("scraper error: host=%s, err=%v", host, err)
				continue
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 300 {
				b, _ := io.ReadAll(resp.Body)
				msg := fmt.Sprintf("scraper error: host=%s, status_code=%d, response(%v)", host, resp.StatusCode, string(b))
				ch <- msg
				continue
			}

			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				ch <- line
			}
		}
	}()

	return ch
}

func (c *Scraper) Lines(ctx context.Context) (int, []error) {
	var errs []error
	var total int
	for _, host := range c.config.Hosts {
		resp, err := c.doRequest(ctx, host)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			errs = append(errs, fmt.Errorf("scrape error => status code: %v, response: %v", resp.StatusCode, string(b)))
			continue
		}

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			b := scanner.Bytes() // 不需要复制内存
			if len(b) == 0 || bytes.HasPrefix(b, []byte("#")) {
				continue
			}
			total++
		}
	}

	return total, errs
}

func New(data []byte) (*Scraper, error) {
	var module ModuleConfig

	// TODO(optimize): beats tlscommon 对 verification_mode 字段做了特殊处理 所以这里采用了`取巧`方法进行替换
	// 避免解析出错 后续有更简洁的方式可进行优化
	data = bytes.ReplaceAll(data, []byte(`verification_mode: none`), []byte(`verification_mode: 1`))
	if err := yaml.Unmarshal(data, &module); err != nil {
		return nil, err
	}

	if len(module.Tasks) == 0 {
		return nil, errors.New("no tasks available")
	}

	config := module.Tasks[0].Module
	if config.Headers == nil {
		config.Headers = map[string]string{}
	}
	config.Headers["Accept"] = "application/openmetrics-text,*/*"
	config.Headers["X-BK-AGENT"] = "bkmonitor-operator"

	if config.BearerFile != "" {
		b, err := os.ReadFile(config.BearerFile)
		if err != nil {
			return nil, errors.Wrap(err, "read bearer file failed")
		}
		config.Headers["Authorization"] = fmt.Sprintf("Bearer %s", b)
	}

	if config.Username != "" || config.Password != "" {
		auth := config.Username + ":" + config.Password
		config.Headers["Authorization"] = fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(auth)))
	}

	tlsConfig, err := outputs.LoadTLSConfig(config.Ssl)
	if err != nil {
		return nil, errors.Wrap(err, "load tls config failed")
	}
	if tlsConfig != nil {
		tlsConfig.Verification = tlscommon.VerifyNone
	}

	var dialer, tlsDialer transport.Dialer
	dialer = transport.NetDialer(config.Timeout)
	tlsDialer, err = transport.TLSDialer(dialer, tlsConfig, config.Timeout)
	if err != nil {
		return nil, errors.Wrap(err, "create tls dialer failed")
	}

	trp := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		},
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return tlsDialer.Dial(network, addr)
		},
		IdleConnTimeout: time.Minute,
	}
	if config.ProxyURL != "" {
		parsed, err := url.Parse(config.ProxyURL)
		if err != nil {
			return nil, errors.Wrap(err, "parse proxy url failed")
		}
		trp.Proxy = http.ProxyURL(parsed)
	}

	if tlsConfig != nil && tlsConfig.Verification == transport.VerifyNone {
		trp.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &Scraper{
		client: &http.Client{
			Transport: trp,
			Timeout:   config.Timeout,
		},
		config: config,
	}, nil
}
