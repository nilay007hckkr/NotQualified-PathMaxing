package classify

import (
	"testing"

	"pathmaxing/internal/domain"
)

func TestParseUA(t *testing.T) {
	tests := []struct {
		ua          string
		device      domain.Device
		browser, os string
	}{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36", domain.DeviceDesktop, "Chrome", "Windows"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0", domain.DeviceDesktop, "Edge", "Windows"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", domain.DeviceDesktop, "Safari", "macOS"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0", domain.DeviceDesktop, "Firefox", "Linux"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", domain.DeviceMobile, "Safari", "iOS"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/129.0.6668.69 Mobile/15E148 Safari/604.1", domain.DeviceMobile, "Chrome", "iOS"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.6668.81 Mobile Safari/537.36", domain.DeviceMobile, "Chrome", "Android"},
		{"Mozilla/5.0 (Linux; Android 14; SAMSUNG SM-S918B) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/26.0 Chrome/122.0.0.0 Mobile Safari/537.36", domain.DeviceMobile, "Samsung Internet", "Android"},
		{"Mozilla/5.0 (Linux; Android 13; SM-X710) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36", domain.DeviceTablet, "Chrome", "Android"},
		{"Mozilla/5.0 (iPad; CPU OS 16_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Mobile/15E148 Safari/604.1", domain.DeviceTablet, "Safari", "iOS"},
		{"Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36", domain.DeviceDesktop, "Chrome", "ChromeOS"},
		{"WhatsApp/2.23.20.0", "", "Other", "Other"},
		{"curl/8.4.0", "", "Other", "Other"},
		{"", "", "Other", "Other"},
	}
	for _, tt := range tests {
		got := ParseUA(tt.ua)
		if got.Device != tt.device || got.Browser != tt.browser || got.OS != tt.os {
			t.Errorf("ParseUA(%.60q) = %+v, want %s/%s/%s", tt.ua, got, tt.device, tt.browser, tt.os)
		}
	}
}
