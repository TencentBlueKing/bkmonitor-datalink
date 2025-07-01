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
	clb "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/clb/v20180317"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	"k8s.io/utils/pointer"
)

const (
	namespaceLbPrivate = "QCE/LB_PRIVATE"
	endpointLbPrivate  = "clb.tencentcloudapi.com"
)

func init() {
	Register(namespaceLbPrivate, &clbQuerier{})
}

type clbQuerier struct{}

func (q *clbQuerier) Query(r *Request) ([]any, error) {
	credential := common.NewCredential(r.SecretId, r.SecretKey)
	cpf := profile.NewClientProfile()
	cpf.HttpProfile.Endpoint = pickEndpoint(endpointLbPrivate)

	client, err := clb.NewClient(credential, r.Region, cpf)
	if err != nil {
		return nil, err
	}

	request := clb.NewDescribeLoadBalancersRequest()
	for k, vs := range r.Filters {
		lst := make([]*string, 0, len(vs))
		for _, v := range vs {
			lst = append(lst, pointer.String(v))
		}
		request.Filters = append(request.Filters,
			&clb.Filter{
				Name:   pointer.String(k),
				Values: lst,
			},
		)
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
