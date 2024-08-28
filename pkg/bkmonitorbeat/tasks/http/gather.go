// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// validateConfig 校验配置 解析 base64 内容
func validateConfig(c *configs.HTTPTaskStepConfig) {
	const base64Prefix = "base64://"

	decode := func(s string) string {
		s = s[len(base64Prefix):]
		b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
		if err != nil {
			return s // 解析失败原路返回
		}
		return string(b)
	}

	if strings.HasPrefix(c.Request, base64Prefix) {
		c.Request = decode(c.Request)
	}
	if strings.HasPrefix(c.Response, base64Prefix) {
		c.Response = decode(c.Response)
	}

	headers := make(map[string]string)
	for k, v := range c.Headers {
		if strings.HasPrefix(v, base64Prefix) {
			headers[k] = decode(v)
		} else {
			headers[k] = v
		}
	}
	c.Headers = headers
}

// makeResponseReader 从 response 获取 reader
func makeResponseReader(response *http.Response) io.ReadCloser {
	var (
		err        error
		responseRd io.ReadCloser
	)
	if response.Header.Get("Content-Encoding") == "gzip" {
		responseRd, err = gzip.NewReader(response.Body)
		if err != nil {
			logger.Errorf("make gzip reader failed: %v", err)
			return nil
		}
	} else {
		responseRd = response.Body
	}
	return responseRd
}

// checkResponseCode 检查返回是否符合配置
func checkResponseCode(step *configs.HTTPTaskStepConfig, response *http.Response) bool {
	if len(step.ResponseCodeList) > 0 {
		for _, code := range step.ResponseCodeList {
			if response.StatusCode == code {
				return true
			}
		}
		return false
	}
	return true
}

type Gather struct {
	tasks.BaseTask
	contentTypeRegexp *regexp.Regexp
	bufferBuilder     tasks.BufferBuilder
}

// UpdateEventByResponse 根据返回写入结果数据
func (g *Gather) UpdateEventByResponse(event *Event, response *http.Response) {
	event.Message = response.Status
	event.ResponseCode = response.StatusCode
	event.ContentLength, _ = strconv.Atoi(response.Header.Get("Content-Length"))

	matches := g.contentTypeRegexp.FindStringSubmatch(response.Header.Get("Content-Type"))
	if len(matches) > 0 {
		for index, name := range g.contentTypeRegexp.SubexpNames() {
			switch name {
			case "mediatype":
				event.MediaType = matches[index]
			case "charset":
				event.Charset = matches[index]
			}
		}
	}
}

// makeRequest 从配置生成请求
func (g *Gather) makeRequest(ctx context.Context, step *configs.HTTPTaskStepConfig, url string) (*http.Request, error) {
	conf := g.GetConfig().(*configs.HTTPTaskConfig)
	requestData, err := utils.ConvertStringToBytes(step.Request, step.RequestFormat)
	if err != nil {
		return nil, errors.Wrapf(err, "convert request data failed, taskID=%v", conf.TaskID)
	}

	logger.Infof("%v: %s %s request: %s", conf.TaskID, step.Method, url, requestData)
	reader := bytes.NewReader(requestData)
	request, err := http.NewRequest(step.Method, url, reader)
	if err != nil {
		return nil, errors.Wrapf(err, "make request failed, taskID=%v", conf.TaskID)
	}
	request = request.WithContext(ctx)

	request.Header.Add("Accept-Charset", "utf-8")
	for key, value := range step.Headers {
		request.Header.Add(key, value)
	}
	return request, nil
}

// Client 请求客户端
type Client interface {
	Do(*http.Request) (*http.Response, error)
}

// GatherURL 测试链接并设置结果事件，url为请求的链接，proxyHost和proxyIP为需要代理的host和ip
func (g *Gather) GatherURL(ctx context.Context, event *Event, step *configs.HTTPTaskStepConfig, url, host string) bool {
	var (
		ok    bool
		count int
		err   error
	)

	conf := g.GetConfig().(*configs.HTTPTaskConfig)
	client := NewClient(conf, map[string]string{host: host})
	utils.RecoverFor(func(err error) {
		logger.Errorf("panic: %v", err)
	})

	// 初始化请求
	request, err := g.makeRequest(ctx, step, url)
	if err != nil {
		logger.Error(err)
		event.Fail(define.CodeBadRequestParams)
		return false
	}
	// 获取结果
	response, err := client.Do(request)
	if err != nil {
		logger.Errorf("task(%d) request failed, url=%v, err: %v", conf.TaskID, url, err)
		event.FailFromError(err)
		return false
	}
	defer response.Body.Close()

	logger.Infof("task(%d): %v %v response: code=%v", conf.TaskID, step.Method, url, response.StatusCode)
	g.UpdateEventByResponse(event, response) // 根据结果设置事件字段

	// 检查响应状态码是否符合预期
	if !checkResponseCode(step, response) {
		event.Fail(define.CodeResponseNotMatch)
		return false
	}
	// 未配置响应内容无需检查
	if step.Response == "" {
		event.SuccessOrTimeout()
		return true
	}

	// 读取响应内容明文reader
	responseRd := makeResponseReader(response)
	if responseRd == nil {
		event.Fail(define.CodeResponseFailed)
		return false
	}
	defer responseRd.Close()

	if step.Response != "" {
		// 读取响应内容字符串
		body := g.bufferBuilder.GetBuffer(conf.BufferSize)
		count, err = responseRd.Read(body)
		if err != nil && err != io.EOF {
			logger.Debugf("task(%d): %v read response error: %v", conf.TaskID, url, err)
			event.FailFromError(err)
			return false
		}
		body = body[:count]
		// 根据返回编码转码为utf8
		decoder := utils.NewDecoder(event.Charset)
		if decoder != nil {
			decoded, err := decoder.Bytes(body)
			if err != nil {
				logger.Debugf("task(%d): %v decode body error: %v", conf.TaskID, url, err)
				body = decoded
			}
		}
		// 对比响应内容是否符合配置
		logger.Debugf("task(%d): %v response: %s", conf.TaskID, url, body)
		ok = utils.IsMatch(step.ResponseFormat, body, []byte(step.Response))
		if !ok {
			event.Fail(define.CodeResponseNotMatch)
			return false
		}
	}
	event.SuccessOrTimeout()
	return true
}

