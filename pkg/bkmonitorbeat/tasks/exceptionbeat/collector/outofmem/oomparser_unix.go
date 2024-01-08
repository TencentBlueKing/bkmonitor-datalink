// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || dragonfly || linux || netbsd || openbsd || solaris || zos

package outofmem

import (
	"context"
	"path"
	"regexp"
	"strconv"

	"github.com/euank/go-kmsg-parser/kmsgparser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var (
	legacyContainerRegexp = regexp.MustCompile(`Task in (.*) killed as a result of limit of (.*)`)
	// Starting in 5.0 linux kernels, the OOM message changed
	containerRegexp = regexp.MustCompile(`oom-kill:constraint=(.*),nodemask=(.*),cpuset=(.*),mems_allowed=(.*),oom_memcg=(.*),task_memcg=(.*),task=(.*),pid=(.*),uid=(.*)`)
	lastLineRegexp  = regexp.MustCompile(`Killed process ([0-9]+) \((.+)\)`)
	firstLineRegexp = regexp.MustCompile(`invoked oom-killer:`)
)

func startTraceOOM(ctx context.Context, infoChan chan *OOMInfo) error {
	oomPsr, err := NewOomParser()
	if err != nil {
		return err
	}

	// 设置 buffer 起缓冲作用 避免事件太多处理不过来被丢弃
	outStream := make(chan *OomInstance, 50)
	outStreamCtx, outStreamCancel := context.WithCancel(context.Background())
	go oomPsr.StreamOoms(outStreamCtx, outStream)

	for {
		select {
		case <-ctx.Done():
			close(infoChan)
			outStreamCancel()
			return nil
		case event := <-outStream:
			infoChan <- &OOMInfo{
				OomInstance: event,
			}
		}
	}
}

// initializes an OomParser object. Returns an OomParser object and an error.
func NewOomParser() (*OomParser, error) {
	parser, err := kmsgparser.NewParser()
	if err != nil {
		return nil, err
	}
	parser.SetLogger(glogAdapter{})
	return &OomParser{parser: parser}, nil
}

// OomParser wraps a kmsgparser in order to extract OOM events from the
// individual kernel ring buffer messages.
type OomParser struct {
	parser kmsgparser.Parser
}

// gets the container name from a line and adds it to the oomInstance.
func getLegacyContainerName(line string, currentOomInstance *OomInstance) error {
	parsedLine := legacyContainerRegexp.FindStringSubmatch(line)
	if parsedLine == nil {
		return nil
	}
	currentOomInstance.VictimContainerName = path.Join("/", parsedLine[1])
	currentOomInstance.ContainerName = path.Join("/", parsedLine[2])
	return nil
}

// gets the container name from a line and adds it to the oomInstance.
func getContainerName(line string, currentOomInstance *OomInstance) (bool, error) {
	parsedLine := containerRegexp.FindStringSubmatch(line)
	if parsedLine == nil {
		// Fall back to the legacy format if it isn't found here.
		return false, getLegacyContainerName(line, currentOomInstance)
	}
	currentOomInstance.ContainerName = parsedLine[5]
	currentOomInstance.VictimContainerName = parsedLine[6]
	currentOomInstance.Constraint = parsedLine[1]
	pid, err := strconv.Atoi(parsedLine[8])
	if err != nil {
		return false, err
	}
	currentOomInstance.Pid = pid
	currentOomInstance.ProcessName = parsedLine[7]
	return true, nil
}

// gets the pid, name, and date from a line and adds it to oomInstance
func getProcessNamePid(line string, currentOomInstance *OomInstance) (bool, error) {
	reList := lastLineRegexp.FindStringSubmatch(line)

	if reList == nil {
		return false, nil
	}

	pid, err := strconv.Atoi(reList[1])
	if err != nil {
		return false, err
	}
	currentOomInstance.Pid = pid
	currentOomInstance.ProcessName = reList[2]
	return true, nil
}

// uses regex to see if line is the start of a kernel oom log
func checkIfStartOfOomMessages(line string) bool {
	potentialOomStart := firstLineRegexp.MatchString(line)
	return potentialOomStart
}

// StreamOoms writes to a provided a stream of OomInstance objects representing
// OOM events that are found in the logs.
// It will block and should be called from a goroutine.
func (p *OomParser) StreamOoms(ctx context.Context, outStream chan<- *OomInstance) {
	err := p.parser.SeekEnd()
	if err != nil {
		logger.Errorf("parser SeekEnd error: %v", err)
	}
	kmsgEntries := p.parser.Parse()
	defer p.parser.Close()

	for {
		select {
		case msg, ok := <-kmsgEntries:
			if !ok {
				return
			}
			isOomMessage := checkIfStartOfOomMessages(msg.Message)
			if isOomMessage {
				oomCurrentInstance := &OomInstance{
					ContainerName:       "/",
					VictimContainerName: "/",
					TimeOfDeath:         msg.Timestamp,
				}
				for msg := range kmsgEntries {
					finished, err := getContainerName(msg.Message, oomCurrentInstance)
					if err != nil {
						logger.Errorf("%v", err)
					}
					if !finished {
						finished, err = getProcessNamePid(msg.Message, oomCurrentInstance)
						if err != nil {
							logger.Errorf("%v", err)
						}
					}
					if finished {
						oomCurrentInstance.TimeOfDeath = msg.Timestamp
						break
					}
				}
				outStream <- oomCurrentInstance
			}
		case <-ctx.Done():
			logger.Errorf("exiting analyzeLines. OOM events will not be reported.")
			return
		}
	}
}

type glogAdapter struct{}

var _ kmsgparser.Logger = glogAdapter{}

func (glogAdapter) Infof(format string, args ...interface{}) {
	logger.Infof(format, args...)
}

func (glogAdapter) Warningf(format string, args ...interface{}) {
	logger.Infof(format, args...)
}

func (glogAdapter) Errorf(format string, args ...interface{}) {
	logger.Errorf(format, args...)
}
