package assetobservation

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestObservationPlans(t *testing.T) {
	tests := []struct {
		name        string
		target      assetTarget
		wantTypes   []string
		wantTLSKey  string
		unsupported bool
	}{
		{
			name:       "public domain",
			target:     assetTarget{AssetType: "domain", NormalizedValue: "api.example.com", NetworkScope: "public"},
			wantTypes:  []string{TypeDomainRegistration, TypeTLSCertificate},
			wantTLSKey: "api.example.com:443",
		},
		{
			name:       "https url explicit port",
			target:     assetTarget{AssetType: "url", NormalizedValue: "https://portal.example.com:8443/login", NetworkScope: "public"},
			wantTypes:  []string{TypeDomainRegistration, TypeTLSCertificate},
			wantTLSKey: "portal.example.com:8443",
		},
		{
			name:        "private domain keeps rdap but skips tls",
			target:      assetTarget{AssetType: "domain", NormalizedValue: "intranet.example.com", NetworkScope: "private"},
			wantTypes:   []string{TypeDomainRegistration, TypeTLSCertificate},
			wantTLSKey:  "intranet.example.com:443",
			unsupported: true,
		},
		{
			name:      "http url only has domain registration",
			target:    assetTarget{AssetType: "url", NormalizedValue: "http://www.example.com/path", NetworkScope: "public"},
			wantTypes: []string{TypeDomainRegistration},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plans, err := observationPlans(tt.target)
			if err != nil {
				t.Fatalf("observationPlans() error = %v", err)
			}
			if len(plans) != len(tt.wantTypes) {
				t.Fatalf("plan count = %d, want %d: %#v", len(plans), len(tt.wantTypes), plans)
			}
			for index, wantType := range tt.wantTypes {
				if plans[index].Type != wantType {
					t.Fatalf("plan[%d].Type = %q, want %q", index, plans[index].Type, wantType)
				}
			}
			if tt.wantTLSKey != "" {
				tlsPlan := plans[len(plans)-1]
				if tlsPlan.EndpointKey != tt.wantTLSKey {
					t.Fatalf("TLS endpoint = %q, want %q", tlsPlan.EndpointKey, tt.wantTLSKey)
				}
				if (tlsPlan.UnsupportedReason != "") != tt.unsupported {
					t.Fatalf("unsupported = %v, want %v", tlsPlan.UnsupportedReason != "", tt.unsupported)
				}
			}
		})
	}
}

func TestObservationPlansRejectsUnsupportedAsset(t *testing.T) {
	_, err := observationPlans(assetTarget{AssetType: "ip", NormalizedValue: "203.0.113.10", NetworkScope: "public"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("observationPlans() error = %v, want ErrUnsupported", err)
	}
}

func TestRefreshInterval(t *testing.T) {
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	if got := refreshInterval(now.Add(10*24*time.Hour), now); got != 6*time.Hour {
		t.Fatalf("near expiry interval = %s", got)
	}
	if got := refreshInterval(now.Add(90*24*time.Hour), now); got != 24*time.Hour {
		t.Fatalf("normal interval = %s", got)
	}
}

func TestIsPublicIP(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1", "10.0.0.1", "100.64.0.1", "169.254.169.254", "192.0.2.1", "198.18.0.1",
		"198.51.100.1", "203.0.113.1", "::1", "fc00::1", "2001:db8::1",
	} {
		if isPublicIP(net.ParseIP(raw)) {
			t.Fatalf("%s should not be public", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !isPublicIP(net.ParseIP(raw)) {
			t.Fatalf("%s should be public", raw)
		}
	}
}
