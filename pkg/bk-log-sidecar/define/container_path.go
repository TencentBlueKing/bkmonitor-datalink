// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//

package define

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/docker/docker/api/types/container"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
)

// ContainerRootPath gey container root path
func ContainerRootPath(c container.InspectResponse) string {
	switch c.Driver {
	case "overlay2":
		return c.GraphDriver.Data["MergedDir"]
	default:
		return fmt.Sprintf("/proc/%d/root", c.State.Pid)
	}
}

// ToHostPath to host path
func ToHostPath(path string) string {
	return filepath.Join(config.HostPath, path)
}

// EvalSymlinks 从容器内递归转换软链，获取日志指向的真实路径。
// 解析相对宿主机根(config.HostPath)，遇到无法 lstat 的路径段直接报错。
func EvalSymlinks(path string) (string, error) {
	return evalSymlinks(path, ToHostPath, false)
}

// splitGlobPrefix 以首个通配符(* ? [)所在路径段为界，拆出可解析的目录前缀与剩余(含通配)后缀。
// 无通配符时返回 (path, "")；通配符出现在第一段时返回 ("", path)。
func splitGlobPrefix(path string) (prefix, suffix string) {
	idx := strings.IndexAny(path, "*?[")
	if idx < 0 {
		return path, ""
	}
	sep := strings.LastIndex(path[:idx], string(filepath.Separator))
	if sep <= 0 {
		return "", path
	}
	return path[:sep], path[sep:]
}

// ResolveSymlinkForMatch 解析采集路径(首个通配符前的目录前缀)在容器 rootfs 内的软链，
// 返回容器视角的真实路径，用于与容器卷挂载重新匹配。无软链或解析失败时原样返回 path。
// rootFs 为容器 rootfs 在宿主机上的路径(已含 config.HostPath 前缀)。
// 采用容错解析：当软链目标落在卷(如 PVC)上、overlay 内 lstat 不到时，停在已解析的目标路径，
// 而非报错，从而得到可用于卷匹配的真实路径。
func ResolveSymlinkForMatch(path, rootFs string) string {
	if rootFs == "" || !filepath.IsAbs(path) {
		return path
	}
	prefix, suffix := splitGlobPrefix(path)
	if prefix == "" {
		return path
	}
	resolved, err := evalSymlinks(prefix, func(p string) string { return filepath.Join(rootFs, p) }, true)
	if err != nil || resolved == "" || resolved == prefix {
		return path
	}
	return resolved + suffix
}

// evalSymlinks 递归解析 path 中的软链。
// toHost 将容器视角路径映射为 sidecar 可访问的宿主机路径；
// tolerant 为 true 时，遇到无法 lstat 的路径段会停止解析并按字面拼接剩余部分(用于卷内目标)。
func evalSymlinks(path string, toHost func(string) string, tolerant bool) (string, error) {
	volLen := 0
	pathSeparator := string(os.PathSeparator)

	if volLen < len(path) && os.IsPathSeparator(path[volLen]) {
		volLen++
	}
	vol := path[:volLen]
	dest := vol
	linksWalked := 0
	for start, end := volLen, volLen; start < len(path); start = end {
		for start < len(path) && os.IsPathSeparator(path[start]) {
			start++
		}
		end = start
		for end < len(path) && !os.IsPathSeparator(path[end]) {
			end++
		}

		// The next path component is in path[start:end].
		if end == start {
			// No more path components.
			break
		} else if path[start:end] == "." {
			// Ignore path component ".".
			continue
		} else if path[start:end] == ".." {
			// Back up to previous component if possible.
			// Note that volLen includes any leading slash.

			// Set r to the index of the last slash in dest,
			// after the volume.
			var r int
			for r = len(dest) - 1; r >= volLen; r-- {
				if os.IsPathSeparator(dest[r]) {
					break
				}
			}
			if r < volLen || dest[r+1:] == ".." {
				// Either path has no slashes
				// (it's empty or just "C:")
				// or it ends in a ".." we had to keep.
				// Either way, keep this "..".
				if len(dest) > volLen {
					dest += pathSeparator
				}
				dest += ".."
			} else {
				// Discard everything since the last slash.
				dest = dest[:r]
			}
			continue
		}

		// Ordinary path component. Add it to result.

		if len(dest) > 0 && !os.IsPathSeparator(dest[len(dest)-1]) {
			dest += pathSeparator
		}

		dest += path[start:end]

		// Resolve symlink.

		fi, err := os.Lstat(toHost(dest))
		if err != nil {
			if tolerant {
				// 无法继续 lstat(如目标落在卷内、overlay 不可见)，剩余部分按字面拼接后返回。
				dest += path[end:]
				return filepath.Clean(dest), nil
			}
			return "", err
		}

		if fi.Mode()&fs.ModeSymlink == 0 {
			if !fi.Mode().IsDir() && end < len(path) {
				if tolerant {
					dest += path[end:]
					return filepath.Clean(dest), nil
				}
				return "", syscall.ENOTDIR
			}
			continue
		}

		// Found symlink.

		linksWalked++
		if linksWalked > 255 {
			return "", errors.New("EvalSymlinks: too many links")
		}

		link, err := os.Readlink(toHost(dest))
		if err != nil {
			if tolerant {
				dest += path[end:]
				return filepath.Clean(dest), nil
			}
			return "", err
		}

		path = link + path[end:]

		if len(link) > 0 && os.IsPathSeparator(link[0]) {
			// Symlink to absolute path.
			dest = link[:1]
			end = 1
		} else {
			// Symlink to relative path; replace last
			// path component in dest.
			var r int
			for r = len(dest) - 1; r >= volLen; r-- {
				if os.IsPathSeparator(dest[r]) {
					break
				}
			}
			if r < volLen {
				dest = vol
			} else {
				dest = dest[:r]
			}
			end = 0
		}
	}
	return filepath.Clean(dest), nil
}
