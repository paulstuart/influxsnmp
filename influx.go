package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	client "github.com/influxdata/influxdb/client/v2"
)

type Sender func(string, map[string]string, map[string]interface{}, time.Time) error

// InfluxConfig defines connection requirements
type InfluxConfig struct {
	Host      string `gcfg:"host"`
	Port      int    `gcfg:"port"`
	DB        string `gcfg:"db"`
	Username  string `gcfg:"user"`
	Password  string `gcfg:"password"`
	Retention string `gcfg:"retention"`
	Batch     client.BatchPointsConfig
}

var (
	debug    bool
	flush    = make(chan struct{})
	Keywords = strings.Fields(`
	ALL           ALTER         ANY           AS            ASC           BEGIN
	BY            CREATE        CONTINUOUS    DATABASE      DATABASES     DEFAULT
	DELETE        DESC          DESTINATIONS  DIAGNOSTICS   DISTINCT      DROP
	DURATION      END           EVERY         EXISTS        EXPLAIN       FIELD
	FOR           FORCE         FROM          GRANT         GRANTS        GROUP
	GROUPS        IF            IN            INF           INNER         INSERT
	INTO          KEY           KEYS          LIMIT         SHOW          MEASUREMENT
	MEASUREMENTS  NOT           OFFSET        ON            ORDER         PASSWORD
	POLICY        POLICIES      PRIVILEGES    QUERIES       QUERY         READ
	REPLICATION   RESAMPLE      RETENTION     REVOKE        SELECT        SERIES
	SERVER        SERVERS       SET           SHARD         SHARDS        SLIMIT
	SOFFSET       STATS         SUBSCRIPTION  SUBSCRIPTIONS TAG           TO
	USER          USERS         VALUES        WHERE         WITH          WRITE
	NAME          KILL
	`)
)

func SenderFlush() {
	log.Println("FLUSHING")
	flush <- struct{}{}
	time.Sleep(1 * time.Second)
}

func (cfg *InfluxConfig) Connect() (client.Client, error) {
	url := fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port)

	conf := client.HTTPConfig{
		Addr:     url,
		Username: cfg.Username,
		Password: cfg.Password,
	}

	conn, err := client.NewHTTPClient(conf)
	if err != nil {
		return conn, err
	}

	_, _, err = conn.Ping(time.Second * 10)
	return conn, err
}

func (cfg *InfluxConfig) NewSender(batchSize, queueSize, period int) (Sender, error) {
	if len(cfg.DB) == 0 {
		return nil, fmt.Errorf("no database specified")
	}
	if debug {
		log.Println("Connecting to:", cfg.Host)
	}
	pts := make(chan *client.Point, queueSize)
	conn, err := cfg.Connect()
	if err != nil {
		return nil, fmt.Errorf("failed connecting to: %s", cfg.Host)
	}
	if debug {
		log.Println("Connected to:", cfg.Host)
	}

	exists := func() bool {
		q := client.Query{Command: "show databases"}
		resp, err := conn.Query(q)
		if err != nil {
			log.Println("error querying for databases: ", err)
			return false
		}
		for _, r := range resp.Results {
			for _, s := range r.Series {
				for _, v := range s.Values {
					for _, x := range v {
						if x.(string) == cfg.DB {
							return true
						}
					}
				}
			}
		}
		return false
	}
	if !exists() {
		return nil, fmt.Errorf("database %s does not exist", cfg.DB)
	}

	bp, err := client.NewBatchPoints(cfg.Batch)
	if err != nil {
		return nil, err
	}

	go func() {
		delay := time.Duration(period) * time.Second
		tick := time.Tick(delay)
		count := 0
		for {
			select {
			case <-tick:
				if len(bp.Points()) == 0 {
					continue
				}
			case p := <-pts:
				//fmt.Println("POINT:", p)
				bp.AddPoint(p)
				count++
				if count < batchSize {
					continue
				}
			case <-flush:
				log.Println("FLUSHED")
				break
			}
			log.Println("SENDING:", count)
			for {
				if err := conn.Write(bp); err != nil {
					log.Println("influxdb write error:", err)
					time.Sleep(30 * time.Second)
					continue
				}
				bp, err = client.NewBatchPoints(cfg.Batch)
				if err != nil {
					log.Println("influxdb batchpoints error:", err)
				}
				log.Println("influxdb write count:", count)
				count = 0
				break
			}
		}
	}()

	//return func(pt *client.Point) error {
	return func(key string, tags map[string]string, fields map[string]interface{}, ts time.Time) error {
		pt, err := client.NewPoint(key, tags, fields, ts)
		if err != nil {
			return err
		}
		pts <- pt
		return nil
	}, nil
}
