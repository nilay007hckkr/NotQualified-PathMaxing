package geo

import (
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/oschwald/geoip2-golang"
)

type fakeDB map[string]string

func (f fakeDB) Country(ip net.IP) (*geoip2.Country, error) {
	code, ok := f[ip.String()]
	if !ok {
		return nil, errors.New("not found")
	}
	var c geoip2.Country
	c.Country.IsoCode = code
	return &c, nil
}

func (fakeDB) Close() error { return nil }

func TestCountry(t *testing.T) {
	l := &Lookup{db: fakeDB{"49.36.1.1": "IN", "2001:4860::8888": "US"}}
	tests := map[string]string{"49.36.1.1": "IN", "::ffff:49.36.1.1": "IN", "2001:4860::8888": "US", "203.0.113.1": ""}
	for ip, want := range tests {
		if got := l.Country(netip.MustParseAddr(ip)); got != want {
			t.Errorf("Country(%s) = %q, want %q", ip, got, want)
		}
	}
	if l.Country(netip.Addr{}) != "" {
		t.Error("the zero address has a country")
	}
	if _, err := Open("does-not-exist.mmdb"); err == nil {
		t.Error("opening a missing database succeeded")
	}
}
