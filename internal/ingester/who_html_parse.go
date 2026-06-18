package ingester

import (
	"log"
	"net/url"
	"regexp"
	"strings"

	"github.com/Akshayvij07/clinical-trial-pipeline/internal/models"
	"golang.org/x/net/html"
)

func parseWHOResultsPage(page string, source string) []models.Trial {
	var trials []models.Trial

	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		log.Printf("[WHO_ICTRP] failed to parse HTML: %v", err)
		return trials
	}

	table := findNodeByID(doc, "GridView1")
	if table == nil {
		log.Printf("[WHO_ICTRP] GridView1 not found")
		return trials
	}

	rows := findAllNodes(table, "tr")

	for _, row := range rows {
		// Skip rows that have <th> — that's the header row
		if len(findAllNodes(row, "th")) > 0 {
			continue
		}

		cells := findAllNodes(row, "td")

		// Skip pagination row — it has many cells with just numbers/">>","<<"
		// Real data rows have exactly 7 cells
		if len(cells) != 7 {
			continue
		}

		status := strings.TrimSpace(extractText(cells[0]))
		id := strings.TrimSpace(extractText(cells[2]))
		title := strings.TrimSpace(extractText(cells[4]))
		regDate := strings.TrimSpace(extractText(cells[5]))

		// Skip if no ID
		if id == "" {
			continue
		}

		trial := models.Trial{
			Source:    source,
			ID:        id,
			Title:     title,
			Status:    models.NormaliseStatus(status),
			SourceURL: "https://trialsearch.who.int/Trial2.aspx?TrialID=" + url.QueryEscape(id),
		}

		// Parse registration date if present
		if regDate != "" {
			trial.StartDate = parseDate(regDate)
		}

		trials = append(trials, trial)
	}

	log.Printf("[WHO_ICTRP] parsed %d trials from page", len(trials))
	return trials
}

var nextPageRe = regexp.MustCompile(`__doPostBack\('(GridView1)','(Page\$\d+)'\)`)

func findNextPageTarget(page string) string {
	// Log ALL doPostBack calls for debugging
	allRe := regexp.MustCompile(`__doPostBack\('([^']+)','([^']+)'\)`)
	for _, m := range allRe.FindAllStringSubmatch(page, -1) {
		log.Printf("[WHO_ICTRP] DEBUG all doPostBack: target=%q arg=%q", m[1], m[2])
	}

	// WHO ICTRP next page button: __doPostBack('GridView1','Page$Next')
	// appears as &gt;&gt; in HTML
	nextRe := regexp.MustCompile(`__doPostBack\('([^']+)','(Page\$(?:Next|\d+))'\)`)
	matches := nextRe.FindAllStringSubmatch(page, -1)

	for _, m := range matches {
		// "Page$Next" or last numbered page link means there's a next page
		if m[2] == "Page$Next" {
			return m[1] + "|" + m[2]
		}
	}

	// Also check for >> as &gt;&gt; near a doPostBack
	gtgtRe := regexp.MustCompile(`javascript:__doPostBack\('([^']+)','(Page\$[^']+)'\)"[^>]*>(?:&gt;&gt;|&raquo;|>>|»)`)
	if m := gtgtRe.FindStringSubmatch(page); m != nil {
		return m[1] + "|" + m[2]
	}

	return ""
}

// ── HTML traversal helpers ──────────────────────────────────────────

func findNodeByID(n *html.Node, id string) *html.Node {
	if n.Type == html.ElementNode {
		for _, a := range n.Attr {
			if a.Key == "id" && a.Val == id {
				return n
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findNodeByID(c, id); result != nil {
			return result
		}
	}
	return nil
}

func findAllNodes(n *html.Node, tag string) []*html.Node {
	var results []*html.Node
	if n.Type == html.ElementNode && n.Data == tag {
		results = append(results, n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		results = append(results, findAllNodes(c, tag)...)
	}
	return results
}

func extractText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(extractText(c))
	}
	return sb.String()
}
