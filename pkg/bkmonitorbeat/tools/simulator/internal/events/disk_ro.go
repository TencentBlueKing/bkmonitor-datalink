// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package events

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"k8s.io/utils/mount"
)

func MakeDiskRO() {
	log.Println(time.Now(), "make diskspace")
	dirPath := os.Getenv("TEST_PATH")
	raise := os.Getenv("RAISE")
	dirPath, err := filepath.Abs(dirPath)
	if err != nil {
		log.Fatalln(err)
		return
	}

	i := mount.New(dirPath)
	ms, err := i.List()
	if err != nil {
		log.Fatalln(err)
		return
	}
	var targetMountOpt string
	if raise == "1" {
		// RO
		targetMountOpt = "ro"
	} else {
		// RW
		targetMountOpt = "rw"
	}
	for _, m := range ms {
		if dirPath != m.Path {
			continue
		}
		remount := true
		newOpts := make([]string, 0, len(m.Opts))
		for _, o := range m.Opts {
			if o == targetMountOpt {
				remount = false
			} else {
				if o == "rw" || o == "ro" {
					o = targetMountOpt
				}
				newOpts = append(newOpts, o)
			}
		}
		if remount {
			log.Printf("remount: %s, opts: %+v", m.Path, newOpts)
			log.Println("PATH", os.Getenv("PATH"))
			err = i.Unmount(m.Path)
			if err != nil {
				log.Printf("unmount: %s, opts: %+v", m.Path, newOpts)
				log.Fatalln(err)
			}
			err = i.Mount(m.Device, m.Path, m.Type, newOpts)
			if err != nil {
				log.Fatalln(err)
			}
		}
	}
}
