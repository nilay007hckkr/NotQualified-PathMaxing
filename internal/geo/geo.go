// Package geo maps a visitor's IP address to a country with MaxMind's free
// GeoLite2 Country database, before the address is thrown away. The
// database is optional: without GEOLITE2_DB_PATH no country is recorded.
// GeoLite2 data is by MaxMind (https://www.maxmind.com), which the
// licence asks us to credit.
package geo

import (
	"fmt"
	"net"
	"net/netip"

	"github.com/oschwald/geoip2-golang"
)

// countryReader is the part of *geoip2.Reader we use, so tests don't need
// a database file.
type countryReader interface {
	Country(ip net.IP) (*geoip2.Country, error)
	Close() error
}

// Lookup implements domain.GeoLookup.
type Lookup struct{ db countryReader }

// Open opens a GeoLite2-Country.mmdb file.
func Open(path string) (*Lookup, error) {
	db, err := geoip2.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open GeoLite2 database: %w", err)
	}
	return &Lookup{db: db}, nil
}

// Country returns the ISO country code for ip, or "" when unknown.
func (l *Lookup) Country(ip netip.Addr) string {
	if !ip.IsValid() {
		return ""
	}
	rec, err := l.db.Country(net.IP(ip.Unmap().AsSlice()))
	if err != nil || rec == nil {
		return ""
	}
	return rec.Country.IsoCode
}

// Close releases the database.
func (l *Lookup) Close() error { return l.db.Close() }
