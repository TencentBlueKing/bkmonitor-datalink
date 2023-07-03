// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"go.etcd.io/bbolt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

var (
	cloudID int
	hostIP  string
	dbPath  string
)

const (
	ConfBBoltStorageBucket = "storage.bbolt.bucket"
)

// 操作transfer缓存的行为
const (
	actionPrint   = "print"
	actionCompare = "compare"
	actionDump    = "dump"
)

// versionCmd represents the cmdb command
var cmdbCmd = &cobra.Command{
	Use:   "cmdb",
	Short: "Print cmdb storage info",
	Run: func(cmd *cobra.Command, args []string) {
		conf := config.Configuration
		storeType := conf.GetString(storage.ConfStorageType)
		mapArgs := make(map[string]interface{})

		// 复制db指定path
		targetPath, err := cmd.Flags().GetString("dump_path")
		if err != nil {
			fmt.Println("Warnning: get dump_path error, new bbolt-db will be in the current path")
		}
		isCopy, err := cmd.Flags().GetBool("dump")
		if err != nil {
			fmt.Println("Warning: get args dump error: ", err)
			logging.Warnf("get args dump error:[%s]", err)
		}
		notTable, err := cmd.Flags().GetBool("no_table")
		if err != nil {
			fmt.Println("Warning: get args dump error: ", err)
			logging.Warnf("get args dump error:[%s]", err)
		}

		// 判断是否打印全部
		isPrintAll, _ := cmd.Flags().GetBool("detail")

		// 是否需要和热数据做对比。
		needCompare, _ := cmd.Flags().GetBool("compare")

		isPrintAllCompareInfo, _ := cmd.Flags().GetBool("all_compare")

		isUpdate, _ := cmd.Flags().GetBool("update")

		if err = preRead(storeType, conf, cmd, mapArgs); err != nil {
			fmt.Println("error occured :", err)
			os.Exit(1)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ctx = config.IntoContext(ctx, conf)

		// 触发一个强制更新cmdb的动作
		if isUpdate {
			// 如果要更新缓存，则要开启cc同步
			conf.Set(storage.ConfStopCcCache, false)
			store, err := define.NewStore(ctx, storeType)
			checkError(err, 1, "err connect to %s", err)

			updater := scheduler.NewCCHostUpdater(conf)
			err = updater.UpdateTo(ctx, store)

			checkError(err, 1, "update cc error: %s", err)
			logging.Info("commit cc cache")
			checkFnError(store.Commit, 1, "commit store error")
			// 设置标志位，否则缓存校验未发现此标志位，仍然会等待同步数据。
			flag := define.StoreFlag
			// CC缓存更新时间间隔
			period := conf.GetDuration(scheduler.ConfSchedulerCCCheckIntervalKey)
			// CC缓存超时时间
			flagExpires := conf.GetDuration(scheduler.ConfSchedulerCCCacheExpires) - period
			err = store.Set(flag, []byte("x"), flagExpires)
			checkError(err, 0, "set store flag error:[%s]", err)
		}

		// NewReader
		cmdbReader, err := storage.NewReaderHelper(ctx, storeType)
		checkError(err, 1, "new readerhelper error :%s", err)

		hostCache, instCache, err := cmdbReader.Filter(hostIP, cloudID)
		checkError(err, 1, "read cache error :%s", err)

		_ = cmdbReader.Close()

		err = afterRead(storeType, mapArgs)
		checkError(err, 1, "error ocuured after read cache: %s", err)

		// NewOperator
		cmdbOp := NewCmdbOperator(storeType, hostCache, instCache)
		if cmdbOp == nil {
			fmt.Println("unsuport store type for op")
			os.Exit(1)
		}

		// 判断需要进行什么操作
		if isCopy {
			var bucket string
			if val, ok := mapArgs["bucket"]; ok {
				bucket = val.(string)
			}
			err = cmdbOp.Dump(targetPath, bucket)
			checkError(err, 1, "error dump: %s", err)
		}

		// 与热数据进行对比
		if needCompare || isPrintAllCompareInfo {
			cmdbOp.preOperate(actionCompare)
			addr := fmt.Sprintf("http://%s:%d", conf.GetString(define.ConfHost), conf.GetInt(define.ConfPort))
			memCache, err := cmdbReader.MemCache(addr, storeType)
			checkError(err, 1, "get mem cache error: %s", err)
			if err = cmdbOp.CompareTo(memCache, isPrintAll, notTable, isPrintAllCompareInfo); err != nil {
				fmt.Println(err)
			}
		}

		// 是否打印全部
		if isPrintAll {
			cmdbOp.Print(notTable)
		}

		fmt.Fprintf(os.Stdout, "%d hosts, %d instances\n", len(hostCache), len(instCache))
	},
}

// operator
type cmdbOperator struct {
	hostCache map[string]*define.StoreItem
	instCache map[string]*define.StoreItem
	opMap     map[string]bool
	storeType string
}

// {storeType: [op1, op2],}
var storeOpMap = map[string]map[string]bool{
	"redis": {actionPrint: true, actionCompare: true},
	"bbolt": {actionPrint: true, actionDump: true},
}

// Print: 是否打印表格，是否打印详情
func (o *cmdbOperator) Print(notTable bool) {
	// table append , then print
	table := utils.NewTableUtil(os.Stdout, notTable)
	table.SetHeader([]string{"key", "topo", "expires_at"})

	for key, item := range o.hostCache {
		table.Append([]string{key, string(item.Data), item.ExpiresAt.String()})
	}
	for key, item := range o.instCache {
		table.Append([]string{key, string(item.Data), item.ExpiresAt.String()})
	}

	table.Render()
}

// Dump: 使用bbolt持久化缓存。
func (o *cmdbOperator) Dump(targetPath, bucketName string) error {
	o.preOperate(actionDump)
	// if bbolt, no, warn, and point a path
	// newbbolt, then copy
	store, err := bbolt.Open(targetPath, 0o740, nil)
	if err != nil {
		return fmt.Errorf("error open store:[%s], bucket:[%s], err:%s", targetPath, bucketName, err)
	}
	defer ExecIgnoreErr(store.Close)
	err = store.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		if err != nil {
			logging.Errorf("CreateBucketIfNotExists error : %s", err)
			return err
		}

		for key, item := range o.hostCache {
			val, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if err = bucket.Put([]byte(key), val); err != nil {
				return err
			}
		}
		for key, item := range o.instCache {
			val, err := json.Marshal(item)
			if err != nil {
				return err
			}
			if err = bucket.Put([]byte(key), val); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = os.Remove(targetPath)
		fmt.Println("ERROR! copy new db error: ", err)
		return err
	}
	fmt.Println("copy success!：", targetPath)
	return nil
}

// CompareTo:
func (o *cmdbOperator) CompareTo(cache map[string]*define.StoreItem, isPrintAll, notTable, allCompare bool) error {
	o.preOperate(actionCompare)

	var (
		dbItem       *define.StoreItem
		memHostCount int
		memInstCount int
		diffCount    int
	)

	table := utils.NewTableUtil(os.Stdout, notTable)
	table.SetHeader([]string{"key", "topo", "mem_topo", "expires_at", "mem_expires_at", "difference"})

	for key, item := range cache {
		var difference []string

		// 计数
		if strings.HasPrefix(key, models.HostInfoStorePrefix) {
			memHostCount++
		}
		if strings.HasPrefix(key, models.InstanceInfoStorePrefix) {
			memInstCount++
		}

		// 遍历 memCache, 对比与db中是否有不同，相比db中缺少了多少台
		hostItem, hasHost := o.hostCache[key]
		instItem, hasInst := o.instCache[key]
		// 内存中存在，而db中不存在。
		if !hasHost && !hasInst {
			table.Append([]string{
				key, "not exist", string(item.Data), "not exist",
				item.ExpiresAt.String(), "",
			})
			continue
		}
		if hasHost {
			dbItem = hostItem
		} else {
			dbItem = instItem
		}

		// 都存在，则对比内存和db中的数据
		if string(dbItem.GetData(false)) != string(item.GetData(false)) {
			difference = append(difference, "topo")
		}
		if dbItem.ExpiresAt.String() != item.ExpiresAt.String() {
			difference = append(difference, "expires_at")
		}

		if len(difference) != 0 {
			diffCount++
		}

		// 是否打印对比相同的数据
		if !allCompare && len(difference) == 0 {
			continue
		}

		table.Append([]string{
			key, string(dbItem.Data), string(item.Data), dbItem.ExpiresAt.String(),
			item.ExpiresAt.String(), strings.Join(difference, ","),
		})
	}

	// 判断是否打印详情，如果不只打印异同点，则也直接全部打印。
	if allCompare {
		table.Render()
	} else if isPrintAll {
		table.Render()
	}

	_, err := fmt.Fprintf(os.Stdout, "%d hosts, %d instances in memory; diff count: %d\n", memHostCount, memInstCount, diffCount)
	return err
}

// NewCmdbOperator: 针对transfer的cmdb缓存数据格式的操作集合。
func NewCmdbOperator(storeType string, hostCache, instCache map[string]*define.StoreItem) *cmdbOperator {
	opMap, has := storeOpMap[storeType]
	// not support type
	if !has {
		return nil
	}
	return &cmdbOperator{hostCache, instCache, opMap, storeType}
}

// preOperate: 校验当前storeType是否能进行此操作
func (o *cmdbOperator) preOperate(op string) bool {
	if !o.opMap[op] {
		fmt.Printf("not support operation for cache type :[%s]\n", o.storeType)
		os.Exit(1)
	}
	return o.opMap[op]
}

// preNewReader: 对于某些cmdb缓存需要进行预先处理才可以进行命令
func preRead(name string, conf define.Configuration, cmd *cobra.Command, argMap map[string]interface{}) error {
	// 根据 name 类型，处理conf
	// conf.Set("store.type", name)
	switch name {
	case "bbolt":
		// bbolt 特性，只能支持一个连接，后面的连接会堵塞。而且为了防止破坏用户指定db文件。
		// 所以实现先复制一个临时文件再连接临时文件。
		serviceID := utils.GetServiceID(conf)
		storePath := filepath.Join(conf.GetString(storage.ConfStorageDataDir), fmt.Sprintf("%s-%s.db", define.AppName, serviceID))
		if dbPath != "" {
			storePath = dbPath
		}

		_, err := os.Stat(storePath)
		checkError(err, 0, fmt.Sprintf("path:[%s] not exits", storePath))

		// 复制db到一个临时db
		tmpDir := path.Dir(storePath)
		tmpDB, _ := copyFile(storePath, tmpDir)
		// 设置dbpath
		conf.Set(storage.ConfStorageTaget, tmpDB)
		argMap["path"] = tmpDB
		// 设置bbolt的bucket name
		if bucket := conf.GetString(ConfBBoltStorageBucket); bucket != "" {
			conf.Set(ConfBBoltStorageBucket, bucket)
			argMap["bucket"] = bucket
		}

		// 设置不维护缓存bbolt中的数据
		conf.Set(storage.ConfStopCcCache, true)

	case "redis":
		// 设置不维护缓存bbolt中的数据
		conf.Set(storage.ConfStopCcCache, true)
	default:
		return fmt.Errorf("unsurport store type for read")
	}
	return nil
}

// afterRead:
func afterRead(name string, argMap map[string]interface{}) error {
	switch name {
	case "bbolt":
		// bbolt 需要删除临时文件
		if tmpPath, has := argMap["path"]; has {
			if err := os.Remove(tmpPath.(string)); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyFile: 拷贝一个临时文件。
func copyFile(src, tmpDir string) (string, error) {
	source, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer ExecIgnoreErr(source.Close)

	dst, err := os.CreateTemp(tmpDir, "tmp-db")
	if err != nil {
		return "", err
	}
	defer ExecIgnoreErr(dst.Close)
	_, err = io.Copy(dst, source)
	return dst.Name(), err
}

func ExecIgnoreErr(fn func() error) {
	_ = fn()
}

func init() {
	rootCmd.AddCommand(cmdbCmd)

	flags := cmdbCmd.Flags()
	flags.IntVarP(&cloudID, "cloud_id", "d", -1, "cloud_id for filter")
	flags.StringVarP(&hostIP, "host_ip", "i", "", "host_ip for filter")
	flags.StringVarP(&dbPath, "db_path", "p", "", "bbolt path")
	flags.Bool("detail", false, "print all db info")
	flags.Bool("dump", false, "dump a new db from path")
	flags.Bool("update", false, "update cmdb data to transfer cache store")
	flags.Bool("compare", false, "compare dbdata with memdata")
	flags.Bool("all_compare", false, "show all data detail compare with dbdata")
	flags.String("dump_path", "transfer.cmdb.db.cpfile", "dump path, default:(RFC3339)nowtime.db")
	flags.BoolP("no_table", "t", false, "print without using table")
}
