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
	Aliases   string `gcfg:"aliases"`
	Config    string `gcfg:"config"`
	Mibs      string `gcfg:"mibs"`
	Tags      string `gcfg:"tags"`
}

// CommonConfig specifies general parameters
type CommonConfig struct {
	HTTPPort int    `gcfg:"httpPort"`
	LogDir   string `gcfg:"logDir"`
	OidFile  string `gcfg:"oidFile"`
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
	quit       = make(chan struct{})
	verbose    bool
	startTime  = time.Now()
	snmpNames  bool
	sample     bool
	dump       bool
	filter     bool
	httpPort   = 8080
	appdir, _  = osext.ExecutableFolder()
	logDir     = filepath.Join(appdir, "log")
	oidFile    = filepath.Join(appdir, "oids.json")
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

func (c *SnmpConfig) profile() snmp.Profile {
	return snmp.Profile{
		Host:      c.Host,
		Community: c.Community,
		Version:   c.Version,
		Port:      c.Port,
		Retries:   c.Retries,
		Timeout:   c.Timeout,
	}
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

func flags() *flag.FlagSet {
	var f flag.FlagSet
	f.BoolVar(&snmpNames, "names", snmpNames, "print column names and exit")
	f.BoolVar(&sample, "sample", sample, "print a sample of collected values and exit")
	f.BoolVar(&dump, "dump", dump, "print output of parsed mibs and exit")
	f.BoolVar(&filter, "filter", filter, "print (filtered by used OIDs) output of parsed mibs and exit")
	f.StringVar(&configFile, "config", configFile, "config file")
	f.BoolVar(&verbose, "verbose", verbose, "verbose mode")
	f.IntVar(&httpPort, "http", httpPort, "http port")
	f.StringVar(&logDir, "logs", logDir, "log directory")
	f.StringVar(&oidFile, "oids", oidFile, "OIDs file")
	f.StringVar(&mibs, "mibs", mibs, "mibs to use")
	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		f.VisitAll(func(flag *flag.Flag) {
			format := "%10s: %s\n"
			fmt.Fprintf(os.Stderr, format, "-"+flag.Name, flag.Usage)
		})
		fmt.Fprintf(os.Stderr, "\nAll settings can be set in config file: %s\n", configFile)
		os.Exit(1)

	}
	return &f
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

	// parse first time to see if config file is being specified
	f := flags()
	f.Parse(os.Args[1:])

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

	if len(cfg.Common.LogDir) > 0 {
		logDir = cfg.Common.LogDir
	}
	if len(cfg.Common.OidFile) > 0 {
		oidFile = cfg.Common.OidFile
	}

	commonTags = pairs(cfg.Common.Tags)

	// re-read cmd line args to override as indicated
	f = flags()
	f.Parse(os.Args[1:])
	os.Mkdir(logDir, 0755)

	// Load or generate mib data
	if len(cfg.Common.MibFile) == 0 {
		fmt.Println("no mibfile specified")
		os.Exit(1)
	}
	if len(mibs) == 0 {
		mibs = cfg.Common.Mibs
	}
	if err := snmp.LoadMIBs(cfg.Common.MibFile, mibs); err != nil {
		panic(err)
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

func prep(c *SnmpConfig, mib *MibConfig) (snmp.Profile, snmp.Criteria) {
	regexps := make([]string, 0, len(mib.Regexps))
	for _, r := range mib.Regexps {
		for _, x := range strings.Fields(r) {
			regexps = append(regexps, x)
		}
	}

	crit := snmp.Criteria{
		OID:     mib.Name,
		Index:   mib.Index,
		Regexps: regexps,
		Keep:    mib.Keep,
		Tags:    pairs(c.Tags),
		Freq:    c.Freq,
		Aliases: pairs(c.Aliases),
	}

	for k, v := range commonTags {
		crit.Tags[k] = v
	}

	return c.profile(), crit
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

func gather(send Sender, c *SnmpConfig, mib *MibConfig) {
	if c.Freq < 1 {
		panic("invalid polling frequency for: " + c.Host)
	}

	p, crit := prep(c, mib)
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
	if err := snmp.Poller(p, crit, sender, errFn, logger); err != nil {
		panic(err)
	}

	name := fmt.Sprintf("%s/%s", c.Host, mib.Name)
	addStats(name, func() snmpStats {
		m.Lock()
		s := stats
		m.Unlock()
		return s
	})
}

// agentList returns an array of snmp hosts and their associated mib info
func agentList() ([]snmpInfo, error) {
	info := make([]snmpInfo, 0, 32)
	for name, c := range cfg.Snmp {
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
			wg.Add(1)
			go func(cfg *SnmpConfig, oid string) {
				if err := coll.Poll(cfg.profile(), oid); err != nil {
					log.Println("poller error:", err)
				}
				wg.Done()
			}(s.Config, name)
		}
	}

	wg.Wait()
	return coll.List()
}

// sampler dumps a single fetch of data from each snmp host/mib
func sampler(agents []snmpInfo) error {
	var wg sync.WaitGroup
	e := make(chan error)
	cnt := 0
	sender, _ := snmp.DebugSender(nil, nil)
	for _, a := range agents {
		wg.Add(1)
		cnt++
		go func(c int, host string, config *SnmpConfig, mib *MibConfig) {
			p, crit := prep(config, mib)
			if err := snmp.Sampler(p, crit, sender); err != nil {
				e <- err
			}
			wg.Done()
		}(cnt, a.Config.Host, a.Config, a.MIB)
	}
	wg.Wait()
	select {
	case err := <-e:
		return err
	default:
	}
	return nil
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

	if sample {
		if err := sampler(agents); err != nil {
			panic(err)
		}
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
		go gather(send, a.Config, a.MIB)
	}

	if httpPort > 0 {
		webServer(httpPort)
	} else {
		<-quit
	}
}
