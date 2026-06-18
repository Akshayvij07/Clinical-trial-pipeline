package ingester

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Akshayvij07/clinical-trial-pipeline/internal/models"
)

const (
	isrctnBase      = "https://www.isrctn.com"
	isrctnFetchSize = 200 // fetch up to 500 in one call
)

type ISRCTNIngester struct {
	client *http.Client
	query  string
}

func NewISRCTNIngester(query string) *ISRCTNIngester {
	return &ISRCTNIngester{
		client: &http.Client{Timeout: 30 * time.Second},
		query:  query,
	}
}

func (g *ISRCTNIngester) Name() string { return models.SourceISRCTN }

func (g *ISRCTNIngester) Fetch(ctx context.Context) ([]models.Trial, error) {
	trials, err := g.fetchAll(ctx)
	if err != nil {
		return nil, err
	}
	log.Printf("[ISRCTN] total fetched: %d trials", len(trials))
	return trials, nil
}

func (g *ISRCTNIngester) fetchAll(ctx context.Context) ([]models.Trial, error) {
	// ISRCTN API has no pagination — use limit to get all results at once.
	// Docs: /api/query/format/<format>?q=<query>&limit=<limit>
	// The API recommends splitting large queries by date range instead of paginating.
	apiURL := fmt.Sprintf(
		"%s/api/query/format/who?q=%s&limit=%d",
		isrctnBase,
		url.QueryEscape(g.query),
		isrctnFetchSize,
	)

	log.Printf("[ISRCTN] API URL: %s", apiURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CeresityPipeline/1.0)")
	req.Header.Set("Accept", "application/xml, text/xml, */*")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET: %w", err)
	}

	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		preview := string(data)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, preview)
	}

	trimmed := strings.TrimSpace(string(data))
	if strings.Contains(trimmed[:min(300, len(trimmed))], "<html") ||
		strings.Contains(trimmed[:min(300, len(trimmed))], "<!DOCTYPE") {
		return nil, fmt.Errorf("ISRCTN returned HTML (bot block?). Preview:\n%s", trimmed[:min(500, len(trimmed))])
	}

	trials, err := parseWHOFormatXML(data, g.Name(), func(id string) string {
		return isrctnBase + "/" + id
	})
	if err != nil {
		preview := string(data)
		if len(preview) > 300 {
			preview = preview[:300]
		}
		log.Printf("[ISRCTN] XML parse failed. Preview: %s", preview)
		return nil, fmt.Errorf("parse XML: %w", err)
	}

	log.Printf("[ISRCTN] fetched %d trials (limit=%d)", len(trials), isrctnFetchSize)
	return trials, nil
}
