// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package file

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
)

// State is used to communicate the reading state of a file
type State struct {
	Id        string        `json:"-"` // local unique id to make comparison more efficient
	Finished  bool          `json:"-"` // harvester state
	FileInfo  os.FileInfo   `json:"-"` // the file info
	INode     uint64        `json:"-"` // the file inode
	Source    string        `json:"source"`
	Offset    int64         `json:"offset"`
	Timestamp time.Time     `json:"timestamp"`
	TTL       time.Duration `json:"ttl"`
	Inactive  int64         `json:"inactive"` // record time, how long time has no write(unit: minutes)
	Type      string        `json:"type"`
}

// NewState creates a new file state
func NewState(fileInfo os.FileInfo, path string, t string) State {
	return State{
		FileInfo:  fileInfo,
		Source:    path,
		Finished:  false,
		Timestamp: time.Now(),
		TTL:       -1, // By default, state does have an infinite ttl
		Inactive:  0,
		Type:      t,
	}
}

var fileID uint64

type File struct {
	State State
	ID    uint64   // uuid
	Tasks sync.Map // map[taskid]*keyword.TaskConfig

	IsDeleted     bool // 标志位，记录文件是否已被删除，用来清理任务
	IsInactivated bool // 标志位，记录文件是否由于长期未写入数据，进入未激活状态，用来清理任务
}

func NewFile(filepath string, info os.FileInfo, inode uint64) (*File, error) {
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not regular file", filepath)
	}
	state := NewState(info, filepath, "")
	state.INode = inode
	return &File{
		State:         state,
		ID:            atomic.AddUint64(&fileID, 1),
		IsDeleted:     false,
		IsInactivated: false,
	}, nil
}

func (f *File) AddTask(t *keyword.TaskConfig) {
	f.Tasks.Store(t.TaskID, t)
}

func (f *File) RmTask(taskID string) {
	f.Tasks.Delete(taskID)
}

// IsSameFile Checks if the two files are the same.
func (f *File) IsSameFile(f2 *File) bool {
	return os.SameFile(f.State.FileInfo, f2.State.FileInfo)
}
