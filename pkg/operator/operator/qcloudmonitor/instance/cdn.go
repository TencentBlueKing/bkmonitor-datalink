// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package instance

import (
	"github.com/pkg/errors"
	cdn "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/cdn/v20180606"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	"k8s.io/utils/ptr"
)

const (
	endpointCDN = "cdn.tencentcloudapi.com"
)

func init() {
	Register(&cdnQuerier{}, "QCE/CDN", "QCE/CDN_LOG_DATA", "QCE/OV_CDN")
}

type cdnQuerier struct{}

func (q *cdnQuerier) Query(p *Parameters) ([]any, error) {
	credential := common.NewCredential(p.SecretId, p.SecretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = pickEndpoint(endpointCDN)

	client, err := cdn.NewClient(credential, p.Region, cpf)
	if err != nil {
		return nil, err
	}

	request, err := q.makeRequest(p)
	if err != nil {
		return nil, err
	}

	response, err := client.DescribeDomains(request)
	if err != nil {
		return nil, err
	}

	if response.Response == nil || response.Response.Domains == nil {
		return nil, nil
	}

	var data []any
	for _, item := range response.Response.Domains {
		data = append(data, item)
	}
	return data, nil
}

func (q *cdnQuerier) Filters() []string {
	return []string{
		"Domain",
		"ResourceId",
		"ProjectId",
		"ServiceType",
		"Status",
	}
}

func (q *cdnQuerier) ParametersJSON(p *Parameters) (string, error) {
	request, err := q.makeRequest(p)
	if err != nil {
		return "", err
	}
	return request.ToJsonString(), nil
}

func (q *cdnQuerier) makeRequest(p *Parameters) (*cdn.DescribeDomainsRequest, error) {
	request := cdn.NewDescribeDomainsRequest()
	for _, tag := range p.Tags {
		request.Filters = append(request.Filters,
			&cdn.DomainFilter{
				Name:  ptr.To(tag.Key()),
				Value: toPointerStrings(tag.Values),
			},
		)
	}

	for _, filter := range p.Filters {
		switch filter.Name {
		case "Domain", "ResourceId", "ProjectId", "ServiceType", "Status":
			request.Filters = append(request.Filters,
				&cdn.DomainFilter{
					Name:  ptr.To(filter.LowerKey()),
					Value: toPointerStrings(filter.Values),
				},
			)
		default:
			return nil, errors.Errorf("illegel filter (%s)", filter.Name)
		}
	}
	return request, nil
}
