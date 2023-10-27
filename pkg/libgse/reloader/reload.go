// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package reloader

import (
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/paths"
)

// ReloadIf
type ReloadIf interface {
	Reload(*common.Config)
}

// Reloader is used to register and reload modules
type Reloader struct {
	handler ReloadIf
	name    string
	done    chan struct{}
	fd      interface{}
}

type ReloaderOpt func(*Reloader)

func WithReloaderSig2() ReloaderOpt {
	return func(r *Reloader) {
		r.fd = sigReload2
	}
}

// NewReloader creates new Reloader instance for the given config with SIGUSR1
func NewReloader(name string, handler ReloadIf, opts ...ReloaderOpt) *Reloader {
	r := &Reloader{
		handler: handler,
		name:    name,
		done:    make(chan struct{}),
		fd:      sigReload,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// PathConfig struct contains the basic path configuration of every beat
type PathConfig struct {
	Path paths.Path `config:"path"`
}
