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
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Event 结果数据
type Event struct {
	*tasks.Event
	URL           string
	Index         int
	Steps         int
	Method        string
	ResponseCode  int
	Message       string
	Charset       string
	ContentLength int
	MediaType     string
	ResolvedIP    string
}

// AsMapStr :
func (e *Event) AsMapStr() common.MapStr {
	mapStr := e.Event.AsMapStr()
	mapStr["url"] = e.URL
	mapStr["steps"] = e.Steps
	mapStr["method"] = e.Method
	mapStr["response_code"] = e.ResponseCode
	mapStr["message"] = e.Message
	mapStr["charset"] = e.Charset
	mapStr["content_length"] = e.ContentLength
	mapStr["media_type"] = e.MediaType
	mapStr["resolved_ip"] = e.ResolvedIP
	return mapStr
}

// ToStep 按照采集子配置填写事件信息
func (e *Event) ToStep(index int, step *configs.HTTPTaskStepConfig, url string) {
	e.URL = url
	e.Method = step.Method
	e.Index = index
}

// OK :
func (e *Event) OK() bool {
	return e.Status == define.GatherStatusOK
}

// Fail :
func (e *Event) Fail(code define.BeatErrorCode) {
	e.Event.Fail(code)
	e.Status = int32(e.Index)
}

// FailFromError :
func (e *Event) FailFromError(err error) {
	e.Message = err.Error()
	switch typ := err.(type) {
	case *url.Error:
		if typ.Timeout() {
			e.Fail(define.BeatErrCodeResponseTimeoutError)
		} else {
			e.Fail(define.BeatErrCodeResponseError)
		}
	}
}

// NewEvent :
func NewEvent(g *Gather) *Event {
	conf := g.GetConfig().(*configs.HTTPTaskConfig)
	event := &Event{
		Event: tasks.NewEvent(g),
		Steps: len(conf.Steps),
		Index: 1,
	}
	return event
}

// makeResponseReader 从response获取reader
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

// Gather :
type Gather struct {
	tasks.BaseTask
	contentTypeRegexp *regexp.Regexp
	bufferBuilder     tasks.BufferBuilder
}

// UpdateEventByResponse 根据返回写入结果数据
func (g *Gather) UpdateEventByResponse(event *Event, response *http.Response) error {
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

	return nil
}

// makeRequest 从配置生成请求
func (g *Gather) makeRequest(ctx context.Context, step *configs.HTTPTaskStepConfig, url string) (*http.Request, error) {
	conf := g.GetConfig().(*configs.HTTPTaskConfig)
	requestData, err := utils.ConvertStringToBytes(step.Request, step.RequestFormat)
	if err != nil {
		logger.Warnf("%v: convert request data error: %v", conf.TaskID, err)
		return nil, err
	}

	logger.Debugf("%v: %s %s request: %s", conf.TaskID, step.Method, url, requestData)
	reader := bytes.NewReader(requestData)
	request, err := http.NewRequest(step.Method, url, reader)
	if err != nil {
		logger.Warnf("%v: create %v failed: %v", conf.TaskID, url, err)
		return nil, err
	}
	request = request.WithContext(ctx)

	request.Header.Add("Accept-Charset", "utf-8")
	for key, value := range step.Headers {
		request.Header.Add(key, value)
	}
	return request, nil
}

