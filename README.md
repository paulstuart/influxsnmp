influxsnmp
==========
Poll network devices via SNMP and save the data in InfluxDB (version 0.12.x)
It uses [github.com/paulstuart/snmputil](https://github.com/paulstuart/snmputil) for snmp processing, and therefore the following functionality:

  * SNMP versions 1, 2, 2c, 3
  * Bulk polling of tabular data
  * Regexp filtering by name of resulting data
  * Auto generating OID lookup for names (if net-snmp-utils is installed)
  * Auto conversion of INTEGER and BIT formats to their named types
  * Optional processing of counter data (deltas and differentials)
