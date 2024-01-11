// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package corefile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sys/unix"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/exceptionbeat/collector"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	ExecutableKeyName     = "executable"
	ExecutablePathKeyName = "executable_path"
	SignalKeyName         = "signal"
	EventTimeKeyName      = "event_time"
)

var (
	CorePatternFile = "/proc/sys/kernel/core_pattern"
	CoreUsesPidFile = "/proc/sys/kernel/core_uses_pid"
	defaultPattern  = ".*"
	patternMap      = map[string]string{
		"%c": "\\d+",
		"%g": "\\d+",
		"%i": "\\d+",
		"%I": "\\d+",
		"%p": "\\d+",
		"%P": "\\d+",
		"%s": "\\d+",
		"%t": "\\d+",
		"%u": "\\d+",
	}
	SpecifierParseMap = map[string]Dimension{
		"%e": {
			name:       ExecutableKeyName,
			translator: nil,
		},
		"%E": {
			name:       ExecutablePathKeyName,
			translator: new(ExecutablePathTranslator),
		},
		"%s": {
			name:       SignalKeyName,
			translator: new(SignalTranslator),
		},
		"%t": {
			name:       EventTimeKeyName,
			translator: nil,
		},
	}
)

type Translator interface {
	Translate(text string) string
}

type SignalTranslator struct{}

func (t *SignalTranslator) Translate(text string) string {
	// 将对应的信号值，转化为信号名
	signalNum, err := strconv.Atoi(text)
	if err != nil {
		return text
	}
	return unix.SignalName(syscall.Signal(signalNum))
}

type ExecutablePathTranslator struct{}

func (t *ExecutablePathTranslator) Translate(text string) string {
	// 将路径中的"!"替换为"/"
	return strings.ReplaceAll(text, "!", "/")
}

type Dimension struct {
	name       string
	translator Translator
}

// buildDimensionKey: 根据传入的维度内容进行拼接，拼接顺序是executePath-executable-signal
// 如果某个key不存在，则使用空字符串替代
func buildDimensionKey(info beat.MapStr) string {
	var (
		result  []string
		content string
		ok      bool
	)

	for _, key := range []string{ExecutablePathKeyName, ExecutableKeyName, SignalKeyName} {
		if content, ok = info[key].(string); ok {
			result = append(result, content)
		} else {
			result = append(result, "")
		}

	}

	return strings.Join(result, "-")
}

// handleCorePatternFileEvent 处理CorePattern文件事件
func (c *Collector) handleCorePatternFileEvent(event fsnotify.Event) {
	// 如果是发现core_pattern的路径发生变化，需要考虑更新
	// 直接写入(write事件)或者通过vim编辑(重命名事件)
	// 其他事件(删除、修改属性、创建)并不关注
	if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Rename == fsnotify.Rename {
		logger.Infof("Collector found pattern->[%s] updated, will refresh core fil path", event.Name)
		err := c.updateCoreFilePath()
		if err != nil {
			logger.Errorf("Collector core file watcher updated error: %s, will wait next update", err)
		}
		errPattern := c.checkPattern()
		if errPattern != nil {
			logger.Errorf("parsing of the pattern had failed")
		}
	}
}

// handleUsesPidFileEvent 处理CoreUsesPidFile文件事件
func (c *Collector) handleCoreUsesPidFileEvent(event fsnotify.Event) {
	// 如果是发现core_uses_pid的路径发生变化，需要考虑更新
	// 直接写入(write事件)或者通过vim编辑(重命名事件)
	// 其他事件(删除、修改属性、创建)并不关注
	if event.Op&fsnotify.Write == fsnotify.Write || event.Op&fsnotify.Rename == fsnotify.Rename {
		logger.Infof("Collector found core_uses_pid->[%s] updated, will refresh core fil path.", event.Name)
		err := c.setCoreUsesPid()
		if err != nil {
			logger.Errorf("Collector core_uses_pid file watcher updated error: %s, will wait next update", err)
		}
	}
}

