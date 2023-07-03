// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/go-ucfg"
	"github.com/elastic/go-ucfg/yaml"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/beater/taskfactory"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// BaseConfigEngine 配置引擎，负责配置管理及心跳上报
type BaseConfigEngine struct {
	ctx        context.Context    //当前生效的ctx
	cancelFunc context.CancelFunc //关闭ctx的function
	bt         define.Beater      //绑定beater，以获取beater的状态数据

	cfg                   *common.Config //生效中的全局配置
	config                *configs.Config
	hasChildPath          bool                           //是否有子任务配置标志位
	tasks                 []define.TaskConfig            //全部任务的列表,该列表由全局任务和子任务整合后生成
	globalTasks           []define.TaskConfig            //全局配置里面的任务列表
	childMetaTasks        []*configs.ChildTaskMetaConfig //子任务列表，是还未被清洗时的存储
	correctChildMetaTasks []*configs.ChildTaskMetaConfig //正确的子任务列表，子配置心跳会用到
	repeatChildMetaTasks  []*configs.ChildTaskMetaConfig //重复的子任务列表，子配置心跳会用到
	wrongChildMetaTasks   []*configs.ChildTaskMetaConfig //错误的子任务列表,子配置心跳会用到
	errorChildMetaTasks   []*configs.ChildTaskMetaConfig //读取失败的子任务列表,子配置心跳会用到

	heartbeatLock sync.Mutex   //心跳数据读写锁
	heartbeatInfo define.Event //用于上传的心跳数据,全局配置部分
}

// NewBaseConfigEngine 获取configEngine
func NewBaseConfigEngine(ctx context.Context) define.ConfigEngine {
	logger.Debug("call NewBaseConfigEngine")
	baseConfig := new(BaseConfigEngine)
	baseConfig.ctx, baseConfig.cancelFunc = context.WithCancel(ctx)
	return baseConfig
}

// Init 配置引擎初始化
func (ce *BaseConfigEngine) Init(cfg *common.Config, bt define.Beater) error {
	logger.Debug("call Init")
	var err error

	// 参数绑定并生成队列
	ce.cfg = cfg
	ce.bt = bt
	ce.tasks = make([]define.TaskConfig, 0)
	ce.globalTasks = make([]define.TaskConfig, 0)
	ce.childMetaTasks = make([]*configs.ChildTaskMetaConfig, 0)
	ce.correctChildMetaTasks = make([]*configs.ChildTaskMetaConfig, 0)
	ce.repeatChildMetaTasks = make([]*configs.ChildTaskMetaConfig, 0)
	ce.wrongChildMetaTasks = make([]*configs.ChildTaskMetaConfig, 0)

	// 获取全局config
	baseConfig := configs.NewConfig()
	err = cfg.Unpack(baseConfig)
	if err != nil {
		return define.ErrUnpackCfgError
	}
	ce.config = baseConfig

	// 检查全局配置,这一步如果有任何错误，则整个采集器会报错并关闭
	err = baseConfig.Clean()
	if err != nil {
		return fmt.Errorf("%s : %w", define.ErrCleanGlobalFail, err)
	}
	// 存储全局配置中的任务
	ce.globalTasks = baseConfig.GetTaskConfigList()

	// 获取子配置中的任务
	err = ce.GetChildTasks()
	if err != nil {
		return define.ErrGetChildTasks
	}

	return nil
}

// GetTaskNum 获取配置正确的任务数
func (ce *BaseConfigEngine) GetTaskNum() int {
	if ce.tasks == nil {
		return 0
	}
	return len(ce.tasks)
}

// HasChildPath 判断是否有子任务路径
func (ce *BaseConfigEngine) HasChildPath() bool {
	return ce.hasChildPath
}

// GetWrongTaskNum 获取配置出错的任务数
func (ce *BaseConfigEngine) GetWrongTaskNum() int {
	if ce.wrongChildMetaTasks == nil || ce.errorChildMetaTasks == nil {
		return 0
	}
	return len(ce.wrongChildMetaTasks) + len(ce.errorChildMetaTasks)
}

// GetChildTasks 获取全部子任务的方案
func (ce *BaseConfigEngine) GetChildTasks() error {
	logger.Info("start to get child tasks,searching for childPath(bkmonitorbeat.include)")
	// 获取子配置的路径
	childPath, err := ce.cfg.String("include", 0)
	if err != nil || childPath == "" {
		ce.hasChildPath = false
		logger.Info("childPath(bkmonitorbeat.include) not set")
		// 没有配置子任务目录是允许的，所以返回nil
		return nil
	}
	ce.hasChildPath = true

	// 获取文件状态，用来确认文件是否是文件夹，不是文件夹则直接退出
	stat, err := os.Stat(childPath)
	if err != nil {
		// 打开子配置文件夹出错，就不加载子任务
		// 如果只是沿用旧版本的采集配置,会有此种情况发生,所以算作正常，只进行错误提示,但不返回error
		return nil
	}
	if !stat.IsDir() {
		return fmt.Errorf("child path is not a directory")
	}

	err = ce.GetTasksFromDir(childPath)
	if err != nil {
		return err
	}
	return nil
}

