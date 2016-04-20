package main

import (
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/influxdata/influxdb/client"
)

func (cfg *InfluxConfig) BP() *client.BatchPoints {
	if len(cfg.Retention) == 0 {
		cfg.Retention = "default"
	}
	return &client.BatchPoints{
		Points:          make([]client.Point, 0, maxOids),
		Database:        cfg.DB,
		RetentionPolicy: cfg.Retention,
	}
}

func makePoint(host string, val *pduValue, when time.Time) client.Point {
	return client.Point{
		Measurement: val.name,
		Tags: map[string]string{
			"host":   host,
			"column": val.column,
		},
		Fields: map[string]interface{}{
			"value": val.value,
		},
		Time: when,
	}
}

func (cfg *InfluxConfig) Connect() error {
	u, err := url.Parse(fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port))
	if err != nil {
		return err
	}

	conf := client.Config{
		URL:      *u,
		Username: cfg.User,
		Password: cfg.Password,
	}

	cfg.conn, err = client.NewClient(conf)
	if err != nil {
		return err
	}

	_, _, err = cfg.conn.Ping()
	return err
}

func (cfg *InfluxConfig) Init() {
	if verbose {
		log.Println("Connecting to:", cfg.Host)
	}
	cfg.iChan = make(chan *client.BatchPoints, 65535)
	if err := cfg.Connect(); err != nil {
		log.Println("failed connecting to:", cfg.Host)
		log.Println("error:", err)
		log.Fatal(err)
	}
	if verbose {
		log.Println("Connected to:", cfg.Host)
	}

	go influxEmitter(cfg)
}

func (c *InfluxConfig) Send(bps *client.BatchPoints) {
	c.iChan <- bps
}

func (c *InfluxConfig) Hostname() string {
	return strings.Split(c.Host, ":")[0]
}

// use chan as a queue so that interupted connections to
// influxdb server don't drop collected data

func influxEmitter(cfg *InfluxConfig) {
	for {
		select {
		case data := <-cfg.iChan:
			if testing {
				break
			}
			if data == nil {
				log.Println("null influx input")
				continue
			}

			// keep trying until we get it (don't drop the data)
			for {
				if _, err := cfg.conn.Write(*data); err != nil {
					cfg.incErrors()
					log.Println("influxdb write error:", err)
					// try again in a bit
					// TODO: this could be better
					time.Sleep(30 * time.Second)
					continue
				} else {
					cfg.incSent()
				}
				break
			}
		}
	}
}
