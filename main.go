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
	Host        string `gcfg:"host"`
	Port        int    `gcfg:"port"`
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

type statsFn func() snmpStats

type SnmpInfo struct {
	Name   string
	Config *SnmpConfig
	MIB    *MibConfig
}

// SystemStatus provides operating statistics
type SystemStatus struct {
	Period, Started string
	Uptime          string
	DB, LogFile     string
	SNMP            map[string]*SnmpConfig
	Influx          map[string]*InfluxConfig
	SnmpStats       map[string]snmpStats
}

type TimeStamp snmp.TimeStamp

var (
	quit          = make(chan struct{})
	verbose       bool
	startTime     = time.Now()
	snmpNames     bool
	sample        bool
	dump          bool
	filter        bool
	httpPort      = 8080
	appdir, _     = osext.ExecutableFolder()
	logDir        = filepath.Join(appdir, "log")
	oidFile       = filepath.Join(appdir, "oids.json")
	configFile    = filepath.Join(appdir, "config.gcfg")
	errorLog      *os.File
	errorDuration = time.Duration(10 * time.Minute)
	errorPeriod   = errorDuration.String()
	errorMax      = 100
	errorName     string
	mibs          string
	statsMap      = make(map[string]statsFn)
	logger        *log.Logger
	commonTags    map[string]string

	cfg = struct {
		Snmp   map[string]*SnmpConfig
		Mibs   map[string]*MibConfig
		Influx map[string]*InfluxConfig
		Common CommonConfig
	}{}
)

func Senders() map[string]Sender {
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

func (c *SnmpConfig) Profile() snmp.Profile {
	return snmp.Profile{
		Host:      c.Host,
		Community: c.Community,
		Version:   c.Version,
		Port:      c.Port,
		Retries:   c.Retries,
		Timeout:   c.Timeout,
	}
}

func getTags(list string) map[string]string {
	t := make(map[string]string)
	for _, item := range strings.Fields(list) {
		if pair := strings.Split(item, "="); len(pair) == 2 {
			t[pair[0]] = pair[1]
		}
	}
	return t
}

func status() SystemStatus {
	stats := map[string]snmpStats{}
	for name, fn := range statsMap {
		stats[name] = fn()
	}
	return SystemStatus{
		LogFile:   errorName,
		Started:   startTime.Format(layout),
		Uptime:    time.Now().Sub(startTime).String(),
		Period:    errorPeriod,
		SNMP:      cfg.Snmp,
		Influx:    cfg.Influx,
		SnmpStats: stats,
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

	commonTags = getTags(cfg.Common.Tags)

	// re-read cmd line args to override as indicated
	f = flags()
	f.Parse(os.Args[1:])
	os.Mkdir(logDir, 0755)

	// Load or generate mib data
	if len(cfg.Common.MibFile) > 0 {
		if err := snmp.CachedMibInfo(cfg.Common.MibFile, cfg.Common.Mibs); err != nil {
			panic(err)
		}
	} else {
		if err := snmp.LoadMibs(cfg.Common.Mibs); err != nil {
			panic(err)
		}
	}
	if len(mibs) == 0 {
		mibs = cfg.Common.Mibs
	}

	if verbose {
		logger = log.New(os.Stderr, "", 0)
	}

	var ferr error
	errorName = fmt.Sprintf("error.%d.log", httpPort)
	errorPath := filepath.Join(logDir, errorName)
	errorLog, ferr = os.OpenFile(errorPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0664)
	if ferr != nil {
		log.Fatal("Can't open error log:", ferr)
	}
}

func errFn(err error) {
	log.Println(err)
}

func makeSender(cfg *InfluxConfig) (Sender, error) {
	conf := client.HTTPConfig{
		Addr:               fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port),
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

func pairs(s string) map[string]string {
	m := make(map[string]string)
	for _, p := range strings.Fields(s) {
		b := strings.Split(p, "=")
		if len(b) == 2 {
			m[b[0]] = b[1]
		}
	}
	return m
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
		Tags:    getTags(c.Tags),
		Freq:    c.Freq,
		Aliases: pairs(c.Aliases),
	}

	for k, v := range commonTags {
		crit.Tags[k] = v
	}

	return c.Profile(), crit
}

func gather(send Sender, c *SnmpConfig, mib *MibConfig) {
	if c.Freq < 1 {
		panic("invalid polling frequency for: " + c.Host)
	}

	p, crit := prep(c, mib)

	sender := func(name string, tags map[string]string, value interface{}, ts snmp.TimeStamp) error {
		values := map[string]interface{}{"value": value, "elapsed": ts.Stop.Sub(ts.Start)}
		return send(name, tags, values, ts.Stop)
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
	if err := snmp.Bulkwalker(p, crit, sender, errFn, logger); err != nil {
		panic(err)
	}

	name := fmt.Sprintf("%s/%s", c.Host, mib.Name)
	statsMap[name] = func() snmpStats {
		m.Lock()
		defer m.Unlock()
		return stats
	}
}

// agentList returns an array of snmp hosts and their associated mib info
func agentList() ([]SnmpInfo, error) {
	info := make([]SnmpInfo, 0, 32)
	for name, c := range cfg.Snmp {
		if len(c.Mibs) > 0 {
			for _, m := range strings.Fields(c.Mibs) {
				mib, ok := cfg.Mibs[m]
				if !ok {
					return info, fmt.Errorf("no mib config found for:%s", m)
				}
				info = append(info, SnmpInfo{name, c, mib})
			}
			continue
		}
		mib, ok := cfg.Mibs[name]
		if !ok {
			if mib, ok = cfg.Mibs["*"]; !ok {
				return info, fmt.Errorf("no mib config found for:%s", name)
			}
		}
		info = append(info, SnmpInfo{name, c, mib})
	}
	return info, nil
}

// filtered returns a list of all OIDs encountered by
// polling the specified devices and their respective OIDs
func filtered(a []SnmpInfo) []string {
	lookup, err := snmp.OIDNames(mibs)
	if err != nil {
		panic(err)
	}

	var w1, w2 sync.WaitGroup
	valid := snmp.RootOID(lookup)
	c := make(chan string, 1024)
	used := make(map[string]struct{})

	// queued access to updating used
	w1.Add(1)
	go func() {
		for oid := range c {
			root := valid(oid)
			if len(root) == 0 {
				panic("no root found for OID:" + oid)
			}
			used[root] = struct{}{}
		}
		w1.Done()
	}()

	poll := func(cfg *SnmpConfig, oid string) {
		client, err := snmp.NewClient(cfg.Profile())
		if err != nil {
			panic(err)
		}
		snmp.BulkWalkAll(client, oid, snmp.OIDCollector(c))
		w2.Done()
		client.Conn.Close()
	}

	for _, s := range a {
		for _, name := range strings.Fields(s.MIB.Name) {
			oid, ok := lookup[name]
			if !ok {
				panic("No OID for name:" + name)
			}
			c <- oid
			w2.Add(1)
			go poll(s.Config, oid)
		}
	}
	w2.Wait()
	close(c)
	w1.Wait()

	list := make([]string, 0, len(used))
	for k, _ := range used {
		list = append(list, k)
	}
	return list
}

// sampler dumps a single fetch of data from each snmp host/mib
func sampler(agents []SnmpInfo) error {
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

func dumper(agents []SnmpInfo) error {
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

	senders := Senders()
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
	errorLog.Close()
}