// handleCoreFileEvent 处理Core文件事件
func (c *Collector) handleCoreFileEvent(event fsnotify.Event, e chan<- define.Event) {
	// 如果是其他路径，那么考虑是corefile文件的产生
	// 只关注文件创建，后续文件的写入或者其他的变化都一律认为属于收敛不再关注
	if strings.Contains(event.Name, c.corePath) && event.Op&fsnotify.Create == fsnotify.Create {
		// 如果发现创建的事件属于路径，则跳过不处理
		info, err := os.Lstat(event.Name)
		if err != nil {
			logger.Errorf("failed to stat file->[%s] stat for err->[%s], nothing will do any more.", event.Name, err)
			return
		}

		if info.IsDir() {
			logger.Infof("Collector found new create event but path->[%s] is dir, nothing will do.", event.Name)
			return
		}

		logger.Infof("Collector found new file->[%s] created, will send corefile event.", event.Name)

		// 创建新的dimension缓存区
		var dimensions beat.MapStr
		var isAnalysisSuccess bool
		if dimensions, isAnalysisSuccess = c.fillDimension(event.Name); !isAnalysisSuccess {
			logger.Errorf(
				"failed to analysis file_path->[%s] by pattern->[%s] maybe file is not corefile, skip it.",
				event.Name, c.pattern,
			)
			return
		}
		extra := buildExtra(event.Name, dimensions)
		if nil == extra {
			// 此时可能是因为从agent获取IP等信息异常，那么此时消息没有必要发送，因为发送后也没法知道是哪个机器发生异常
			return
		}
		c.send(extra, e)

	} else if event.Name == c.corePath && event.Op&fsnotify.Remove == fsnotify.Remove {
		logger.Infof("corePath->[%s] is delete, nothing will watch any more and add success set to false", c.corePath)
		c.isCorePathAddSuccess = false
	}
}

// checkSystemFile 检查系统配置变更
func (c *Collector) checkSystemFile() {

	if !c.isCorePathAddSuccess && c.corePath != "" {
		logger.Infof("corePath->[%s] add failed before, will retry now.", c.corePath)
		if err := c.coreWatcher.Add(c.corePath); err != nil {
			logger.Infof("corePath->[%s] still add failed for->[%s], will try next 30s", c.corePath, err)
			return
		}
		logger.Infof("yo, corePath->[%s] add success now.", c.corePath)
		c.isCorePathAddSuccess = true
	} else {
		logger.Debugf("corePath->[%s] is already add success or corePath is empty, nothing will do", c.corePath)
	}
	if !c.isCoreUsesPidAddSuccess {
		if err := c.coreWatcher.Add(CoreUsesPidFile); err != nil {
			logger.Infof("core_uses_pid->[%s] still add failed for->[%s], will try next 30s", CoreUsesPidFile, err)
			return
		}
		logger.Infof("yo, core_uses_pid->[%s] add success now.", CoreUsesPidFile)
		c.isCoreUsesPidAddSuccess = true
	}
	if !c.isCorePatternAddSuccess {
		logger.Infof("corePattern->[%s] add failed before, will retry now.", CorePatternFile)
		if err := c.coreWatcher.Add(CorePatternFile); err != nil {
			logger.Infof("corePattern->[%s] still add failed for->[%s], will try next 30s", CorePatternFile, err)
			return
		}
		logger.Infof("yo, corePattern->[%s] add success now.", CorePatternFile)
		c.isCorePatternAddSuccess = true
	} else {
		logger.Debugf("corePattern->[%s] is already add success, nothing will do", CorePatternFile)
	}
}

// handleSendEvent 处理上报
func (c *Collector) handleSendEvent(e chan<- define.Event) {
	var now = time.Now()
	// 遍历检查是否存在需要发送的缓存事件
	for key, reportInfo := range c.reportTimeInfo {
		// 如果有上报时间已经超过的，而且存在上报记录信息的，需要上报
		if now.Sub(reportInfo.time) > c.reportTimeGap && reportInfo.count > 0 {
			logger.Debugf("key->[%s] last report time->[%s] now is more than gap->[%s] will report it", key, reportInfo.time, c.reportTimeGap)
			reportInfo.info["count"] = reportInfo.count
			collector.Send(int(c.dataid), reportInfo.info, e)
			logger.Debugf("key->[%s] last report time->[%s] gap->[%s] now is reported it, will update report time and count", key, reportInfo.time, c.reportTimeGap)

			reportInfo.time = time.Now()
			reportInfo.count = 0
			logger.Infof("key->[%s] is report and count is set to zero and report time set to now", key)
		}
	}
	logger.Debugf("routine check for corefile delay report done.")
}

