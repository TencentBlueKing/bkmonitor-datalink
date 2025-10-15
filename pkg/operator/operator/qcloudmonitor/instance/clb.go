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
	clb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/clb/v20180317"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	"k8s.io/utils/ptr"
)

const (
	endpointLB = "clb.tencentcloudapi.com"
)

func init() {
	Register(&clbQuerier{}, "QCE/LB_PRIVATE", "QCE/LB_PUBLIC")
}

type clbQuerier struct{}

func (q *clbQuerier) Query(p *Parameters) ([]any, error) {
	credential := common.NewCredential(p.SecretId, p.SecretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = pickEndpoint(endpointLB)

	client, err := clb.NewClient(credential, p.Region, cpf)
	if err != nil {
		return nil, err
	}

	request, err := q.makeRequest(p)
	if err != nil {
		return nil, err
	}

	response, err := client.DescribeLoadBalancers(request)
	if err != nil {
		return nil, err
	}

	if response.Response == nil || response.Response.LoadBalancerSet == nil {
		return nil, nil
	}

	var data []any
	for _, item := range response.Response.LoadBalancerSet {
		data = append(data, item)
	}
	return data, nil
}

func (q *clbQuerier) Filters() []string {
	return []string{
		"Domain",
		"ProjectId",
		"VpcId",
		"Forward",
		"LoadBalancerVips",
		"LoadBalancerId",
		"LoadBalancerName",
		"LoadBalancerType",
	}
}

func (q *clbQuerier) ParametersJSON(p *Parameters) (string, error) {
	request, err := q.makeRequest(p)
	if err != nil {
		return "", err
	}
	return request.ToJsonString(), nil
}

func (q *clbQuerier) makeRequest(p *Parameters) (*clb.DescribeLoadBalancersRequest, error) {
	request := clb.NewDescribeLoadBalancersRequest()
	for _, tag := range p.Tags {
		request.Filters = append(request.Filters,
			&clb.Filter{
				Name:   ptr.To(tag.Key()),
				Values: toPointerStrings(tag.Values),
			},
		)
	}

	for _, filter := range p.Filters {
		switch filter.Name {
		case "Domain":
			request.Domain = toPointerStringsAt(filter.Values, 0)
		case "ProjectId":
			request.ProjectId = toPointerInt64At(filter.Values, 0)
		case "VpcId":
			request.VpcId = toPointerStringsAt(filter.Values, 0)
		case "Forward":
			request.Forward = toPointerInt64At(filter.Values, 0)
		case "LoadBalancerVips":
			request.LoadBalancerVips = toPointerStrings(filter.Values)
		case "LoadBalancerId":
			request.LoadBalancerIds = toPointerStrings(filter.Values)
		case "LoadBalancerName":
			request.LoadBalancerName = toPointerStringsAt(filter.Values, 0)
		case "LoadBalancerType":
			request.LoadBalancerType = toPointerStringsAt(filter.Values, 0)
		default:
			return nil, errors.Errorf("illegel filter (%s)", filter.Name)
		}
	}
	return request, nil
}
