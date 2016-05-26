influxsnmp
==========
Poll network devices via SNMP and save the data in InfluxDB (version 0.12.x)
It uses [github.com/paulstuart/snmputil](https://github.com/paulstuart/snmputil) for snmp processing, and therefore has the following functionality:

  * SNMP versions 1, 2/2c, 3
  * Bulk polling of all tabular data
  * Regexp filtering by name of resulting data
  * Auto conversion of INTEGER and BIT formats to their named types
  * Auto generating OID lookup for names (if net-snmp-utils is installed)
  * Optional processing of counter data (deltas and differentials)
  * Overide column aliases with custom labels
  * Auto throttling of requests - never poll faster than device can respond

influxsnmp uses a datafile of parsed MIB objects in order to use symbolic names and to do automated formatting of polled data. If a previously saved file is not available, it will generate and same one automatically. The resulting file of such actions may be quite large (all OIDs included).

To create a MIB file of only the OIDs that will be used, run the following command:

    influxsnmp -dump -filter > mibFile.json

As it is using snmptranslate to create the dump file, one can export MIBDIRS to point to the directories containing mib files
