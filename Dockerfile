FROM golang:1.9.1

ADD *.go /go/src/github.com/wrboyce/influxsnmp/
RUN go get -v github.com/wrboyce/influxsnmp

VOLUME /app
CMD influxsnmp -config /app/config.gcfg