// checkResponseCode 检查返回是否符合配置
func (g *Gather) checkResponseCode(step *configs.HTTPTaskStepConfig, response *http.Response) bool {
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

// Client 请求客户端
type Client interface {
	Do(*http.Request) (*http.Response, error)
}

// GatherURL 测试链接并设置结果事件，url为请求的链接，proxyHost和proxyIP为需要代理的host和ip
func (g *Gather) GatherURL(
	ctx context.Context, event *Event, step *configs.HTTPTaskStepConfig,
	url, host string,
) bool {
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
		event.Fail(define.BeatErrCodeRequestInitError)
		return false
	}
	// 获取结果
	response, err := client.Do(request)
	if err != nil {
		logger.Debugf("%v: %v failed: %v", conf.TaskID, url, err)
		event.FailFromError(err)
		return false
	}
	defer func() {
		err := response.Body.Close()
		if err != nil {
			logger.Warnf("%v: close response error: %v", conf.TaskID, err)
		}
	}()

	logger.Debugf("%v: %v %v response: code=%v, status=%v",
		conf.TaskID, step.Method, url, response.StatusCode, response.Status,
	)
	// 根据结果设置事件字段
	err = g.UpdateEventByResponse(event, response)
	if err != nil {
		logger.Warnf("update event by response failed: %v", err)
		return false
	}
	// 检查响应状态码是否符合预期
	if !g.checkResponseCode(step, response) {
		event.Fail(define.BeatErrCodeResponseCodeError)
		return false
	}
	// 未配置响应内容无需检查
	if step.Response == "" {
		logger.Debugf("%v: %v return without match", conf.TaskID, url)
		event.SuccessOrTimeout()
		return true
	}
	// 读取响应内容明文reader
	responseRd := makeResponseReader(response)
	if responseRd == nil {
		event.Fail(define.BeatErrCodeResponseHandleError)
		return false
	}
	defer func() {
		err := responseRd.Close()
		if err != nil {
			logger.Warnf("%v: close response reader error: %v", conf.TaskID, err)
		}
	}()

	if step.Response != "" {
		// 读取响应内容字符串
		body := g.bufferBuilder.GetBuffer(conf.BufferSize)
		count, err = responseRd.Read(body)
		if err != nil && err != io.EOF {
			logger.Debugf("%v: %v read response error: %v", conf.TaskID, url, err)
			event.FailFromError(err)
			return false
		}
		body = body[:count]
		// 根据返回编码转码为utf8
		decoder := utils.NewDecoder(event.Charset)
		if decoder != nil {
			decoded, err := decoder.Bytes(body)
			if err != nil {
				logger.Debugf("%v: %v decode body error: %v", conf.TaskID, url, err)
				body = decoded
			}
		}
		// 对比响应内容是否符合配置
		logger.Debugf("%v: %v response: %s", conf.TaskID, url, body)
		ok = utils.IsMatch(step.ResponseFormat, body, []byte(step.Response))
		if !ok {
			logger.Debugf("%v: %v match body fail with type[%v]", conf.TaskID, url, step.ResponseFormat)
			event.Fail(define.BeatErrCodeResponseMatchError)
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
		logger.Errorf("create cookiejar failed????: %v", err)
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
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return client
}

// Run 主入口
func (g *Gather) Run(ctx context.Context, e chan<- define.Event) {
	var (
		conf = g.GetConfig().(*configs.HTTPTaskConfig)
	)

	g.PreRun(ctx)
	defer g.PostRun(ctx)

	pResultMap := make(map[string][]string)
	for index, step := range conf.Steps {
		// 1. 获取 URLList 如果 url 和 urlList 均为空，则直接返回空结果，并且错误码为 success
		// 2. 遍历 URLList 逐个 url 判断是 ip 还是域名
		//   1) 是 ip 则直接测试连接获取测试结果
		//   2) 是域名则解析域名，获取 ip 列表：
		// dns_check_mode: all
		//  1) target_ip_type:0 所有 ip 都测试
		//	2) target_ip_type:4 只测试 ipv4 的ip 若无，则返回错误码 3011
		//	3) target_ip_type:6 只测试 ipv6 的ip 若无，则返回错误码 3012
		// dns_check_mode: single
		//	1) target_ip_type:0 取 ip 列表第一个 ip 做测试
		//	2) target_ip_type:4 从 ip 列表中查找是否存在 ipv4 的 ip，存在则取第一个测试，不存在则返回错误码 3011
		//	3) target_ip_type:6 从 ip 列表中查找是否存在 ipv6 的 ip，存在则取第一个测试，不存在则返回错误码 3012
		if step.URL == "" && (len(step.URLList) == 0) {
			//不上报任何数据
			logger.Debugf("http URLList is empty.")
			return
		}

		//获取配置的url列表
		urls := make([]string, 0)
		if step.URL != "" {
			urls = append(urls, step.URL)
		}
		if len(step.URLList) > 0 {
			urls = step.URLList
		}
		hostsInfo := tasks.GetHostsInfo(ctx, urls, conf.DNSCheckMode, conf.TargetIPType, configs.Http)
		for _, h := range hostsInfo {
			if h.Errno != define.BeatErrCodeOK {
				event := NewEvent(g)
				event.ToStep(1, step, h.Host)
				event.Fail(h.Errno)
				e <- event
			} else {
				pResultMap[h.Host] = h.Ips
			}
		}
		// 获取子配置代理配置
		var wg sync.WaitGroup
		for u, ips := range pResultMap {
			// 获取并发限制信号量
			err := g.GetSemaphore().Acquire(ctx, int64(len(ips)))
			if err != nil {
				logger.Errorf("Semaphore Acquire failed for task http task id: %d", g.TaskConfig.GetTaskID())
				return
			}
			// 按照代理IP列表逐个请求
			for _, ipStr := range ips {
				wg.Add(1)
				go func(i int, s *configs.HTTPTaskStepConfig, url, h string) {
					event := NewEvent(g)
					event.ToStep(i+1, step, url)
					event.ResolvedIP = h
					gCtx, cancelFunc := context.WithTimeout(ctx, conf.GetTimeout())
					defer func() {
						cancelFunc()
						// 设置事件完成时间
						event.EndAt = time.Now()
						wg.Done()
						// 释放信号量
						g.GetSemaphore().Release(1)
						// 发送事件
						e <- event
					}()
					event.StartAt = time.Now()
					// 检查url并设置结果事件
					g.GatherURL(gCtx, event, s, u, h)
				}(index, step, u, ipStr)
			}
		}

		wg.Wait()
	}
}

// New :
func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{
		contentTypeRegexp: regexp.MustCompile(`(?P<mediatype>[^;\s]*)\s*;?\s*(?:charset\s*=\s*(?P<charset>[^;\s]*)|)\s*;?\s*`),
	}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig

	gather.Init()

	return gather
}
