// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/elastic/beats/libbeat/outputs"
	"github.com/elastic/beats/libbeat/outputs/transport"
	"github.com/elastic/beats/metricbeat/mb"
)

type authConfig struct {
	bearerFile  string
	bearerToken string
	username    string
	password    string
}

type HTTPClient struct {
	base     mb.BaseMetricSet
	client   *http.Client
	method   string
	rawQuery string

	baseHeader map[string]string
	authConf   authConfig
}

func NewHTTPClient(base mb.BaseMetricSet) (*HTTPClient, error) {
	config := struct {
		TLS         *outputs.TLSConfig `config:"ssl"`
		Timeout     time.Duration      `config:"timeout"`
		Headers     map[string]string  `config:"headers"`
		BearerFile  string             `config:"bearer_file"`
		BearerToken string             `config:"bearer_token"`
		Username    string             `config:"username"`
		Password    string             `config:"password"`
		ProxyURL    string             `config:"proxy_url"`
		Query       url.Values         `config:"query"`
	}{}
	if err := base.Module().UnpackConfig(&config); err != nil {
		return nil, err
	}

	params := make(url.Values)
	for k, v := range config.Query {
		params[k] = make([]string, len(v))
		copy(params[k], v)
	}

	var rawQuery string
	if len(params) > 0 {
		rawQuery = params.Encode()
	}

	tlsConfig, err := outputs.LoadTLSConfig(config.TLS)
	if err != nil {
		return nil, err
	}

	dialer := transport.NetDialer(config.Timeout)
	tlsDialer, err := transport.TLSDialer(dialer, tlsConfig, config.Timeout)
	if err != nil {
		return nil, err
	}

	trp := &http.Transport{
		Dial:            dialer.Dial,
		DialTLS:         tlsDialer.Dial,
		IdleConnTimeout: time.Minute * 5,
	}

	if tlsConfig != nil && tlsConfig.Verification == transport.VerifyNone {
		trp.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	if config.ProxyURL != "" {
		parsed, err := url.Parse(config.ProxyURL)
		if err != nil {
			return nil, err
		}
		trp.Proxy = http.ProxyURL(parsed)
	}

	authConf := authConfig{
		bearerFile:  config.BearerFile,
		bearerToken: config.BearerToken,
		username:    config.Username,
		password:    config.Password,
	}

	return &HTTPClient{
		base: base,
		client: &http.Client{
			Transport: trp,
			Timeout:   config.Timeout,
		},
		baseHeader: config.Headers,
		authConf:   authConf,
		method:     "GET",
		rawQuery:   rawQuery,
	}, nil
}

func (cli *HTTPClient) getHeaders() (map[string]string, error) {
	headers := make(map[string]string)

	for k, v := range cli.baseHeader {
		headers[k] = v
	}
	headers["Accept"] = "application/openmetrics-text,*/*"
	headers["X-BK-AGENT"] = "bkmonitorbeat"

	if cli.authConf.bearerToken != "" {
		headers["Authorization"] = fmt.Sprintf("Bearer %s", cli.authConf.bearerToken)
	}

	if cli.authConf.bearerToken == "" && cli.authConf.bearerFile != "" {
		data, err := os.ReadFile(cli.authConf.bearerFile)
		if err != nil {
			return nil, err
		}
		headers["Authorization"] = fmt.Sprintf("Bearer %s", data)
	}

	if cli.authConf.username != "" || cli.authConf.password != "" {
		auth := cli.authConf.username + ":" + cli.authConf.password
		headers["Authorization"] = fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(auth)))
	}
	return headers, nil
}

func (cli *HTTPClient) FetchResponse() (*http.Response, error) {
	u, err := url.Parse(cli.base.HostData().SanitizedURI)
	if err != nil {
		return nil, err
	}
	u.RawQuery = cli.rawQuery
	reqUrl := u.String()
	if cli.rawQuery == "" {
		reqUrl = cli.base.HostData().SanitizedURI
	}
	var reader io.Reader
	req, err := http.NewRequest(cli.method, reqUrl, reader)
	if err != nil {
		return nil, err
	}

	if cli.base.HostData().User != "" || cli.base.HostData().Password != "" {
		req.SetBasicAuth(cli.base.HostData().User, cli.base.HostData().Password)
	}

	headers, err := cli.getHeaders()
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := cli.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making http request: %v", err)
	}

	return resp, nil
}
