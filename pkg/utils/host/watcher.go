// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	BkCloudIDKey     = "bk_cloud_id"
	BkHostIDKey      = "bk_host_id"
	BkHostInnerIPKey = "bk_host_innerip"
	BkBizIDKey       = "bk_biz_id"
	BkSetIDKey       = "bk_set_id"
	BkModuleIDKey    = "bk_module_id"
	BkObjectIDKey    = "bk_obj_id"
	BkInstIDKey      = "bk_inst_id"
	BkTenantIDKey    = "tenant_id"
	BkDataIDKey      = "dataid"
	CustomerToposKey = "layer"
	AssociationsKey  = "associations"
	ChildLayerKey    = "child"

	DefaultLength = 10
)

var (
	ErrGetAssociationFailed = errors.New("get association content failed")
	ErrFileNotExist         = errors.New("host id file not exist")
	ErrParseFileFailed      = errors.New("parse host id file failed")
	ErrParseHostInfoFailed  = errors.New("translate info(number to int) failed")

	// 标志位 除了这几个 key 以外的数据都被过滤
	basicNodeList = map[string]int{
		BkBizIDKey:       1,
		BkSetIDKey:       1,
		BkModuleIDKey:    1,
		CustomerToposKey: 1,
	}
)

// Info Info数据载体
type Info []map[string]interface{}

// Watcher :
type Watcher interface {
	Start() error
	Stop()
	Reload(ctx context.Context, address string, length int, mustFileExist bool) error
	GetInfo() (Info, error)
	GetInfoByLevelID(name string, id int) (Info, error)
	GetInfoByCloudIdAndIp(bkCloudId, bkInnerIp string) (Info, error)
	GetUpdateTime() time.Time
	GetBizId() int64
	GetCloudId() string
	GetHostId() int32
	GetHostInnerIp() string
	GetTenantID() string
	GetStaticDataID() int32
	UpdateOnce() error
	Notify() <-chan struct{}
}

var DefaultCMDBLevel = map[string]bool{
	"set":    true,
	"module": true,
	"biz":    true,
}

type emptyWatcher struct {
	t time.Time
}

// NewEmptyWatcher 空 watcher，mock 掉所有 watcher 功能
func NewEmptyWatcher() Watcher {
	return &emptyWatcher{t: time.Now()}
}

func (w *emptyWatcher) Start() error { return nil }

func (w *emptyWatcher) Stop() {}

func (w *emptyWatcher) Reload(ctx context.Context, address string, length int, mustFileExist bool) error {
	return nil
}

func (w *emptyWatcher) GetInfo() (Info, error) {
	return Info{}, nil
}

func (w *emptyWatcher) GetUpdateTime() time.Time {
	return w.t
}

func (w *emptyWatcher) GetInfoByLevelID(name string, id int) (Info, error) {
	return make(Info, 0), nil
}

func (w *emptyWatcher) GetInfoByCloudIdAndIp(bkCloudId, bkInnerIp string) (Info, error) {
	return make(Info, 0), nil
}

func (w *emptyWatcher) GetBizId() int64 {
	return 0
}

func (w *emptyWatcher) GetCloudId() string {
	return "0"
}

func (w *emptyWatcher) GetHostId() int32 {
	return 0
}

func (w *emptyWatcher) GetHostInnerIp() string {
	return "127.0.0.1"
}

func (w *emptyWatcher) UpdateOnce() error {
	return nil
}

func (w *emptyWatcher) Notify() <-chan struct{} {
	return nil
}

func (w *emptyWatcher) GetTenantID() string { return "" }

func (w *emptyWatcher) GetStaticDataID() int32 { return 0 }

// idWatcher :
type idWatcher struct {
	ctx            context.Context
	cancel         context.CancelFunc
	notifyList     []chan struct{}
	notifyListLock sync.Mutex

	bkHostInnerIP string
	bkCloudID     string
	bkHostID      int32
	bkBizID       int64
	bkTenantID    string
	bkDataID      int32

	Info     Info
	hostLock sync.RWMutex

	filePath           string
	cmdbLevelMaxLength int

	mustFileExist bool      // 配置状态位 该位为 true 要求一定 hostid 文件一定存在
	fileNotExist  bool      // 运行时状态位 判断 hostid 文件是否存在
	inUse         bool      // 运行时状态位 判断当前 hostid 是否解析成功
	t             time.Time // 更新时间
}

// Config host文件读取配置
type Config struct {
	HostIDPath         string `config:"host_id_path"`
	CMDBLevelMaxLength int    `config:"cmdb_level_max_length"`
	IgnoreCmdbLevel    bool   `config:"ignore_cmdb_level"`
	MustHostIDExist    bool   `config:"must_host_id_exist"`
}

