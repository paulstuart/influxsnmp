package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	client "github.com/influxdata/influxdb/client/v2"
	"github.com/kardianos/osext"
	snmp "github.com/paulstuart/snmputil"
	"gopkg.in/gcfg.v1"
)

const layout = "2006-01-02 15:04:05"

// SnmpConfig specifies the snmp device to probe
type SnmpConfig struct {
	Host      string `gcfg:"host"`
	Community string `gcfg:"community"`
	Version   string `gcfg:"version"`
	Port      int    `gcfg:"port"`
	Retries   int    `gcfg:"retries"`
	Timeout   int    `gcfg:"timeout"`
	Freq      int    `gcfg:"freq"`
	Count     int    `gcfg:"count"`
	Aliases   string `gcfg:"aliases"`
	Config    string `gcfg:"config"`
	Mibs      string `gcfg:"mibs"`
	Tags      string `gcfg:"tags"`
	Disabled  bool   `gcfg:"disabled"`
}

// CommonConfig specifies general parameters
type CommonConfig struct {
	HTTPPort int    `gcfg:"httpPort"`
	Tags     string `gcfg:"tags"`
	Mibs     string `gcfg:"mibs"`
	MibFile  string `gcfg:"mibfile"`
	Elapsed  bool   `gcfg:"elapsed"`
}

// MibConfig specifies what OIDs to query
type MibConfig struct {
	Name    string   `gcfg:"name"`
	Index   string   `gcfg:"index"`
	Regexps []string `gcfg:"regexp"`
	Keep    bool     `gcfg:"keep"`
	Count   int      `gcfg:"count"`
}

// InfluxConfig defines connection requirements
type InfluxConfig struct {
	URL         string `gcfg:"url"`
	Database    string `gcfg:"database"`
	Username    string `gcfg:"username"`
	Password    string `gcfg:"password"`
	Retention   string `gcfg:"retention"`
	Consistency string `gcfg:"consistency"`
	SkipVerify  bool   `gcfg:"skip_verify"`
	Timeout     int    `gcfg:"timeout"`
	BatchSize   int    `gcfg:"batchSize"`
	QueueSize   int    `gcfg:"queueSize"`
	Flush       int    `gcfg:"flush"`
}

type snmpStats struct {
	GetCnt    int
	ErrCnt    int
	LastError error
	LastTime  time.Time
}

type statsFunc func() snmpStats

type snmpInfo struct {
	Name   string
	Config *SnmpConfig
	MIB    *MibConfig
}

// SystemStatus provides operating statistics
type SystemStatus struct {
	Period    string
	Started   string
	Uptime    string
	DB        string
	SNMP      map[string]*SnmpConfig
	Influx    map[string]*InfluxConfig
	SnmpStats map[string]snmpStats
}

// TimeStamp contains the start and stop time of PDU collection
type TimeStamp snmp.TimeStamp

var (
	startTime  = time.Now()
	quit       sync.WaitGroup
	verbose    bool
	sample     bool
	dump       bool
	filter     bool
	httpPort   = 8080
	appdir, _  = osext.ExecutableFolder()
	configFile = filepath.Join(appdir, "config.gcfg")
	mibs       string
	statsMap   = make(map[string]statsFunc)
	logger     *log.Logger
	commonTags map[string]string
	sLock      sync.Mutex

	cfg = struct {
		Snmp   map[string]*SnmpConfig
		Mibs   map[string]*MibConfig
		Influx map[string]*InfluxConfig
		Common CommonConfig
	}{}
)

func getSenders() map[string]Sender {
	s := map[string]Sender{}
	for name, c := range cfg.Influx {
		sender, err := makeSender(c)
		if err != nil {
			panic(err)
		}
		s[name] = sender
	}
	return s
}

func (c *SnmpConfig) profiles() []snmp.Profile {
	hosts := strings.Fields(c.Host)
	list := make([]snmp.Profile, 0, len(hosts))
	for _, host := range hosts {
		p := snmp.Profile{
			Host:      host,
			Community: c.Community,
			Version:   c.Version,
			Port:      c.Port,
			Retries:   c.Retries,
			Timeout:   c.Timeout,
		}
		list = append(list, p)
	}
	return list
}

