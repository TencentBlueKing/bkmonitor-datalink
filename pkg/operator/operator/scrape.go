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
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/discover"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/operator/scraper"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
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

func (s scrapeStat) ID() string {
	return fmt.Sprintf("%s/%s", s.Namespace, s.MonitorName)
}

func (c *Operator) scrapeForce(namespace, monitor string) chan string {
	statefulset, daemonset := c.collectChildConfigs()
	childConfigs := make([]*discover.ChildConfig, 0, len(statefulset)+len(daemonset))
	childConfigs = append(childConfigs, statefulset...)
	childConfigs = append(childConfigs, daemonset...)

	cfgs := make([]*discover.ChildConfig, 0)
	for _, cfg := range childConfigs {
		if cfg.Meta.Namespace == namespace {
			if monitor == "" || cfg.Meta.Name == monitor {
				cfgs = append(cfgs, cfg)
			}
		}
	}

	out := make(chan string, 8)
	if len(cfgs) == 0 {
		out <- "warning: no monitor targets found"
		close(out)
		return out
	}

	wg := sync.WaitGroup{}
	for _, cfg := range cfgs {
		wg.Add(1)
		go func(cfg *discover.ChildConfig) {
			defer wg.Done()
			client, err := scraper.New(cfg.Data)
			if err != nil {
				logger.Warnf("failed to crate scraper http client: %v", err)
				return
			}

			for text := range client.StringCh() {
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

func (c *Operator) scrapeAll() *scrapeStats {
	now := time.Now()
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

	wg := sync.WaitGroup{}
	for _, cfg := range childConfigs {
		wg.Add(1)
		go func(cfg *discover.ChildConfig) {
			defer wg.Done()
			client, err := scraper.New(cfg.Data)
			if err != nil {
				logger.Warnf("failed to crate scraper http client: %v", err)
				return
			}
			lines, errs := client.Lines()
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

	for _, stat := range stats {
		c.mm.SetScrapedLinesCount(stat.ID(), stat.Lines)
		c.mm.SetScrapedErrorsCount(stat.ID(), stat.Errors)
	}
	c.mm.ObserveScrapedDuration(now)
	ret := &scrapeStats{
		MonitorCount: len(stats),
		LinesTotal:   linesTotal,
		ErrorsTotal:  errorsTotal,
		Stats:        stats,
	}
	c.scrapeUpdated = time.Now()

	return ret
}
