// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package poller

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jmespath/go-jmespath"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

type HttpPoller struct {
	DataSource *define.DataSource
	Plugin     *define.HttpPullPlugin

	client *http.Client

	timeMaker *TimeMaker
	paginator Paginator

	unmarshalFn utils.UnmarshalFn

	compiledEventsPath *jmespath.JMESPath
	compiledTotalPath  *jmespath.JMESPath
}

func (p *HttpPoller) Pull() (define.Payload, error) {
	logger := logging.GetLogger()

	payload := define.Payload{IgnoreResult: false}

	p.paginator.Reset()

	timeContext := p.timeMaker.GetTimeRange()

	logger.Infof("Poller(%s) start pull, time: %v", p.Plugin.PluginID, timeContext)

	for {
		// 构造请求变量
		context := Context{}
		context.Update(timeContext, p.paginator.GetAndNext())

		// 构造请求对象，其中包括渲染变量模板
		request, err := p.NewRequest(context)
		if err != nil {
			return payload, fmt.Errorf("make request error: %+v", err)
		}

		logger.Debugf("request data: %+v", request)

		// 进行请求，获取原始数据
		rawData, err := p.PerformRequest(request)
		if err != nil {
			return payload, fmt.Errorf("perform request error: %+v", err)
		}

		logger.Debugf("response raw data: %s", rawData)

		// 反序列化
		data, err := p.UnmarshalEvents(rawData)
		if err != nil {
			return payload, fmt.Errorf("unmarshal response data error: %+v", err)
		}

		// 类型转换：从接口返回的数据获取实际的告警内容
		events, err := p.ConvertEvents(data)
		if err != nil {
			return payload, fmt.Errorf("convert to event data error: %+v", err)
		}

		// 装载到 payload 中
		payload.AddEvents(events...)

		if p.compiledTotalPath == nil {
			// 如果没有设置 total 的取值方式，那么就将总页数设置为 MaxSize
			p.paginator.SetTotalToMax()
		} else {
			// 获取数据总数
			totalCount := 0
			total, err := p.compiledTotalPath.Search(data)
			if err != nil {
				logger.Warnf("Poller(%s) get total count failed: %+v, data: %+v", p.Plugin.PluginID, err, data)
			} else {
				totalCountFloat, _ := total.(float64)
				totalCount = int(totalCountFloat)
			}
			// 设置总数量
			p.paginator.SetTotal(totalCount)
		}

		// 判断是否已经翻到最后一页
		if !p.paginator.HasNext() {
			break
		}
	}

	// 同步最后一次拉取时间
	p.timeMaker.CommitLastCheckTime()

	logger.Infof("Poller(%s) finish pull, event count: %d", p.Plugin.PluginID, payload.GetEventCount())
	return payload, nil
}

func (p *HttpPoller) GetInterval() int {
	// 获取拉取周期间隔
	return p.Plugin.Interval
}

func (p *HttpPoller) Init() {
	// 初始化请求客户端
	if p.client == nil {
		p.client = &http.Client{
			Timeout: time.Duration(p.Plugin.Timeout) * time.Second,
		}
	}
	// 初始化分页器
	if p.paginator == nil {
		basePaginator := BasePaginator{
			PageSize: p.Plugin.Pagination.Option.PageSize,
			MaxSize:  p.Plugin.Pagination.Option.MaxSize,
		}
		switch p.Plugin.Pagination.Type {
		case define.PaginationTypeLimitOffset:
			p.paginator = &LimitOffsetPaginator{
				BasePaginator: basePaginator,
			}
		case define.PaginationTypePageNumber:
			p.paginator = &PageNumberPaginator{
				BasePaginator: basePaginator,
			}
		default:
			p.paginator = &NilPaginator{}
		}
	}

	if p.timeMaker == nil {
		p.timeMaker = &TimeMaker{
			DataID:   p.DataSource.DataID,
			Interval: p.Plugin.Interval,
			Overlap:  p.Plugin.Overlap,
			Format:   p.Plugin.TimeFormat,
		}
	}

	p.unmarshalFn = utils.GetUnmarshalFn(p.Plugin.SourceFormat)

	if p.Plugin.EventsPath != "" {
		p.compiledEventsPath = jmespath.MustCompile(p.Plugin.EventsPath)
	}

	if p.Plugin.Pagination.Option.TotalPath != "" {
		p.compiledTotalPath = jmespath.MustCompile(p.Plugin.Pagination.Option.TotalPath)
	}
}

