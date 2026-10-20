package classify

import (
	"strings"

	"pathmaxing/internal/domain"
)

// Agent is what a user agent string says about the device and software.
type Agent struct {
	Device  domain.Device // empty when it can't be told (most bots)
	Browser string
	OS      string
}

// ParseUA reads the device, browser and OS from a user agent. It looks for
// the tokens browsers have used for years rather than parsing versions, which
// is enough for device targeting and analytics breakdowns. iPads on iPadOS 13
// and later send a desktop Mac user agent, so they count as desktop.
func ParseUA(ua string) Agent {
	s := strings.ToLower(ua)
	var a Agent

	switch {
	case strings.Contains(s, "windows"):
		a.OS = "Windows"
	case strings.Contains(s, "iphone"), strings.Contains(s, "ipad"), strings.Contains(s, "ipod"):
		a.OS = "iOS"
	case strings.Contains(s, "android"):
		a.OS = "Android"
	case strings.Contains(s, "cros"):
		a.OS = "ChromeOS"
	case strings.Contains(s, "mac os x"), strings.Contains(s, "macintosh"):
		a.OS = "macOS"
	case strings.Contains(s, "linux"):
		a.OS = "Linux"
	default:
		a.OS = "Other"
	}

	switch {
	case strings.Contains(s, "edg/"), strings.Contains(s, "edga/"), strings.Contains(s, "edgios/"):
		a.Browser = "Edge"
	case strings.Contains(s, "opr/"), strings.Contains(s, "opera"):
		a.Browser = "Opera"
	case strings.Contains(s, "samsungbrowser/"):
		a.Browser = "Samsung Internet"
	case strings.Contains(s, "firefox/"), strings.Contains(s, "fxios/"):
		a.Browser = "Firefox"
	case strings.Contains(s, "chrome/"), strings.Contains(s, "crios/"):
		a.Browser = "Chrome"
	case strings.Contains(s, "safari/") && strings.Contains(s, "version/"):
		a.Browser = "Safari"
	default:
		a.Browser = "Other"
	}

	if !strings.HasPrefix(s, "mozilla/") {
		return a // not a browser: leave the device unknown
	}
	switch {
	case strings.Contains(s, "ipad"), strings.Contains(s, "tablet"),
		strings.Contains(s, "android") && !strings.Contains(s, "mobile"):
		a.Device = domain.DeviceTablet
	case strings.Contains(s, "mobi"), strings.Contains(s, "iphone"), strings.Contains(s, "ipod"):
		a.Device = domain.DeviceMobile
	default:
		a.Device = domain.DeviceDesktop
	}
	return a
}