// NewWatcher 提供一个Info，并启动监听
func NewWatcher(ctx context.Context, c Config) Watcher {
	// 如果关闭了hostid上报，使用假的watcher替代真的
	if c.IgnoreCmdbLevel {
		return NewEmptyWatcher()
	}
	w := new(idWatcher)
	w.ctx, w.cancel = context.WithCancel(ctx)

	// 处理默认值
	if c.HostIDPath == "" {
		c.HostIDPath = DefaultPath
	}
	if c.CMDBLevelMaxLength == 0 {
		c.CMDBLevelMaxLength = DefaultLength
	}

	w.mustFileExist = c.MustHostIDExist
	w.filePath = c.HostIDPath
	w.cmdbLevelMaxLength = c.CMDBLevelMaxLength
	w.notifyList = make([]chan struct{}, 0)

	return w
}

// Start
func (w *idWatcher) Start() error {
	err := w.startWatch()
	if err != nil {
		return fmt.Errorf("start host id watch failed, error: %s, file path: %s", err, w.filePath)
	}
	return nil
}

// GetInfo
func (w *idWatcher) GetInfo() (Info, error) {
	w.hostLock.RLock()
	defer w.hostLock.RUnlock()

	// 如果文件不存在的状态位为 true,此时抛出错误
	if w.fileNotExist {
		return nil, ErrFileNotExist
	}
	// 文件解析失败也抛出错误
	if !w.inUse {
		return nil, ErrParseFileFailed
	}
	return w.Info, nil
}

func (w *idWatcher) GetUpdateTime() time.Time {
	return w.t
}

func (w *idWatcher) GetBizId() int64 {
	return w.bkBizID
}

func (w *idWatcher) GetCloudId() string {
	return w.bkCloudID
}

func (w *idWatcher) GetHostId() int32 {
	return w.bkHostID
}

func (w *idWatcher) GetHostInnerIp() string {
	return w.bkHostInnerIP
}

func (w *idWatcher) GetTenantID() string { return w.bkTenantID }

func (w *idWatcher) GetStaticDataID() int32 { return w.bkDataID }

func (w *idWatcher) changeInfo(info Info) {
	// 成功修改则置为true
	w.inUse = true
	w.Info = info
	w.t = time.Now()
	for _, m := range info {
		if v, ok := m[BkBizIDKey]; ok {
			if bizID, ok := v.(int64); ok {
				w.bkBizID = bizID
				break
			}
		}
	}
}

// Stop
func (w *idWatcher) Stop() {
	w.cancel()
}

// Reload reload 失败会导致监听停止
func (w *idWatcher) Reload(ctx context.Context, filePath string, cmdbLevelMaxLength int, mustFileExist bool) error {
	// 关闭旧的循环
	if w.cancel != nil {
		// 此处会触发已有的watcher任务关闭
		w.cancel()
	}

	// 重新初始化 watcher
	w.ctx, w.cancel = context.WithCancel(ctx)

	// 处理默认值
	if filePath == "" {
		filePath = DefaultPath
	}
	if cmdbLevelMaxLength == 0 {
		cmdbLevelMaxLength = DefaultLength
	}
	w.filePath = filePath
	w.cmdbLevelMaxLength = cmdbLevelMaxLength
	w.mustFileExist = mustFileExist
	err := w.startWatch()
	if err != nil {
		logger.Warnf("try to start watch failed,filepath:%s,cmdb_max_length:%d,err:%s", filePath,
			cmdbLevelMaxLength, err.Error())
		return fmt.Errorf("start host id watch failed, error: %s,file path: %s", err, filePath)
	}
	return nil
}

func (w *idWatcher) UpdateOnce() error {
	_, err := os.Stat(w.filePath)
	if err != nil {
		// 允许文件不存在，则不报错,否则报错
		if os.IsNotExist(err) && w.mustFileExist {
			logger.Warnf("add file path into watcher failed, path: %s, err: %s", w.filePath, err.Error())
			return err
		}
		logger.Warnf("add file path into watcher failed,path:%s, err:%s", w.filePath, err.Error())
		w.fileNotExist = true
	} else {
		w.fileNotExist = false
	}

	// 初始化先更新一个Info
	if !w.fileNotExist {
		err = w.updateInfo()
		if err != nil {
			logger.Warnf("update first host id failed,err:%s", err.Error())
		}
	}

	return nil
}