func (p *HttpPoller) NewRequest(context Context) (*http.Request, error) {
	logger := logging.GetLogger()

	var payload io.Reader
	var err error

	// 是否需要特殊注入Content-Type的header
	var contentType string
	var strPayload string

	// 1. 处理 Request Body
	switch p.Plugin.Body.DataType {
	case define.HttpBodyTypeFormData:
		// form data 需使用 multipart 生成请求payload
		buffer := &bytes.Buffer{}
		writer := multipart.NewWriter(buffer)
		for _, kvPair := range p.Plugin.Body.Params {
			if kvPair.IsEnabled {
				err = writer.WriteField(kvPair.Key, context.Render(kvPair.Value))
			}
		}
		err = writer.Close()
		if err != nil {
			return nil, err
		}
		payload = buffer
		contentType = writer.FormDataContentType()
	case define.HttpBodyTypeUrlEncoded:
		// 形如 a=b&c=d
		data := url.Values{}
		for _, kvPair := range p.Plugin.Body.Params {
			if kvPair.IsEnabled {
				data.Add(kvPair.Key, context.Render(kvPair.Value))
			}
		}

		strPayload = context.Render(data.Encode())
		payload = strings.NewReader(strPayload)
		contentType = "application/x-www-form-urlencoded"
	default:
		// 其余情况都是直接解析 Content 字段内容，如json
		strPayload = context.Render(p.Plugin.Body.Content)
		logger.Debugf("Poller(%s) new request with body: %s", p.Plugin.PluginID, strPayload)
		payload = strings.NewReader(strPayload)
		contentType = p.GetContentType()
	}

	// 2. 生成request对象
	request, err := http.NewRequest(p.Plugin.Method, p.Plugin.URL, payload)
	if err != nil {
		return nil, err
	}

	// 3. 注入 URL 参数
	query := request.URL.Query()
	for _, kvPair := range p.Plugin.Params {
		if kvPair.IsEnabled {
			query.Add(kvPair.Key, context.Render(kvPair.Value))
		}
	}
	request.URL.RawQuery = query.Encode()

	// 4. 根据数据类型类型，增加默认请求头
	if contentType != "" {
		request.Header.Add("Content-Type", contentType)
	}

	// 5. 处理鉴权
	switch p.Plugin.Authorize.Type {
	case define.HttpAuthTypeBasic:
		// Basic Auth鉴权参数组装
		request.SetBasicAuth(p.Plugin.Authorize.Option.UserName, p.Plugin.Authorize.Option.Password)
	case define.HttpAuthTypeBearerToken:
		// bearertoken鉴权组装
		bearer := "Bearer " + p.Plugin.Authorize.Option.Token
		request.Header.Add("Authorization", bearer)
	case define.HttpAuthTypeTencentCloud:
		// 通用的腾讯云API的鉴权
		SetTencentAuth(*request, p.Plugin.URL, p.Plugin.Authorize.Option.TencentApiAuth, strPayload, p.Plugin.Method)
	}

	// 6. 注入用户自定义请求头
	for _, kvPair := range p.Plugin.Headers {
		if kvPair.IsEnabled {
			request.Header.Set(kvPair.Key, context.Render(kvPair.Value))
		}
	}

	return request, nil
}

func (p *HttpPoller) GetContentType() string {
	// 获取post请求的content_type
	var contentType string
	switch p.Plugin.Body.ContentType {
	case define.HttpBodyContentTypeText:
		contentType = "text/plain"
	case define.HttpBodyContentTypeHtml:
		contentType = "text/html"
	case define.HttpBodyContentTypeXml:
		contentType = "application/xml"
	case define.HttpBodyContentTypeJson:
		contentType = "application/json; charset=utf-8"
	}
	return contentType
}

func (p *HttpPoller) PerformRequest(request *http.Request) ([]byte, error) {
	// 执行请求
	response, err := p.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	return ioutil.ReadAll(response.Body)
}

func (p *HttpPoller) ConvertEvents(v interface{}) ([]define.Event, error) {
	// 获取事件数据
	logger := logging.GetLogger()

	var eventValue interface{}
	var err error
	if p.Plugin.EventsPath == "" {
		eventValue = v
	} else {
		eventValue, err = p.compiledEventsPath.Search(v)
		if err != nil {
			return nil, fmt.Errorf("fetch events by events_path error: %+v", err)
		}
	}

	var events []define.Event
	if p.Plugin.MultipleEvents {
		// 一次请求返回多条告警事件的情况
		eventList, ok := eventValue.([]interface{})
		if !ok {
			return nil, fmt.Errorf("eventsPath(%s) is not type of `[]Event`", p.Plugin.EventsPath)
		}
		for _, rawEvent := range eventList {
			event, ok := rawEvent.(map[string]interface{})
			if !ok {
				logger.Errorf("event(%+v) is not type of `Event`", rawEvent)
				continue
			}
			events = append(events, event)
		}
	} else {
		// 单告警解析情况
		event, ok := eventValue.(map[string]interface{})
		events = []define.Event{event}
		if !ok {
			return nil, fmt.Errorf("eventsPath(%s) is not type of `Event`", p.Plugin.EventsPath)
		}
	}

	return events, nil
}

func (p *HttpPoller) UnmarshalEvents(rawData []byte) (interface{}, error) {
	// 对原始数据进行反序列化
	var data interface{}
	err := p.unmarshalFn(rawData, &data)
	if err != nil {
		return nil, fmt.Errorf("unmarshal raw data error: %+v", err)
	}
	return data, nil
}

func NewHttpPoller(d *define.DataSource) (Poller, error) {
	plugin, err := define.NewHttpPullPlugin(d.Option)
	if err != nil {
		return nil, err
	}
	poller := &HttpPoller{
		DataSource: d,
		Plugin:     plugin,
	}
	poller.Init()
	return poller, nil
}

func init() {
	RegisterPoller("http_pull", NewHttpPoller)
}
