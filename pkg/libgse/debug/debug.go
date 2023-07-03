// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package debug

import (
	"net/http"
	"strings"
)

func cmdsListHandler(w http.ResponseWriter, r *http.Request) {
	successResponse(w, m{
		"cmds": []m{
			{"path": "/cmds", "desc": "所有可用命令"},
			{"path": "/version", "desc": "获取版本"},
			{"path": "/cmds/loglevel", "desc": "日志级别, 使用GET获取当前级别，PUT value=debug|info|warning|error|critical设置当前级别"},
			{"path": "/debug/pprof/[name]", "desc": "下载pprof文件，可选类型: " + strings.Join(getPprofNames(), ", ")},
		},
	})
}

func newVersionHandlerFunc(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		successResponse(w, m{
			"version": version,
		})
	}
}

func newCmdsHandler(prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cmd := strings.TrimPrefix(r.URL.Path, prefix)
		switch cmd {
		case "loglevel":
			logLevelHandlerFunc(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// NewDebugHandler returns a new http.Handler that handles debug requests.
// example:
//
//	go func() {
//		log.Println(http.ListenAndServe("localhost:6060", debug.NewDebugHandler("v1.hello")))
//	}()
//
// GET /cmds to get all available commands
func NewDebugHandler(version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/cmds", cmdsListHandler)
	mux.HandleFunc("/version", newVersionHandlerFunc(version))
	mux.Handle("/cmds/", newCmdsHandler("/cmds/"))
	mux.HandleFunc("/debug/pprof/", pprofDownloadHandleFunc)
	return mux
}