// 启动监听，并分析文件，更新数据
func (w *idWatcher) startWatch() error {
	if err := w.UpdateOnce(); err != nil {
		return err
	}

	// 开始持续监听
	go w.loopWatch()
	return nil
}

// loopWatch 监听hostid文件变化
// 每10秒，检查一次hostid文件，发现ModifyTime变更后，则触发一次更新
func (w *idWatcher) loopWatch() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var prevModeTime time.Time
	for {
		select {
		case <-w.ctx.Done():
			logger.Warn("get ctx done,return")
			return
		case <-ticker.C:
			fileInfo, err := os.Stat(w.filePath)
			if err != nil {
				w.fileNotExist = true
				continue
			}

			w.fileNotExist = false
			modTime := fileInfo.ModTime()
			if modTime != prevModeTime {
				prevModeTime = modTime

				// time.C触发后，更新hostid信息
				err = w.updateInfo()
				if err != nil {
					logger.Warnf("update host id failed,err:%s", err.Error())
				}
			}
		}
	}
}

// 从associations里获取cmdb_level
func (w *idWatcher) getInfoFromAssociations(associations map[string]interface{}) (Info, error) {
	// 原拓扑是个字典，所以这里转换为列表
	list := make(Info, 0, len(associations))
	for _, v := range associations {
		if meta, ok := v.(map[string]interface{}); ok {
			// 只获取associations里指定key的数据，其他的过滤掉
			logger.Debugf("before delete node: %v", meta)
			w.deleteNode(meta)
			logger.Debugf("after delete node: %v", meta)

			// 将number类型转换为实际的int
			if !w.formatNumber(meta) {
				// 转换失败则跳过该条
				logger.Warnf("format number failed,may cache some wrong type,meta:%v", meta)
				continue
			}
			// 处理topos这个key下面的自定义节点
			if !w.formatTopos(meta) {
				logger.Warnf("format topos failed,convert type failed or missing required value,meta:%v", meta)
				continue
			}
			// 将处理好的数据添加到列表
			list = append(list, meta)
			// 控制list长度最大值,超过长度就退出循环,直接输出
			if len(list) >= w.cmdbLevelMaxLength {
				logger.Warnf("cmdb level reached max length,use limited cmdb level,max length:%d", w.cmdbLevelMaxLength)
				break
			}
		} else {
			logger.Warnf("convert failed,wrong data:%v", v)
		}
	}
	return list, nil
}

// sendInfo 真正更新Info的方法
func (w *idWatcher) updateInfo() error {
	w.hostLock.Lock()
	defer w.hostLock.Unlock()
	// 先将可用置为false
	w.inUse = false

	hostIdInfo, err := w.getHostIdInfoFromFile()
	if err != nil {
		logger.Warn("get info from hostid file error: %s", err.Error())
		return err
	}

	bkHostInnerIP, ok := hostIdInfo[BkHostInnerIPKey].(string)
	if !ok {
		logger.Warnf("find bk_host_innerip data failed, info value: %v", hostIdInfo)
		bkHostInnerIP = ""
	}
	if strings.Contains(bkHostInnerIP, ",") {
		bkHostInnerIP = strings.Split(bkHostInnerIP, ",")[0]
	}
	w.bkHostInnerIP = bkHostInnerIP

	bkCloudID, ok := hostIdInfo[BkCloudIDKey].(int64)
	if !ok {
		logger.Warnf("find bk_cloud_id data failed, info value:%v", hostIdInfo)
		bkCloudID = 0
	}
	w.bkCloudID = strconv.FormatInt(bkCloudID, 10)

	bkHostID, ok := hostIdInfo[BkHostIDKey].(int64)
	if !ok {
		logger.Warnf("find bk_host_id data failed, info value:%v", hostIdInfo)
		bkHostID = 0
	}
	w.bkHostID = int32(bkHostID)

	bkTenantID, ok := hostIdInfo[BkTenantIDKey].(string)
	if !ok {
		logger.Warnf("find tanant_id data failed, info value:%v", hostIdInfo)
		bkTenantID = ""
	}
	w.bkTenantID = bkTenantID

	bkDataID, ok := hostIdInfo[BkDataIDKey].(int64)
	if !ok {
		logger.Warnf("find dataid data failed, info value:%v", hostIdInfo)
		bkDataID = 0
	}
	w.bkDataID = int32(bkDataID)

	// 获取associations，这里存放的就是拓扑
	associations, ok := hostIdInfo[AssociationsKey].(map[string]interface{})
	if !ok {
		logger.Warnf("find and convert associations data failed,info value:%v", hostIdInfo)
		return ErrGetAssociationFailed
	}

	// 分析文件，获取cmdb_level
	topoLinkInfoList, err := w.getInfoFromAssociations(associations)
	if err != nil {
		logger.Warnf("get error while anaylize host_id info,info:%s,err:%s", topoLinkInfoList, err.Error())
		return err
	}
	// 更新使用中的cmdb_level
	w.changeInfo(topoLinkInfoList)
	logger.Infof("update host info from path->[%s] success", w.filePath)
	w.notifyAll()
	logger.Debugf("host watcher update hostIdInfo: %+v result: %+v", hostIdInfo, w)
	return nil
}

