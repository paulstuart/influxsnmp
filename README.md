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
