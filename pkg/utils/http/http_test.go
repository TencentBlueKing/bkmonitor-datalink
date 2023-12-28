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
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHttpRequest(t *testing.T) {
	// 启动一个模拟的 HTTP 服务器
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello go")
	})

	// 创建http.Server
	server := &http.Server{
		Addr:    ":9999",
		Handler: mux,
	}

	// 开启一个goroutine 来监听和提供HTTP服务
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Errorf("ListenAndServe error: %v", err)
			return
		}
		t.Logf("Server has started")
	}()
	defer func() {
		// 优雅地关闭服务器，等待活动的请求完成，并设置超时
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(timeoutCtx); err != nil {
			t.Fatalf("ServerShutdown error, %v", err)
			return
		}
		t.Log("Server has exited")
	}()

	client := NetHttpClient{}
	// Request 测试
	opt := Options{
		BaseUrl: "http://127.0.0.1:9999",
	}
	response, err := client.Request(context.Background(), "GET", opt)
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	assert.Equal(t, "hello go", string(body))
	t.Logf("Request Body: %v+", string(body))

	// Get 测试
	params := map[string][]string{
		"aa": []string{"aa01", "aa02"},
	}
	response, err = client.Get(context.Background(), opt.BaseUrl, params, Options{})
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	body, _ = io.ReadAll(response.Body)
	assert.Equal(t, "hello go", string(body))
	t.Logf("Get Body: %v+", string(body))

	// Post 测试
	reqBody := []byte("{\"aa\": \"aa01\"}")
	response, err = client.Post(context.Background(), opt.BaseUrl, reqBody, "", Options{})
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	body, _ = io.ReadAll(response.Body)
	assert.Equal(t, "hello go", string(body))
	t.Logf("Post Body: %v+", string(body))
}
