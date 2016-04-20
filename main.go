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
	"sync/atomic"
	"time"

	"github.com/influxdata/influxdb/client"
	"github.com/kardianos/osext"
	"github.com/soniah/gosnmp"
	"gopkg.in/gcfg.v1"
)

const layout = "2006-01-02 15:04:05"

type SnmpConfig struct {
	Host      string `gcfg:"host"`
	Public    string `gcfg:"community"`
	Port      int    `gcfg:"port"`
	Retries   int    `gcfg:"retries"`
	Timeout   int    `gcfg:"timeout"`
	Repeat    int    `gcfg:"repeat"`
	Freq      int    `gcfg:"freq"`
	PortFile  string `gcfg:"portfile"`
	Config    string `gcfg:"config"`
	labels    map[string]string
	asName    map[string]string
	asOID     map[string]string
	oids      []string
	mib       *MibConfig
	Influx    *InfluxConfig
	LastError time.Time
	Requests  int64
	Gets      int64
	Errors    int64
	debugging chan bool
	enabled   chan chan bool
}

type InfluxConfig struct {
	Host      string `gcfg:"host"`
	Port      int    `gcfg:"port"`
	DB        string `gcfg:"db"`
	User      string `gcfg:"user"`
	Password  string `gcfg:"password"`
	Retention string `gcfg:"retention"`
	iChan     chan *client.BatchPoints
	conn      *client.Client
	Sent      int64
	Errors    int64
}

type HTTPConfig struct {
	Port int `gcfg:"port"`
}

type GeneralConfig struct {
	LogDir  string `gcfg:"logdir"`
	OidFile string `gcfg:"oidfile"`
}

type MibConfig struct {
	Scalers bool     `gcfg:"scalers"`
	Name    string   `gcfg:"name"`
	Columns []string `gcfg:"column"`
}

var (
	quit          = make(chan struct{})
	verbose       bool
	startTime     = time.Now()
	testing       bool
	snmpNames     bool
	repeat        = 0
	freq          = 30
	httpPort      = 8080
	oidToName     = make(map[string]string)
	nameToOid     = make(map[string]string)
	appdir, _     = osext.ExecutableFolder()
	logDir        = filepath.Join(appdir, "log")
	oidFile       = filepath.Join(appdir, "oids.txt")
	configFile    = filepath.Join(appdir, "config.gcfg")
	errorLog      *os.File
	errorDuration = time.Duration(10 * time.Minute)
	errorPeriod   = errorDuration.String()
	errorMax      = 100
	errorName     string

	cfg = struct {
		Snmp    map[string]*SnmpConfig
		Mibs    map[string]*MibConfig
		Influx  map[string]*InfluxConfig
		HTTP    HTTPConfig
		General GeneralConfig
	}{}
)

func fatal(v ...interface{}) {
	log.SetOutput(os.Stderr)
	log.Fatalln(v...)
}

func (c *SnmpConfig) DebugAction() string {
	debug := make(chan bool)
	c.enabled <- debug
	if <-debug {
		return "disable"
	}
	return "enable"
}

