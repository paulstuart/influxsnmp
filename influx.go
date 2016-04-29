package main

import (
	"fmt"
	"time"

	client "github.com/influxdata/influxdb/client/v2"
)

// Sender is a function that accepts the components of a datapoint
type Sender func(string, map[string]string, map[string]interface{}, time.Time) error

const (
	// DefaultBatchSize is the default number points to batch before sending
	DefaultBatchSize = 4096
	// DefaultQueueSize is the default size the write queue
	DefaultQueueSize = 65535
	// DefaultFlush is the default of how often to send accumulated datapoints (in seconds)
	DefaultFlush = 10
)

// dbCheck ensures the given database exists
func dbCheck(conn client.Client, database string) error {
	if len(database) == 0 {
		return fmt.Errorf("no database specified")
	}
	q := client.Query{Command: "show databases"}
	resp, err := conn.Query(q)
	if err != nil {
		return err
	}

	for _, r := range resp.Results {
		for _, s := range r.Series {
			for _, v := range s.Values {
				for _, d := range v {
					if d.(string) == database {
						return nil
					}
				}
			}
		}
	}
	return fmt.Errorf("database %s does not exist", database)
}

// NewSender returns a function that will accept datapoints to send to influxdb
func NewSender(
	config interface{},
	batch client.BatchPointsConfig,
	batchSize int,
	queueSize int,
	flush int,
	errFunc func(error),
) (Sender, error) {
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}
	if queueSize <= 0 {
		queueSize = DefaultQueueSize
	}
	if flush <= 0 {
		flush = DefaultFlush
	}

	var conn client.Client
	var err error

	switch conf := config.(type) {
	case client.HTTPConfig:
		conn, err = client.NewHTTPClient(conf)
		if err != nil {
			return nil, err
		}

		_, _, err = conn.Ping(conf.Timeout)
		if err != nil {
			return nil, fmt.Errorf("cannot ping influxdb server: %s", conf.Addr)
		}

		if err := dbCheck(conn, batch.Database); err != nil {
			return nil, err
		}
	case client.UDPConfig:
		conn, err = client.NewUDPClient(conf)
		if err != nil {
			return nil, err
		}
	}

	pts := make(chan *client.Point, queueSize)

	bp, err := client.NewBatchPoints(batch)
	if err != nil {
		return nil, err
	}

	go func() {
		delay := time.Duration(flush) * time.Second
		tick := time.Tick(delay)
		count := 0
		for {
			select {
			case p := <-pts:
				bp.AddPoint(p)
				count++
				if count < batchSize {
					continue
				}
			case <-tick:
				if len(bp.Points()) == 0 {
					continue
				}
			}
			for {
				if err := conn.Write(bp); err != nil {
					if errFunc != nil {
						errFunc(err)
					}
					continue
				}
				bp, _ = client.NewBatchPoints(batch)
				count = 0
				break
			}
		}
	}()

	return func(key string, tags map[string]string, fields map[string]interface{}, ts time.Time) error {
		pt, err := client.NewPoint(key, tags, fields, ts)
		if err != nil {
			return err
		}
		pts <- pt
		return nil
	}, nil
}
