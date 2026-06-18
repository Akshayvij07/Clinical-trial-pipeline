package ingester_test

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ── paste the exact functions from euctr.go to test locally ──────────────────

var eudractRe = regexp.MustCompile(`\b(20\d{2}-\d{6}-\d{2})\b`)
var tagRe = regexp.MustCompile(`<[^>]+>`)
var whitespaceRe = regexp.MustCompile(`\s+`)
var entityRe = regexp.MustCompile(`&[a-zA-Z]+;|&#\d+;`)

func cleanHTMLText(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&nbsp;", " ", "&quot;", `"`).Replace(s)
	s = entityRe.ReplaceAllString(s, "")
	s = whitespaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func extractUntilStop(s string) string {
	stops := []string{"<br", "</td", "</tr", "</div", "\n\n"}
	end := len(s)
	for _, stop := range stops {
		if i := strings.Index(strings.ToLower(s), stop); i >= 0 && i < end {
			end = i
		}
	}
	return s[:end]
}

func extractLabelledValue(html, label string) string {
	searchTerms := []string{label + ":", label + "*:"}
	lowerHTML := strings.ToLower(html)
	for _, term := range searchTerms {
		lowerTerm := strings.ToLower(term)
		idx := strings.Index(lowerHTML, lowerTerm)
		if idx < 0 {
			continue
		}
		start := idx + len(term)
		if start >= len(html) {
			continue
		}
		chunk := html[start:]
		value := extractUntilStop(chunk)
		if v := cleanHTMLText(value); v != "" {
			return v
		}
	}
	return ""
}

func extractCountriesAndStatus(html string) ([]string, string) {
	lower := strings.ToLower(html)
	idx := strings.Index(lower, "trial protocol:")
	if idx < 0 {
		return nil, "unknown"
	}
	chunk := html[idx+len("trial protocol:"):]
	chunk = extractUntilStop(chunk)
	chunk = cleanHTMLText(chunk)
	fmt.Println("  [debug] trial protocol chunk:", chunk)

	re := regexp.MustCompile(`([A-Z]{2})\s*\(([^)]+)\)`)
	matches := re.FindAllStringSubmatch(chunk, -1)
	countryMap := map[string]string{
		"DE": "Germany", "FR": "France", "AT": "Austria", "GB": "United Kingdom",
		"IT": "Italy", "ES": "Spain", "NL": "Netherlands", "BE": "Belgium",
	}
	var countries []string
	statusCount := map[string]int{}
	seenCountry := map[string]bool{}
	for _, m := range matches {
		name, ok := countryMap[m[1]]
		if !ok {
			name = m[1]
		}
		if !seenCountry[name] {
			seenCountry[name] = true
			countries = append(countries, name)
		}
		statusCount[strings.ToLower(strings.TrimSpace(m[2]))]++
	}
	overall := "unknown"
	max := 0
	for s, c := range statusCount {
		if c > max {
			max = c
			overall = s
		}
	}
	return countries, overall
}

func extractPhaseFromTitle(title string) string {
	re := regexp.MustCompile(`(?i)phase\s+(I{1,3}V?|[1-4][abc]?(?:/[1-4])?)`)
	m := re.FindStringSubmatch(title)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

func parseDateEUCTR(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, f := range []string{"2006-01-02", "02/01/2006"} {
		if t, err := time.Parse(f, s); err == nil {
			return &t
		}
	}
	return nil
}

func main() {
	// Simulate exactly one <tr> row as it appears on the EUCTR results page
	// Based on the actual data the user pasted
	simulatedRow := `<tr class="odd">
  <td class="first">
    <a href="/ctr-search/trial/2006-004484-54/DE">2006-004484-54</a>
  </td>
  <td>Sponsor Protocol Number: IP-CAT-OC-02</td>
  <td>Start Date*: 2007-08-30</td>
  <td>
    Sponsor Name: Fresenius Biotech GmbH<br/>
    Full Title: Multicenter, single-arm, phase II study of the tri functional antibody catumaxomab (anti EpCAM x anti-CD3) administered intra- and postoperatively in patients with epithelial ovarian cancer<br/>
    Medical condition: Epithelial Ovarian Cancer<br/>
    Population Age: Adults, Elderly &nbsp; Gender: Female
  </td>
  <td>Trial protocol: DE (Completed) AT (Completed)</td>
  <td>Trial results: No results available</td>
</tr>`

	fmt.Println("═══ EUCTR Parser Test ═══")
	fmt.Println()

	// ID
	ids := eudractRe.FindAllString(simulatedRow, -1)
	id := ""
	if len(ids) > 0 {
		id = ids[0]
	}
	fmt.Printf("  ID         → %q\n", id)

	// Title
	title := extractLabelledValue(simulatedRow, "Full Title")
	fmt.Printf("  Title      → %q\n", title)

	// Sponsor
	sponsor := extractLabelledValue(simulatedRow, "Sponsor Name")
	fmt.Printf("  Sponsor    → %q\n", sponsor)

	// Medical Condition
	condition := extractLabelledValue(simulatedRow, "Medical condition")
	fmt.Printf("  Condition  → %q\n", condition)

	// Start Date
	startRaw := extractLabelledValue(simulatedRow, "Start Date")
	startDate := parseDateEUCTR(startRaw)
	fmt.Printf("  Start Date → raw=%q  parsed=%v\n", startRaw, startDate)

	// Countries + Status
	countries, status := extractCountriesAndStatus(simulatedRow)
	fmt.Printf("  Countries  → %v\n", countries)
	fmt.Printf("  Status     → %q\n", status)

	// Phase (from title)
	phase := extractPhaseFromTitle(title)
	fmt.Printf("  Phase      → %q\n", phase)

	// Population / Gender
	pop := extractLabelledValue(simulatedRow, "Population Age")
	gender := extractLabelledValue(simulatedRow, "Gender")
	fmt.Printf("  Population → %q\n", pop)
	fmt.Printf("  Gender     → %q\n", gender)

	// Source URL
	sourceURL := "https://www.clinicaltrialsregister.eu/ctr-search/trial/" + id + "/GB"
	fmt.Printf("  Source URL → %s\n", sourceURL)

	fmt.Println()
	fmt.Println("─── Trial Struct Mapping ───")
	fmt.Printf("  Trial.ID          = %q\n", id)
	fmt.Printf("  Trial.Source      = \"EUCTR\"\n")
	fmt.Printf("  Trial.Title       = %q\n", title)
	fmt.Printf("  Trial.Status      = %q\n", status)
	fmt.Printf("  Trial.Phase       = %q (normalised from %q)\n", "phase_2", phase)
	fmt.Printf("  Trial.Sponsor     = %q\n", sponsor)
	if startDate != nil {
		fmt.Printf("  Trial.StartDate   = %s\n", startDate.Format("2006-01-02"))
	}
	fmt.Printf("  Trial.Conditions  = %v\n", []string{condition})
	fmt.Printf("  Trial.Countries   = %v\n", countries)
	fmt.Printf("  Trial.SourceURL   = %q\n", sourceURL)
}
