package urldetect

import (
	"testing"
)

func TestExtract(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "single URL",
			text: "check out https://example.com for more",
			want: []string{"https://example.com"},
		},
		{
			name: "multiple URLs",
			text: "see https://example.com and http://other.org/path",
			want: []string{"https://example.com", "http://other.org/path"},
		},
		{
			name: "trailing period",
			text: "Visit https://example.com.",
			want: []string{"https://example.com"},
		},
		{
			name: "trailing comma",
			text: "Links: https://a.com, https://b.com, done",
			want: []string{"https://a.com", "https://b.com"},
		},
		{
			name: "trailing exclamation",
			text: "Wow https://example.com!",
			want: []string{"https://example.com"},
		},
		{
			name: "trailing question mark",
			text: "Have you seen https://example.com?",
			want: []string{"https://example.com"},
		},
		{
			name: "URL in parentheses",
			text: "check this (https://example.com/page) out",
			want: []string{"https://example.com/page"},
		},
		{
			name: "URL with query params",
			text: "see https://example.com/search?q=hello&lang=en for results",
			want: []string{"https://example.com/search?q=hello&lang=en"},
		},
		{
			name: "URL with balanced parens (Wikipedia)",
			text: "see https://en.wikipedia.org/wiki/Foo_(bar) for info",
			want: []string{"https://en.wikipedia.org/wiki/Foo_(bar)"},
		},
		{
			name: "URL with unbalanced closing paren",
			text: "(see https://example.com/path)",
			want: []string{"https://example.com/path"},
		},
		{
			name: "no URLs",
			text: "just plain text with no links",
			want: nil,
		},
		{
			name: "empty string",
			text: "",
			want: nil,
		},
		{
			name: "deduplicate",
			text: "https://example.com and https://example.com again",
			want: []string{"https://example.com"},
		},
		{
			name: "tweet URL",
			text: "check https://twitter.com/user/status/123456789",
			want: []string{"https://twitter.com/user/status/123456789"},
		},
		{
			name: "URL with fragment",
			text: "see https://example.com/page#section for details",
			want: []string{"https://example.com/page#section"},
		},
		{
			name: "trailing semicolon",
			text: "link: https://example.com;",
			want: []string{"https://example.com"},
		},
		{
			name: "trailing colon",
			text: "link: https://example.com:",
			want: []string{"https://example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Extract(tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("Extract(%q) = %v, want %v", tt.text, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("Extract(%q)[%d] = %q, want %q", tt.text, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestIsXURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://twitter.com/user/status/123", true},
		{"https://mobile.twitter.com/user/status/123", true},
		{"https://x.com/user/status/123", true},
		{"http://twitter.com/user", true},
		{"https://twitter.com", true},
		{"https://example.com", false},
		{"https://nottwitter.com", false},
		{"https://x.com.evil.com/foo", false},
		{"not a url", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := IsXURL(tt.url); got != tt.want {
				t.Errorf("IsXURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestExtractTweetID(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://twitter.com/user/status/123456789", "123456789"},
		{"https://x.com/someone/status/987654321", "987654321"},
		{"https://mobile.twitter.com/user/status/111222333", "111222333"},
		{"http://twitter.com/user/status/444555666", "444555666"},
		{"https://twitter.com/user/status/123456789?s=20", "123456789"},
		{"https://twitter.com/user", ""},
		{"https://example.com/user/status/123", ""},
		{"https://twitter.com", ""},
		{"not a url", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := ExtractTweetID(tt.url); got != tt.want {
				t.Errorf("ExtractTweetID(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}
