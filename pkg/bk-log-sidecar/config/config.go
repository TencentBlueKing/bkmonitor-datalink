// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

// Package config basic config
package config

import "flag"

var (
	DockerSocket     string
	DockerApiVersion string

	ContainerHostPath string
	WindowsReloadPath string

	ContainerdNamespace string
	ContainerdAddress   string
	ContainerdStatePath string

	BkunifylogbeatConfig  string
	BkunifylogbeatPidFile string
	HostPath              string
	DelayCleanConfig      int
	BkEnv                 string
	HttpProf              string
)

// FlagInit init flag
func FlagInit() {
	flag.StringVar(&ContainerdNamespace, "containerd-namespace", "k8s.io", "namespace of containerd")
	flag.StringVar(&ContainerdAddress, "containerd-address", "/run/containerd/containerd.sock", "address of containerd")
	flag.StringVar(&ContainerdStatePath, "containerd-state-path", "/run/containerd", "state directory for containerd")
	flag.StringVar(&DockerSocket, "docker-socket", "unix:///var/run/docker.sock", "docker socket file")
	flag.StringVar(&ContainerHostPath, "container-host-path", "/", "container host path")
	flag.StringVar(&WindowsReloadPath, "windows-reload-path", "/windows-reload-path", "windows reload signal path")
	flag.StringVar(&BkunifylogbeatConfig, "bkunifylogbeat-config", "", "bkunifylogbeat config path")
	flag.StringVar(&BkunifylogbeatPidFile, "bkunifylogbeat-pid-file", "", "bkunifylogbeat pid file")
	flag.StringVar(&HostPath, "host-path", "/", "host path")
	flag.StringVar(&DockerApiVersion, "docker-api-version", "1.40", "docker Api version")
	flag.IntVar(&DelayCleanConfig, "delay-clean-config", 30, "delay cleaning")
	flag.StringVar(&BkEnv, "bk-env", "", "bk env label value")
	flag.StringVar(&HttpProf, "httpprof", "127.0.0.1:16060", "http pprof address")
}
