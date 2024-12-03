// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package input

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/input/file"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	CounterRead         = uint64(0)  // 计数器，读的行数
	CounterLastOpenFile atomic.Value // 最后一次打开的文件
	ScanTickerDuration  = 5 * time.Second
)

const (
	FilePositionHead = int64(0)
	FilePositionEnd  = int64(-1)
)

var SingleInstance *Input // Input模块

// Input task data
type Input struct {
	cfg        map[string]*keyword.TaskConfig
	ctx        context.Context
	outputs    []chan<- interface{}
	wg         sync.WaitGroup
	lastStates []file.State

	WatchFiles sync.Map // 所有打开的文件 <filename, *FileWatcher>
	inodeMap   sync.Map // 所有监听文件 <inode, filename>
}

// New construct a new input
func New(ctx context.Context, conf map[string]*keyword.TaskConfig, states []file.State) (*Input, error) {
	input := &Input{
		cfg:        conf,
		ctx:        ctx,
		lastStates: states,
	}

	return input, nil
}

func (client *Input) Start() error {
	logger.Infof("Starting input, %s", client.ID())
	client.wg.Add(1)
	defer client.wg.Done()

	// continue last states
	if len(client.lastStates) > 0 {
		client.continueLastState()
	}

	// 在Start & Reload场景下，主动触发一次扫描
	watchFiles := client.scanTasks()
	client.refreshWatchFileAndTasks(watchFiles, true, true)

	go func() {
		tt := time.NewTicker(ScanTickerDuration)
		defer tt.Stop()
		for {
			select {
			// 定期扫描目录，查看是否有新的目录
			case <-tt.C:
				logger.Infof("len of tasks(%d)", len(client.cfg))
				newWatchFiles := client.scanTasks()
				logger.Infof("scan files(%d)", len(newWatchFiles))
				client.refreshWatchFileAndTasks(newWatchFiles, false, false)

				// 检查fw.File.IsInactivated状态的文件的修改时间，
				// 如果文件修改时间小于Inactive后，需要重新加入Tail，
				// 并将IsInactivated设置为false
				lenOfWatchingFiles := 0
				inactiveFiles := 0
				reactiveFiles := 0
				client.WatchFiles.Range(func(key, value interface{}) bool {
					filename := key.(string)
					fw := value.(*FileWatcher)
					if fw.File.IsInactivated {
						inactiveFiles++
						fileInfo := fw.File.State.FileInfo

						mtime := fileInfo.ModTime()
						if time.Now().Sub(mtime) < fw.File.State.TTL {
							// 文件修改时间，小于设定的超时不监听时间。 这时需要重新开始监听tail
							logger.Infof("file=>(%s) mod time has change, start tail again.", filename)
							err := fw.Restart()
							if err != nil {
								logger.Errorf("start tail file=>(%s) again error=>%v", filename, err)
							} else {
								fw.File.IsInactivated = false
								reactiveFiles++
							}
						}
					} else {
						lenOfWatchingFiles++
					}
					return true
				})
				logger.Infof(
					"len of Watching Files(%d) inactive(%d) reactiveFiles(%d)",
					lenOfWatchingFiles, inactiveFiles, reactiveFiles,
				)
			case <-client.ctx.Done():
				return
			}
		}
	}()
	return nil
}

// Stop stops the input and with it all harvesters
func (client *Input) Stop() {
	// close read files
	client.WatchFiles.Range(func(_, fw interface{}) bool {
		close(fw.(*FileWatcher).quit)
		return true
	})
}

// Wait wg.wait for task
func (client *Input) Wait() {
	client.wg.Wait()
}

// Reload reload task
func (client *Input) Reload(_ interface{}) {
	// 在Start & Reload场景下，主动触发一次扫描
	watchFiles := client.scanTasks()
	client.refreshWatchFileAndTasks(watchFiles, true, false)

	logger.Info("[Reload] Input module reload success.")
}

func (client *Input) ID() string {
	return "input-0"
}

func (client *Input) AddOutput(_ chan<- interface{}) {}

func (client *Input) AddInput(_ <-chan interface{}) {}

func (client *Input) continueLastState() {
	// continue read last files
	for _, state := range client.lastStates {
		filename := state.Source
		extraInfo := client.getTaskListByFile(filename, state.FileInfo)
		err := client.watchFile(filename, state.Offset, extraInfo, false)
		if err != nil {
			logger.Errorf("continue last state, add filename=>(%s) to watch error=>(%v)", filename, err)
		}
		logger.Infof("continue last state, add filename=>(%s) to watch success.", filename)
	}
}

