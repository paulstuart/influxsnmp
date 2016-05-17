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
	Scalers bool     `gcfg:"scalers"`
	Name    string   `gcfg:"name"`
	Index   string   `gcfg:"index"`
	Columns []string `gcfg:"column"`
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

// SystemStatus provides operating statistics
type SystemStatus struct {
	Period, Started string
	Uptime          string
	DB, LogFile     string
	SNMP            map[string]*SnmpConfig
	Influx          map[string]*InfluxConfig
	SnmpStats       map[string]snmpStats
}

var (
	wg            sync.WaitGroup
	quit          = make(chan struct{})
	verbose       bool
	startTime     = time.Now()
	snmpNames     bool
	sample        bool
	dump          bool
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
	senders       = map[string]Sender{}
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

	for name, c := range cfg.Influx {
		sender, err := makeSender(c)
		if err != nil {
			panic(err)
		}
		senders[name] = sender
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

func gather(send Sender, c *SnmpConfig, mib *MibConfig) {
	if c.Freq < 1 {
		panic("invalid polling frequency for: " + c.Host)
	}

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

	p := snmp.Profile{
		Host:      c.Host,
		Community: c.Community,
		Version:   c.Version,
		Port:      c.Port,
		Retries:   c.Retries,
		Timeout:   c.Timeout,
	}

	if dump {
		if err := snmp.OIDList(mibs, os.Stdout); err != nil {
			panic(err)
		}
		return
	}
	if sample {
		sender, _ := snmp.DebugSender(nil, nil)
		wg.Add(1)
		go func() {
			if err := snmp.Sampler(p, crit, sender); err != nil {
				panic(err)
			}
			wg.Done()
		}()
		return
	}

	sender := func(name string, tags map[string]string, value interface{}, when time.Time) error {
		values := map[string]interface{}{"value": value}
		return send(name, tags, values, when)
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

func main() {
	for name, c := range cfg.Snmp {
		send, ok := senders[name]
		if !ok {
			send, ok = senders["*"]
			if !ok {
				panic("No sender for: " + name)
			}
		}
		if len(c.Mibs) > 0 {
			for _, m := range strings.Fields(c.Mibs) {
				mib, ok := cfg.Mibs[m]
				if !ok {
					panic("no mib config found for: " + m)
				}
				gather(send, c, mib)
			}
			continue
		}
		mib, ok := cfg.Mibs[name]
		if !ok {
			if mib, ok = cfg.Mibs["*"]; !ok {
				panic("no mib found for: " + name)
			}
		}
		gather(send, c, mib)
	}
	if sample {
		wg.Wait()
		return
	}
	if httpPort > 0 {
		webServer(httpPort)
	} else {
		<-quit
	}
	errorLog.Close()
}
