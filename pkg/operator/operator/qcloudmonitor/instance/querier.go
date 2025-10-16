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
	"sort"
	"strconv"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/configs"
)

type Parameters struct {
	Namespace string   `json:"namespace"`
	SecretId  string   `json:"secretId"`
	SecretKey string   `json:"secretKey"`
	Region    string   `json:"region"`
	Tags      []Tag    `json:"tags"`
	Filters   []Filter `json:"filters"`
}

type Tag struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
	Fuzzy  bool     `json:"fuzzy"`
}

func (t Tag) Key() string {
	return "tag:" + t.Name
}

type Filter struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

func (f Filter) LowerKey() string {
	return strings.ToLower(f.Name)
}

func pickEndpoint(ep string) string {
	if !configs.G().QCloudMonitor.Private {
		return ep
	}

	parts := strings.Split(ep, ".")

	var dst []string
	dst = append(dst, parts[0])
	dst = append(dst, "internal")
	dst = append(dst, parts[1:]...)
	return strings.Join(dst, ".")
}

func toPointerStrings(ss []string) []*string {
	lst := make([]*string, 0, len(ss))
	for _, s := range ss {
		lst = append(lst, ptr.To(s))
	}
	return lst
}

func toPointerStringsAt(ss []string, idx int) *string {
	lst := toPointerStrings(ss)
	if len(lst) == 0 || idx >= len(lst) {
		return nil
	}
	return lst[idx]
}

func toPointerInt64At(ss []string, idx int) *int64 {
	s := toPointerStringsAt(ss, idx)
	if s == nil {
		return nil
	}

	i, err := strconv.Atoi(*s)
	if err != nil {
		return nil
	}
	return ptr.To(int64(i))
}

// Querier 产品示例查询接口
type Querier interface {
	// Query 查询接口
	Query(p *Parameters) ([]any, error)

	// Filters 支持的 filters 字段
	Filters() []string

	// ParametersJSON 返回 Parameters JSON 字符串
	ParametersJSON(p *Parameters) (string, error)
}

var queriers = make(map[string]Querier)

func Register(q Querier, namespaces ...string) {
	for _, namespace := range namespaces {
		if _, ok := queriers[namespace]; ok {
			panic("duplicate register querier")
		}
		queriers[namespace] = q
	}
}

func Get(namespace string) (Querier, bool) {
	q, ok := queriers[namespace]
	return q, ok
}

func Namespaces() []string {
	ns := make([]string, 0, len(queriers))
	for n := range queriers {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return ns
}