// GetTasksFromDir 从文件夹中提取任务,使用递归逻辑处理多层文件夹
func (ce *BaseConfigEngine) GetTasksFromDir(dirPath string) error {

	// 读取文件夹下的文件列表
	fileList, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}

	// 获取目录的绝对路径，若配置文件中为相对路径则以当前路径为前缀修饰
	basePath, err := filepath.Abs(dirPath)
	if err != nil {
		return err
	}

	// 遍历文件列表，获取各子配置文件中的任务
	for _, file := range fileList {
		multiDir, err := ce.cfg.Bool("multi_child_dir", 0)
		if err != nil {
			multiDir = false
		}
		logger.Infof("multi_child_dir state:%v", multiDir)
		// 如果目标是文件夹,则进入文件夹内部寻找任务文件
		if file.IsDir() {
			// 状态位监测,为false则跳过子文件夹的检查
			if multiDir {
				err := ce.GetTasksFromDir(filepath.Join(basePath, file.Name()))
				if err != nil {
					logger.Errorf("get inner task failed,error:%v", err)
				}

			}
			continue
		}
		// 如果不是文件夹，就直接分析文件
		taskConfig, err := ce.GetChildTask(file, basePath)
		if err != nil {
			// 如果有错误，则获取失败，但是仍记录一条心跳到heartbeatlist,这条心跳只上传错误任务的配置文件路径，以及错误代码
			//path := filepath.Join(basePath, file.Name())
			//ce.errorChildMetaTasks = append(ce.errorChildMetaTasks, &configs.ChildTaskMetaConfig{Path: path})
			continue
		}
		// 如果没有错误，就直接将获得的任务加入到childtask列表中,提供给CleanTask进行整合
		// 这里没有添加成功任务的心跳，因为没有进行Clean。添加这个心跳的工作将在CleanTask中完成
		ce.childMetaTasks = append(ce.childMetaTasks, taskConfig)
	}
	return nil
}

// GetChildTask 通过文件信息获取子任务
func (ce *BaseConfigEngine) GetChildTask(file os.DirEntry, basePath string) (*configs.ChildTaskMetaConfig, error) {
	getTaskFailError := define.ErrGetTaskFailed

	// 获取子配置文件路径
	absPath := filepath.Join(basePath, file.Name())
	logger.Infof("child yaml absolute path:%v", absPath)
	buf, err := os.ReadFile(absPath)
	if err != nil || len(buf) == 0 {
		logger.Errorf("read file content failed,error:%v", err)
		return nil, getTaskFailError
	}
	logger.Debugf("child yaml content:%v", string(buf))
	ucfgConfig, err := ce.ParseToUcfg(buf)
	if err != nil {
		logger.Errorf("parse file content to ucfg failed,error:%v", err)
		return nil, getTaskFailError
	}
	basicConfig, err := ce.GetBasicMetaConfig(ucfgConfig)
	if err != nil {
		logger.Errorf("get basicConfig failed,error:%v", err)
		return nil, getTaskFailError
	}
	childMetaConfig, err := ce.FillConfig(basicConfig, ucfgConfig, absPath)
	if err != nil {
		logger.Errorf("fill childMetaConfig failed,error:%v", err)
		return nil, getTaskFailError
	}
	return childMetaConfig, nil

}

// ParseToUcfg 通过输入的字符串解析出一个ucfg.Config对象，用来进一步转换出指定的TaskConfig
func (ce *BaseConfigEngine) ParseToUcfg(buf []byte) (*ucfg.Config, error) {
	return yaml.NewConfig(buf)
}

// GetBasicMetaConfig 根据配置信息获取到基础的MetaConfig,作为后面对yaml文件反序列化的载体
func (ce *BaseConfigEngine) GetBasicMetaConfig(ucfgConfig *ucfg.Config) (define.TaskMetaConfig, error) {
	// 从对象中获取任务类型
	cfgType, err := ucfgConfig.String("type", 0)
	if err != nil {
		logger.Errorf("get task type failed,error:%v", err)
		return nil, err
	}
	// 通过任务类型从工厂中取出指定类型的任务
	taskMetaConfig, err := taskfactory.GetTaskConfigByName(cfgType)
	if err != nil {
		logger.Errorf("get task by type failed,error:%v", err)
		return nil, err
	}
	return taskMetaConfig, nil
}

// FillConfig 将yaml文件中的参数填充到空的metaconfig中
func (ce *BaseConfigEngine) FillConfig(taskMetaConfig define.TaskMetaConfig, ucfgConfig *ucfg.Config, absPath string) (*configs.ChildTaskMetaConfig, error) {
	var err error
	// 在taskconfig外层增加装饰，以存储version,path,name和taskid
	childTaskConfig := new(configs.ChildTaskMetaConfig)
	childTaskConfig.TaskMetaConfig = taskMetaConfig
	childTaskConfig.Path = absPath
	// 读取文件数据，写入空任务对象中
	err = ucfgConfig.Unpack(childTaskConfig)
	if err != nil {
		logger.Errorf("unpack child failed,error:%v", err)
		return nil, err
	}

	// 返回子任务的对象
	return childTaskConfig, nil
}

