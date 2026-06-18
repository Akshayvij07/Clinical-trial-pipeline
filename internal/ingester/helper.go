package ingester

import (
	"regexp"
	"strings"
	"time"
)

// extractBlock returns up to n chars of html around needle.
func extractBlock(html, needle string, n int) string {
	i := strings.Index(html, needle)
	if i < 0 {
		return ""
	}
	start := i - n
	if start < 0 {
		start = 0
	}
	end := i + n
	if end > len(html) {
		end = len(html)
	}
	return html[start:end]
}

// extractField looks for class="field" patterns and grabs the value.
func extractField(block, class string) string {
	patterns := []string{
		`class="` + class + `"`,
		`class='` + class + `'`,
		strings.ToLower(class) + `">`,
	}
	for _, p := range patterns {
		i := strings.Index(strings.ToLower(block), strings.ToLower(p))
		if i < 0 {
			continue
		}
		after := block[i:]
		val := extractBetween(after, ">", "<")
		if v := cleanText(val); v != "" {
			return v
		}
	}
	return ""
}

func extractBetween(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	s = s[i+len(open):]
	j := strings.Index(s, close)
	if j < 0 {
		return s
	}
	return strings.TrimSpace(s[:j])
}

func splitClean(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	var out []string
	for _, p := range parts {
		if t := cleanText(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// cleanStrings trims, drops empties, and dedupes a slice of already-split values.
func cleanStrings(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		t := cleanText(s)
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}

func cleanText(s string) string {
	// strip HTML tags
	tagRe := regexp.MustCompile(`<[^>]+>`)
	s = tagRe.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func parseDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, f := range []string{"2006-01-02", "02/01/2006", "January 2006", "2006"} {
		if t, err := time.Parse(f, s); err == nil {
			return &t
		}
	}
	return nil
}
