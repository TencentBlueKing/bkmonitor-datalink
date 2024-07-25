package prometheus

import (
	"sort"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/promql"
)

func findSamples(samples []prompb.Sample, start, end time.Time) []prompb.Sample {
	startTs := start.UnixNano() / int64(time.Millisecond)
	endTs := end.UnixNano() / int64(time.Millisecond)

	// 使用二分搜索找到第一个符合条件的索引
	startIdx := sort.Search(len(samples), func(i int) bool {
		return samples[i].Timestamp >= startTs
	})

	// 使用二分搜索找到最后一个符合条件的索引
	endIdx := sort.Search(len(samples), func(i int) bool {
		return samples[i].Timestamp > endTs
	})
	// 如果找到的索引在数组范围内，返回符合条件的切片
	if startIdx < len(samples) && startIdx < endIdx {
		return samples[startIdx:endIdx]
	}

	return nil
}

func filterTimeSeriesByWindow(qr prompb.QueryResult, windowStart, windowEnd time.Time) *prompb.QueryResult {
	newQR := prompb.QueryResult{
		Timeseries: make([]*prompb.TimeSeries, len(qr.Timeseries)),
	}

	var qrWg sync.WaitGroup
	nilSampleCount := 0
	qrPool, _ := ants.NewPool(10)
	defer qrPool.Release()
	for i, ts := range qr.Timeseries {
		qrWg.Add(1)
		i, ts := i, ts // 避免闭包捕获问题
		qrPool.Submit(func() {
			defer qrWg.Done()
			samples := findSamples(ts.Samples, windowStart, windowEnd)
			if samples == nil {
				nilSampleCount++
				return
			}
			newTS := &prompb.TimeSeries{
				Labels:  ts.Labels,
				Samples: samples,
			}
			newQR.Timeseries[i] = newTS
		})
	}
	qrWg.Wait()
	// 通过统计，如果所有时间戳都为空，则返回nil 过滤空时间片
	if nilSampleCount == len(qr.Timeseries) {
		return nil
	}
	return &newQR
}

func mergeVectorsToMatrix(vectors []promql.Vector) promql.Matrix {
	if len(vectors) == 0 {
		return nil
	}

	// 假设所有非空 Vector 的第一个 Sample 的 Labels 是相同的
	var commonLabels labels.Labels

	// 创建一个 Series 来存储所有的 Points
	series := promql.Series{
		Points: make([]promql.Point, 0),
	}

	// 遍历所有 Vector，将非空 Vector 的 Points 添加到 Series 中
	for _, vector := range vectors {
		if len(vector) > 0 {
			if commonLabels == nil {
				// 设置公共 Labels
				commonLabels = vector[0].Metric
				series.Metric = commonLabels
			}
			for _, sample := range vector {
				series.Points = append(series.Points, sample.Point)
			}
		}
	}

	// 如果没有有效的 Points，返回 nil
	if len(series.Points) == 0 {
		return nil
	}

	// 创建 Matrix 并添加 Series
	matrix := promql.Matrix{series}

	return matrix
}
