// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beat

import (
	"crypto/md5"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/elastic/beats/libbeat/cfgfile"
	"github.com/elastic/beats/libbeat/cmd/instance"
	"github.com/elastic/beats/libbeat/common"
	libbeatlogp "github.com/elastic/beats/libbeat/logp"

	bkcommon "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/logp"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/pidfile"
	reloader2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/reloader"
	bkstorage "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/storage"
)

type pathConfig struct {
	PidFilePath string `config:"pid"`
	DataPath    string `config:"data"`
}

const (
	pathConfigField    = "path"
	resourceLimitField = "resource_limit"
)

var defaultPathConfig = pathConfig{
	PidFilePath: "",
	DataPath:    "./data",
}

var (
	reloader     *reloader2.Reloader
	mutex        sync.Mutex
	rawconfig    *Config
	beatSettings instance.Settings
)

func baseInit(beatName string, version string) (*Config, error) {
	rawconfig = nil
	ReloadChan = make(chan bool)
	Done = make(chan bool)
	commonBKBeat = BKBeat{
		Finished:    false,
		BeaterState: BeaterBeforeOpening,
		Done:        make(chan struct{}),
	}
	cfgfile.ChangeDefaultCfgfileFlag(beatName)

	flag.Parse()

	gse.GseCheck = *gseCheck

	// Print version
	versionFlag := flag.Lookup("v")
	if versionFlag.Value.String() == "true" {
		fmt.Printf("%s\n", version)
		os.Exit(0)
	}

	// Get Pid file path
	var err error
	rawconfig, err = cfgfile.Load("", nil)
	if err != nil {
		commonBKBeat.BeaterState = BeaterFailToOpen
		return nil, err
	}
	var pathCfg *common.Config
	pathConfig := pathConfig(defaultPathConfig)
	if rawconfig.HasField(pathConfigField) {
		pathCfg, err = rawconfig.Child(pathConfigField, -1)
		if err != nil {
			commonBKBeat.BeaterState = BeaterFailToOpen
			return nil, err
		}
		err = pathCfg.Unpack(&pathConfig)
		if err != nil {
			commonBKBeat.BeaterState = BeaterFailToOpen
			return nil, err
		}
	} else {
		pathConfig.PidFilePath = ""
	}
	pidFilePath, err := bkcommon.MakePifFilePath(beatName, pathConfig.PidFilePath)
	if err != nil {
		commonBKBeat.BeaterState = BeaterFailToOpen
		return nil, err
	}

	// Reload event
	if *reloadFlag {
		err = reloader2.ReloadEvent(beatName, pidFilePath)
		if err != nil {
			fmt.Println(err.Error())
		}
		os.Exit(0)
	}

	if !*testMode {
		// 非 gsecheck 时需要锁定 pid
		if !*gseCheck {
			err = pidfile.TryLock(pidFilePath)
			if err != nil {
				commonBKBeat.BeaterState = BeaterFailToOpen
				return nil, err
			}
		}

		// Init bkstorage
		dbName := ".bkpipe.db"
		// gsecheck 时使用 init db
		if *gseCheck {
			dbName = ".bkpipe.init.db"
		}
		dbFilePath := filepath.Join(pathConfig.DataPath, beatName+dbName)
		err = bkstorage.Init(dbFilePath, nil)
		if err != nil {
			commonBKBeat.BeaterState = BeaterFailToOpen
			return nil, fmt.Errorf("initializing storage %s error: %v", dbFilePath, err)
		}
	}

	errorMessageChan = make(chan error)
	wg.Add(1)
	// Init libbeat
	go func() {
		beatSettings.Name = beatName
		beatSettings.Version = version
		err := instance.Run(beatSettings, creator)
		if err != nil {
			commonBKBeat.BeaterState = BeaterFailToOpen
			freeResource()
			wg.Done()
			errorMessageChan <- err
			return
		}
		close(Done)
		return
	}()
	wg.Wait()

	err, ok := <-errorMessageChan
	if ok {
		err = fmt.Errorf("failed to initialize libbeat: %s", err.Error())
		fmt.Println(err.Error())
		return nil, err
	}

	logp.SetLogger(libbeatlogp.L())

	if !*testMode {
		// Init reloader
		reloader = reloader2.NewReloader(beatName, &commonBKBeat)
		if err := reloader.Run(pidFilePath); err != nil {
			logp.L.Errorf(err.Error())
			commonBKBeat.BeaterState = BeaterFailToOpen
			commonBKBeat.Stop()
			return nil, err
		}
	}

	commonBKBeat.BeaterState = BeaterRunning

	if rcf, enabled := getResourceLimit(); enabled {
		SetResourceLimit(getResourceIdentify(beatName), rcf.Cpu, rcf.Mem)
	}

	return commonBKBeat.LocalConfig, nil
}