func criteria(s *SnmpConfig, m *MibConfig) []snmp.Criteria {
	regexps := make([]string, 0, len(m.Regexps))
	for _, r := range m.Regexps {
		for _, x := range strings.Fields(r) {
			regexps = append(regexps, x)
		}
	}

	names := strings.Fields(m.Name)
	list := make([]snmp.Criteria, 0, len(names))
	for _, name := range names {
		count := s.Count
		if m.Count > 0 {
			count = m.Count
		}
		crit := snmp.Criteria{
			OID:     name,
			Index:   m.Index,
			Regexps: regexps,
			Keep:    m.Keep,
			Tags:    pairs(s.Tags),
			Freq:    s.Freq,
			Aliases: pairs(s.Aliases),
			Count:   count,
		}

		for k, v := range commonTags {
			crit.Tags[k] = v
		}
		list = append(list, crit)
	}

	return list
}

func status() SystemStatus {
	return SystemStatus{
		Started:   startTime.Format(layout),
		Uptime:    time.Now().Sub(startTime).String(),
		SNMP:      cfg.Snmp,
		Influx:    cfg.Influx,
		SnmpStats: getStats(),
	}
}

func pairs(list string) map[string]string {
	t := make(map[string]string)
	for _, item := range strings.Fields(list) {
		if pair := strings.Split(item, "="); len(pair) == 2 {
			t[pair[0]] = pair[1]
		}
	}
	return t
}

func init() {
	log.SetOutput(os.Stderr)

	flag.BoolVar(&sample, "sample", sample, "print a sample of collected values and exit")
	flag.BoolVar(&dump, "dump", dump, "print output of parsed mibs and exit")
	flag.BoolVar(&filter, "filter", filter, "(filtered by used OIDs) output of dump option")
	flag.StringVar(&configFile, "config", configFile, "config file")
	flag.BoolVar(&verbose, "verbose", verbose, "verbose mode")
	flag.IntVar(&httpPort, "http", httpPort, "http port")
	flag.StringVar(&mibs, "mibs", mibs, "mibs to use")
	flag.Parse()

	// now load up config settings
	if _, err := os.Stat(configFile); err != nil {
		log.Fatal(err)
	}
	data, err := ioutil.ReadFile(configFile)
	if err != nil {
		log.Fatal(err)
	}
	err = gcfg.ReadStringInto(&cfg, string(data))
	if err != nil {
		log.Fatalf("Failed to parse gcfg data: %s", err)
	}
	httpPort = cfg.Common.HTTPPort

	commonTags = pairs(cfg.Common.Tags)

	if len(mibs) == 0 {
		mibs = cfg.Common.Mibs
	}

	if verbose {
		logger = log.New(os.Stderr, "", 0)
	}
}

func errFn(err error) {
	log.Println(err)
}

func makeSender(cfg *InfluxConfig) (Sender, error) {
	conf := client.HTTPConfig{
		Addr:               cfg.URL,
		Username:           cfg.Username,
		Password:           cfg.Password,
		Timeout:            (time.Duration(cfg.Timeout) * time.Second),
		InsecureSkipVerify: cfg.SkipVerify,
	}
	batch := client.BatchPointsConfig{
		Precision:        "s",
		Database:         cfg.Database,
		RetentionPolicy:  cfg.Retention,
		WriteConsistency: cfg.Consistency,
	}

	return NewSender(conf, batch, cfg.BatchSize, cfg.QueueSize, cfg.Flush, errFn)
}

func addStats(name string, fn statsFunc) {
	sLock.Lock()
	statsMap[name] = fn
	sLock.Unlock()
}

func getStats() map[string]snmpStats {
	m := make(map[string]snmpStats)
	sLock.Lock()
	for k, fn := range statsMap {
		m[k] = fn()
	}
	sLock.Unlock()
	return m
}