func (w *idWatcher) notifyAll() {
	w.notifyListLock.Lock()
	defer w.notifyListLock.Unlock()
	s := struct{}{}
	for _, notify := range w.notifyList {
		select {
		case notify <- s:
		default:
		}
	}
	w.notifyList = nil
}

func (w *idWatcher) getHostIdInfoFromFile() (map[string]interface{}, error) {
	// 读取文件
	buf, err := os.ReadFile(w.filePath)
	if err != nil {
		logger.Warnf("get error while read host id file,file path:%s,err:%s", w.filePath, err.Error())
		return nil, err
	}
	return w.parseHostIdInfo(buf)
}

func (w *idWatcher) parseHostIdInfo(buf []byte) (map[string]interface{}, error) {
	var (
		err  error
		info map[string]interface{}
	)
	decoder := json.NewDecoder(bytes.NewReader(buf))
	// 将所有数字转换为json.Number类型
	// 在后面的步骤中会将json.Number转换成int类型
	// 使用这个步骤的原因是decode的默认逻辑会将数字转换为float64
	decoder.UseNumber()
	// 将解析得到的字符串
	err = decoder.Decode(&info)
	logger.Debugf("now get info: %v", info)

	if err != nil {
		logger.Warnf("decode host id file failed,input:%s,err:%s", buf, err.Error())
		return nil, err
	}
	// 将number类型转换为实际的int
	if !w.formatNumber(info) {
		// 转换失败则跳过该条
		logger.Warnf("format number failed,may cache some wrong type,info:%v", info)
		return nil, ErrParseHostInfoFailed
	}
	return info, nil
}

func (w *idWatcher) deleteNode(meta map[string]interface{}) {
	for k := range meta {
		// 如果不在处理的NodeList里，就删掉这个数据
		_, ok := basicNodeList[k]
		if !ok {
			delete(meta, k)
		}
	}
}

func (w *idWatcher) formatNumber(meta map[string]interface{}) bool {
	logger.Debugf("input meta:%v", meta)
	for k, v := range meta {
		item, ok := v.(json.Number)
		// 不是number就不管
		if ok {
			// 转成int不报错就算它是int,float型转int会error
			if i, err := item.Int64(); err == nil {
				meta[k] = i
				continue
			}
			logger.Warnf("convert number to int failed,number:%s", item)
			// 全都匹配失败是有问题的
			return false
		}
	}
	return true
}

// 根据要求的格式处理topos，只留需要的字段
func (w *idWatcher) formatTopos(meta map[string]interface{}) bool {
	// 只要是处理完了topos的内容，则需要将layer这个层级清理了
	// 因为此时已经将自定义层级的内容打平提升到和内容内容同一个位置上
	defer func() {
		if _, ok := meta[CustomerToposKey]; ok {
			delete(meta, CustomerToposKey)
		}
		logger.Debugf("all level is check now, meta->[%v]", meta)
	}()
	var ok bool

	// 没有自定义拓扑的情况是正常的，此时可以直接忽略返回
	topos, ok := meta[CustomerToposKey].(map[string]interface{})
	if !ok {
		logger.Debugf("no layer in meta,meta:%v, no custom layer will added.", meta)
		return true
	}

	// 遍历递归获取topos下及其所有child所有内容，只关注bk_inst_id和bk_obj_id这两个内容
	var (
		currentObjectID   string
		currentInstanceID json.Number
		tempInt           int64
		err               error
	)

	currentLevelTopo := topos // 初始化当前层级的记录
	for {
		if currentObjectID, ok = currentLevelTopo[BkObjectIDKey].(string); !ok {
			logger.Warnf("failed to get object id for current topo is: %v, maybe ask cmdb for help", currentLevelTopo)
			return false
		}

		if currentInstanceID, ok = currentLevelTopo[BkInstIDKey].(json.Number); !ok {
			logger.Warnf("failed to get instance id for current topo is: %v, maybe ask cmdb for help", currentLevelTopo)
			return false
		}

		// 将该内容实例ID和对象ID追加到meta中
		if tempInt, err = currentInstanceID.Int64(); err != nil {
			logger.Warnf("failed to trans instanceID to int64, will jump it.")
			return false
		}

		meta[currentObjectID] = tempInt
		logger.Debugf("got new objectID->[%s] instanceID->[%v] and mate now is: %v", currentObjectID, currentInstanceID, meta)

		// 当层遍历完成后，需要关注下一个层级的child是否仍然存在，如果存在，需要继续递归遍历
		if childValue, ok := currentLevelTopo[ChildLayerKey].(map[string]interface{}); ok && childValue != nil {
			// 此时表示可以拿到下一个层级的内容，需要继续递归获取判断
			currentLevelTopo = childValue
			logger.Debugf("still got Child layer->[%v], will continue.", currentLevelTopo)
			continue
		}

		// 如果没法continue，表示此时的递归已经完成，可以退出
		break
	}

	return true
}

