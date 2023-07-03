// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

// initDecorator 初始化装饰器列表
func (httpService *Service) initDecorator() {
	// panic 处理
	panicDecorator := httpService.panicDecoratorGenerator()

	// 基础装饰器，处理读锁和身份验证
	httpService.basicAuthDecorator = []Decorator{
		httpService.authorizationDecoratorGenerator(QueryFailedCountInc),
		httpService.openDecoratorGenerator(httpService.lock.RLock, httpService.lock.RUnlock),
		panicDecorator,
	}

	// 服务信息修改装饰器，处理写锁和身份验证
	httpService.configAuthDecorator = []Decorator{
		httpService.authorizationDecoratorGenerator(QueryFailedCountInc),
		httpService.openDecoratorGenerator(httpService.lock.Lock, httpService.lock.Unlock),
		panicDecorator,
	}

	// query装饰器
	httpService.queryDecorator = []Decorator{
		httpService.postOrGetCheckDecoratorGenerator(QueryFailedCountInc),
		httpService.authorizationDecoratorGenerator(QueryFailedCountInc),
		httpService.availableDecoratorGenerator(QueryFailedCountInc),
		httpService.receivedIncDecoratorGenerator(QueryReceivedCountInc),
		httpService.openDecoratorGenerator(httpService.lock.RLock, httpService.lock.RUnlock),
		panicDecorator,
	}

	// raw_query装饰器
	httpService.queryDecorator = []Decorator{
		httpService.postOrGetCheckDecoratorGenerator(RawQueryFailedCountInc),
		httpService.authorizationDecoratorGenerator(RawQueryFailedCountInc),
		httpService.availableDecoratorGenerator(RawQueryFailedCountInc),
		httpService.receivedIncDecoratorGenerator(RawQueryReceivedCountInc),
		httpService.openDecoratorGenerator(httpService.lock.RLock, httpService.lock.RUnlock),
		panicDecorator,
	}

	// write装饰器
	httpService.writeDecorator = []Decorator{
		httpService.postCheckDecoratorGenerator(WriteFailedCountInc),
		httpService.authorizationDecoratorGenerator(WriteFailedCountInc),
		httpService.availableDecoratorGenerator(WriteFailedCountInc),
		httpService.receivedIncDecoratorGenerator(WriteReceivedCountInc),
		httpService.openDecoratorGenerator(httpService.lock.RLock, httpService.lock.RUnlock),
		panicDecorator,
	}

	// create db装饰器
	httpService.createDBDecorator = []Decorator{
		httpService.checkDBSingleWordDecoratorGenerator(CreateDBFailedCountInc),
		httpService.postOrGetCheckDecoratorGenerator(CreateDBFailedCountInc),
		httpService.authorizationDecoratorGenerator(CreateDBFailedCountInc),
		httpService.availableDecoratorGenerator(CreateDBFailedCountInc),
		httpService.receivedIncDecoratorGenerator(CreateDBReceivedCountInc),
		httpService.openDecoratorGenerator(httpService.lock.RLock, httpService.lock.RUnlock),
		panicDecorator,
	}
}
