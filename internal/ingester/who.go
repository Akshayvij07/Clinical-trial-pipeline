package ingester

import (
	"context"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Akshayvij07/clinical-trial-pipeline/internal/models"
)

const (
	whoSearchURL = "https://trialsearch.who.int/Default.aspx"
	whoUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	whoMaxPages  = 1 // 50 pages × 100 = 5000 trials max
)

type WHOIngester struct {
	client *http.Client
	query  string
}

func NewWHOIngester(query string) *WHOIngester {
	jar, _ := cookiejar.New(nil)
	return &WHOIngester{
		client: &http.Client{Timeout: 60 * time.Second, Jar: jar},
		query:  query,
	}
}

func (w *WHOIngester) Name() string {
	return models.SourceWHO
}

// Fetch implements the WHO ICTRP scraping flow:
//
//  1. GET Default.aspx — session cookie + hidden fields
//  2. POST search (Button1=Search)
//  3. POST page size change (DropDownList1=100 via __doPostBack)
//     — DropDownList1 is the correct control name (confirmed from HTML)
//     — it fires __doPostBack on change, separate from the search POST
//  4. Parse GridView1 rows, paginate via Page$2, Page$3, etc.
//
// Pagination notes from actual HTML:
//   - Pages are linked as __doPostBack('GridView1','Page$2') etc.
//   - ">>" maps to Page$Last (last page), NOT Page$Next
//   - We increment page number and check if that page link exists
func (w *WHOIngester) Fetch(ctx context.Context) ([]models.Trial, error) {
	if w.query == "" {
		log.Printf("[WHO_ICTRP] no query configured; source disabled")
		return nil, nil
	}

	// Step 1: GET homepage
	log.Printf("[WHO_ICTRP] step 1: loading homepage")
	homepage, err := w.get(ctx, whoSearchURL)
	if err != nil {
		return nil, fmt.Errorf("load homepage: %w", err)
	}

	// Step 2: POST search
	log.Printf("[WHO_ICTRP] step 2: submitting search for %q", w.query)
	searchFields := extractHiddenFields(homepage)
	searchFields["TextBox1"] = w.query
	searchFields["Button1"] = "Search"

	time.Sleep(1 * time.Second)
	resultsPage, err := w.post(ctx, whoSearchURL, searchFields)
	if err != nil {
		return nil, fmt.Errorf("submit search: %w", err)
	}

	if strings.Contains(resultsPage, "No records found") {
		log.Printf("[WHO_ICTRP] no results for query %q", w.query)
		return nil, nil
	}

	if !strings.Contains(resultsPage, "GridView1") {
		log.Printf("[WHO_ICTRP] search results missing GridView1:\n%s",
			resultsPage[:min(400, len(resultsPage))])
		return nil, fmt.Errorf("search results page missing GridView1")
	}

	// Step 3: Change page size to 100
	// Dropdown name is DropDownList1 (confirmed from actual results page HTML).
	// It fires __doPostBack('DropDownList1','') on change.
	log.Printf("[WHO_ICTRP] step 3: setting page size to 100")
	pageSizeFields := extractHiddenFields(resultsPage)
	pageSizeFields["__EVENTTARGET"] = "DropDownList1"
	pageSizeFields["__EVENTARGUMENT"] = ""
	pageSizeFields["DropDownList1"] = "100"
	delete(pageSizeFields, "Button1")
	delete(pageSizeFields, "Button2")
	delete(pageSizeFields, "Button7")
	delete(pageSizeFields, "Button8")

	time.Sleep(1 * time.Second)
	bigPage, err := w.post(ctx, whoSearchURL, pageSizeFields)
	if err != nil {
		log.Printf("[WHO_ICTRP] page size change failed: %v — using 10/page", err)
	} else if !strings.Contains(bigPage, "GridView1") {
		log.Printf("[WHO_ICTRP] page size response missing GridView1 — using 10/page")
	} else {
		log.Printf("[WHO_ICTRP] page size set to 100 successfully")
		resultsPage = bigPage
	}

	// Step 4: Parse page 1
	log.Printf("[WHO_ICTRP] step 4: parsing results")
	allTrials := parseWHOResultsPage(resultsPage, w.Name())
	log.Printf("[WHO_ICTRP] page 1 → %d trials", len(allTrials))

	// Step 5: Paginate
	// WHO uses Page$2, Page$3 ... Page$N links.
	// ">>" is Page$Last — we don't use it, we just increment page number
	// and check if a link to that page exists in the current HTML.
	currentPage := 1
	for currentPage < whoMaxPages {
		nextPage := currentPage + 1
		nextArg := fmt.Sprintf("Page$%d", nextPage)

		// Page link appears as 'Page$N' or &#39;Page$N&#39; (HTML-escaped)
		hasNextLink := strings.Contains(resultsPage, "'"+nextArg+"'") ||
			strings.Contains(resultsPage, "&#39;"+nextArg+"&#39;")

		if !hasNextLink {
			log.Printf("[WHO_ICTRP] no link to page %d, stopping at page %d",
				nextPage, currentPage)
			break
		}

		log.Printf("[WHO_ICTRP] fetching page %d", nextPage)
		nextFields := extractHiddenFields(resultsPage)
		nextFields["__EVENTTARGET"] = "GridView1"
		nextFields["__EVENTARGUMENT"] = nextArg
		nextFields["DropDownList1"] = "100"
		delete(nextFields, "Button1")
		delete(nextFields, "Button2")
		delete(nextFields, "Button7")
		delete(nextFields, "Button8")

		time.Sleep(1 * time.Second)
		resultsPage, err = w.post(ctx, whoSearchURL, nextFields)
		if err != nil {
			log.Printf("[WHO_ICTRP] pagination error on page %d: %v", nextPage, err)
			break
		}

		if !strings.Contains(resultsPage, "GridView1") {
			log.Printf("[WHO_ICTRP] page %d missing GridView1, stopping", nextPage)
			break
		}

		pageTrials := parseWHOResultsPage(resultsPage, w.Name())
		log.Printf("[WHO_ICTRP] page %d → %d trials", nextPage, len(pageTrials))

		if len(pageTrials) == 0 {
			break
		}
		allTrials = append(allTrials, pageTrials...)
		currentPage = nextPage
	}

	log.Printf("[WHO_ICTRP] fetched %d trials total for query %q", len(allTrials), w.query)
	return allTrials, nil
}