// GetInfoByLevelID: 根据提供的层级名及层级ID，返回对应的Info信息
func (w *idWatcher) GetInfoByLevelID(name string, id int) (Info, error) {
	var (
		info    = make(Info, 0)
		allInfo Info
		err     error
		levelID interface{}
		exists  bool
	)

	if allInfo, err = w.GetInfo(); err != nil {
		logger.Errorf("failed to get info for->[%s] no cmdb_level will return", err)
		return info, err
	}

	// 1. 遍历当前所有的Info
	for _, currentInfo := range allInfo {
		// 2. 判断是否存在需要的层级信息

		// 如果是默认层级，需要增加BK开头和ID的结尾
		// 如果第二次遍历的时候，此时已经增加上了bk开头，所以不会命中，将会保持bk_xx_id搜索
		if _, isDefaultLevel := DefaultCMDBLevel[name]; isDefaultLevel {
			name = "bk_" + name + "_id"
			logger.Debugf("level->[%s] is change to default level format.", name)
		}

		if levelID, exists = currentInfo[name]; !exists {
			logger.Debugf("level->[%s] is not exists in info->[%v], will try next one", name, currentInfo)
			continue
		}

		// 判断层级ID是否符合要求的
		if levelIDInt, ok := levelID.(int64); !ok || levelIDInt != int64(id) {
			logger.Infof("is failed to convert levelID to string->[%t] or levelID->[%d] not match target_id->[%d]",
				ok, levelID, id)
			continue
		}

		// 给topoLink中补充IP这一层的信息
		currentInfo[BkCloudIDKey] = w.bkCloudID
		currentInfo[BkHostInnerIPKey] = w.bkHostInnerIP
		// 3. 追加信息
		info = append(info, currentInfo)
		logger.Debugf("match level->[%s] and id->[%d] now total->[%d]", name, id, len(info))
	}

	// 4. 返回内容
	return info, nil
}

// GetInfoByLevelID: 根据提供的云区域ID和主机IP，返回对应的Info信息
func (w *idWatcher) GetInfoByCloudIdAndIp(bkCloudId, bkInnerIp string) (Info, error) {
	var (
		info    = make(Info, 0)
		allInfo Info
		err     error
	)

	if allInfo, err = w.GetInfo(); err != nil {
		logger.Errorf("failed to get info for->[%s] no cmdb_level will return", err)
		return info, err
	}

	if w.bkCloudID != bkCloudId || w.bkHostInnerIP != bkInnerIp {
		logger.Debugf("%s->[%s] is not exists in info, or %s->[%s] is not exists in info->[%v]"+
			" will try next one", BkCloudIDKey, bkCloudId, BkHostInnerIPKey, bkInnerIp, allInfo)
	}

	// 1. 遍历当前所有的Info
	logger.Debugf("cmdblevelinfo: %v", allInfo)
	for _, currentInfo := range allInfo {
		// 给topoLink中补充IP这一层的信息
		currentInfo[BkCloudIDKey] = w.bkCloudID
		currentInfo[BkHostInnerIPKey] = w.bkHostInnerIP

		// 2. 追加信息
		info = append(info, currentInfo)
		logger.Debugf("match %s->[%s] and %s->[%s] now total->[%d]",
			BkCloudIDKey, bkCloudId, BkHostInnerIPKey, bkInnerIp, len(info))
	}

	// 3. 返回内容
	return info, nil
}

func (w *idWatcher) Notify() <-chan struct{} {
	w.notifyListLock.Lock()
	defer w.notifyListLock.Unlock()
	notify := make(chan struct{})
	w.notifyList = append(w.notifyList, notify)
	return notify
}
