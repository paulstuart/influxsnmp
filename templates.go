package main

import (
	"text/template"
)

var (
	tmpl = template.Must(template.New("home").Parse(page))
	errs = template.Must(template.New("errors").Parse(errors))
	logs = template.Must(template.New("logs").Parse(logfiles))
)

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
<p>Last error: {{$stat.Error}} ({{$stat.LastError}})</p>
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
{{/*
<p>Last Error: {{$snmp.LastError}}</p>
<p>DB Host: {{$snmp.Influx.Hostname}}</p>
<p>DB Name: {{$snmp.Influx.DB}}</p>
<p>Errors: {{.Errors}}</p>
<p>Requests: {{.Requests}}</p>
<p>Replies: {{.Gets}}</p>
<form action="/snmp/debug" method="POST">
SNMP Debugging:<button type="submit" value="{{.DebugAction}}">{{.DebugAction}}</button>
<input type="hidden" name="action" id="action" value="{{.DebugAction}}">
<input type="hidden" name="host" id="host" value="{{.Host}}">
</form>
*/}}
</div>
{{ end}}
{{ range $key,$influx := .Influx }}
<div>
<p class="snmp">Influx {{$key}}</p>
<p>Host: {{$influx.Host}}</p>
<p>Database: {{$influx.DB}}</p>
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
