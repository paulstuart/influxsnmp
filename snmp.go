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

// save snmp data so the collected results can be sent off in bulk
func addValue(ctrl *Control, series *InfluxSeries, pdu gosnmp.SnmpPDU) error {
	i := strings.LastIndex(pdu.Name, ".")
	root := pdu.Name[1:i]
	suffix := pdu.Name[i+1:]
	switch pdu.Type {
	case gosnmp.OctetString:
		if root == ctrl.nameOid {
			b := pdu.Value.([]byte)
			ctrl.found[suffix] = string(b)
		}
	default:
		name, ok := oidToName[root]
		if !ok {
			name = root
		}
		if _, ok := ctrl.usable[name]; !ok {
			return nil
		}
		what := ctrl.found[suffix]
		if col, ok := ctrl.labels[what]; ok {
			key := fmt.Sprintf("%s.%s", name, col)
			series.Data = append(series.Data, InfluxData{key, pdu.Value})
		}
	}
	return nil
}

func addToSeries(c *SnmpConfig, series *InfluxSeries, pdu gosnmp.SnmpPDU) error {
	i := strings.LastIndex(pdu.Name, ".")
	root := pdu.Name[1:i]
	suffix := pdu.Name[i+1:]
	col := c.labels[c.asOID[suffix]]
	name, ok := oidToName[root]
	if !ok {
		return fmt.Errorf("Invalid oid: %s", pdu.Name)
	}
	if len(col) == 0 {
		return nil // not an OID of interest
	}
	name += "." + col
	series.Data = append(series.Data, InfluxData{name, pdu.Value})
	return nil
}

func snmpStats(client *gosnmp.GoSNMP, cfg *SnmpConfig) error {
	snmpReqs++
	now := time.Now()
	series := InfluxSeries{When: now.Unix() * 1000, prefix: cfg.Prefix}
	for i := 0; i < len(cfg.oids); i += maxOids {
		end := i + maxOids
		if end > len(cfg.oids) {
			end = len(cfg.oids)
		}
		pkt, err := client.Get(cfg.oids[i:end])
		if err != nil {
			errLog("SNMP (%s) get error: %s\n", cfg.Host, err)
			errorSNMP++
			cfg.LastError = now
			return err
		}
		snmpGets++
		for _, pdu := range pkt.Variables {
			addToSeries(cfg, &series, pdu)
		}
	}
	cfg.Influx.Send(series)
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
	if snmpDebug {
		client.Logger = s.DebugLog()
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
	client, err := snmpClient(s)
	if err != nil {
		fatal(err)
	}
	defer client.Conn.Close()
	spew(s.Host, "OIDS -", s.Prefix)
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
			case snmpDebug = <-cDebug:
				if snmpDebug && client.Logger == nil {
					client.Logger = s.DebugLog()
				} else {
					client.Logger = nil
				}
			}
		}
	}
	wg.Done()
}
