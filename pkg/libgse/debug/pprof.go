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
	httpPprof "net/http/pprof"
	"runtime/pprof"
	"strings"
)

func getPprofNames() []string {
	pprofNames := []string{"cmdline", "profile", "trace"}
	for _, profile := range pprof.Profiles() {
		pprofNames = append(pprofNames, profile.Name())
	}
	return pprofNames
}

func pprofDownloadHandleFunc(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/debug/pprof/")
	if name == "" {
		http.Error(w, "pprof name is empty", http.StatusBadRequest)
		return
	}
	switch name {
	case "cmdline":
		httpPprof.Cmdline(w, r)
	case "profile":
		httpPprof.Profile(w, r)
	case "trace":
		httpPprof.Trace(w, r)
	case "symbol":
		httpPprof.Symbol(w, r)
	default:
		httpPprof.Handler(name).ServeHTTP(w, r)
	}
}