func (c *SnmpConfig) LoadPorts() {
	c.labels = make(map[string]string)
	if len(c.PortFile) == 0 {
		return
	}
	data, err := ioutil.ReadFile(filepath.Join(appdir, c.PortFile))
	if err != nil {
		log.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		// strip comments
		comment := strings.Index(line, "#")
		if comment >= 0 {
			line = line[:comment]
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		c.labels[f[0]] = f[1]
	}
}

func (c *SnmpConfig) incRequests() {
	atomic.AddInt64(&c.Requests, 1)
}

func (c *SnmpConfig) incGets() {
	atomic.AddInt64(&c.Gets, 1)
}

func (c *SnmpConfig) incErrors() {
	atomic.AddInt64(&c.Errors, 1)
}

func (c *InfluxConfig) incErrors() {
	atomic.AddInt64(&c.Errors, 1)
}

func (c *InfluxConfig) incSent() {
	atomic.AddInt64(&c.Sent, 1)
}

// loads [last_octet]name for device
func (c *SnmpConfig) Translate() {
	client, err := snmpClient(c)
	if err != nil {
		fatal("Client connect error:", err)
	}
	defer client.Conn.Close()
	spew("Looking up column names for:", c.Host)
	pdus, err := client.BulkWalkAll(nameOid)
	if err != nil {
		fatal("SNMP bulkwalk error", err)
	}
	c.asName = make(map[string]string)
	c.asOID = make(map[string]string)
	for _, pdu := range pdus {
		switch pdu.Type {
		case gosnmp.OctetString:
			i := strings.LastIndex(pdu.Name, ".")
			suffix := pdu.Name[i+1:]
			name := string(pdu.Value.([]byte))
			_, ok := c.labels[name]
			if len(c.PortFile) == 0 || ok {
				c.asName[name] = suffix
				c.asOID[suffix] = name
			}
		}
	}
	// make sure we got everything
	for k := range c.labels {
		if _, ok := c.asName[k]; !ok {
			fatal("No OID found for:", k)
		}
	}

}

func spew(x ...interface{}) {
	if verbose {
		fmt.Println(x...)
	}
}

func (c *SnmpConfig) OIDs() {
	if c.mib == nil {
		fatal("NO MIB!")
	}
	c.oids = []string{}
	for _, col := range c.mib.Columns {
		base, ok := nameToOid[col]
		if !ok {
			fatal("no oid for col:", col)
		}
		// just named columns
		if len(c.PortFile) > 0 {
			for k := range c.asOID {
				c.oids = append(c.oids, base+"."+k)
			}
		} else if c.mib.Scalers {
			// or plain old scaler instances
			c.oids = append(c.oids, base+".0")
		} else {
			c.oids = append(c.oids, base)
		}
	}
	if len(c.mib.Columns) > 0 {
		spew("COLUMNS", c.mib.Columns)
		spew(c.oids)
	}
}

func flags() *flag.FlagSet {
	var f flag.FlagSet
	f.BoolVar(&testing, "testing", testing, "print data w/o saving")
	f.BoolVar(&snmpNames, "names", snmpNames, "print column names and exit")
	f.StringVar(&configFile, "config", configFile, "config file")
	f.BoolVar(&verbose, "verbose", verbose, "verbose mode")
	f.IntVar(&repeat, "repeat", repeat, "number of times to repeat")
	f.IntVar(&freq, "freq", freq, "delay (in seconds)")
	f.IntVar(&httpPort, "http", httpPort, "http port")
	f.StringVar(&logDir, "logs", logDir, "log directory")
	f.StringVar(&oidFile, "oids", oidFile, "OIDs file")
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
	// parse first time to see if config file is being specified
	f := flags()
	f.Parse(os.Args[1:])
	// now load up config settings
	if _, err := os.Stat(configFile); err != nil {
		log.Fatal(err)
	} else {
		data, err := ioutil.ReadFile(configFile)
		if err != nil {
			log.Fatal(err)
		}
		err = gcfg.ReadStringInto(&cfg, string(data))
		if err != nil {
			log.Fatalf("Failed to parse gcfg data: %s", err)
		}
		httpPort = cfg.HTTP.Port
	}

	if len(cfg.General.LogDir) > 0 {
		logDir = cfg.General.LogDir
	}
	if len(cfg.General.OidFile) > 0 {
		oidFile = cfg.General.OidFile
	}
	// load oid lookup data
	data, err := ioutil.ReadFile(oidFile)
	if err != nil {
		log.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		nameToOid[f[0]] = f[1]
		oidToName[f[1]] = f[0]
	}

	for _, s := range cfg.Snmp {
		s.LoadPorts()
		s.debugging = make(chan bool)
		s.enabled = make(chan chan bool)
	}
	var ok bool
	for name, c := range cfg.Snmp {
		if c.mib, ok = cfg.Mibs[name]; !ok {
			if c.mib, ok = cfg.Mibs["*"]; !ok {
				fatal("No mib data found for config:", name)
			}
		}
		c.Translate()
		c.OIDs()
		if c.Freq == 0 {
			c.Freq = freq
		}
	}

	// only run when one needs to see the interface names of the device
	if snmpNames {
		for _, c := range cfg.Snmp {
			fmt.Println("\nSNMP host:", c.Host)
			fmt.Println("=========================================")
			printSnmpNames(c)
		}
		os.Exit(0)
	}

	// re-read cmd line args to override as indicated
	f = flags()
	f.Parse(os.Args[1:])
	os.Mkdir(logDir, 0755)

	// now make sure each snmp device has a db
	for name, c := range cfg.Snmp {
		// default is to use name of snmp config, but it can be overridden
		if len(c.Config) > 0 {
			name = c.Config
		}
		if c.Influx, ok = cfg.Influx[name]; !ok {
			if c.Influx, ok = cfg.Influx["*"]; !ok {
				fatal("No influx config for snmp device:", name)
			}
		}
		c.Influx.Init()
	}

	var ferr error
	errorName = fmt.Sprintf("error.%d.log", cfg.HTTP.Port)
	errorPath := filepath.Join(logDir, errorName)
	errorLog, ferr = os.OpenFile(errorPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0664)
	if ferr != nil {
		log.Fatal("Can't open error log:", ferr)
	}
}

func errLog(msg string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, msg, args...)
	fmt.Fprintf(errorLog, msg, args...)
}

func errMsg(msg string, err error) {
	now := time.Now()
	errLog("%s\t%s: %s\n", now.Format(layout), msg, err)
}

func main() {
	var wg sync.WaitGroup
	defer func() {
		errorLog.Close()
	}()
	for _, c := range cfg.Snmp {
		wg.Add(1)
		go c.Gather(repeat, &wg)
	}
	if repeat > 0 {
		wg.Wait()
	} else {
		if httpPort > 0 {
			webServer(httpPort)
		} else {
			<-quit
		}
	}
}
