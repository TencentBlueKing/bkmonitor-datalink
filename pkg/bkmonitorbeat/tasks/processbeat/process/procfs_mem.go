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
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/elastic/gosigar"
)

func (r *procFSReader) readMem(pid int32, pageSize uint64) (gosigar.ProcMem, error) {
	contents, err := r.readFile(filepath.Join(r.procDir(pid), "statm"))
	if err != nil {
		return gosigar.ProcMem{}, err
	}
	return parseProcStatmMem(contents, pageSize)
}

func parseProcStatmMem(contents []byte, pageSize uint64) (gosigar.ProcMem, error) {
	var ret gosigar.ProcMem
	fields := strings.Fields(string(contents))
	if len(fields) < 3 {
		return ret, fmt.Errorf("invalid proc statm: %q", string(contents))
	}

	size, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return ret, err
	}
	rss, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return ret, err
	}
	share, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return ret, err
	}

	ret.Size = size * pageSize
	ret.Resident = rss * pageSize
	ret.Share = share * pageSize
	return ret, nil
}