// NewClient proxyMap代理配置 key: host value: proxy ip, 如{"example.com": "127.0.0.1"}
var NewClient = func(conf *configs.HTTPTaskConfig, proxyMap map[string]string) Client {
	cj, err := cookiejar.New(nil)
	if err != nil {
		logger.Errorf("create cookiejar failed: %v", err)
	}
	dialer := net.Dialer{
		Timeout: conf.Timeout,
	}
	transport := &http.Transport{
		MaxResponseHeaderBytes: int64(conf.BufferSize),
		DisableKeepAlives:      true,
		TLSClientConfig: &tls.Config{
			// 跳过https证书检查
			InsecureSkipVerify: conf.InsecureSkipVerify,
			Renegotiation:      tls.RenegotiateFreelyAsClient,
		},
		Proxy: func(_ *http.Request) (*url.URL, error) {
			if conf.Proxy != "" {
				return url.Parse(conf.Proxy)
			}
			return nil, nil
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			network = "tcp"
			switch conf.TargetIPType {
			case configs.IPv4:
				network = "tcp4"
			case configs.IPv6:
				network = "tcp6"
			}
			logger.Debugf("http dial with network: %s", network)
			// 指定ip时按照ip请求
			if len(proxyMap) > 0 {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					logger.Errorf("parse addr %s failed: %v", addr, err)
					host = addr
				}
				// 当跳转时host可能变化，此时不需要代理
				if proxyIP, ok := proxyMap[host]; ok {
					addr = net.JoinHostPort(proxyIP, port)
				}
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	client := &http.Client{
		Transport: transport,
		Jar:       cj,
		Timeout:   conf.GetTimeout(),
	}
	return client
}

func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	conf := g.GetConfig().(*configs.HTTPTaskConfig)
	for _, c := range conf.Steps {
		validateConfig(c)
		logger.Debugf("validated step config: %#v", c)
	}

	g.PreRun(ctx)
	defer g.PostRun(ctx)

	for index, step := range conf.Steps {
		urls := step.URLs()
		if len(urls) == 0 {
			continue
		}

		// dns_check_mode
		// - all: 检查域名解析出来的所有 ip
		// - single: 检查域名解析出来的随机一个 ip
		resolvedIPs := make(map[string][]string)
		hostsInfo := tasks.GetHostsInfo(ctx, urls, conf.DNSCheckMode, conf.TargetIPType, configs.Http)
		for _, h := range hostsInfo {
			if h.Errno != define.CodeOK {
				event := NewEvent(g)
				event.ToStep(index, step.Method, h.Host)
				event.Fail(h.Errno)
				e <- event
			} else {
				resolvedIPs[h.Host] = h.Ips
			}
		}

		type Arg struct {
			index      int
			stepConfig *configs.HTTPTaskStepConfig
			url        string
			resolvedIP string
		}

		doRequest := func(arg Arg) {
			event := NewEvent(g)
			event.ToStep(arg.index+1, arg.stepConfig.Method, arg.url)
			event.ResolvedIP = arg.resolvedIP
			subCtx, cancelFunc := context.WithTimeout(ctx, conf.GetTimeout())
			defer func() {
				cancelFunc()
				event.EndAt = time.Now()
				g.GetSemaphore().Release(1)
				e <- event
			}()
			g.GatherURL(subCtx, event, arg.stepConfig, arg.url, arg.resolvedIP)
		}

		var wg sync.WaitGroup
		for host, ips := range resolvedIPs {
			err := g.GetSemaphore().Acquire(ctx, int64(len(ips)))
			if err != nil {
				logger.Errorf("task(%d) semaphore acquire failed", g.TaskConfig.GetTaskID())
				return
			}

			for _, ip := range ips {
				wg.Add(1)
				arg := Arg{index: index, stepConfig: step, url: host, resolvedIP: ip}
				go func() {
					defer wg.Done()
					doRequest(arg)
				}()
			}
		}
		wg.Wait()
	}
}

func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{
		contentTypeRegexp: regexp.MustCompile(`(?P<mediatype>[^;\s]*)\s*;?\s*(?:charset\s*=\s*(?P<charset>[^;\s]*)|)\s*;?\s*`),
	}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig
	gather.Init()

	return gather
}
