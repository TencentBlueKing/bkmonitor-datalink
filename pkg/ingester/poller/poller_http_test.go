// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package poller_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/poller"
)

func TestHTTPPoller(t *testing.T) {
	url := "/get_alarm_info/"
	data := map[string]interface{}{
		"bk_app_code":   "{{bk_app_code}}",
		"bk_app_secret": "{{bk_app_secret}}",
		"dept":          "{{dept}}",
		"begin_time":    "{{begin_time}}",
		"end_time":      "{{end_time}}",
		"pageSize":      500,
	}
	body, err := json.Marshal(data)
	if err != nil {
		t.Errorf("HTTPPoller data error %s", err)
	}

	plugin := define.HttpPullPlugin{
		Plugin: define.Plugin{
			PluginID:   "tnm",
			PluginType: "http_pull",
		},
		SourceFormat:   "json",
		MultipleEvents: true,
		EventsPath:     "data[2]",
		URL:            url,
		Body: define.HttpBodyConfig{
			DataType:    "raw",
			ContentType: define.HttpBodyContentTypeJson,
			Content:     string(body),
		},
		Method: "POST",
		Pagination: define.HttpPaginationConfig{
			Type: "limit_offset",
		},
		TimeFormat: "datetime",
		Interval:   60,
		Overlap:    300,
	}
	plugin.Pagination.Option.PageSize = 10
	plugin.Pagination.Option.TotalPath = "data[0]"

	p, err := poller.NewHttpPoller(&define.DataSource{
		MQConfig: define.MetaClusterInfo{ClusterType: "dummy"},
		Option:   plugin,
	})
	assert.NoError(t, err)
	payload, err := p.Pull()
	assert.NoError(t, err)
	assert.NotEmpty(t, payload)
}

func TestTencentPoller(t *testing.T) {
	url := "https://monitor.tencentcloudapi.com"
	body := `{"PageNumber":{{page}},"PageSize":{{page_size}},"Module":"monitor","EndTime":{{end_time}}}`

	secretId := "{{secretId}}"
	secretKey := "{{secretKey}}"
	version := "2018-07-24"
	action := "DescribeAlarmHistories"
	region := "ap-guangzhou"
	// {"Response": {"TotalCount": 0, "Histories": [], "RequestId": "d3c46f23-5e08-4af1-aaf1-25ad4d376912"}}

	plugin := GetTencentPollerPlugin(url, version, region, secretId, secretKey, action, body)

	p, err := poller.NewHttpPoller(&define.DataSource{
		MQConfig: define.MetaClusterInfo{ClusterType: "dummy"},
		Option:   plugin,
	})
	assert.NoError(t, err)
	payload, err := p.Pull()
	assert.NoError(t, err)
	fmt.Println(payload)
}

func TestTencentCVMPoller(t *testing.T) {
	secretId := "{{secretId}}"
	secretKey := "{{secretKey}}"
	url := "https://cvm.tencentcloudapi.com"
	version := "2017-03-12"
	action := "DescribeInstances"
	region := "ap-guangzhou"
	body := `{"MaxLimit": 1, "Filters": [{"Values": ["\u672a\u547d\u540d"], "Name": "instance-name"}]}`

	plugin := GetTencentPollerPlugin(url, version, region, secretId, secretKey, action, body)

	p, err := poller.NewHttpPoller(&define.DataSource{
		MQConfig: define.MetaClusterInfo{ClusterType: "dummy"},
		Option:   plugin,
	})
	assert.NoError(t, err)
	payload, err := p.Pull()
	assert.NoError(t, err)
	fmt.Println(payload)
}

func GetTencentPollerPlugin(url string, version string, region string, secretId string,
	secretKey string, action string, body string,
) *define.HttpPullPlugin {
	plugin := define.HttpPullPlugin{
		Plugin: define.Plugin{
			PluginID:   "tencent_cloud_cvm",
			PluginType: "http_pull",
		},
		SourceFormat:   "json",
		MultipleEvents: true,
		EventsPath:     "Response.InstanceSet",
		URL:            url,
		Body: define.HttpBodyConfig{
			DataType:    "raw",
			ContentType: define.HttpBodyContentTypeJson,
			Content:     string(body),
		},
		Method: "POST",
		Authorize: define.HttpAuthorizeConfig{
			Type: define.HttpAuthTypeTencentCloud,
			Option: define.AuthOption{
				TencentApiAuth: define.TencentApiAuth{
					SecretId:  secretId,
					SecretKey: secretKey,
					Action:    action,
					Version:   version,
					Region:    region,
				},
			},
		},
		Pagination: define.HttpPaginationConfig{
			Type: define.PaginationTypePageNumber,
		},
		TimeFormat: "timestamp",
		Interval:   60,
		Overlap:    300,
	}
	plugin.Pagination.Option.PageSize = 10
	plugin.Pagination.Option.TotalPath = "Response.TotalCount"
	return &plugin
}
