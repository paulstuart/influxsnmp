influxsnmp
==========
Poll network devices via SNMP and save the data in InfluxDB (version 0.9.x)

It requires mib oids to be "pre-digested", e.g.,

    snmptranslate -M $MIBDIR -Tz -On -m IF-MIB | sed -e 's/"//g' > oids.txt

The results from the above are included in the project as a start.
