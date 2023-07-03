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
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// Handler 基础HTTP接口
type Handler func(writer http.ResponseWriter, request *http.Request)

// Decorator 装饰器接口
type Decorator func(httpFunc Handler) Handler

// FailedCountInc failed计数
type FailedCountInc func(db string, code string) error

// ReceivedCountInc received计数
type ReceivedCountInc func(db string) error

// BasicCountInc 基础计数
type BasicCountInc func() error

// decorate 装饰指定的HTTP处理方法，第一个参数是被装饰的方法，后面的是装饰器列表,注意装饰顺序为从前到后,最后一个装饰会被最先调用
func (httpService *Service) decorate(preFunc Handler, decoratorList ...Decorator) Handler {
	for _, decorator := range decoratorList {
		preFunc = decorator(preFunc)
	}
	return preFunc
}

// openDecoratorGenerator 入口装饰器,处理http锁,以及关闭body,要求动态传入lock和unlock方法
func (httpService *Service) openDecoratorGenerator(lockFunc func(), unlockFunc func()) func(Handler) Handler {
	return func(httpFunc Handler) Handler {
		return func(writer http.ResponseWriter, request *http.Request) {
			flowLog := logging.NewEntry(map[string]interface{}{
				"module":  moduleName,
				"flow_id": common.GetFlow(request),
			})
			defer func() { _ = request.Body.Close() }()
			lockFunc()
			flowLog.Tracef("get RLock")
			defer func() {
				unlockFunc()
				flowLog.Tracef("release RLock")
			}()
			httpFunc(writer, request)
		}
	}
}

// receivedIncDecoratorGenerator 指标增长装饰器
func (httpService *Service) receivedIncDecoratorGenerator(metricInc ReceivedCountInc) func(Handler) Handler {
	return func(httpFunc Handler) Handler {
		return func(writer http.ResponseWriter, request *http.Request) {
			flowLog := logging.NewEntry(map[string]interface{}{
				"module":  moduleName,
				"flow_id": common.GetFlow(request),
			})
			defer func() { _ = request.Body.Close() }()
			db := strings.TrimSpace(request.URL.Query().Get("db"))
			if db == "" {
				db = strings.TrimSpace(request.Header.Get("db"))
			}
			metricError(moduleName, metricInc(db), flowLog)
			httpFunc(writer, request)
		}
	}
}

// openDecoratorGenerator panic装饰器,处理基础panic记录
func (httpService *Service) panicDecoratorGenerator() func(Handler) Handler {
	return func(httpFunc Handler) Handler {
		return func(writer http.ResponseWriter, request *http.Request) {
			flowLog := logging.NewEntry(map[string]interface{}{
				"module":  moduleName,
				"flow_id": common.GetFlow(request),
			})
			// panic处理
			defer func() {
				if p := recover(); p != nil {
					err := common.PanicCountInc()
					if err != nil {
						flowLog.Errorf("panic count inc failed,error:%s", err)
					}
					db := strings.TrimSpace(request.URL.Query().Get("db"))
					sql := strings.TrimSpace(request.URL.Query().Get("q"))
					flowLog.Errorf("get panic handle request,panic info:%v,panic db:%s,sql:%s", p, db, sql)
					// 打印堆栈
					var buf [4096]byte
					n := runtime.Stack(buf[:], false)
					flowLog.Errorf("panic stack ==> %s\n", buf[:n])
				}
			}()

			// 向下调用
			httpFunc(writer, request)
		}
	}
}

// availableDecoratorGenerator 检查服务是否可用
func (httpService *Service) availableDecoratorGenerator(metricInc FailedCountInc) func(Handler) Handler {
	return func(httpFunc Handler) Handler {
		return func(writer http.ResponseWriter, request *http.Request) {
			flowLog := logging.NewEntry(map[string]interface{}{
				"module":  moduleName,
				"flow_id": common.GetFlow(request),
			})
			db := strings.TrimSpace(request.URL.Query().Get("db"))
			if !httpService.available {
				flowLog.Errorf("get query request,but proxy not ready")
				metricError(moduleName, metricInc(db, strconv.Itoa(innerFail)), flowLog)
				// 返回信息
				httpService.writeBack(writer, fmt.Sprintf(errTemplate, "proxy not ready"), innerFail, flowLog)
				return
			}
			httpFunc(writer, request)
		}
	}
}

