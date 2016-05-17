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
)

// hFunc defines the path and the function associated with it
type hFunc struct {
	Path string
	Func http.HandlerFunc
}

func myIps() (ips []string) {
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !strings.HasPrefix(ipnet.String(), "127.") && strings.Contains(ipnet.String(), ".") {
			ips = append(ips, strings.Split(ipnet.String(), "/")[0])
		}
	}
	return
}

func faviconPage(w http.ResponseWriter, r *http.Request) {
	fav := "favicon.ico"
	http.ServeFile(w, r, fav)
}

func homePage(w http.ResponseWriter, r *http.Request) {
	const layout = "Jan 2, 2006 at 3:04pm (MST)"

	if err := tmpl.Execute(w, status()); err != nil {
		log.Printf("home error:%s\n", err)
	}
}

func getErrors() []string {
	data, err := ioutil.ReadFile(errorName)
	if err != nil {
		log.Printf("error reading log file: %s error:%s\n", errorName, err)
		return []string{}
	}
	lines := strings.Split(string(data), "\n")
	sort.Sort(sort.Reverse(sort.StringSlice(lines)))
	return lines
}

func errorsPage(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Errors  []string
		LogFile string
	}{
		Errors:  getErrors(),
		LogFile: errorName,
	}
	if err := errs.Execute(w, data); err != nil {
		log.Printf("home error:%s\n", err)
	}
}

func logsPage(w http.ResponseWriter, r *http.Request) {
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
				log.Printf("truncate of error log failure: %v\n", err)
			}
		} else {
			if err := os.Remove(name); err != nil {
				log.Printf("delete file error: %v\n", err)
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

func logsList(w http.ResponseWriter, r *http.Request) {
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
			log.Printf("logs list error:%s\n", err)
		}
	}
}

var webHandlers = []hFunc{
	{"/logs/", logsPage},
	{"/logs", logsList},
	{"/favicon.ico", faviconPage},
	{"/errors", errorsPage},
	{"/", homePage},
}

func webServer(port int) {
	for _, h := range webHandlers {
		http.HandleFunc(h.Path, h.Func)
	}

	server := fmt.Sprintf(":%d", port)
	fmt.Println("Web interface:")
	for _, ip := range myIps() {
		fmt.Printf("http://%s:%d\n", ip, port)
	}
	http.ListenAndServe(server, nil)
}