// addCoreWatch 增加监听core文件路径
func (c *Collector) addCoreWatch() {
	logger.Infof("Collector add path->[%s] to watcher.", c.corePath)
	err := c.coreWatcher.Add(c.corePath)
	if err != nil {
		logger.Errorf("Collector add \"%s\" to watcher failed with error: %s, will wait next pattern update", c.corePath, err)
	} else {
		c.isCorePathAddSuccess = true
	}
}

// watchSystemFiles 监听系统配置文件
func (c *Collector) watchSystemFiles() {
	err := c.coreWatcher.Add(CorePatternFile)
	if err != nil {
		logger.Errorf("Collector add \"%s\" to watcher failed with error: %s", CorePatternFile, err)
		c.isCorePatternAddSuccess = false
	} else {
		c.isCorePatternAddSuccess = true
	}

	err = c.coreWatcher.Add(CoreUsesPidFile)
	if err != nil {
		logger.Errorf("Collector add \"%s\" to watcher failed with error: %s", CoreUsesPidFile, err)
		c.isCoreUsesPidAddSuccess = false
	} else {
		c.isCoreUsesPidAddSuccess = true
	}
}

// loopCheck 定期的每30秒检查一次是否需要更新
func (c *Collector) loopCheck(ctx context.Context, e chan<- define.Event) {
	logger.Info("loopCheck start", c.coreWatcher.WatchList())
	var (
		corePathCheckerTicker = time.NewTicker(30 * time.Second)
		reportCheckTicker     = time.NewTicker(c.reportTimeGap / 2)
	)

	for {
		select {
		case <-ctx.Done():
			c.Stop()
			logger.Info("corefile collector exit")
			return

		case event, ok := <-c.coreWatcher.Events:
			// corefile文件事件
			if !ok {
				c.Stop()
				logger.Info("Collector core file watcher closed")
				return
			}
			logger.Infof("file event: %s %v", event.Name, event.Op)
			// 判断是core_pattern还是其他发生变化，需要有不同的动作处理
			if CorePatternFile == event.Name {
				c.handleCorePatternFileEvent(event)
			} else if CoreUsesPidFile == event.Name {
				c.handleCoreUsesPidFileEvent(event)
			} else {
				c.handleCoreFileEvent(event, e)
			}
		case <-corePathCheckerTicker.C:
			// 定期检查系统配置信息是否有变化
			c.checkSystemFile()
		case <-reportCheckTicker.C:
			c.handleSendEvent(e)
		case err, ok := <-c.coreWatcher.Errors:
			// 异常退出
			if !ok {
				c.Stop()
				logger.Infof("Collector core file watcher closed")
				return
			}
			logger.Errorf("Collector core file watcher error: %s", err)
		case _, ok := <-c.done:
			if !ok {
				// 结束采集
				return
			}
			reportCheckTicker = time.NewTicker(c.reportTimeGap / 2)
			break
		}
	}
}

func (c *Collector) statistic(ctx context.Context, e chan<- define.Event) {
	c.isCorePathAddSuccess = false
	c.isCorePatternAddSuccess = false
	path, err := c.getCoreFilePath()
	if err != nil {
		logger.Errorf("Collector obtaining file's name failed with error message: %s", err)
	}

	logger.Infof("Core file path read from core_pattern: %s", path)
	c.corePath = path
	errUsesPid := c.setCoreUsesPid()
	if errUsesPid != nil {
		logger.Error("set isUsesPid  had failed")
	}
	errPattern := c.checkPattern()
	if errPattern != nil {
		logger.Errorf("parsing of the pattern [%s] had failed", c.pattern)
	}
	c.coreWatcher, err = fsnotify.NewWatcher()
	if err != nil {
		logger.Errorf("Collector initing core file watcher watcher failed with error message: %s", err)
		c.Stop()
		return
	}
	defer func() {
		_ = c.coreWatcher.Close()
	}()

	if path != "" {
		c.addCoreWatch()
	}
	c.watchSystemFiles()
	c.loopCheck(ctx, e)
}

