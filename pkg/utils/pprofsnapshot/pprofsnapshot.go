// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pprofsnapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/afero"
	"golang.org/x/net/context"
)

type option struct {
	debugLevel      int
	enabledProfiles map[string]struct{}
	samplingSeconds int
}

type Option func(o *option)

// WithDebugLevel sets profiles debug level
func WithDebugLevel(level int) Option {
	return func(o *option) {
		if level < 0 {
			return
		}
		o.debugLevel = level
	}
}

// WithEnabledProfiles sets collected profiles.
// supported: goroutine/threadcreate/heap/allocs/block/mutex/cpu
func WithEnabledProfiles(profiles []string) Option {
	return func(o *option) {
		filter := make(map[string]struct{})
		supported := supportedProfiles()
		for _, profile := range profiles {
			if _, ok := supported[profile]; ok {
				filter[profile] = struct{}{}
			}
		}
		if len(filter) <= 0 {
			return
		}
		o.enabledProfiles = filter
	}
}

// WithSamplingSeconds sets sampling duration in seconds.
func WithSamplingSeconds(n int) Option {
	return func(o *option) {
		if n <= 0 {
			return
		}
		o.samplingSeconds = n
	}
}

func supportedProfiles() map[string]struct{} {
	return map[string]struct{}{
		"goroutine":    {},
		"threadcreate": {},
		"heap":         {},
		"allocs":       {},
		"block":        {},
		"mutex":        {},
		"cpu":          {},
	}
}

func defaultOption() option {
	return option{
		debugLevel:      0,
		enabledProfiles: supportedProfiles(),
		samplingSeconds: 30,
	}
}

type Collector struct {
	opt *option
}

func NewCollector(opts ...Option) *Collector {
	option := defaultOption()
	collector := &Collector{opt: &option}
	for _, opt := range opts {
		opt(collector.opt)
	}
	return collector
}

// Write writes collected data to the io.Writer.
func (c *Collector) Write(ctx context.Context, w io.Writer) (int, error) {
	b, err := c.Collect(ctx)
	if err != nil {
		return 0, err
	}
	return w.Write(b)
}

// Collect returns the compressed tarball data in bytes
func (c *Collector) Collect(ctx context.Context) ([]byte, error) {
	data := make(map[string][]byte)
	for profile := range c.opt.enabledProfiles {
		buf := &bytes.Buffer{}
		if profile == "cpu" {
			if err := pprof.StartCPUProfile(buf); err != nil {
				return nil, err
			}
			timer := time.NewTimer(time.Duration(c.opt.samplingSeconds) * time.Second)
			select {
			case <-timer.C:
			case <-ctx.Done():
			}
			pprof.StopCPUProfile()
			data[profile] = buf.Bytes()
			continue
		}

		if err := pprof.Lookup(profile).WriteTo(buf, c.opt.debugLevel); err != nil {
			return nil, err
		}
		data[profile] = buf.Bytes()
	}

	return c.compress(data)
}

func (c *Collector) compress(data map[string][]byte) ([]byte, error) {
	memFs := afero.NewMemMapFs()
	buf := &bytes.Buffer{}
	zr := gzip.NewWriter(buf)
	tw := tar.NewWriter(zr)

	for name, content := range data {
		f, err := memFs.Create(name + ".pprof")
		if err != nil {
			return nil, err
		}
		defer f.Close()

		if _, err := f.Write(content); err != nil {
			return nil, err
		}

		stat, err := f.Stat()
		if err != nil {
			return nil, err
		}

		if err := tw.WriteHeader(&tar.Header{
			Name:    "profiles/" + f.Name(),
			Mode:    int64(os.ModePerm),
			Size:    stat.Size(),
			ModTime: stat.ModTime(),
		}); err != nil {
			return nil, err
		}

		if _, err := tw.Write(content); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zr.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// HandlerFor returns the http.Handler for the profiles snapshots collected.
func HandlerFor(opts ...Option) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		collector := NewCollector(opts...)
		query := r.URL.Query()
		if debugLevel := query.Get("debug"); debugLevel != "" {
			if i, err := strconv.Atoi(debugLevel); err == nil {
				WithDebugLevel(i)(collector.opt)
			}
		}
		if duration := query.Get("seconds"); duration != "" {
			if i, err := strconv.Atoi(duration); err == nil {
				WithSamplingSeconds(i)(collector.opt)
			}
		}
		if profiles := query.Get("profiles"); profiles != "" {
			p := strings.Split(strings.ReplaceAll(profiles, " ", ""), ",")
			WithEnabledProfiles(p)(collector.opt)
		}
		b, err := collector.Collect(r.Context())
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(b)
	})
}

// HandlerFuncFor returns the http.HandlerFunc for the profiles snapshots collected.
func HandlerFuncFor(opts ...Option) http.HandlerFunc {
	return HandlerFor(opts...).ServeHTTP
}
