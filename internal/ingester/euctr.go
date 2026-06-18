package ingester

// euctr.go — scrapes https://www.clinicaltrialsregister.eu/ctr-search/search
//
// Uses goquery (jQuery-style selectors) for robust HTML parsing.
// EUCTR search results page structure:
//
//	Each result is a <table class="result"> containing rows with:
//	- EudraCT number in an <a href="/ctr-search/trial/YYYY-NNNNNN-NN/CC">
//	- Fields as plain text like "Sponsor Name: X", "Full Title: Y"
//	- Trial protocol: "DE (Completed) AT (Ongoing)"
//
// Install: go get github.com/PuerkitoBio/goquery

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Akshayvij07/clinical-trial-pipeline/internal/models"
	"github.com/PuerkitoBio/goquery"
)

const (
	euctrBase     = "https://www.clinicaltrialsregister.eu"
	euctrMaxPages = 20
)

var eudractRe = regexp.MustCompile(`\b(20\d{2}-\d{6}-\d{2})\b`)

// nameToCode maps country name → ISO 2-letter code for building EUCTR URLs.
var nameToCode = map[string]string{
	"Germany":        "DE",
	"France":         "FR",
	"Italy":          "IT",
	"Spain":          "ES",
	"United Kingdom": "GB",
	"Netherlands":    "NL",
	"Belgium":        "BE",
	"Austria":        "AT",
	"Sweden":         "SE",
	"Poland":         "PL",
	"Portugal":       "PT",
	"Czech Republic": "CZ",
	"Hungary":        "HU",
	"Romania":        "RO",
	"Denmark":        "DK",
	"Finland":        "FI",
	"Norway":         "NO",
	"Switzerland":    "CH",
	"United States":  "US",
	"Canada":         "CA",
	"Australia":      "AU",
	"India":          "IN",
	"Brazil":         "BR",
	"Russia":         "RU",
	"Turkey":         "TR",
	"Greece":         "GR",
	"Ireland":        "IE",
	"Slovakia":       "SK",
	"Slovenia":       "SI",
	"Croatia":        "HR",
	"Bulgaria":       "BG",
	"Lithuania":      "LT",
	"Latvia":         "LV",
	"Estonia":        "EE",
	"Cyprus":         "CY",
	"Luxembourg":     "LU",
	"Malta":          "MT",
	"Iceland":        "IS",
	"Serbia":         "RS",
	"Ukraine":        "UA",
	"Belarus":        "BY",
}

type EUCTRIngester struct {
	client   *http.Client
	query    string
	maxPages int // 0 = use default
}

func NewEUCTRIngester(query string, maxPages int) *EUCTRIngester {
	if maxPages <= 0 {
		maxPages = 50
	}
	return &EUCTRIngester{
		client:   &http.Client{Timeout: 30 * time.Second},
		query:    query,
		maxPages: maxPages,
	}
}

func (e *EUCTRIngester) Name() string { return models.SourceEUCTR }

func (e *EUCTRIngester) Fetch(ctx context.Context) ([]models.Trial, error) {
	var all []models.Trial

	for page := 1; page <= e.maxPages; page++ {
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		case <-time.After(1200 * time.Millisecond):
		}

		pageURL := fmt.Sprintf(
			"%s/ctr-search/search?query=%s&page=%d",
			euctrBase, e.query, page,
		)
		log.Printf("[EUCTR] page %d → %s", page, pageURL)

		trials, hasMore, err := e.fetchPage(ctx, pageURL)
		if err != nil {
			log.Printf("[EUCTR] page %d error: %v", page, err)
			break
		}
		log.Printf("[EUCTR] page %d → %d trials", page, len(trials))
		all = append(all, trials...)

		if !hasMore || len(trials) == 0 {
			break
		}
	}
	return all, nil
}

func (e *EUCTRIngester) fetchPage(ctx context.Context, pageURL string) ([]models.Trial, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, false, fmt.Errorf("status %d: %s",
			resp.StatusCode, string(body[:min(200, len(body))]))
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("parse HTML: %w", err)
	}

	trials := e.parseDoc(doc)
	// Add inside fetchPage() after parsing doc
	hasMore := e.hasNextPage(doc)
	log.Printf("[EUCTR] hasMore=%v (pagination check)", hasMore)

	return trials, hasMore, nil
}