func (c *Collector) getCoreFilePath() (string, error) {
	var corePattern string
	// 若配置中未申明 CoreFile 路径和格式，则读取系统内置的配置文件 CorePatternFile
	if c.coreFilePattern == "" {
		file, err := os.Open(CorePatternFile)
		if err != nil {
			return "", err
		}
		defer func() {
			_ = file.Close()
		}()
		var corePatternArr = make([]byte, 512)
		_, err = file.Read(corePatternArr)
		if err != nil {
			return "", err
		}
		corePattern = string(corePatternArr)
	} else {
		corePattern = c.coreFilePattern
		if !strings.HasSuffix(corePattern, "\n") {
			corePattern += "\n"
		}
	}

	ind := strings.LastIndex(corePattern, "/")
	if ind == -1 || corePattern[0] != '/' {
		return "", fmt.Errorf("no core file storing path found, please check /proc/sys/kernel/core_pattern " +
			"and exceptionbeat_task.corepattern in bkmonitorbeat.config")
	}
	end := strings.LastIndex(corePattern, "\n")
	if end == -1 {
		return "", fmt.Errorf("can not found \\n in file content, please check /proc/sys/kernel/core_pattern " +
			" and exceptionbeat_task.corepattern in bkmonitorbeat.config")
	}
	logger.Infof("end index of core_pattern file content is %d", end)
	c.pattern = corePattern[ind+1 : end]
	return corePattern[0:ind], nil
}

func (c *Collector) updateCoreFilePath() error {
	path, err := c.getCoreFilePath()
	if err != nil {
		logger.Errorf("Collector obtaining file's name failed with error message: %s", err)
		return err
	}
	logger.Infof("Core file path read from core_pattern: %s", path)

	if path == "" {
		logger.Errorf("Collector found bad core_pattern->[%s] will not update", path)
		return nil
	}

	if c.corePath != "" && c.isCorePathAddSuccess {
		err = c.coreWatcher.Remove(c.corePath)
		if err != nil {
			logger.Errorf("Collector remove \"%s\" from watcher failed with error: %s", c.corePath, err)
			return err
		}
	}

	c.corePath = path
	err = c.coreWatcher.Add(c.corePath)
	if err != nil {
		logger.Errorf("Collector add \"%s\" to watcher failed with error: %s", c.corePath, err)
		c.isCorePathAddSuccess = false
		return err
	}
	c.isCorePathAddSuccess = true
	logger.Infof("Collector add new path watcher->[%s]", c.corePath)
	return nil
}

