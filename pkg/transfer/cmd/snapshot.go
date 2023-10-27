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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	httpLib "net/http"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/dghubble/sling"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

type snapshotHelper struct {
	*scheduler.ClusterHelper
	rootDir, snapshotDir string
	logFile              *os.File
	flags                *pflag.FlagSet
}

// CreateRootDir
func (s *snapshotHelper) CreateRootDir() {
	dir, err := os.MkdirTemp("", "transfer-dump")
	if err != nil {
		exitf(-1, "create temporary dir failed: %v", err)
	}
	s.rootDir = dir
}

// RemoveRootDir
func (s *snapshotHelper) RemoveRootDir() {
	utils.CheckError(os.RemoveAll(s.rootDir))
}

// CreateSnapshotDir
func (s *snapshotHelper) CreateSnapshotDir() {
	snapshotDir := path.Join(s.rootDir, "snapshot")
	utils.CheckError(os.Mkdir(snapshotDir, 0o777))
	s.snapshotDir = snapshotDir
}

// CreateSnapshotLog
func (s *snapshotHelper) CreateSnapshotLog() {
	logFile, err := os.Create(path.Join(s.snapshotDir, "snapshot.log"))
	if err != nil {
		exitf(-1, "create snapshot log failed: %v", err)
	}
	s.logFile = logFile
}

// Log
func (s *snapshotHelper) Log(format string, v ...interface{}) {
	_, e := fmt.Fprintf(s.logFile, format, v...)
	utils.CheckError(e)
}

// CloseSnapshotLog
func (s *snapshotHelper) CloseSnapshotLog() {
	utils.CheckError(s.logFile.Close())
}

// CaptureVersion
func (s *snapshotHelper) CaptureVersion() {
	file, err := os.Create(path.Join(s.snapshotDir, "version"))
	if err != nil {
		exitf(-1, "create version file error: %v", err)
	}
	_, err = fmt.Fprintf(file, "version: %s\nhash: %s", define.Version, define.BuildHash)
	if err != nil {
		exitf(-1, "write version file error: %v", err)
	}
}

// CaptureLog
func (s *snapshotHelper) CaptureLog() {
	conf := config.Configuration
	logFile := conf.GetString(logging.ConfOutFile)
	files, err := filepath.Glob(logFile + "*")
	if err != nil {
		return
	}

	fmt.Printf("capturing files...\n")
	for _, f := range files {
		fmt.Printf("\t%s\n", f)
		data, err := os.ReadFile(f)
		if err != nil {
			s.Log("read log %s error: %v\n", f, err)
			continue
		}
		err = os.WriteFile(path.Join(s.snapshotDir, filepath.Base(f)), data, 0o644)
		if err != nil {
			s.Log("write log %s error: %v\n", f, err)
		}
	}
}

// CaptureService
func (s *snapshotHelper) CaptureService(api *sling.Sling, service *define.ServiceInfo, callback func()) {
	defer callback()
	base := api.Base(fmt.Sprintf("http://%s:%d", service.Address, service.Port))

	for _, urlConfig := range []struct {
		url, name string
	}{
		{"/debug/pprof/allocs?debug=2", "allocs.txt"},
		{"/debug/pprof/block?debug=2", "block.txt"},
		{"/debug/pprof/cmdline", "cmdline.dat"},
		{"/debug/pprof/goroutine?debug=2", "goroutine.txt"},
		{"/debug/pprof/heap?debug=2", "heap.txt"},
		{"/debug/pprof/mutex?debug=2", "mutex.txt"},
		{"/debug/pprof/profile?seconds=3", "profile.dat"},
		{"/debug/pprof/threadcreate?debug=2", "threadcreate.txt"},
		{"/debug/pprof/trace?seconds=3", "trace.dat"},
		{"/metrics", "metrics.txt"},
		{"/debug/vars", "vars.json"},
		{"/status/process", "process.json"},
		{"/status/settings", "settings.json"},
	} {
		name := fmt.Sprintf("%s-%s", service.ID, urlConfig.name)
		fmt.Printf("\t%s\n", name)
		request, err := base.Get(urlConfig.url).Request()
		if err != nil {
			s.Log("make %s request error: %v\n", urlConfig.url, err)
			continue
		}

		response, err := httpLib.DefaultClient.Do(request)
		if err != nil {
			s.Log("request to %s error: %v\n", urlConfig.url, err)
			continue
		}

		data, err := io.ReadAll(response.Body)
		if err != nil {
			s.Log("read response from %s error: %v\n", urlConfig.url, err)
			continue
		}
		utils.CheckError(os.WriteFile(path.Join(s.snapshotDir, name), data, 0o644))
	}
}

