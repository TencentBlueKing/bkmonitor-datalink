// MIT License

// Copyright (c) 2021~2024 腾讯蓝鲸

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package http

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
)

func TestNewCustomEventByHttpEvent(t *testing.T) {
	now := time.Now().Add(-time.Second * 10)

	event := &Event{
		Event: &tasks.Event{
			DataID:            int32(123),
			BizID:             2,
			TaskID:            3,
			TaskType:          "http",
			StartAt:           now,
			EndAt:             now.Add(time.Second * 10),
			Status:            1,
			ErrorCode:         define.CodeIPNotFound,
			Available:         1,
			AvailableDuration: 10,
			Labels: []map[string]string{
				{"xxx": "1"},
				{"yyy": "2"},
			},
		},
		URL:           "https://www.qq.com",
		Index:         1,
		Steps:         1,
		Method:        "GET",
		ResponseCode:  200,
		Message:       "OK",
		Charset:       "UTF-8",
		ContentLength: 1024,
		MediaType:     "text/html",
		ResolvedIP:    "",
	}

	customEvent := NewCustomEventByHttpEvent(event)
	m := customEvent.AsMapStr()

	t.Logf("customEvent: %v", m)

	assert.Equal(t, common.MapStr{
		"data": []map[string]interface{}{
			{
				"dimension": map[string]string{
					"bk_biz_id":     "2",
					"error_code":    "1211",
					"media_type":    "text/html",
					"message":       "OK",
					"method":        "GET",
					"resolved_ip":   "",
					"response_code": "200",
					"status":        "1",
					"task_id":       "3",
					"task_type":     "http",
					"url":           "https://www.qq.com",
					"xxx":           "1",
					"bk_agent_id":   "",
					"ip":            "",
					"bk_cloud_id":   "0",
					"node_id":       "0:",
				},
				"metrics": map[string]interface{}{
					"available":     1.0,
					"task_duration": 10000,
				},
				"target":    "https://www.qq.com",
				"timestamp": now.Unix() * 1000,
			},
			{
				"dimension": map[string]string{
					"bk_biz_id":     "2",
					"error_code":    "1211",
					"media_type":    "text/html",
					"message":       "OK",
					"method":        "GET",
					"resolved_ip":   "",
					"response_code": "200",
					"status":        "1",
					"task_id":       "3",
					"task_type":     "http",
					"url":           "https://www.qq.com",
					"yyy":           "2",
					"bk_agent_id":   "",
					"ip":            "",
					"bk_cloud_id":   "0",
					"node_id":       "0:",
				},
				"metrics": map[string]interface{}{
					"available":     1.0,
					"task_duration": 10000,
				},
				"target":    "https://www.qq.com",
				"timestamp": now.Unix() * 1000,
			},
		},
		"dataid":    int32(123),
		"time":      now.Unix(),
		"timestamp": now.Unix(),
	}, m)
}