// HTTP helpers

func (w *WHOIngester) get(ctx context.Context, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	w.setBrowserHeaders(req)
	body, err := w.do(req)
	return string(body), err
}

func (w *WHOIngester) post(ctx context.Context, target string, fields map[string]string) (string, error) {
	data, err := w.postRaw(ctx, target, fields)
	return string(data), err
}

func (w *WHOIngester) postRaw(ctx context.Context, target string, fields map[string]string) ([]byte, error) {
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://trialsearch.who.int")
	req.Header.Set("Referer", whoSearchURL)
	w.setBrowserHeaders(req)
	return w.do(req)
}

func (w *WHOIngester) setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", whoUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
}

func (w *WHOIngester) do(req *http.Request) ([]byte, error) {
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// ASP.NET hidden field extractor

var (
	hiddenInputRe = regexp.MustCompile(`(?i)<input[^>]*type=["']hidden["'][^>]*>`)
	nameAttrRe    = regexp.MustCompile(`(?i)\bname=["']([^"']*)["']`)
	valueAttrRe   = regexp.MustCompile(`(?i)\bvalue=["']([^"']*)["']`)
)

func extractHiddenFields(page string) map[string]string {
	fields := map[string]string{}
	for _, tag := range hiddenInputRe.FindAllString(page, -1) {
		nameMatch := nameAttrRe.FindStringSubmatch(tag)
		if nameMatch == nil {
			continue
		}
		value := ""
		if valMatch := valueAttrRe.FindStringSubmatch(tag); valMatch != nil {
			value = html.UnescapeString(valMatch[1])
		}
		fields[nameMatch[1]] = value
	}
	return fields
}

var exportButtonRe = regexp.MustCompile(
	`(?i)<input[^>]*value=["'][^"']*[Ee]xport[^"']*["'][^>]*name=["']([^"']*)["']` +
		`|<input[^>]*name=["']([^"']*)["'][^>]*value=["'][^"']*[Ee]xport[^"']*["']`)

func findExportButtonName(page string) string {
	m := exportButtonRe.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}
