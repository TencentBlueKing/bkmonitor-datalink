// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package operator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/scraper"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	defaultConcurrency = 16
)

type scrapeStats struct {
	MonitorCount int          `json:"monitor_count"`
	LinesTotal   int          `json:"lines_total"`
	ErrorsTotal  int          `json:"errors_total"`
	Stats        []scrapeStat `json:"stats"`
}

type scrapeStat struct {
	MonitorName string `json:"monitor_name"`
	Namespace   string `json:"namespace"`
	Lines       int    `json:"lines"`
	Errors      int    `json:"errors"`
}

type scrapeAnalyze struct {
	Metric string `json:"metric"`
	Count  int    `json:"count"`
	Sample string `json:"sample"`
}

func (s scrapeStat) ID() string {
	return fmt.Sprintf("%s/%s", s.Namespace, s.MonitorName)
}

func parseMetricName(s string) string {
	i := strings.Index(s, "{")
	if i > 0 {
		return strings.TrimSpace(s[:i])
	}

	parts := strings.Fields(s)
	for _, part := range parts {
		if part == "" {
			continue
		}
		return strings.TrimSpace(part)
	}
	return ""
}

func (c *Operator) scrapeAnalyze(ctx context.Context, namespace, monitor, endpoint string, workers, topn int) []scrapeAnalyze {
	ch := c.scrapeLines(ctx, namespace, monitor, endpoint, workers)

	stats := make(map[string]int)
	sample := make(map[string]string)

	for line := range ch {
		s := parseMetricName(line)
		if s == "" {
			continue
		}
		stats[s]++
		sample[s] = line
	}

	ret := make([]scrapeAnalyze, 0, len(stats))
	for k, v := range stats {
		ret = append(ret, scrapeAnalyze{
			Metric: k,
			Count:  v,
			Sample: sample[k],
		})
	}

	sort.Slice(ret, func(i, j int) bool {
		return ret[i].Count > ret[j].Count
	})

	if topn > 0 {
		if len(ret) > topn {
			return ret[:topn]
		}
	}

	return ret
}

func (c *Operator) scrapeLines(ctx context.Context, namespace, monitor, endpoint string, workers int) chan string {
	statefulset, daemonset := c.collectChildConfigs()
	childConfigs := make([]*discover.ChildConfig, 0, len(statefulset)+len(daemonset))
	childConfigs = append(childConfigs, statefulset...)
	childConfigs = append(childConfigs, daemonset...)

	cfgs := make([]*discover.ChildConfig, 0)
	for _, cfg := range childConfigs {
		if cfg.Meta.Namespace != namespace {
			continue
		}
		if monitor == "" || cfg.Meta.Name == monitor {
			if endpoint == "" || strings.Contains(cfg.FileName, strings.ReplaceAll(endpoint, ".", "-")) {
				cfgs = append(cfgs, cfg)
			}
		}
	}

	out := make(chan string, defaultConcurrency)
	if len(cfgs) == 0 {
		out <- fmt.Sprintf("warning: no monitor targets found, namespace=%s, monitor=%s", namespace, monitor)
		close(out)
		return out
	}

	if workers <= 0 {
		workers = defaultConcurrency
	}
	sem := make(chan struct{}, workers)
	logger.Infof("scrape task: namespace=%s, monitor=%s, workers=%d", namespace, monitor, workers)

	wg := sync.WaitGroup{}
	wg.Add(len(cfgs))
	for _, cfg := range cfgs {
		go func(cfg *discover.ChildConfig) {
			sem <- struct{}{}
			defer func() {
				wg.Done()
				<-sem
			}()
			client, err := scraper.New(cfg.Data)
			if err != nil {
				logger.Warnf("failed to crate scraper http client: %v", err)
				return
			}

			for text := range client.StringCh(ctx) {
				out <- text
			}
		}(cfg)
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

func (c *Operator) scrapeAllStats(ctx context.Context, workers int) *scrapeStats {
	statefulset, daemonset := c.collectChildConfigs()
	childConfigs := make([]*discover.ChildConfig, 0, len(statefulset)+len(daemonset))
	childConfigs = append(childConfigs, statefulset...)
	childConfigs = append(childConfigs, daemonset...)

	calc := make(map[string]*scrapeStat)
	statsCh := make(chan *scrapeStat, 1)

	stop := make(chan struct{}, 1)
	go func() {
		for stat := range statsCh {
			if _, ok := calc[stat.ID()]; !ok {
				calc[stat.ID()] = stat
				continue
			}
			calc[stat.ID()].Lines += stat.Lines
		}
		stop <- struct{}{}
	}()

	if workers <= 0 {
		workers = defaultConcurrency
	}
	sem := make(chan struct{}, workers)

	wg := sync.WaitGroup{}
	for _, cfg := range childConfigs {
		wg.Add(1)
		go func(cfg *discover.ChildConfig) {
			sem <- struct{}{}
			defer func() {
				wg.Done()
				<-sem
			}()
			client, err := scraper.New(cfg.Data)
			if err != nil {
				logger.Warnf("failed to crate scraper http client: %v", err)
				return
			}
			lines, errs := client.Lines(ctx)
			for _, err := range errs {
				logger.Warnf("failed to scrape target, namespace=%s, monitor=%s, err: %v", cfg.Meta.Namespace, cfg.Meta.Name, err)
			}
			statsCh <- &scrapeStat{
				MonitorName: cfg.Meta.Name,
				Namespace:   cfg.Meta.Namespace,
				Lines:       lines,
				Errors:      len(errs),
			}
		}(cfg)
	}

	wg.Wait()
	close(statsCh)
	<-stop

	var linesTotal, errorsTotal int
	var stats []scrapeStat
	for _, v := range calc {
		stats = append(stats, *v)
		linesTotal += v.Lines
		errorsTotal += v.Errors
	}

	// 倒序
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Lines > stats[j].Lines
	})

	ret := &scrapeStats{
		MonitorCount: len(stats),
		LinesTotal:   linesTotal,
		ErrorsTotal:  errorsTotal,
		Stats:        stats,
	}

	return ret
}
