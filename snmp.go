package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/soniah/gosnmp"
)

type Control struct {
	nameOid string
	found   map[string]string
	labels  map[string]string
	usable  map[string]struct{}
}

var (
	errorSNMP int
	nameOid   = "1.3.6.1.2.1.31.1.1.1.1" // ifName
)

const (
	maxOids = 60 // const in gosnmp
)

type pduValue struct {
	name, column string
	value        interface{}
}

func getPoint(cfg *SnmpConfig, pdu gosnmp.SnmpPDU) *pduValue {
	i := strings.LastIndex(pdu.Name, ".")
	root := pdu.Name[1:i]
	suffix := pdu.Name[i+1:]
	col := cfg.labels[cfg.asOID[suffix]]
	name, ok := oidToName[root]
	if verbose {
		log.Println("ROOT:", root, "SUFFIX:", suffix, "COL:", col, "NAME:", "VALUE:", pdu.Value)
	}
	if !ok {
		log.Printf("Invalid oid: %s\n", pdu.Name)
		return nil
	}
	if len(col) == 0 {
		log.Println("empty col for:", cfg.asOID[suffix])
		return nil // not an OID of interest
	}
	return &pduValue{name, col, pdu.Value}
}

func snmpStats(snmp *gosnmp.GoSNMP, cfg *SnmpConfig) error {
	now := time.Now()
	if cfg == nil {
		log.Fatal("cfg is nil")
	}
	if cfg.Influx == nil {
		log.Fatal("influx cfg is nil")
	}
	bps := cfg.Influx.BP()
	// we can only get 'maxOids' worth of snmp requests at a time
	for i := 0; i < len(cfg.oids); i += maxOids {
		end := i + maxOids
		if end > len(cfg.oids) {
			end = len(cfg.oids)
		}
		cfg.incRequests()
		pkt, err := snmp.Get(cfg.oids[i:end])
		if err != nil {
			errLog("SNMP (%s) get error: %s\n", cfg.Host, err)
			cfg.incErrors()
			cfg.LastError = now
			return err
		}
		cfg.incGets()
		if verbose {
			log.Println("SNMP GET CNT:", len(pkt.Variables))
		}
		for _, pdu := range pkt.Variables {
			val := getPoint(cfg, pdu)
			if val == nil {
				continue
			}
			pt := makePoint(cfg.Influx, val, now)
			bps.Points = append(bps.Points, pt)

		}
	}
	cfg.Influx.Send(bps)
	return nil
}

func printSnmpNames(c *SnmpConfig) {
	client, err := snmpClient(c)
	if err != nil {
		fatal(err)
	}
	defer client.Conn.Close()
	pdus, err := client.BulkWalkAll(nameOid)
	if err != nil {
		fatal("SNMP bulkwalk error", err)
	}
	for _, pdu := range pdus {
		switch pdu.Type {
		case gosnmp.OctetString:
			fmt.Println(string(pdu.Value.([]byte)), pdu.Name)
		}
	}
}

func snmpClient(s *SnmpConfig) (*gosnmp.GoSNMP, error) {
	client := &gosnmp.GoSNMP{
		Target:    s.Host,
		Port:      uint16(s.Port),
		Community: s.Public,
		Version:   gosnmp.Version2c,
		Timeout:   time.Duration(s.Timeout) * time.Second,
		Retries:   s.Retries,
	}
	err := client.Connect()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	return client, err
}

func (s *SnmpConfig) DebugLog() *log.Logger {
	name := filepath.Join(logDir, "debug_"+strings.Replace(s.Host, ".", "-", -1)+".log")
	if l, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0664); err == nil {
		return log.New(l, "", 0)
	} else {
		fmt.Fprintln(os.Stderr, err)
		return nil
	}
}

func (s *SnmpConfig) Gather(count int, wg *sync.WaitGroup) {
	debug := false
	client, err := snmpClient(s)
	if err != nil {
		fatal(err)
	}
	defer client.Conn.Close()
	spew(strings.Join(s.oids, "\n"))
	c := time.Tick(time.Duration(freq) * time.Second)
	for {
		err := snmpStats(client, s)
		if count > 0 {
			count--
			if count == 0 {
				break
			}
		}
		// was seeing clients getting "wedged" -- so just restart
		if err != nil {
			errLog("snmp error - reloading snmp client: %s", err)
			client.Conn.Close()
			for {
				if client, err = snmpClient(s); err == nil {
					break
				}
				errLog("snmp client connect error: %s", err)
				time.Sleep(time.Duration(s.Timeout) * time.Second)
			}
		}

		// pause for interval period and have optional debug toggling
	LOOP:
		for {
			select {
			case <-c:
				break LOOP
			case debug := <-s.debugging:
				log.Println("debugging:", debug)
				if debug && client.Logger == nil {
					client.Logger = s.DebugLog()
				} else {
					client.Logger = nil
				}
			case status := <-s.enabled:
				status <- debug
			}
		}
	}
	wg.Done()
}
