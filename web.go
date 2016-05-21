package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
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

var webHandlers = []hFunc{
	{"/favicon.ico", faviconPage},
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
