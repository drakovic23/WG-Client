package main

import (
	"net"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func TestGetNextIP(t *testing.T) {
	serverIP := net.ParseIP("10.8.0.1").To4()
	_, subnet, _ := net.ParseCIDR("10.8.0.0/24")

	peer := func(ip string) wgtypes.Peer {
		return wgtypes.Peer{AllowedIPs: []net.IPNet{{IP: net.ParseIP(ip).To4(), Mask: net.CIDRMask(32, 32)}}}
	}

	tests := []struct {
		name  string
		peers []wgtypes.Peer
		want  string
	}{
		{name: "skips the server address", want: "10.8.0.2"},
		{name: "skips a taken address", peers: []wgtypes.Peer{peer("10.8.0.2")}, want: "10.8.0.3"},
		{name: "fills a gap", peers: []wgtypes.Peer{peer("10.8.0.3")}, want: "10.8.0.2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &wgtypes.Device{Peers: tt.peers}
			got, err := getNextIP(d, "new-key", serverIP, *subnet)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}
