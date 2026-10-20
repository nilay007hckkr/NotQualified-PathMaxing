// Package classify labels each visit as a human or as a kind of bot, with a
// reason code saying why.
package classify

import (
	"net/http"
	"strings"

	"pathmaxing/internal/domain"
)

// Reason codes. They are stored with every click event, so a dashboard can
// show why a click was not counted as human.
const (
	ReasonBrowser      = "browser_user_agent"
	ReasonHead         = "head_request"
	ReasonPrefetch     = "prefetch"
	ReasonEmptyUA      = "empty_user_agent"
	ReasonLinkPreview  = "ua_link_preview"
	ReasonBotKeyword   = "ua_bot_keyword"
	ReasonHTTPLibrary  = "ua_http_library"
	ReasonNonBrowserUA = "ua_not_a_browser"
)

// Simple is the default classifier. It trusts what the user agent says
// about itself and treats HEAD and prefetch requests as bots, because a
// person clicking a link sends a GET. The honest classifier replaces it with
// more signals.
type Simple struct{}

// linkPreviewAgents fetch a link the moment it is pasted into a chat, to
// build a preview card. Counting them as clicks would burn one-time links.
var linkPreviewAgents = []string{
	"whatsapp", "facebookexternalhit", "facebot", "slackbot", "twitterbot",
	"telegrambot", "discordbot", "linkedinbot", "skypeuripreview", "redditbot",
}

var botKeywords = []string{"bot", "crawler", "spider", "slurp", "scanner", "preview"}

var httpLibraries = []string{
	"curl/", "wget/", "python-requests", "python-urllib", "go-http-client",
	"java/", "okhttp", "axios/", "node-fetch", "libwww-perl", "httpclient",
}

// Classify implements domain.TrafficClassifier.
func (Simple) Classify(r domain.RequestInfo) domain.Classification {
	ua := strings.ToLower(strings.TrimSpace(r.UserAgent))
	switch {
	case r.Method == http.MethodHead:
		return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonHead}
	case strings.EqualFold(r.Purpose, "prefetch"):
		return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonPrefetch}
	case ua == "":
		return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonEmptyUA}
	case containsAny(ua, linkPreviewAgents):
		return domain.Classification{Class: domain.ClassLinkPreview, Reason: ReasonLinkPreview}
	case containsAny(ua, botKeywords):
		return domain.Classification{Class: domain.ClassCrawler, Reason: ReasonBotKeyword}
	case containsAny(ua, httpLibraries):
		return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonHTTPLibrary}
	case !strings.HasPrefix(ua, "mozilla/"):
		// Every mainstream browser still starts its user agent with Mozilla/.
		return domain.Classification{Class: domain.ClassOtherBot, Reason: ReasonNonBrowserUA}
	}
	return domain.Classification{Class: domain.ClassHuman, Reason: ReasonBrowser}
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
