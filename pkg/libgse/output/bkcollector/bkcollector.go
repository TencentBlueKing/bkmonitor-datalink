package bkcollector

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/elastic/beats/libbeat/outputs"
	"github.com/elastic/beats/libbeat/publisher"
)

type Output struct {
	exporter    *otlptrace.Exporter
	bkdatatoken string
}

func init() {
	outputs.RegisterType("bkcollector", MakeBkCollector)

}

func MakeBkCollector(_ outputs.IndexManager, beat beat.Info, observer outputs.Observer, cfg *common.Config) (outputs.Group, error) {
	//
	c := defaultConfig
	err := cfg.Unpack(&c)
	if err != nil {
		logp.Err("unpack config error, %v", err)
		return outputs.Fail(err)
	}
	err = BkCollectorConnect(c.GrpcHost)
	if err != nil {
		return outputs.Fail(err)
	}
	output := NewOutput(c.GrpcHost, c.BkDataToken)
	if output == nil {
		return outputs.Fail(fmt.Errorf("new client error"))
	}

	return outputs.Success(int(c.EventBufferMax), 0, output)
}

func ToMap(data string) map[string]interface{} {
	var mapInfo map[string]interface{}

	// 将 JSON 字符串转换为 map
	err := json.Unmarshal([]byte(data), &mapInfo)
	if err != nil {
		return nil
	}
	return mapInfo
}
func (c *Output) Publish(batch publisher.Batch) error {
	events := batch.Events()
	for i := range events {
		data := events[i].Content.Fields
		items := data.String()
		mapItem := ToMap(items)
		Items := mapItem["items"].([]interface{})

		for _, value := range Items {
			mapData := value.(map[string]interface{})
			log := mapData["data"].(string)
			mapLog := ToMap(log)
			if mapLog == nil {
				continue
			}
			roSpan := PushData(mapLog, c.bkdatatoken)
			err := c.exporter.ExportSpans(context.Background(), roSpan)
			if err != nil {
				logp.Err("push data err : %v", err)
			}
		}
	}
	batch.ACK()
	return nil
}

func (c *Output) String() string {
	return "bkcollector"
}

func (c *Output) Close() error {
	err := c.exporter.Shutdown(context.Background())
	if err != nil {
		logp.Err("exporter Shutdown error : %v", err)
	}
	return nil
}
func NewExporter(GrpcHost string) *otlptrace.Exporter {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(GrpcHost),

		otlptracegrpc.WithReconnectionPeriod(50 * time.Millisecond),
	}

	client := otlptracegrpc.NewClient(opts...)
	exp, err := otlptrace.New(context.Background(), client)
	if err != nil {
		logp.Err("failed to create a new collector exporter: %v", err)
		return nil
	}
	err = exp.Start(context.Background())
	if err != nil {
		logp.Err(" failed to start new collector exporter : %v", err)

	}
	return exp
}

func NewOutput(GrpcHost string, bkDataToken string) *Output {
	exp := NewExporter(GrpcHost)
	output := Output{
		exporter:    exp,
		bkdatatoken: bkDataToken,
	}
	return &output
}