// getDimensionRegs 获取完整正则匹配对象和所有维度匹配对象列表
func (c *Collector) getDimensionReg(greedy bool) *regexp.Regexp {
	patternArrLen := len(c.patternList)
	// 根据pattern拼接正则表达式，对corefile文件名进行维度提取
	content := `(%s%s)`
	dimensionReg := `^`
	for i, value := range c.patternList {
		if i < (patternArrLen - 1) {
			specifier := value[2]
			dimension, exist := SpecifierParseMap[specifier]
			var groupName string
			if exist {
				groupName = fmt.Sprintf("?P<%s>", dimension.name)
			} else {
				groupName = ""
			}
			pattern := defaultPattern
			if p, ok := patternMap[specifier]; ok {
				pattern = p
			}
			if !greedy {
				pattern = pattern + "?"
			}
			// 分隔符如果包含正则元字符,则需要进行转义
			safeDelimiter := value[1]
			for _, v := range []string{"*", "+", "?", "$", "^", ".", "|", `\`, "(", ")", "{", "}", "[", "]"} {
				if v == safeDelimiter {
					safeDelimiter = strings.ReplaceAll(safeDelimiter, v, fmt.Sprintf(`\`+v))
					break
				}
			}
			dimensionReg = strings.Join([]string{dimensionReg, fmt.Sprintf(content, groupName, pattern)}, safeDelimiter)
		} else {
			// 处理自己最后补充的占位符前缀
			dimensionReg += value[1]
		}
	}
	dimensionReg += "$"
	reg := regexp.MustCompile(dimensionReg)
	return reg
}

type regexGroup struct {
	name        string
	value       string
	greedyValue string
}

func (c *Collector) parseDimensions(groups []regexGroup) beat.MapStr {
	dimensions := beat.MapStr{}
	ignoredForConfused := 0
	for _, group := range groups {
		// 有歧义跳过
		if group.value != group.greedyValue {
			ignoredForConfused++
			continue
		}

		if group.name != "" && group.value != "" {
			dimensions[group.name] = group.value
		}
	}
	if ignoredForConfused > 0 {
		logger.Infof("dimension ignored for confused regex groups: %+v", groups)
	}
	return dimensions
}

// fillDimension: 填充维度信息到dimensions当中，如果解析失败，那么直接返回dimensions，不对其中的任何内容进行修改
// 返回内容表示是否可以按照正则正常解析；如果正则解析失败的，很可能是用户自己瞎写的文件，不应该触发告警
func (c *Collector) fillDimension(filePath string) (beat.MapStr, bool) {

	// 获取core file文件名
	fileName, errFileName := c.getCoreFileName(filePath)
	if errFileName != nil {
		logger.Error(errFileName)
		return beat.MapStr{}, false
	}
	if c.patternList == nil {
		// 如果此时无法正常获取正则规则，那么我们会认为无法判断，会将任何文件都返回
		logger.Error("parsing of the pattern had failed")
		return beat.MapStr{}, true
	}
	reg := c.getDimensionReg(false)
	logger.Infof("core file dimensionReg: %s, filename: %s", reg.String(), fileName)

	// 贪婪
	greedyReg := c.getDimensionReg(true)
	logger.Infof("core file dimensionReg greedy: %s, filename: %s", greedyReg.String(), fileName)
	// 提取有分组别名的维度
	result := reg.FindAllStringSubmatch(fileName, -1)
	// 说明没有完全匹配上，说明有问题，那么此时直接返回原本的维度信息
	if len(result) != 1 {
		logger.Errorf("%s, dimensionReg: %s, filename: %s", ErrRegexMatch, reg.String(), fileName)
		return beat.MapStr{}, false
	}

	values := result[0][1:]
	names := reg.SubexpNames()[1:]
	// 贪婪模式匹配用来对比结果
	var greedyValues []string
	greedyResult := greedyReg.FindAllStringSubmatch(fileName, -1)
	if len(greedyResult) == 1 {
		greedyValues = greedyResult[0][1:]
	}
	// 组装字段值列表
	groups := make([]regexGroup, 0, len(names))
	for i, name := range names {
		var greedyValue string
		if len(greedyValues) > i {
			greedyValue = greedyValues[i]
		}
		group := regexGroup{
			name:        name,
			value:       values[i],
			greedyValue: greedyValue,
		}
		groups = append(groups, group)
	}
	// 提取维度
	dimensions := c.parseDimensions(groups)
	// 翻译维度
	for _, d := range SpecifierParseMap {
		dimensionName := d.name
		if value, ok := dimensions[dimensionName].(string); ok && d.translator != nil {
			dimensions[dimensionName] = d.translator.Translate(value)
		}
	}
	// 假如没有executable但是executable_path有值，executable可以通过executable_path获得
	_, executableExist := dimensions["executable"]
	executablePath, executablePathExist := dimensions["executable_path"]
	if !executableExist && executablePathExist && executablePath != "" {
		dimensions["executable"] = filepath.Base(executablePath.(string))
	}

	return dimensions, true
}

func (c *Collector) checkPattern() error {
	// 因为匹配的是{前缀}+{占位符}。如果pattern是以非占位符结尾，当前使用的正则会无法匹配到
	// 需要在匹配前补一个固定的占位符，让正则可以匹配类似pattern：xxx-%e-end
	myPattern := c.pattern + "%z"
	// 提取pattern中的占位符及占位符的前缀
	reg := regexp.MustCompile(`(.*?)(%[a-zA-Z])`)
	result := reg.FindAllStringSubmatch(myPattern, -1)
	c.patternList = nil
	// 未能匹配到占位符，则直接返回
	if len(result) < 1 {
		logger.Infof("%s, regex: %s, pattern: %s", ErrRegexMatch, reg, c.pattern)
		return ErrRegexMatch
	}

	for key, value := range result[:len(result)-1] {
		// 第一个占位符允许没有前缀，后续占位符必须有前缀
		if key != 0 && value[1] == "" {
			logger.Errorf("%s, pattern: %s", ErrPatternDelimiter, c.pattern)
			return ErrPatternDelimiter
		}
	}
	c.patternList = result
	return nil
}

func (c *Collector) setCoreUsesPid() error {
	// 获取是否否添加pid作为扩展名
	file, err := os.Open(CoreUsesPidFile)
	if err != nil {
		logger.Errorf("open %s failed", CoreUsesPidFile)
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	var coreUsesPidArr = make([]byte, 512)
	_, err = file.Read(coreUsesPidArr)
	if err != nil {
		logger.Errorf("read %s failed", CoreUsesPidFile)
		return err
	}
	content := string(coreUsesPidArr[0:1])
	if content == "0" {
		c.isUsesPid = false
	} else {
		c.isUsesPid = true
	}
	logger.Infof("use_pid file content->[%s] will set use_pid to->[%v]", content, c.isUsesPid)
	return nil
}

func (c *Collector) getCoreFileName(filePath string) (string, error) {
	// 从文件路径中切割出文件名
	fileName := filepath.Base(filePath)
	// 如果使用了PID，同时在corefile路径中没有使用%p，那么我们需要切割pid
	if c.isUsesPid && !strings.Contains(c.pattern, "%p") {
		extInd := strings.LastIndex(fileName, ".")
		if extInd == -1 {
			return "", fmt.Errorf("core_uses_pid is true, but can not find the file extension in file name [%s], and the file path is: %s", fileName, filePath)
		}
		fileName = fileName[0:extInd]
	}
	return fileName, nil
}

// send: 发送消息，但是在发送前会判断维度是否存在发送缓冲阶段
// 例如，当某个corefile出现的时候，我们会第一时间发送一个corefile事件。
// 但如果在上报缓冲时间(默认1分钟)中，那么新产生的时间只会记录计数，不会上报，直到下一个1分钟再统一上报
func (c *Collector) send(info beat.MapStr, e chan<- define.Event) {
	var (
		now            = time.Now()
		infoKey        = buildDimensionKey(info)
		reportInfo     *ReportInfo
		ok, shouldSend bool
	)

	// 如果是发现存在计数，而且上报间隔还没有到，那么只是增加计数，不做发送动作
	if reportInfo, ok = c.reportTimeInfo[infoKey]; !ok {
		// 追加计数为1的内容
		info["count"] = 1
		// 如果上报记录不曾存在，那么则会立马上报
		logger.Debugf("key->[%s] is not exists, will report this event", infoKey)
		shouldSend = true
		logger.Infof("key->[%s] is not exists, event is reported now", infoKey)
		// 需要更新缓存信息, 缓存的信息应该是0，因为本次的次数已经发送了，没必要计算
		c.reportTimeInfo[infoKey] = &ReportInfo{time: now, count: 0, info: info}
		logger.Infof("key->[%s] now is added to buffer", infoKey)
	} else {
		// 先更新计数器
		reportInfo.count++
		info["count"] = reportInfo.count
		reportInfo.info = info
		logger.Infof("key->[%s] is already exists, will add count->[%d]", infoKey, reportInfo.count)

		// 再判断是否已经有很大的间隔未发送消息，如果是需要立马发送一个消息
		if now.Sub(reportInfo.time) > c.reportTimeGap {
			shouldSend = true
			reportInfo.time = now
			reportInfo.count = 0
			logger.Infof("key->[%s] last report time is->[%s] is more than gap->[%s] will sent it now, reset", infoKey, reportInfo.time, c.reportTimeGap)
		}
	}

	if shouldSend {
		collector.Send(int(c.dataid), info, e)
	}

	logger.Infof("key->[%s] process done", infoKey)
}
