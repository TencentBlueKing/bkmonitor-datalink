// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package grpcmiddleware

import (
	"strings"

	"google.golang.org/grpc"
)

var middlewares = map[string]func(string) grpc.ServerOption{}

func Register(name string, f func(opt string) grpc.ServerOption) {
	middlewares[name] = f
}

func Get(name string) grpc.ServerOption {
	if name == "" {
		return nil
	}

	nameOpts := strings.Split(name, ";")
	if len(nameOpts) == 1 {
		return middlewares[nameOpts[0]]("")
	}
	return middlewares[nameOpts[0]](nameOpts[1])
}