// authorizationDecoratorGenerator 检查认证信息
func (httpService *Service) authorizationDecoratorGenerator(metricInc FailedCountInc) func(Handler) Handler {
	return func(httpFunc Handler) Handler {
		return func(writer http.ResponseWriter, request *http.Request) {
			flowLog := logging.NewEntry(map[string]interface{}{
				"module":  moduleName,
				"flow_id": common.GetFlow(request),
			})
			db := strings.TrimSpace(request.URL.Query().Get("db"))
			if !httpService.auth.Check(request) {
				flowLog.Errorf("authenticate failed")
				metricError(moduleName, metricInc(db, strconv.Itoa(authFail)), flowLog)
				// 返回信息
				httpService.writeBack(writer, fmt.Sprintf(errTemplate, "authenticate failed"), authFail, flowLog)
				return
			}
			// 校验完毕后删除认证,因为之后连接backend要使用proxy提供的认证
			request.Header.Del("Authorization")
			httpFunc(writer, request)
		}
	}
}

// postOrGetCheckDecoratorGenerator 检查是否是POST GET方法
func (httpService *Service) postOrGetCheckDecoratorGenerator(metricInc FailedCountInc) func(Handler) Handler {
	return func(httpFunc Handler) Handler {
		return func(writer http.ResponseWriter, request *http.Request) {
			flowLog := logging.NewEntry(map[string]interface{}{
				"module":  moduleName,
				"flow_id": common.GetFlow(request),
			})
			db := strings.TrimSpace(request.URL.Query().Get("db"))
			if request.Method != PostMethod && request.Method != GetMethod {
				// 其余的方法都是不允许的
				metricError(moduleName, metricInc(db, strconv.Itoa(methodFail)), flowLog)
				// 返回信息
				httpService.writeBack(writer, fmt.Sprintf(errTemplate, "wrong method"), methodFail, flowLog)
				return
			}
			httpFunc(writer, request)
		}
	}
}

// postCheckDecoratorGenerator 检查是否是POST方法
func (httpService *Service) postCheckDecoratorGenerator(metricInc FailedCountInc) func(Handler) Handler {
	return func(httpFunc Handler) Handler {
		return func(writer http.ResponseWriter, request *http.Request) {
			flowLog := logging.NewEntry(map[string]interface{}{
				"module":  moduleName,
				"flow_id": common.GetFlow(request),
			})
			db := strings.TrimSpace(request.URL.Query().Get("db"))
			if request.Method != PostMethod {
				// 其余的方法都是不允许的
				metricError(moduleName, metricInc(db, strconv.Itoa(methodFail)), flowLog)
				// 返回信息
				httpService.writeBack(writer, fmt.Sprintf(errTemplate, "wrong method"), methodFail, flowLog)
				return
			}
			httpFunc(writer, request)
		}
	}
}

// getCheckDecoratorGenerator 检查是否是GET方法
func (httpService *Service) getCheckDecoratorGenerator(metricInc FailedCountInc) func(Handler) Handler {
	return func(httpFunc Handler) Handler {
		return func(writer http.ResponseWriter, request *http.Request) {
			flowLog := logging.NewEntry(map[string]interface{}{
				"module":  moduleName,
				"flow_id": common.GetFlow(request),
			})
			db := strings.TrimSpace(request.URL.Query().Get("db"))
			if request.Method != GetMethod {
				// 其余的方法都是不允许的
				metricError(moduleName, metricInc(db, strconv.Itoa(methodFail)), flowLog)
				// 返回信息
				httpService.writeBack(writer, fmt.Sprintf(errTemplate, "wrong method"), methodFail, flowLog)
				return
			}
			httpFunc(writer, request)
		}
	}
}

// getCheckDecoratorGenerator 检查是否是GET方法
func (httpService *Service) checkDBSingleWordDecoratorGenerator(metricInc FailedCountInc) func(Handler) Handler {
	return func(httpFunc Handler) Handler {
		return func(writer http.ResponseWriter, request *http.Request) {
			flowLog := logging.NewEntry(map[string]interface{}{
				"module":  moduleName,
				"flow_id": common.GetFlow(request),
			})
			db := strings.TrimSpace(request.URL.Query().Get("db"))
			if !CheckSingleWord(db) {
				flowLog.Errorf("get trouble when anaylizing db name,create statement will not start,db name:%s", db)
				metricError(moduleName, metricInc(db, strconv.Itoa(outerFail)), flowLog)
				// 返回信息
				httpService.writeBack(writer, fmt.Sprintf(errTemplate, "bad db name"), outerFail, flowLog)
				return

			}
			httpFunc(writer, request)
		}
	}
}
