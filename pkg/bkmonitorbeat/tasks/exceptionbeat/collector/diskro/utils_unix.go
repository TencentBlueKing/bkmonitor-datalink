// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package diskro

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type MountPointInfo struct {
	Device     string   `json:"device"`      // 设备名
	MountPoint string   `json:"mount_point"` // 挂载路径
	FileSystem string   `json:"file_system"` // 文件系统
	Options    []string `json:"options"`     // 挂载路径属性
}

func NewMountPointInfo(stat disk.PartitionStat) *MountPointInfo {
	return &MountPointInfo{
		Device:     stat.Device,
		MountPoint: stat.Mountpoint,
		FileSystem: stat.Fstype,
		Options:    stat.Opts,
	}
}

// NewBatchMountPointInfo  批量生成MountPoint信息
func NewBatchMountPointInfo(statList []disk.PartitionStat) []*MountPointInfo {
	var resultList = make([]*MountPointInfo, 0, len(statList))

	for _, stat := range statList {
		resultList = append(resultList, NewMountPointInfo(stat))
	}

	return resultList
}

// genUniqueKey  生成唯一 key，方便标识功能
func (mp *MountPointInfo) genUniqueKey() string {
	return strings.Join([]string{
		mp.Device, mp.FileSystem, mp.MountPoint, // 同一个文件系统的同一个设备挂载到同一个路径下，如果有差异则认为是存在了变化
	}, "-")
}

// getHistoryInfo 读取该路径的历史信息
func (mp *MountPointInfo) getHistoryInfo() *MountPointInfo {
	var (
		history     string
		err         error
		uniqueKey   = mp.genUniqueKey()
		historyInfo = &MountPointInfo{}
	)

	if history, err = storage.Get(uniqueKey); err != nil {
		// 有可能是找不到记录的问题，所以可以接受
		logger.Warnf("failed to get history by key->[%s] for err->[%s]", uniqueKey, err)
		return nil
	}

	if err = json.Unmarshal([]byte(history), historyInfo); err != nil {
		logger.Errorf("failed to restore data from history for err->[%s] by data->[%s]", err, history)
		return nil
	}

	logger.Debugf("restore success with data->[%#v]", historyInfo)
	return historyInfo
}

// setHistoryInfo  保存当前状态的信息。注意，由于我们只关注从RW到RO的状态变化，因此如果是RO状态的信息，没有必要存储
func (mp *MountPointInfo) setHistoryInfo() error {
	var (
		history   []byte
		err       error
		uniqueKey = mp.genUniqueKey()
	)

	// 如果是只读状态的，不需要进行保存
	if mp.IsReadOnly() {
		logger.Infof("read only: mount_point->[%s] which is read only status->[%#v]", mp.MountPoint, mp.Options)
		return nil
	}

	if history, err = json.Marshal(mp); err != nil {
		logger.Errorf("failed to marshal mount_point->[%s] info for->[%s]", mp.MountPoint, err)
		return err
	}
	logger.Debugf("mount_point->[%s] ready to save with data->[%s]", mp.MountPoint, history)

	if err = storage.Set(uniqueKey, string(history), 0); err != nil {
		logger.Errorf("failed to save mount_point->[%s] for err->[%s]", mp.MountPoint, err)
		return err
	}

	logger.Infof("mount_point->[%s] save success", mp.MountPoint)
	return nil
}

// IsReadOnlyStatusChange  返回当前的状态是否有从RW到RO的状态转变
func (mp *MountPointInfo) IsReadOnlyStatusChange() bool {
	var (
		historyMp *MountPointInfo
	)

	// 如果能拿到状态，表示之前这个挂载点是存在过RW的状态的。所以此时如果是RO的状态，那么可以直接判断有发生过变化
	if !mp.IsReadOnly() {
		logger.Infof("mount_point->[%s] is not readonly status now, nothing will check", mp.MountPoint)
		return false
	}

	// 如果找不到历史记录，表示：1. 初始化状态，不需要告警；2. 这个mountPoint历史上没有过RW状态，不用存储
	// 上述两个情况都表示mountPoint不可能存在exceptionbeat能感知的RW到RO转换，所以直接返回false
	if historyMp = mp.getHistoryInfo(); historyMp == nil {
		logger.Debugf("mount_point->[%s] has no history info, so not change status can data can detect", mp.MountPoint)
		return false
	}

	// 当前是RO状态，而且有历史状态，表示已经是找到了变化的情况，需要返回true
	logger.Debugf("mount_point->[%s] history data is found and now is readonly status now", mp.MountPoint)
	return true
}

// IsReadOnly 是否只读状态的
func (mp *MountPointInfo) IsReadOnly() bool {
	logger.Debugf("mount_point->[%s] will checkout all options->[%#v]", mp.MountPoint, mp.Options)
	// 遍历所有的状态是否存在只读标志位
	for _, status := range mp.Options {
		logger.Debugf("mount_point->[%s] going to check option->[%s]", mp.MountPoint, status)
		if status == "ro" {
			logger.Debugf("mount_point->[%s] readonly status bit found", mp.MountPoint)
			return true
		}
	}

	logger.Debugf("mount_point->[%s] no readonly bit is found", mp.MountPoint)
	return false
}

// SaveStatus 保存状态信息
func (mp *MountPointInfo) SaveStatus() error {
	return mp.setHistoryInfo()
}

// IsMatchRule 是否满足规则的匹配，用于黑白名单的判断使用
func (mp *MountPointInfo) IsMatchRule(rule []string) bool {
	var (
		preRuleRe *regexp.Regexp
		err       error
	)

	var filePathList = filepath.SplitList(mp.MountPoint)
	logger.Debugf("mount_point->[%s] is split to->[%#v]", mp.MountPoint, filePathList)

	for _, filePath := range filePathList {
		for _, preRule := range rule {
			if preRuleRe, err = regexp.Compile(preRule); err != nil {
				logger.Errorf("failed to compile re for rule->[%s] for err->[%s]", preRule, err)
				continue
			}

			if preRuleRe.Match([]byte(filePath)) {
				logger.Infof("file_path->[%s] match rule->[%s] will return true", filePath, preRule)
				return true
			}
		}
	}

	logger.Infof("mount_point->[%s] match none rule->[%#v]", mp.MountPoint, rule)
	return false
}
