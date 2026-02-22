package urldetect

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	urlRe      = regexp.MustCompile(`https?://[^\s<>\[\]'"` + "`" + `]+`)
	tweetRe    = regexp.MustCompile(`https?://(?:(?:mobile\.)?twitter\.com|x\.com)/\w+/status/(\d+)`)
	trailingRe = regexp.MustCompile(`[.,!?'";:]+$`)
)

// Extract returns all unique URLs found in text.
func Extract(text string) []string {
	matches := urlRe.FindAllString(text, -1)
	seen := make(map[string]struct{}, len(matches))
	var result []string
	for _, m := range matches {
		u := cleanTrailing(m)
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		result = append(result, u)
	}
	return result
}

// cleanTrailing strips trailing punctuation that isn't part of the URL structure.
// It preserves closing parens that have matching opens within the URL.
func cleanTrailing(u string) string {
	for {
		prev := u
		// If it ends with ')' check for balanced parens.
		if strings.HasSuffix(u, ")") {
			opens := strings.Count(u, "(")
			closes := strings.Count(u, ")")
			if closes > opens {
				u = u[:len(u)-1]
				continue
			}
		}
		// If it ends with ']' check for balanced brackets.
		if strings.HasSuffix(u, "]") {
			opens := strings.Count(u, "[")
			closes := strings.Count(u, "]")
			if closes > opens {
				u = u[:len(u)-1]
				continue
			}
		}
		// Strip other trailing punctuation.
		u = trailingRe.ReplaceAllString(u, "")
		if u == prev {
			break
		}
	}
	return u
}

// IsXURL returns true if the URL is a Twitter/X URL.
func IsXURL(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "twitter.com" || host == "mobile.twitter.com" || host == "x.com"
}

// ExtractTweetID returns the tweet ID from a Twitter/X status URL,
// or empty string if not a tweet URL.
func ExtractTweetID(u string) string {
	m := tweetRe.FindStringSubmatch(u)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