// Capture
func (s *snapshotHelper) Capture() {
	conf := config.Configuration
	user, password := http.GetBasicAuthInfo(conf)
	services, err := s.ListServices()
	if err != nil {
		s.Log("get cluster service information failed %v", err)
		return
	}

	var wg sync.WaitGroup
	fmt.Printf("capturing status...\n")
	for _, service := range services {
		name := fmt.Sprintf("service-%s.json", service.ID)
		fmt.Printf("\t%s\n", name)
		data, err := json.Marshal(service)
		if err == nil {
			err = os.WriteFile(path.Join(s.snapshotDir, name), data, 0o644)
			if err != nil {
				s.Log("write service status %s error: %v\n", service.ID, err)
			}
		}

		wg.Add(1)
		go s.CaptureService(sling.New().SetBasicAuth(user, password), service, wg.Done)
	}

	leaders, err := s.ListLeaders()
	if err != nil {
		s.Log("get leader service information failed %v", err)
	} else {
		name := "service-leader.txt"
		fmt.Printf("\t%s\n", name)
		var buf bytes.Buffer
		for _, service := range leaders {
			buf.WriteString(service.ID)
			buf.WriteString("\n")
		}
		err = os.WriteFile(path.Join(s.snapshotDir, name), buf.Bytes(), 0o644)
		if err != nil {
			s.Log("write service leader error: %v\n", err)
		}
	}
	wg.Wait()
}

// Pack
func (s *snapshotHelper) Pack(output string) {
	file, err := os.Create(output)
	if err != nil {
		exitf(-1, "create output file %v failed: %v", output, err)
	}
	defer utils.CheckFnError(file.Close)

	gzipWriter := gzip.NewWriter(file)
	defer utils.CheckFnError(gzipWriter.Close)

	tarWriter := tar.NewWriter(gzipWriter)
	defer utils.CheckFnError(tarWriter.Close)

	files, err := os.ReadDir(s.snapshotDir)
	if err != nil {
		exitf(-1, "list files in %s error: %v", s.snapshotDir, err)
	}

	fmt.Printf("packing...\n")
	for _, f := range files {
		fmt.Printf("\t%s\n", f.Name())
		data, err := os.ReadFile(path.Join(s.snapshotDir, f.Name()))
		if err != nil {
			fmt.Printf("read snapshot file %s error %v\n", f.Name(), err)
			continue
		}

		if f.IsDir() {
			continue
		}

		info, err := f.Info()
		if err != nil {
			continue
		}

		err = tarWriter.WriteHeader(&tar.Header{
			Name:    info.Name(),
			Mode:    int64(info.Mode()),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
		if err != nil {
			fmt.Printf("write tar header for %s err %v\n", f.Name(), err)
			continue
		}
		_, err = tarWriter.Write(data)
		if err != nil {
			fmt.Printf("write tar file %s err %v\n", f.Name(), err)
			continue
		}
	}
}

// snapshotCmd represents the snapshot command
var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Capture and dump transfer server info",
	Run: func(cmd *cobra.Command, args []string) {
		clusterHelper, err := scheduler.NewClusterHelper(context.Background(), config.Configuration)
		utils.CheckError(err)

		helper := &snapshotHelper{
			ClusterHelper: clusterHelper,
			flags:         cmd.Flags(),
		}

		helper.CreateRootDir()
		defer helper.RemoveRootDir()

		helper.CreateSnapshotDir()
		helper.CreateSnapshotLog()
		defer helper.CloseSnapshotLog()

		helper.Capture()
		helper.CaptureVersion()
		helper.CaptureLog()

		flags := cmd.Flags()
		name, err := flags.GetString("output")
		if err != nil {
			exitf(-1, "get output file name failed")
		}

		helper.Pack(name)
	},
}

func init() {
	rootCmd.AddCommand(snapshotCmd)
	flags := snapshotCmd.Flags()
	now := time.Now()
	flags.StringP("output", "o",
		fmt.Sprintf("transfer-%s.tar.gz", now.Format("2006.01.02 15-04-05")),
		"output file to dump data",
	)
}
