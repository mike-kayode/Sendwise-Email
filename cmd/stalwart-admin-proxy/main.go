package main

import (
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"
)

func main() {
	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = "172.17.0.1:18081"
	}
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		log.Fatalf("invalid LISTEN_ADDR: %v", err)
	}
	ip := net.ParseIP(host)
	if err := validateHostGatewayIP(ip); err != nil {
		log.Fatal(err)
	}

	target, err := url.Parse("http://127.0.0.1:8080")
	if err != nil {
		log.Fatalf("invalid Stalwart management URL: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "Stalwart management endpoint unavailable", http.StatusBadGateway)
		log.Printf("management proxy upstream error: %v", err)
	}
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           proxy,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("management proxy listen failed: %v", err)
	}
}

func validateHostGatewayIP(ip net.IP) error {
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() {
		return net.InvalidAddrError("host-gateway must be a private, non-loopback IP")
	}
	return nil
}
