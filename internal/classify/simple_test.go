package classify

import (
	"testing"

	"pathmaxing/internal/domain"
)

func TestSimple(t *testing.T) {
	const chrome = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
	tests := []struct {
		name      string
		req       domain.RequestInfo
		wantClass domain.TrafficClass
		wantWhy   string
	}{
		{"desktop Chrome", domain.RequestInfo{Method: "GET", UserAgent: chrome}, domain.ClassHuman, ReasonBrowser},
		{"HEAD is never a click", domain.RequestInfo{Method: "HEAD", UserAgent: chrome}, domain.ClassOtherBot, ReasonHead},
		{"browser prefetch", domain.RequestInfo{Method: "GET", UserAgent: chrome, Purpose: "prefetch"}, domain.ClassOtherBot, ReasonPrefetch},
		{"no user agent", domain.RequestInfo{Method: "GET"}, domain.ClassOtherBot, ReasonEmptyUA},
		{"WhatsApp preview", domain.RequestInfo{Method: "GET", UserAgent: "WhatsApp/2.23.20.0"}, domain.ClassLinkPreview, ReasonLinkPreview},
		{"Facebook preview", domain.RequestInfo{Method: "GET", UserAgent: "facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php)"}, domain.ClassLinkPreview, ReasonLinkPreview},
		{"Slack preview", domain.RequestInfo{Method: "GET", UserAgent: "Slackbot-LinkExpanding 1.0 (+https://api.slack.com/robots)"}, domain.ClassLinkPreview, ReasonLinkPreview},
		{"Googlebot", domain.RequestInfo{Method: "GET", UserAgent: "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"}, domain.ClassCrawler, ReasonBotKeyword},
		{"curl", domain.RequestInfo{Method: "GET", UserAgent: "curl/8.4.0"}, domain.ClassOtherBot, ReasonHTTPLibrary},
		{"python", domain.RequestInfo{Method: "GET", UserAgent: "python-requests/2.31.0"}, domain.ClassOtherBot, ReasonHTTPLibrary},
		{"unknown non-browser", domain.RequestInfo{Method: "GET", UserAgent: "SomeTool/1.0"}, domain.ClassOtherBot, ReasonNonBrowserUA},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Simple{}.Classify(tt.req)
			if got.Class != tt.wantClass || got.Reason != tt.wantWhy {
				t.Fatalf("got %s/%s, want %s/%s", got.Class, got.Reason, tt.wantClass, tt.wantWhy)
			}
		})
	}
}