// watchFile start to collect file from pos offset
func (client *Input) watchFile(filename string, pos int64, extraInfo fileExtraInfo, isFirstScan bool) error {
	logger.Infof("watch file %s, at %d", filename, pos)

	ff, err := file.NewFile(filename, extraInfo.info, extraInfo.inode)
	if err != nil {
		return fmt.Errorf("new file '%s' error, %v", filename, err)
	}

	if len(extraInfo.taskConfigs) == 0 {
		return fmt.Errorf("no task in tasklist, filename(%s) will not add to tail", filename)
	}
	// set ttl
	var retainFileBytes int64
	for _, task := range extraInfo.taskConfigs {
		ff.AddTask(task)
		if ff.State.TTL < task.Input.CloseInactive {
			ff.State.TTL = task.Input.CloseInactive
		}
		// 启动时首次扫描不前置读取位置
		if !isFirstScan {
			retainFileBytes = task.RawText.RetainFileBytes
		}
	}
	logger.Infof("retainFileBytes: %d", retainFileBytes)

	// check file mtime
	mtime := ff.State.FileInfo.ModTime()
	age := time.Now().Sub(mtime)
	ff.State.Inactive = int64(age.Minutes())
	// 旧文件不读取
	if ff.State.TTL > 0 && age > ff.State.TTL {
		ff.IsInactivated = true
		logger.Infof("file=>(%s) has no write for long time(%d)", filename, ff.State.TTL)
	}

	// if pos is negtive, tail file from end
	fileSize := ff.State.FileInfo.Size()
	if pos == FilePositionHead && age.Minutes() > 1 {
		// If you need read from the beginning but the file is not modified within the last minute,
		// then modify it to read from the end
		logger.Infof("file=>(%s) has no write in the last 1 minute, tail from end(%s).", filename, fileSize)
		pos = fileSize
	}

	// FilePositionEnd(-1) will be marked as fileSize
	if pos < 0 {
		delta := fileSize - retainFileBytes
		// retain prefix file content
		logger.Infof("pos before handle retainFileBytes: %v", pos)
		if delta < 0 {
			pos = 0
		} else {
			pos = delta
		}
		logger.Infof("pos after handle retainFileBytes: %v", pos)
	}

	// if file is truncated, read from head
	if fileSize < pos {
		logger.Infof("file %s is truncated, read from head", filename)
		pos = 0
	}

	ff.State.Offset = pos

	return client.startTailFile(ff)
}

// startTailFile start to collect file
func (client *Input) startTailFile(f *file.File) error {
	filename := f.State.Source
	fw, err := NewFileWatcher(f)
	if err != nil {
		logger.Errorf("new file watcher error， filename=>%s, error=>%v", filename, err)
		return err
	}

	client.WatchFiles.Store(filename, fw)

	CounterLastOpenFile.Store(filename)

	// 未活跃的文件无需启动监听
	if !fw.File.IsInactivated {
		// start reader
		go fw.Start()
	}

	return nil
}

// isFileExcluded checks if the given path should be excluded
func (client *Input) isFileExcluded(filename string, regs []*regexp.Regexp) bool {
	for _, exclude := range regs {
		ok := exclude.MatchString(filename)
		if ok {
			return true
		}
	}
	return false
}

type fileExtraInfo struct {
	info        os.FileInfo
	inode       uint64
	taskConfigs []*keyword.TaskConfig
}

// scanTasks
// 扫描任务，根据当前任务配置，扫描目录以及文件。获取到待监听目录、以及待监听文件与任务之间的映射关系
func (client *Input) scanTasks() map[string]fileExtraInfo {
	logger.Info("scan tasks")
	watchFiles := make(map[string]fileExtraInfo)
	scanFilesCount := 0
	for _, task := range client.cfg {
		taskWatchFilesMap := make(map[string]bool)
		for _, path := range task.Input.Paths {
			fileInfoMap := client.scanPathByPattern(path, task.Input.ExcludeFiles, task.Processer.ScanSleep)
			if fileInfoMap == nil {
				continue
			}
			scanFilesCount += len(fileInfoMap)
			for filename, info := range fileInfoMap {
				// add new files
				extraInfo, exists := watchFiles[filename]
				if exists {
					extraInfo.taskConfigs = append(extraInfo.taskConfigs, task)
				} else {
					extraInfo = fileExtraInfo{
						info:        info,
						taskConfigs: []*keyword.TaskConfig{task},
					}
				}
				watchFiles[filename] = extraInfo
				taskWatchFilesMap[filename] = true
			}
			// 防止文件夹数量多时CPU占用过高
			time.Sleep(task.Processer.ScanSleep)
		}
	}

	return watchFiles
}

