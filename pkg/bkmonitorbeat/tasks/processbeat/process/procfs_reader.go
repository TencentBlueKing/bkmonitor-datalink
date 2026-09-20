// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package process

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

const uidNameCacheTTL = 10 * time.Minute

// procFSReader avoids gopsutil's repeated /proc/<pid>/stat and boot time reads during full host scans.
type procFSReader struct {
	root string

	bootTime func() (uint64, error)
	readFile func(string) ([]byte, error)
	readLink func(string) (string, error)

	lookupUsername func(string) (string, error)
	now            func() time.Time

	mut        sync.Mutex
	boot       uint64
	bootLoaded bool
	uidNames   map[string]uidNameCacheEntry
}

type uidNameCacheEntry struct {
	name     string
	expireAt time.Time
}

func newProcFSReader(root string) *procFSReader {
	if root == "" {
		root = "/proc"
	}
	r := &procFSReader{
		root:           root,
		readFile:       os.ReadFile,
		readLink:       os.Readlink,
		lookupUsername: lookupUsername,
		now:            time.Now,
		uidNames:       map[string]uidNameCacheEntry{},
	}
	r.bootTime = r.readBootTime
	return r
}

func (r *procFSReader) readMeta(pid int32) (define.ProcStat, error) {
	var stat define.ProcStat
	procDir := r.procDir(pid)
	statBytes, err := r.readFile(filepath.Join(procDir, "stat"))
	if err != nil {
		return stat, err
	}
	parsed, err := parseProcStat(statBytes)
	if err != nil {
		return stat, err
	}

	boot, err := r.cachedBootTime()
	if err != nil {
		return stat, err
	}
	statusName, uid := r.statusIdentity(procDir)
	cmd, cmdSlice := r.cmdline(procDir)
	name := parsed.name
	if statusName != "" {
		name = statusName
	}

	stat.Pid = pid
	stat.PPid = parsed.ppid
	stat.Name = completeProcName(name, cmdSlice)
	stat.Status = procStateName(parsed.state)
	stat.Created = int64(boot*1000 + parsed.startTimeTicks*1000/cachedClockTicks())
	stat.Username = r.username(uid)
	stat.Cmd, stat.CmdSlice = cmd, cmdSlice
	stat.Exe, _ = r.readLink(filepath.Join(procDir, "exe"))
	stat.Cwd, _ = r.readLink(filepath.Join(procDir, "cwd"))
	return stat, nil
}

func (r *procFSReader) procDir(pid int32) string {
	return filepath.Join(r.root, strconv.Itoa(int(pid)))
}

func (r *procFSReader) cachedBootTime() (uint64, error) {
	r.mut.Lock()
	defer r.mut.Unlock()

	if r.bootLoaded {
		return r.boot, nil
	}
	boot, err := r.bootTime()
	if err != nil {
		return 0, err
	}
	r.boot = boot
	r.bootLoaded = true
	return boot, nil
}

func (r *procFSReader) readBootTime() (uint64, error) {
	statBytes, err := r.readFile(filepath.Join(r.root, "stat"))
	if err != nil {
		return 0, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(statBytes))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "btime ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return 0, fmt.Errorf("wrong btime format")
		}
		return strconv.ParseUint(fields[1], 10, 64)
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("could not find btime")
}

func (r *procFSReader) username(uid string) string {
	if uid == "" {
		return ""
	}

	now := r.now()
	r.mut.Lock()
	if entry, ok := r.uidNames[uid]; ok && now.Before(entry.expireAt) {
		r.mut.Unlock()
		return entry.name
	}
	r.mut.Unlock()

	name, err := r.lookupUsername(uid)
	if err != nil || name == "" {
		name = uid
	}

	r.mut.Lock()
	r.uidNames[uid] = uidNameCacheEntry{
		name:     name,
		expireAt: now.Add(uidNameCacheTTL),
	}
	r.mut.Unlock()
	return name
}

func (r *procFSReader) statusIdentity(procDir string) (name, uid string) {
	statusBytes, err := r.readFile(filepath.Join(procDir, "status"))
	if err != nil {
		return "", ""
	}
	scanner := bufio.NewScanner(bytes.NewReader(statusBytes))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "Name:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		case strings.HasPrefix(line, "Uid:"):
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				uid = fields[1]
			}
		}
		if name == "" || uid == "" {
			continue
		}
		return name, uid
	}
	return name, uid
}

func (r *procFSReader) cmdline(procDir string) (string, []string) {
	cmdBytes, err := r.readFile(filepath.Join(procDir, "cmdline"))
	if err != nil || len(cmdBytes) == 0 {
		return "", nil
	}
	cmdBytes = bytes.TrimRight(cmdBytes, "\x00")
	if len(cmdBytes) == 0 {
		return "", nil
	}
	parts := strings.Split(string(cmdBytes), "\x00")
	return strings.Join(parts, " "), parts
}

func completeProcName(name string, cmdSlice []string) string {
	if len(name) < 15 || len(cmdSlice) == 0 {
		return name
	}
	extendedName := path.Base(cmdSlice[0])
	if strings.HasPrefix(extendedName, name) {
		return extendedName
	}
	return name
}

type procStatFields struct {
	name           string
	state          string
	ppid           int32
	startTimeTicks uint64
}

func parseProcStat(stat []byte) (procStatFields, error) {
	var ret procStatFields
	line := strings.TrimSpace(string(stat))
	left := strings.IndexByte(line, '(')
	right := strings.LastIndexByte(line, ')')
	if left < 0 || right <= left {
		return ret, fmt.Errorf("invalid proc stat line")
	}
	ret.name = line[left+1 : right]
	fields := strings.Fields(line[right+1:])
	if len(fields) < 20 {
		return ret, fmt.Errorf("insufficient proc stat fields")
	}
	ret.state = fields[0]
	ppid, err := strconv.ParseInt(fields[1], 10, 32)
	if err != nil {
		return ret, err
	}
	ret.ppid = int32(ppid)
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return ret, err
	}
	ret.startTimeTicks = startTime
	return ret, nil
}

func lookupUsername(uid string) (string, error) {
	u, err := user.LookupId(uid)
	if err != nil {
		return "", err
	}
	return u.Username, nil
}
