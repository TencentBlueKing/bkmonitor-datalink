// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package common

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
)

const (
	IegDockerFile = "/etc/ieg-docker.conf"
)

func IsDockerEnv() bool {
	if _, err := os.Stat(IegDockerFile); err == nil {
		return true
	}

	return isInDocker()
}

// 当且仅当 /.dockerenv 的前提下 文件存在且 cgroup 权限为 ro 或者 /proc/1/sched 进程号不为 1
func isInDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return ifCgroupReadonly() || ifSchedProc()
	}

	return false
}

type mount struct {
	Device     string
	Path       string
	Filesystem string
	Flags      string
}

// $ cat /proc/self/mounts
// ...
// cgroup /sys/fs/cgroup/systemd cgroup rw,nosuid,nodev,noexec,relatime,xattr,release_agent=/usr/lib/systemd/systemd-cgroups-agent,name=systemd 0 0
// pstore /sys/fs/pstore pstore rw,nosuid,nodev,noexec,relatime 0 0
// cgroup /sys/fs/cgroup/memory cgroup rw,nosuid,nodev,noexec,relatime,memory 0 0
// cgroup /sys/fs/cgroup/perf_event cgroup rw,nosuid,nodev,noexec,relatime,perf_event 0 0
// cgroup /sys/fs/cgroup/devices cgroup rw,nosuid,nodev,noexec,relatime,devices 0 0
// cgroup /sys/fs/cgroup/freezer cgroup rw,nosuid,nodev,noexec,relatime,freezer 0 0
// cgroup /sys/fs/cgroup/blkio cgroup rw,nosuid,nodev,noexec,relatime,blkio 0 0
// cgroup /sys/fs/cgroup/cpu,cpuacct cgroup rw,nosuid,nodev,noexec,relatime,cpuacct,cpu 0 0
// cgroup /sys/fs/cgroup/cpuset cgroup rw,nosuid,nodev,noexec,relatime,cpuset 0 0
// cgroup /sys/fs/cgroup/net_cls,net_prio cgroup rw,nosuid,nodev,noexec,relatime,net_prio,net_cls 0 0
// cgroup /sys/fs/cgroup/hugetlb cgroup rw,nosuid,nodev,noexec,relatime,hugetlb 0 0
// cgroup /sys/fs/cgroup/pids cgroup rw,nosuid,nodev,noexec,relatime,pids 0 0
// ...
func ifCgroupReadonly() bool {
	bs, err := ioutil.ReadFile("/proc/self/mounts")
	if err != nil {
		return false
	}

	scanner := bufio.NewScanner(bytes.NewReader(bs))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), " ", 5)
		if len(parts) != 5 {
			continue
		}

		m := mount{parts[0], parts[1], parts[2], parts[3]}
		if m.Device == "cgroup" && isContainRo(m.Flags) {
			return true
		}
	}

	return false
}

func isContainRo(s string) bool {
	for _, f := range strings.Split(s, ",") {
		if f == "ro" {
			return true
		}
	}

	return false
}

// $ cat /proc/1/sched
// systemd (963838, #threads: 1)
// ---------------------------------------------------------
// se.exec_start                      :    8479909718.879537
// se.vruntime                        :          2223.728642
// se.sum_exec_runtime                :          1381.767962
// nr_switches                        :                10278
// nr_voluntary_switches              :                 8845
// nr_involuntary_switches            :                 1433
// se.load.weight                     :                 1024
// policy                             :                    0
// prio                               :                  120
// clock-delta                        :                   79
// ...
func ifSchedProc() bool {
	bs, err := ioutil.ReadFile("/proc/1/sched")
	if err != nil {
		return false
	}

	var line string
	scanner := bufio.NewScanner(bytes.NewReader(bs))
	// 只读取第一行数据
	for scanner.Scan() {
		line = scanner.Text()
		break
	}

	if line == "" {
		return false
	}

	split := strings.SplitN(line, " ", 2)
	if len(split) != 2 {
		return false
	}

	s := split[1]
	l := strings.Index(s, "(")
	r := strings.Index(s, ",")
	if l+1 >= r {
		return false
	}

	i, err := strconv.Atoi(s[l+1 : r])
	if err != nil {
		return false
	}

	// 进程号不为 1 则代表是在容器环境内
	if i != 1 {
		return true
	}

	return false
}