type resourceConfig struct {
	Enabled bool    `config:"enabled"`
	Cpu     float64 `config:"cpu"`
	Mem     int     `config:"mem"`
}

func getResourceLimit() (*resourceConfig, bool) {
	resourceCfg, err := rawconfig.Child(resourceLimitField, -1)
	if err != nil {
		return nil, false
	}

	var rcf resourceConfig
	if err := resourceCfg.Unpack(&rcf); err != nil {
		return nil, false
	}

	// 禁用 cgroups
	if !rcf.Enabled {
		return nil, false
	}

	return &rcf, true
}

func getResourceIdentify(name string) string {
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		return name
	}

	return fmt.Sprintf("%s-%x", name, md5.Sum([]byte(dir)))
}

// Init initializes user's beat
func Init(beatName string, version string) (*Config, error) {
	mutex.Lock()
	if !beatNotRunning() {
		mutex.Unlock()
		return nil, fmt.Errorf("%s has already been created", beatName)
	}
	config, err := baseInit(beatName, version)
	mutex.Unlock()
	return config, err
}

// InitWithPublishConfig initializes user's beat with user specified publish config
func InitWithPublishConfig(beatName string, version string, pubConfig PublishConfig, settings instance.Settings) (*Config, error) {
	mutex.Lock()
	if !beatNotRunning() {
		mutex.Unlock()
		return nil, fmt.Errorf("%s has already been created", beatName)
	}
	publishConfig = pubConfig
	beatSettings = settings
	config, err := baseInit(beatName, version)

	mutex.Unlock()
	return config, err
}

func freeResource() {
	pidfile.UnLock()
	bkstorage.Close()
}

// GetConfig fetch the config instance of user's beat
func GetConfig() *Config {
	if beatNotRunning() {
		return nil
	}
	return commonBKBeat.LocalConfig
}

func GetRawConfig() *Config {
	return rawconfig
}

// Send sends a mapstr event
func Send(event MapStr) bool {
	if beatNotRunning() {
		return false
	}
	if commonBKBeat.Client == nil {
		return false
	}
	(*commonBKBeat.Client).Publish(bkEventToEvent(event))
	if *testMode {
		time.Sleep(time.Second * 2)
		os.Exit(0)
	}
	return true
}

// SendEvent sends a Event type event
func SendEvent(event Event) bool {
	if beatNotRunning() {
		return false
	}
	if commonBKBeat.Client == nil {
		return false
	}
	(*commonBKBeat.Client).Publish(formatEvent(event))
	if *testMode {
		time.Sleep(time.Second * 2)
		os.Exit(0)
	}
	return true
}

// Stop stops the beat
func Stop() error {
	mutex.Lock()
	if beatNotRunning() {
		mutex.Unlock()
		return errors.New("no beat running")
	}
	commonBKBeat.Stop()
	reloader.Stop()
	freeResource()
	logp.SetLogger(nil)
	commonBKBeat.BeaterState = BeaterStoped
	mutex.Unlock()
	return nil
}
