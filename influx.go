package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	influxdb "github.com/influxdb/influxdb/client"
)

type InfluxData struct {
	Name  string
	Value interface{}
}

type InfluxSeries struct {
	prefix string
	When   int64
	Data   []InfluxData
}

var (
	influxCount, errorInflux int64
)

func (cfg *InfluxConfig) Init() {
	cfg.iChan = make(chan InfluxSeries, 65535)
	if testing {
		go influxEmitter(cfg.iChan, nil)
		return
	}
	client, err := influxdb.NewClient(&influxdb.ClientConfig{
		Host:     cfg.Host,
		Username: cfg.User,
		Password: cfg.Password,
		Database: cfg.DB,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err = client.Ping(); err != nil {
		log.Fatal(err)
	}
	go influxEmitter(cfg.iChan, client)
}

func (c *InfluxConfig) Send(data InfluxSeries) {
	c.iChan <- data
}

func (c *InfluxConfig) Hostname() string {
	return strings.Split(c.Host, ":")[0]
}

func influxEmitter(given chan InfluxSeries, client *influxdb.Client) {
	for {
		select {
		case data := <-given:
			// convert to influx "packet"
			series := []*influxdb.Series{}
			for _, item := range data.Data {
				name := item.Name
				if len(data.prefix) > 0 {
					name = data.prefix + "." + name
				}
				if testing || verbose {
					fmt.Println("INFLUX:", data.When, name, item.Value)
				}
				if testing {
					continue
				}
				series = append(series, &influxdb.Series{
					Name:    name,
					Columns: []string{"time", "value"},
					Points: [][]interface{}{
						{data.When, item.Value},
					},
				})
			}
			if testing {
				break
			}

			// keep trying until we get it (don't drop the data)
			for {
				if err := client.WriteSeries(series); err != nil {
					errMsg("Influx write error:", err)
					errorInflux++
					// try again in a bit
					time.Sleep(30 * time.Second)
					continue
				} else {
					influxCount++
				}
				break
			}
		}
	}
}