// hasNextPage checks several selectors that EUCTR uses for the "Next" link.
func (e *EUCTRIngester) hasNextPage(doc *goquery.Document) bool {
	found := false
	doc.Find("a").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		href, exists := a.Attr("href")
		if !exists {
			return true
		}
		text := strings.TrimSpace(a.Text())

		if strings.Contains(href, "page=") {
			// Match "Next»", "Next", ">", "»" etc.
			if strings.HasPrefix(text, "Next") || text == ">" || text == "»" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// parseDoc extracts trials using goquery selectors.
func (e *EUCTRIngester) parseDoc(doc *goquery.Document) []models.Trial {
	var trials []models.Trial
	seen := map[string]bool{}

	// Strategy 1: each result is a <table class="result">
	doc.Find("table.result").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		id := eudractRe.FindString(text)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		trials = append(trials, e.buildTrial(id, text, s))
	})

	// Strategy 2: results inside <tr> rows (older EUCTR layout)
	if len(trials) == 0 {
		doc.Find("tr").Each(func(_ int, s *goquery.Selection) {
			text := strings.TrimSpace(s.Text())
			id := eudractRe.FindString(text)
			if id == "" || seen[id] {
				return
			}
			seen[id] = true
			trials = append(trials, e.buildTrial(id, text, s))
		})
	}

	// Strategy 3: find EudraCT links anywhere on the page
	if len(trials) == 0 {
		doc.Find("a[href*='/ctr-search/trial/']").Each(func(_ int, a *goquery.Selection) {
			href, _ := a.Attr("href")
			id := eudractRe.FindString(href)
			if id == "" || seen[id] {
				return
			}
			seen[id] = true
			container := a.Closest("table, tr, div, li, article")
			text := strings.TrimSpace(container.Text())
			trials = append(trials, e.buildTrial(id, text, container))
		})
	}

	return trials
}

// buildTrial fills a Trial struct from a text block and goquery selection.
func (e *EUCTRIngester) buildTrial(id, text string, s *goquery.Selection) models.Trial {
	t := models.Trial{
		ID:         id,
		Source:     models.SourceEUCTR,
		IngestedAt: time.Now().UTC(),
	}

	t.Title = extractLabel(text, "Full Title")
	t.Sponsor = extractLabel(text, "Sponsor Name")

	cond := extractLabel(text, "Medical condition")
	if cond != "" {
		t.Conditions = []string{cond}
	}

	startRaw := extractLabel(text, "Start Date")
	t.StartDate = parseDateMulti(startRaw)

	// Parse countries and status from the protocol line first,
	// so we have countries available for URL building.
	t.Countries, t.Status = parseProtocolLine(text)

	// Build the correct detail URL using the first country's ISO code.
	t.SourceURL = euctrTrialURL(id, text, t.Countries)

	phase := extractLabel(text, "Phase")
	if phase == "" {
		phase = phaseFromText(t.Title)
	}
	t.Phase = models.NormalisePhase(phase)

	pop := extractLabel(text, "Population Age")
	gender := extractLabel(text, "Gender")
	if pop != "" {
		t.Interventions = append(t.Interventions, "Population: "+pop)
	}
	if gender != "" {
		t.Interventions = append(t.Interventions, "Gender: "+gender)
	}

	return t
}

// euctrTrialURL builds the correct EUCTR detail page URL.
//
// EUCTR detail URLs require a country-code suffix that must match a country
// where the trial is actually registered — e.g. /BE for Belgium, /DE for
// Germany. Using the wrong code (e.g. always /GB) returns a 404 for trials
// not registered in the UK.
//
// Strategy:
//  1. Use the ISO code of the first country in the parsed countries list.
//  2. If no country name matched our map, fall back to the raw 2-letter code
//     extracted directly from the "Trial protocol:" line in the page text.
//  3. Last resort: return a search URL that always resolves.
func euctrTrialURL(id, text string, countries []string) string {
	// Try first country name → ISO code
	if len(countries) > 0 {
		if code, ok := nameToCode[countries[0]]; ok {
			return euctrBase + "/ctr-search/trial/" + id + "/" + code
		}
	}

	// Fallback: extract first raw 2-letter code from "Trial protocol:" line
	re := regexp.MustCompile(`([A-Z]{2})\s*\(`)
	lower := strings.ToLower(text)
	idx := strings.Index(lower, "trial protocol:")
	if idx >= 0 {
		chunk := text[idx:]
		if m := re.FindStringSubmatch(chunk); m != nil {
			return euctrBase + "/ctr-search/trial/" + id + "/" + m[1]
		}
	}

	// Last resort: search URL always works
	return euctrBase + "/ctr-search/search?query=" + id
}