// ReInit 重载配置
func (ce *BaseConfigEngine) ReInit(cfg *common.Config) error {
	logger.Debug("call ReInit")
	return ce.Init(cfg, ce.bt)
}

// GetTaskConfigList 获取可执行任务集合
func (ce *BaseConfigEngine) GetTaskConfigList() []define.TaskConfig {
	logger.Debug("call GetTaskConfigList")
	return ce.tasks
}

// CleanTaskConfigList 校验任务，将校验通过的任务存入tasks中,不通过的存入wrongTasks
func (ce *BaseConfigEngine) CleanTaskConfigList() error {
	logger.Debug("call CleanTaskConfigList")

	// 从全局配置中先获取到一些任务
	ce.tasks = ce.globalTasks

	// 检查子任务，检查通过的子任务会被加入到正式执行任务队列,但是如果子任务有错误也不会造成采集器退出，而是记录错误心跳
	ce.checkChildError()

	// 将全部通过的任务进行一次去重，以ident为标识
	ce.checkConfigRepeat()

	return nil
}

func (ce *BaseConfigEngine) checkChildError() {
	var err error
	childRepeatMap := make(map[string]int)
	for _, childTask := range ce.childMetaTasks {
		if err = childTask.Clean(); err != nil {
			logger.Infof("wrong child config,path:%v", childTask.Path)
			// wrongChildTasks记录了所有出错的子任务
			//ce.wrongChildMetaTasks = append(ce.wrongChildMetaTasks, childTask)
		} else {
			logger.Infof("correct child config,path:%v", childTask.Path)

			// 获取校验通过的子任务
			taskList := childTask.TaskMetaConfig.GetTaskConfigList()

			// 先进行查重
			repeatFlag := false
			for _, task := range taskList {
				if _, ok := childRepeatMap[task.GetIdent()]; ok {
					repeatFlag = true
				}
				childRepeatMap[task.GetIdent()] = 1
			}

			// 查重不通过则加入重复任务队列
			if repeatFlag {
				ce.repeatChildMetaTasks = append(ce.repeatChildMetaTasks, childTask)
				continue
			}

			// 查重通过则加入正确任务队列
			for _, task := range taskList {
				ce.tasks = append(ce.tasks, task)
			}
			// correctChildTasks记录了所有正确的子任务
			ce.correctChildMetaTasks = append(ce.correctChildMetaTasks, childTask)

		}

	}
}

func (ce *BaseConfigEngine) checkConfigRepeat() {
	repeatMap := make(map[string]define.TaskConfig)
	typeMap := make(map[string]int)
	for _, v := range ce.tasks {
		if _, ok := repeatMap[v.GetIdent()]; !ok {
			repeatMap[v.GetIdent()] = v

			if preNum, ok := typeMap[v.GetType()]; ok {
				typeMap[v.GetType()] = preNum + 1
			} else {
				typeMap[v.GetType()] = 1
			}
		}

	}

	// 将去重后的任务列表重新赋值给ce.tasks
	tasks := make([]define.TaskConfig, 0)
	for _, v := range repeatMap {
		tasks = append(tasks, v)
	}
	ce.tasks = tasks

	// 输出整体任务信息
	for k, v := range typeMap {
		logger.Infof("get %v %v tasks", v, k)
	}
}

// RefreshHeartBeat 更新心跳数据，包括一个全局心跳数据和一列任务心跳数据
// 目前只更新全局心跳数据
func (ce *BaseConfigEngine) RefreshHeartBeat() error {
	logger.Debug("call RefreshHeartBeat")
	// 刷新全局心跳
	ce.refreshGlobalHeartBeat()

	// 刷新子任务心跳
	ce.refreshChildHeartBeat()
	return nil
}

func (ce *BaseConfigEngine) refreshGlobalHeartBeat() {
	ce.heartbeatLock.Lock()
	defer ce.heartbeatLock.Unlock()
	bt, ok := ce.bt.(*MonitorBeater)
	if !ok {
		logger.Errorf("get beater failed")
		return
	}
	ce.heartbeatInfo = NewGlobalHeartBeatEvent(bt)

}

func (ce *BaseConfigEngine) refreshChildHeartBeat() {}

// SendHeartBeat 发送心跳数据，包括一个全局心跳数据和一列任务心跳数据
// 目前只发送全局心跳数据
func (ce *BaseConfigEngine) SendHeartBeat() error {
	logger.Debug("call SendHeartBeat")
	ce.heartbeatLock.Lock()
	defer ce.heartbeatLock.Unlock()
	ok := beat.Send(ce.heartbeatInfo.AsMapStr())
	if !ok {
		return fmt.Errorf("publish failed")
	}

	return nil
}

// GetGlobalConfig 获取全局配置信息
func (ce *BaseConfigEngine) GetGlobalConfig() define.Config {
	logger.Debug("call GetGlobalConfig")
	return ce.config
}
