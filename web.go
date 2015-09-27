package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"
)

type HFunc struct {
	Path string
	Func http.HandlerFunc
}

func MyIps() (ips []string) {
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !strings.HasPrefix(ipnet.String(), "127.") && strings.Contains(ipnet.String(), ".") {
			ips = append(ips, strings.Split(ipnet.String(), "/")[0])
		}
	}
	return
}

func FaviconPage(w http.ResponseWriter, r *http.Request) {
	fav := "favicon.ico"
	http.ServeFile(w, r, fav)
}

const page = `<!DOCTYPE html>
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
<p><a href="/logs">Logs files</a></p>
<h1>Config</h1>
{{ range $key,$snmp := .SNMP }}
<div>
<p class="snmp">SNMP {{$key}}</p>
<p>Host: {{$snmp.Host}}</p>
<p>Freq: {{$snmp.Freq}}</p>
<p>Retries: {{$snmp.Retries}}</p>
<p>Timeout: {{$snmp.Timeout}}</p>
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
</div>
{{ end}}
{{ range $key,$influx := .Influx }}
<div>
<p class="snmp">Influx {{$key}}</p>
<p>Host: {{$influx.Host}}</p>
<p>Database: {{$influx.DB}}</p>
<p>Sent: {{$influx.Sent}}</p>
<p>Errors: {{$influx.Errors}}</p>
</div>
{{ end }}
<p><a href="/debug/pprof/">Profiler</a></p>
</body>
</html>
`

const errors = `<!DOCTYPE html>
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

const logfiles = `<!DOCTYPE html>
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

var tmpl = template.Must(template.New("home").Parse(page))
var errs = template.Must(template.New("errors").Parse(errors))
var logs = template.Must(template.New("logs").Parse(logfiles))

func HomePage(w http.ResponseWriter, r *http.Request) {
	const layout = "Jan 2, 2006 at 3:04pm (MST)"
	data := struct {
		Period, Started string
		Uptime          string
		DB, LogFile     string
		DebugAction     string
		SNMP            map[string]*SnmpConfig
		Influx          map[string]*InfluxConfig
	}{
		LogFile: errorName,
		Started: startTime.Format(layout),
		Uptime:  time.Now().Sub(startTime).String(),
		Period:  errorPeriod,
		SNMP:    cfg.Snmp,
		Influx:  cfg.Influx,
	}

	if err := tmpl.Execute(w, data); err != nil {
		errLog("home error:%s\n", err)
	}
}

func getErrors() []string {
	data, err := ioutil.ReadFile(errorName)
	if err != nil {
		errLog("error reading log file: %s error:%s\n", errorName, err)
		return []string{}
	}
	lines := strings.Split(string(data), "\n")
	sort.Sort(sort.Reverse(sort.StringSlice(lines)))
	return lines
}

func ErrorsPage(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Errors  []string
		LogFile string
	}{
		Errors:  getErrors(),
		LogFile: errorName,
	}
	if err := errs.Execute(w, data); err != nil {
		errLog("home error:%s\n", err)
	}
}

func DebugPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		r.ParseForm()
		action := r.Form.Get("action")
		host := r.Form.Get("host")
		fmt.Println("debug action:", action, "host:", host, "debug", (action == "enable"))
		for _, c := range cfg.Snmp {
			if host == c.Host {
				c.debugging <- (action == "enable")
				break
			}
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func LogsPage(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Path
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i:]
	}
	name = filepath.Join(logDir, name)
	if r.Method == "POST" {
		if _, err := os.Stat(name); err != nil {
			fmt.Println("file doesn't exist:", name, "error:", err)
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if name == errorName {
			if err := errorLog.Truncate(0); err != nil {
				errLog("truncate of error log failure: %v\n", err)
			}
		} else {
			if err := os.Remove(name); err != nil {
				errLog("delete file error: %v\n", err)
			}
		}
		http.Redirect(w, r, "/logs", http.StatusSeeOther)
		return
	} else if r.Method == "GET" {
		file, err := os.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		fi, _ := file.Stat()
		w.Header().Set("Cache-control", "public, max-age=259200")
		http.ServeContent(w, r, name, fi.ModTime(), file)
	}
}

func LogsList(w http.ResponseWriter, r *http.Request) {
	files, err := ioutil.ReadDir(logDir)
	if err != nil {
		fmt.Fprintln(w, err)
	} else if len(files) == 0 {
		fmt.Fprintln(w, "No logs found")
	} else {
		data := struct {
			LogFiles []os.FileInfo
			ErrLog   string
		}{
			LogFiles: files,
			ErrLog:   errorName,
		}
		if err := logs.Execute(w, data); err != nil {
			errLog("logs list error:%s\n", err)
		}
	}
}

var webHandlers = []HFunc{
	{"/logs/", LogsPage},
	{"/logs", LogsList},
	{"/favicon.ico", FaviconPage},
	{"/errors", ErrorsPage},
	{"/snmp/debug", DebugPage},
	{"/", HomePage},
}

func webServer(port int) {
	if port < 80 {
		log.Fatal("Invalid port:", port)
	}
	for _, h := range webHandlers {
		http.HandleFunc(h.Path, h.Func)
	}

	http_server := fmt.Sprintf(":%d", port)
	fmt.Println("Web interface:")
	for _, ip := range MyIps() {
		fmt.Printf("http://%s:%d\n", ip, port)
	}
	http.ListenAndServe(http_server, nil)
}