// ── Text field extractors ─────────────────────────────────────────────────────

var euctrLabels = []string{
	"EudraCT Number", "Sponsor Protocol Number", "Start Date",
	"Sponsor Name", "Full Title", "Medical condition",
	"Population Age", "Gender", "Trial protocol", "Trial results",
	"Disease", "Version",
}

// extractLabel extracts the value after "Label:" up to the next known label.
func extractLabel(text, label string) string {
	lower := strings.ToLower(text)
	search := strings.ToLower(label) + ":"

	idx := strings.Index(lower, search)
	if idx < 0 {
		idx = strings.Index(lower, strings.ToLower(label)+"*:")
		if idx < 0 {
			return ""
		}
		search = strings.ToLower(label) + "*:"
	}

	start := idx + len(search)
	if start >= len(text) {
		return ""
	}

	end := len(text)
	for _, stop := range euctrLabels {
		if strings.EqualFold(stop, label) {
			continue
		}
		stopSearch := strings.ToLower(stop) + ":"
		if i := strings.Index(lower[start:], stopSearch); i >= 0 {
			if start+i < end {
				end = start + i
			}
		}
	}

	value := strings.TrimSpace(text[start:end])
	value = strings.Join(strings.Fields(value), " ")
	return value
}

// parseProtocolLine parses "Trial protocol: DE (Completed) AT (Ongoing)"
func parseProtocolLine(text string) (countries []string, status string) {
	lower := strings.ToLower(text)
	idx := strings.Index(lower, "trial protocol:")
	if idx < 0 {
		return nil, "unknown"
	}

	start := idx + len("trial protocol:")
	end := len(text)
	for _, stop := range euctrLabels {
		stopSearch := strings.ToLower(stop) + ":"
		if i := strings.Index(lower[start:], stopSearch); i >= 0 {
			if start+i < end {
				end = start + i
			}
		}
	}

	chunk := strings.TrimSpace(text[start:end])
	re := regexp.MustCompile(`([A-Z]{2})\s*\(([^)]+)\)`)
	matches := re.FindAllStringSubmatch(chunk, -1)

	// Reverse map for parseProtocolLine
	codeToName := map[string]string{
		"DE": "Germany", "FR": "France", "IT": "Italy", "ES": "Spain",
		"GB": "United Kingdom", "NL": "Netherlands", "BE": "Belgium",
		"AT": "Austria", "SE": "Sweden", "PL": "Poland", "PT": "Portugal",
		"CZ": "Czech Republic", "HU": "Hungary", "RO": "Romania",
		"DK": "Denmark", "FI": "Finland", "NO": "Norway", "CH": "Switzerland",
		"US": "United States", "CA": "Canada", "AU": "Australia",
		"IN": "India", "BR": "Brazil", "RU": "Russia", "TR": "Turkey",
		"GR": "Greece", "IE": "Ireland", "SK": "Slovakia", "SI": "Slovenia",
		"HR": "Croatia", "BG": "Bulgaria", "LT": "Lithuania", "LV": "Latvia",
		"EE": "Estonia", "CY": "Cyprus", "LU": "Luxembourg", "MT": "Malta",
		"IS": "Iceland", "RS": "Serbia", "UA": "Ukraine", "BY": "Belarus",
	}

	statusCount := map[string]int{}
	seen := map[string]bool{}

	for _, m := range matches {
		name, ok := codeToName[m[1]]
		if !ok {
			name = m[1]
		}
		if !seen[name] {
			seen[name] = true
			countries = append(countries, name)
		}
		statusCount[strings.ToLower(strings.TrimSpace(m[2]))]++
	}

	best := "unknown"
	maxCount := 0
	for s, c := range statusCount {
		if c > maxCount {
			maxCount = c
			best = s
		}
	}
	return countries, models.NormaliseStatus(best)
}

// phaseFromText extracts phase from free text like "phase II study"
func phaseFromText(text string) string {
	re := regexp.MustCompile(`(?i)phase\s+(I{1,3}V?|[1-4][abc]?(?:[/\-][1-4])?)`)
	m := re.FindStringSubmatch(text)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// parseDateMulti tries multiple date formats.
func parseDateMulti(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, f := range []string{
		"2006-01-02", "02/01/2006", "01/02/2006",
		"2 January 2006", "January 2006", "2006",
		"02-Jan-2006", "2006-01",
	} {
		if t, err := time.Parse(f, s); err == nil {
			return &t
		}
	}
	return nil
}
