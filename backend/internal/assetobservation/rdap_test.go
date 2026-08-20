package assetobservation

import (
	"errors"
	"testing"
	"time"
)

func TestRegistrableDomain(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "api.Example.COM.", want: "example.com"},
		{input: "*.service.example.co.uk", want: "example.co.uk"},
		{input: "门户.中国", want: "xn--zoup46i.xn--fiqs8s"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := registrableDomain(tt.input)
			if err != nil {
				t.Fatalf("registrableDomain() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("registrableDomain() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegistrableDomainRejectsInvalidInput(t *testing.T) {
	if _, err := registrableDomain("localhost"); !errors.Is(err, errRDAPUnsupported) {
		t.Fatalf("registrableDomain() error = %v, want errRDAPUnsupported", err)
	}
}

func TestParseBootstrapServices(t *testing.T) {
	payload := rdapBootstrap{Services: [][][]string{
		{{"com", ".NET"}, {"https://rdap.example.test/v1", "http://insecure.test/"}},
		{{"com"}, {"https://rdap.example.test/v1/"}},
	}}
	got := parseBootstrapServices(payload)
	if len(got["com"]) != 1 || got["com"][0] != "https://rdap.example.test/v1/" {
		t.Fatalf("unexpected com services: %#v", got["com"])
	}
	if len(got["net"]) != 1 {
		t.Fatalf("unexpected net services: %#v", got["net"])
	}
}

func TestRDAPExpiration(t *testing.T) {
	events := []struct {
		Action string `json:"eventAction"`
		Date   string `json:"eventDate"`
		Actor  string `json:"eventActor"`
	}{
		{Action: "registration", Date: "2020-01-01T00:00:00Z"},
		{Action: "expiration", Date: "2027-03-01T00:00:00Z"},
		{Action: "expiration", Date: "2028-03-01T00:00:00Z"},
	}
	got, metadata, err := rdapExpiration(events)
	if err != nil {
		t.Fatalf("rdapExpiration() error = %v", err)
	}
	want := time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("rdapExpiration() = %s, want %s", got, want)
	}
	if len(metadata) != 3 {
		t.Fatalf("metadata length = %d, want 3", len(metadata))
	}
}

func TestRDAPExpirationPrefersRegistrarExpiration(t *testing.T) {
	events := []struct {
		Action string `json:"eventAction"`
		Date   string `json:"eventDate"`
		Actor  string `json:"eventActor"`
	}{
		{Action: "expiration", Date: "2029-03-01T00:00:00Z"},
		{Action: "registrar expiration", Date: "2028-03-01T00:00:00Z"},
	}
	got, _, err := rdapExpiration(events)
	if err != nil {
		t.Fatalf("rdapExpiration() error = %v", err)
	}
	want := time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("rdapExpiration() = %s, want registrar expiration %s", got, want)
	}
}