func gather(send Sender, p snmp.Profile, crit snmp.Criteria, mibID string) {
	if crit.Freq < 1 {
		panic("invalid polling frequency for: " + p.Host)
	}
	var sender snmp.Sender
	if cfg.Common.Elapsed {
		sender = func(name string, tags map[string]string, value interface{}, ts snmp.TimeStamp) error {
			elapsed := int(ts.Stop.Sub(ts.Start).Nanoseconds() / 1000000)
			values := map[string]interface{}{"value": value, "elapsed": elapsed}
			return send(name, tags, values, ts.Stop)
		}
	} else {
		sender = func(name string, tags map[string]string, value interface{}, ts snmp.TimeStamp) error {
			values := map[string]interface{}{"value": value}
			return send(name, tags, values, ts.Stop)
		}
	}
	// influxdb saves uint64 as a string
	// so this is a workaround for now
	sender = snmp.IntegerSender(sender)

	var stats snmpStats
	var m sync.Mutex

	errFn := func(err error) {
		m.Lock()
		if err == nil {
			stats.GetCnt++
		} else {
			stats.ErrCnt++
			stats.LastError = err
			stats.LastTime = time.Now()
		}
		m.Unlock()
	}
	name := fmt.Sprintf("%s/%s", p.Host, mibID)
	addStats(name, func() snmpStats {
		m.Lock()
		s := stats
		m.Unlock()
		return s
	})
	if err := snmp.Poller(p, crit, sender, errFn, logger); err != nil {
		log.Println("SNMP polling error:", err)
	}
	quit.Done()
}

// agentList returns an array of snmp hosts and their associated mib info
func agentList() ([]snmpInfo, error) {
	info := make([]snmpInfo, 0, len(cfg.Snmp))
	for name, c := range cfg.Snmp {
		if c.Disabled {
			continue
		}
		if len(c.Mibs) > 0 {
			for _, m := range strings.Fields(c.Mibs) {
				mib, ok := cfg.Mibs[m]
				if !ok {
					return info, fmt.Errorf("no mib config found for:%s", m)
				}
				info = append(info, snmpInfo{name, c, mib})
			}
			continue
		}
		mib, ok := cfg.Mibs[name]
		if !ok {
			if mib, ok = cfg.Mibs["*"]; !ok {
				return info, fmt.Errorf("no mib config found for:%s", name)
			}
		}
		info = append(info, snmpInfo{name, c, mib})
	}
	return info, nil
}

// filtered returns a list of all OIDs encountered by
// polling the specified devices and their respective OIDs
func filtered(a []snmpInfo) []string {
	var wg sync.WaitGroup
	coll := snmp.NewCollector(mibs)
	for _, s := range a {
		for _, name := range strings.Fields(s.MIB.Name) {
			for _, profile := range s.Config.profiles() {
				wg.Add(1)
				go func(p snmp.Profile, oid string) {
					if err := coll.Poll(p, oid); err != nil {
						log.Println("poller error:", err)
					}
					wg.Done()
				}(profile, name)
			}
		}
	}

	wg.Wait()
	return coll.List()
}

// sampler dumps a single fetch of data from each snmp host/mib
func sampler(agents []snmpInfo) {
	var wg sync.WaitGroup
	sender, _ := snmp.DebugSender(nil, nil)
	for _, a := range agents {
		for _, profile := range a.Config.profiles() {
			for _, crit := range criteria(a.Config, a.MIB) {
				wg.Add(1)
				go func(p snmp.Profile, crit snmp.Criteria) {
					if err := snmp.Sampler(p, crit, sender); err != nil {
						log.Printf("error sampling host %s: %s\n", p.Host, err)
					}
					wg.Done()
				}(profile, crit)
			}
		}
	}
	wg.Wait()
}

// dumper creates a json file of parsed mib entries
func dumper(agents []snmpInfo) error {
	if len(mibs) == 0 {
		return fmt.Errorf("error: no MIBs specified")
	}
	var oids []string
	if filter {
		oids = filtered(agents)
	}
	return snmp.OIDList(mibs, oids, os.Stdout)
}

func main() {
	agents, err := agentList()
	if err != nil {
		panic(err)
	}

	if dump {
		if err := dumper(agents); err != nil {
			panic(err)
		}
		return
	}

	// Load or generate mib data
	if len(cfg.Common.MibFile) == 0 {
		fmt.Println("no mibfile specified")
		os.Exit(1)
	}
	for _, file := range strings.Fields(cfg.Common.MibFile) {
		if err := snmp.LoadMIBs(file, mibs); err != nil {
			panic(err)
		}
	}

	if sample {
		sampler(agents)
		return
	}

	senders := getSenders()
	for _, a := range agents {
		send, ok := senders[a.Name]
		if !ok {
			send, ok = senders["*"]
			if !ok {
				panic("No sender for: " + a.Name)
			}
		}
		for _, profile := range a.Config.profiles() {
			for _, crit := range criteria(a.Config, a.MIB) {
				quit.Add(1)
				go gather(send, profile, crit, a.Name)
			}
		}
	}

	if httpPort > 0 {
		go webServer(httpPort)
	}
	quit.Wait()
}