// scanPathByPattern
// pathPattern: c:\\xxx*\\*.log  dirPatten: c:\\xxx*   basePatten: *.log
func (client *Input) scanPathByPattern(
	pathPattern string, excludeFiles []*regexp.Regexp, scanSleep time.Duration,
) map[string]os.FileInfo {
	dirPatten := filepath.Dir(pathPattern)
	basePatten := filepath.Base(pathPattern)

	logger.Infof("scan dirPattern=>(%s)", dirPatten)
	dirs, err := filepath.Glob(dirPatten)
	if err != nil {
		logger.Errorf("glob [%s] err, %v", dirPatten, err)
		return nil
	}
	logger.Infof("got dirs=>(%v)", dirs)

	fileInfoMap := make(map[string]os.FileInfo)
	// 启动的时候, 每个任务的采集路径都需要扫描
	for _, dirname := range dirs {
		newFileInfoMap := client.scanFileInfoMapByPattern(dirname, basePatten, excludeFiles, scanSleep)
		if newFileInfoMap != nil {
			for path, info := range newFileInfoMap {
				fileInfoMap[path] = info
			}
		}
	}
	return fileInfoMap
}

func readLinkFileInfo(path string) (string, os.FileInfo, error) {
	targetPath, err := os.Readlink(path)
	if err != nil {
		return "", nil, fmt.Errorf("fail to read link %s %v", path, err)
	}
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(filepath.Dir(path), targetPath)
	}
	info, err := os.Stat(targetPath)
	if err != nil {
		return "", nil, fmt.Errorf("fail to get file info %s %v", targetPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return readLinkFileInfo(targetPath)
	}
	return targetPath, info, nil
}

