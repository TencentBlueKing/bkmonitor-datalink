// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/minio/highwayhash"
	"github.com/stretchr/testify/assert"
	boom "github.com/tylertreat/BoomFilters"
	"github.com/wcharczuk/go-chart/v2"

	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// TestExists test bloom-filter
func TestExists(t *testing.T) {
	sbf := boom.NewScalableBloomFilter(10000, 0.01, 0.8)
	sbf.Add([]byte("00a03a4cce5618e6803d501a8b53f4d5"))
	assert.Equal(t, sbf.Test([]byte("5ab316c948a61737ac3005d8972bba5c")), false)
	assert.Equal(t, sbf.Test([]byte("0390577dbfbedd4a90c6f298b2fc99e9")), false)
	assert.Equal(t, sbf.Test([]byte("354ee86daa34778251c84ef6e506e9f1")), false)
	assert.Equal(t, sbf.Test([]byte("488761020445082f3bd255ee99ffa13e")), false)
	assert.Equal(t, sbf.Test([]byte("8fee8f742d8b4aed94ea2ffeff87e1b6")), false)
	assert.Equal(t, sbf.Test([]byte("b55ad0120589eb93716f5e3e3bd2244e")), false)
	assert.Equal(t, sbf.Test([]byte("b1daa202b36af1c325ca0b0f49e01990")), false)
	assert.Equal(t, sbf.Test([]byte("d8ccbd9187cc98d87de91e664b84e47a")), false)
	assert.Equal(t, sbf.Test([]byte("9edce68a6f5cb53c1c74502abf4579ad")), false)
	assert.Equal(t, sbf.Test([]byte("4ffef2b39c0461530f5d22008189ac0b")), false)
	assert.Equal(t, sbf.Test([]byte("8e29ecaa88d775d03ce6f2b3a263f74d")), false)
	assert.Equal(t, sbf.Test([]byte("2e14519dca83efcd791b361d85f2ed1f")), false)
}

func TestKeyHash(t *testing.T) {
	h, err := highwayhash.New([]byte("test"))
	if err != nil {
		panic(err)
	}

	traceId := "b55ad0120589eb93716f5e3e3bd2244e"
	h.Write([]byte("b55ad0120589eb93716f5e3e3bd2244e"))
	key := h.Sum(nil)
	t.Logf("%s -> %d bytes", traceId, len(key))
}

func TestKeyMd5(t *testing.T) {
	traceId := "b55ad0120589eb93716f5e3e3bd2244e"

	hash := md5.New()
	hash.Write([]byte(traceId))
	shortStr := hex.EncodeToString(hash.Sum(nil))

	t.Log("Original string:", traceId)
	t.Log("Shortened string:", shortStr, "len", len(shortStr))
}

// TestKeyBase64 test storage key
func TestKeyBase64(t *testing.T) {
	originalStr := "b55ad0120589eb93716f5e3e3bd2244e"

	encodedStr := base64.StdEncoding.EncodeToString([]byte(originalStr))

	t.Log("Original string:", originalStr)
	t.Log("Shortened string:", encodedStr, "len", len(encodedStr))
}

// TestNormalBloom test MemoryBloom
func TestNormalBloom(t *testing.T) {
	var blooms []boom.Filter

	sbf := boom.NewBloomFilter(uint(10000000000), 0.01)
	bloom1 := newBloomClient(sbf, func() { sbf.Reset() }, BloomOptions{})
	bloom2 := newBloomClient(sbf, func() { sbf.Reset() }, BloomOptions{})
	blooms = append(blooms, bloom1)
	blooms = append(blooms, bloom2)

	for index, b := range blooms {
		b.Add([]byte("b55ad0120589eb93716f5e3e3bd2244e"))
		t.Log(index, " exist -> ", b.Test([]byte("b55ad0120589eb93716f5e3e3bd2244e")))
	}
}

func generateRandomString(length int) string {
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	s := make([]rune, length)
	for i := range s {
		s[i] = letters[rand.Intn(len(letters))]
	}
	return string(s)
}

type benchMarkParams struct {
	initCap   int
	fpRate    float64
	layers    int
	count     int
	magnitude int
}

func readAndWrite(bloomFilter BloomOperator, count, magnitude int) ([]float64, []float64, time.Duration) {
	start := time.Now()
	existCount := 0
	increase := 0.0
	fmt.Printf("start: %s \n", time.Now())
	firstHappend := false
	var xValues []float64
	var yValues []float64

	for i := 0; i < count; i++ {
		a := 0
		for j := 0; j < magnitude; j++ {
			dataToAdd := generateRandomString(32)
			bloomFilter.Add(BloomStorageData{Key: dataToAdd})
			a++
		}
		b := 0
		tmpExistsCount := 0
		for j := 0; j < magnitude; j++ {
			dataToCheck := generateRandomString(32)
			e, _ := bloomFilter.Exist(dataToCheck)
			if e {
				if !firstHappend {
					firstHappend = true
				}
				tmpExistsCount++
			}
			b++
		}
		now := time.Now()
		if tmpExistsCount != 0 && !firstHappend {
			fmt.Printf("first misjudge happen: %s \n", now)
		}
		if existCount != 0 {
			increase = (float64(tmpExistsCount) / float64(existCount)) * 100
		}
		fmt.Printf(
			"existsCount: %d -> %d (%.2f) - %s (write: %d / read: %d ) %d/%d \n",
			existCount, tmpExistsCount+existCount, increase, time.Now(), a, b, i, count,
		)
		existCount += tmpExistsCount
		xValues = append(xValues, math.Round(now.Sub(start).Seconds()*100)/100)
		yValues = append(yValues, float64(existCount))
	}
	end := time.Now()
	return xValues, yValues, end.Sub(start)
}

func exportChart(x, y []float64, duration time.Duration, title string) {
	graph := chart.Chart{
		Title:      fmt.Sprintf("%s - duration: %s", title, duration),
		TitleStyle: chart.Style{FontSize: 15},
		Series: []chart.Series{
			chart.ContinuousSeries{
				XValues: x,
				YValues: y,
			},
		},
	}

	buffer := bytes.NewBuffer([]byte{})
	err := graph.Render(chart.PNG, buffer)
	if err != nil {
		panic(err)
	}

	file, err := os.Create("output.png")
	if err != nil {
		monitorLogger.Fatal(err)
	}
	_, err = io.Copy(file, buffer)
	if err != nil {
		monitorLogger.Fatal(err)
	}
}

func startBenchmark(count, magnitude int, title string, options BloomOptions) {
	bloomFilter, _ := newLayersCapDecreaseBloomClient("", context.TODO(), options)
	xValues, yValues, duration := readAndWrite(bloomFilter, count, magnitude)
	exportChart(xValues, yValues, duration, title)
}

// BenchmarkBloomFilter overLap bloom-filter benchmark
func BenchmarkBloomFilter(b *testing.B) {
	count := 600
	magnitude := 1000000
	fpRate := 0.01
	initCap := 1000000000
	layers := 10
	startBenchmark(
		count,
		magnitude,
		fmt.Sprintf(
			"InitCap: %d fpRate: %f layers: %d count: %d read&write: %d/s",
			initCap, fpRate, layers, count, magnitude,
		),
		BloomOptions{
			fpRate: fpRate,
			normalOverlapBloomOptions: OverlapBloomOptions{
				2 * time.Hour,
			},
			layersCapDecreaseBloomOptions: LayersCapDecreaseBloomOptions{
				cap:     initCap,
				layers:  10,
				divisor: 2,
			},
		})
}
