package main

import (
	"net"
	"testing"
)

func TestHostGatewayAddressMustBePrivateAndNonLoopback(t *testing.T) {
	tests := []struct {
		addr string
		ok   bool
	}{
		{addr: "172.17.0.1:18081", ok: true},
		{addr: "10.0.0.1:18081", ok: true},
		{addr: "127.0.0.1:18081", ok: false},
		{addr: "8.8.8.8:18081", ok: false},
		{addr: "0.0.0.0:18081", ok: false},
		{addr: "invalid", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			host, _, err := net.SplitHostPort(tt.addr)
			if err == nil {
				ip := net.ParseIP(host)
				err = validateHostGatewayIP(ip)
			}
			if (err == nil) != tt.ok {
				t.Fatalf("validate %q error = %v, want success %t", tt.addr, err, tt.ok)
			}
		})
	}
}
