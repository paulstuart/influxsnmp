package main

import (
	"html/template"
	"time"
)

var (
	tmpl, errs, logs *template.Template
	funcMap          = template.FuncMap{
		"dateFmt": dateFmt,
	}
)

func init() {
	tmpl = template.Must(template.New("home").Funcs(funcMap).Parse(page))
	errs = template.Must(template.New("errors").Parse(errors))
	logs = template.Must(template.New("logs").Parse(logfiles))
	tmpl = tmpl.Funcs(funcMap)
}

func dateFmt(when interface{}) string {
	t := when.(time.Time)
	if t.IsZero() {
		return ""
	}
	return t.Format(layout)
}

const (
	page = `<!DOCTYPE html>
<html lang="en" xml:lang="en">
<head>
<title>Netstats</title>
<style>
p {
    lineheight: 50%;
    margin-top: 0.01em;
    -webkit-margin-before: 0.5em;
    -webkit-margin-after: 0.5em;
}
div {
    margin: 0.5em;
    border: 2px solid black;
}
.snmp {
    font-weight: bold;
}
</style>
</head>
<body>
<h1>Netstats</h1>
<p>Started: {{.Started}}</p>
<p>Uptime: {{.Uptime}}</p>
{{ range $key,$stat := .SnmpStats }}
<div>
<p class="snmp">SNMP Stats for {{$key}}</p>
<p>Get count: {{$stat.GetCnt}}</p>
<p>Error count: {{$stat.ErrCnt}}</p>
<p>Last error: {{$stat.LastError}} ({{dateFmt $stat.LastTime}})</p>
{{/*
*/}}
</div>
{{ end }}
<p><a href="/logs">Logs files</a></p>
<h1>Config</h1>
{{ range $key,$snmp := .SNMP }}
<div>
<p class="snmp">SNMP {{$key}}</p>
<p>Host: {{$snmp.Host}}</p>
<p>Freq: {{$snmp.Freq}}</p>
<p>Retries: {{$snmp.Retries}}</p>
<p>Timeout: {{$snmp.Timeout}}</p>
</div>
{{ end}}
{{ range $key,$influx := .Influx }}
<div>
<p class="snmp">Influx {{$key}}</p>
<p>Host: {{$influx.Host}}</p>
<p>Database: {{$influx.Database}}</p>
{{/*
<p>Sent: {{$influx.Sent}}</p>
<p>Errors: {{$influx.Errors}}</p>
*/}}
</div>
{{ end }}
<p><a href="/debug/pprof/">Profiler</a></p>
</body>
</html>
`

	errors = `<!DOCTYPE html>
<html lang="en" xml:lang="en">
<head>
<title>Errors</title>
</head>
<body>
<h1>Errors</h1>
{{ range .Errors}}
<p>{{.}}</p>
{{end}}
</body>
</html>
`

	logfiles = `<!DOCTYPE html>
<html lang="en" xml:lang="en">
<head>
<title>Log Files</title>
</head>
<body>
<h1><a href="/">Home</a></h1>
<h1>Log Files</h1>
{{ range .LogFiles }}
<form action="/logs/{{.Name}}" method="POST">
<p><a href="/logs/{{.Name}}">{{.Name}}</a><button type="submit">{{ if eq .Name $.ErrLog }}Truncate{{else}}Delete{{end}}</button></p>
<input type="hidden" name="truncate" value="{{ if eq .Name $.ErrLog}}true{{else}}false{{end}}"></p>
<input type="hidden" name="errlog" value="{{$.ErrLog}}">
</form>
{{end}}
</body>
</html>
`
)