func (client *Input) scanFileInfoMapByPattern(
	dirname, basePatten string, excludeFiles []*regexp.Regexp, scanSleep time.Duration,
) map[string]os.FileInfo {
	logger.Infof("scan info dirname=>(%s) basePatten=>(%s)", dirname, basePatten)
	// open files in dir
	filePattern := filepath.Join(dirname, basePatten)
	files, err := filepath.Glob(filePattern)
	if err != nil {
		logger.Errorf("glob filePattern=>[%s] with err=>[%v]", filePattern, err)
		return nil
	}
	fileInfoMap := make(map[string]struct{})
	for _, f := range files {
		fileInfoMap[f] = struct{}{}
	}
	infoMap := make(map[string]os.FileInfo)
	_ = filepath.WalkDir(dirname, func(path string, d fs.DirEntry, err error) error {
		// 防止文件数量多时CPU占用过高
		time.Sleep(scanSleep)
		if err != nil {
			logger.Errorf("walkdir failed %s %v", path, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if _, ok := fileInfoMap[path]; ok {
			if client.isFileExcluded(path, excludeFiles) {
				logger.Infof("exclude file %s", path)
				return nil
			}
			if _, exists := infoMap[path]; exists {
				// 已读取过的无需重复读取
				return nil
			}
			var info os.FileInfo
			var err0 error
			// 软链需要读取指向文件信息
			if d.Type()&os.ModeSymlink != 0 {
				var targetPath string
				targetPath, info, err0 = readLinkFileInfo(path)
				if err0 != nil {
					logger.Errorf("readLinkFileInfo failed %s %v", path, err0)
					return nil
				}
				path = targetPath
			} else {
				info, err0 = d.Info()
				if err0 != nil {
					logger.Errorf("fail to get file info %s %v", path, err0)
					return nil
				}
			}
			infoMap[path] = info
		}
		return nil
	})
	return infoMap
}

func (client *Input) scanFileByPattern(filePattern string, excludeFiles []*regexp.Regexp) []string {
	logger.Infof("scan filePattern=>(%s)", filePattern)
	// open files in dir
	files, err := filepath.Glob(filePattern)
	if err != nil {
		logger.Errorf("glob filePattern=>[%s] with err=>[%v]", filePattern, err)
		return nil
	}
	logger.Infof("got files=>(%v)", files)
	fileList := make([]string, 0)
	for _, filename := range files {
		if client.isFileExcluded(filename, excludeFiles) {
			logger.Infof("exclude file %s", filename)
			continue
		}
		fileList = append(fileList, filename)
	}
	return fileList
}

// refreshWatchFileAndTasks
// 全量更新文件到任务的对应关系，触发时机在Start或者Reload(isTaskChange=true)，或者定时任务(isTaskChange=false)
func (client *Input) refreshWatchFileAndTasks(newWatchFiles map[string]fileExtraInfo, isTaskChange, isFirstScan bool) {
	for filename, extraInfo := range newWatchFiles {
		fw, ok := client.WatchFiles.Load(filename)
		if ok {
			// if has already tail, update taskList
			// 这里没有对去掉的task做清理，是因为task的去除是由task.Ctx.Done信号控制
			fileWatch := fw.(*FileWatcher)
			if !isTaskChange {
				// 如果task没有变更，则不进行任务列表的更新。加快逻辑
				continue
			}
			for _, task := range extraInfo.taskConfigs {
				_, exists := fileWatch.File.Tasks.Load(task.TaskID)
				if !exists {
					fileWatch.File.AddTask(task)
					if fileWatch.File.State.TTL < task.Input.CloseInactive {
						fileWatch.File.State.TTL = task.Input.CloseInactive
					}
				}
			}
		} else {
			// add new watch
			position := FilePositionEnd
			if !isTaskChange {
				// 如果通过定时任务发现的文件，那么需要从头开始
				position = FilePositionHead
			}
			inode, err := getFileInode(filename, extraInfo.info)
			if err == nil {
				fileWatch, exists := client.getExistsWatchFileByInode(inode)
				if exists {
					// 已存在相同inode的文件，按照存在文件的读取进度读取
					position = fileWatch.File.State.Offset
				}
				client.inodeMap.Store(inode, filename)
				extraInfo.inode = inode
			}
			err = client.watchFile(filename, position, extraInfo, isFirstScan)
			if err != nil {
				logger.Errorf("add filename=>(%s) to watch error=>(%v)", filename, err)
				continue
			}

			logger.Infof("add filename=>(%s) to watch success", filename)
		}
		// 文件存在多个任务中时取最大休眠时间
		var maxScanSleep time.Duration
		for _, task := range extraInfo.taskConfigs {
			if task.Processer.ScanSleep > maxScanSleep {
				maxScanSleep = task.Processer.ScanSleep
			}
		}
		if maxScanSleep > 0 {
			// 防止文件数量多时CPU占用过高
			time.Sleep(maxScanSleep)
		}
	}

	client.removeDeletedFiles(newWatchFiles)
}

// removeDeletedFiles
// 移除掉已经被删除的文件
func (client *Input) removeDeletedFiles(newWatchFiles map[string]fileExtraInfo) {
	reAddToWatchFiles := make(map[string]fileExtraInfo)
	client.WatchFiles.Range(func(key, value interface{}) bool {
		filename := key.(string)
		fw := value.(*FileWatcher)

		logger.Infof("current watch file(%s), stat is %+v", filename, fw.File.State)

		// 老的文件在新的文件列表不存在，则去除对该文件的watch，以及tail
		extraInfo, exists := newWatchFiles[filename]
		if !exists {
			logger.Warnf("file(%s) not exists, remove in watch files.", filename)
			client.WatchFiles.Delete(filename)
			fw.File.IsDeleted = true
			return true
		}

		newFileInfo := extraInfo.info

		// 文件被移除，或者其上层目录被移走，同时新建了相同目录和文件。
		// fsnotify拿不到对应的事件，在这里做一遍清理，删除老的文件，并将新的文件加入tail
		if !os.SameFile(newFileInfo, fw.File.State.FileInfo) {
			logger.Errorf("not the same file. watch file(%s) is not exists, but the same file is created.", filename)
			client.WatchFiles.Delete(filename)
			if fw.File.State.INode > 0 {
				if v, ok := client.inodeMap.Load(fw.File.State.INode); ok {
					if inodeFilename, ok := v.(string); ok && inodeFilename == filename {
						client.inodeMap.Delete(fw.File.State.INode)
					}
				}
			}
			fw.File.IsDeleted = true

			reAddToWatchFiles[filename] = extraInfo
		}
		fw.File.State.FileInfo = newFileInfo

		return true
	})

	for filename, extraInfo := range reAddToWatchFiles {
		err := client.watchFile(filename, FilePositionEnd, extraInfo, false)
		if err != nil {
			logger.Errorf("add filename=>(%s) to watch error=>(%v)", filename, err)
		}
	}
}

// getTaskListByFile
// 根据文件名，从task配置中扫描，获取匹配上的task列表
func (client *Input) getTaskListByFile(filename string, info os.FileInfo) fileExtraInfo {
	taskList := make([]*keyword.TaskConfig, 0)
	for _, task := range client.cfg {
		if client.isFileExcluded(filename, task.Input.ExcludeFiles) {
			continue
		}

		for _, patten := range task.Input.Paths {
			ok, err := filepath.Match(patten, filename)
			if err != nil {
				logger.Errorf("match '%s' and '%s' failed, %v", patten, filename, err)
			}
			if ok {
				taskList = append(taskList, task)
			}
		}
	}

	return fileExtraInfo{
		info:        info,
		taskConfigs: taskList,
	}
}

func (client *Input) getExistsWatchFileByInode(inode uint64) (*FileWatcher, bool) {
	v, ok := client.inodeMap.Load(inode)
	if !ok {
		return nil, false
	}
	filename, ok := v.(string)
	if !ok {
		return nil, false
	}
	fwInterface, ok := client.WatchFiles.Load(filename)
	if !ok {
		return nil, false
	}
	fw, ok := fwInterface.(*FileWatcher)
	return fw, ok
}
