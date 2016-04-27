influxsnmp
==========
Poll network devices via SNMP and save the data in InfluxDB (version 0.12.x)

It requires mib oids to be "pre-digested", e.g.,

    snmptranslate -Tz -On -m ALL | tr -d '"' > oids.txt

The results from the above are included in the project as a start.
