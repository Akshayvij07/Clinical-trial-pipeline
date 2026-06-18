package ingester

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/Akshayvij07/clinical-trial-pipeline/internal/models"
)

// ── WHO-format XML ───────────────────────────────────────────────────────────
// Both the WHO ICTRP weekly export and the ISRCTN XML API use the same schema:
// root element <trials>, each trial a <trial> child with the bulk of fields
// nested under <main>, plus sibling sections for countries and keywords.

type WHOFormatExport struct {
	XMLName xml.Name         `xml:"trials"`
	Trials  []WHOFormatTrial `xml:"trial"`
}

type WHOFormatTrial struct {
	Main                   WHOFormatMain        `xml:"main"`
	Countries              WHOFormatCountries   `xml:"countries"`
	HealthConditionKeyword WHOFormatKeywordList `xml:"health_condition_keyword"`
	InterventionKeyword    WHOFormatKeywordList `xml:"intervention_keyword"`
}

type WHOFormatMain struct {
	TrialID           string `xml:"trial_id"`
	PrimarySponsor    string `xml:"primary_sponsor"`
	PublicTitle       string `xml:"public_title"`
	ScientificTitle   string `xml:"scientific_title"`
	DateRegistration  string `xml:"date_registration"`
	DateEnrolment     string `xml:"date_enrolment"`
	RecruitmentStatus string `xml:"recruitment_status"`
	URL               string `xml:"url"`
	Phase             string `xml:"phase"`
	HCFreetext        string `xml:"hc_freetext"`
}

type WHOFormatCountries struct {
	Country []string `xml:"country2"`
}

type WHOFormatKeywordList struct {
	HCKeyword []string `xml:"hc_keyword"`
	IKeyword  []string `xml:"i_keyword"`
}

// parseWHOFormatXML parses WHO-format trial XML shared by the WHO ICTRP
// weekly export and the ISRCTN API. urlForID builds a fallback SourceURL
// when the trial's own <url> field is blank.
func parseWHOFormatXML(data []byte, source string, urlForID func(id string) string) ([]models.Trial, error) {
	var export WHOFormatExport
	if err := xml.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("xml.Unmarshal: %w", err)
	}

	var out []models.Trial
	for _, t := range export.Trials {
		id := strings.TrimSpace(t.Main.TrialID)
		if id == "" {
			continue
		}

		trial := models.Trial{
			ID:     id,
			Source: source,
			SourceURL: func() string {
				if t.Main.URL != "" {
					return strings.TrimSpace(t.Main.URL)
				}
				return urlForID(id)
			}(),
			IngestedAt: time.Now().UTC(),
		}

		trial.Title = strings.TrimSpace(t.Main.PublicTitle)
		if trial.Title == "" {
			trial.Title = strings.TrimSpace(t.Main.ScientificTitle)
		}

		trial.Status = models.NormaliseStatus(t.Main.RecruitmentStatus)
		trial.Sponsor = strings.TrimSpace(t.Main.PrimarySponsor)
		trial.Phase = models.NormalisePhase(t.Main.Phase)

		trial.Conditions = cleanStrings(t.HealthConditionKeyword.HCKeyword)
		if len(trial.Conditions) == 0 && t.Main.HCFreetext != "" {
			trial.Conditions = splitClean(t.Main.HCFreetext, ";")
		}
		trial.Interventions = cleanStrings(t.InterventionKeyword.IKeyword)
		trial.Countries = cleanStrings(t.Countries.Country)

		dateStr := t.Main.DateEnrolment
		if dateStr == "" {
			dateStr = t.Main.DateRegistration
		}
		trial.StartDate = parseDateMulti(dateStr)

		out = append(out, trial)
	}

	return out, nil
}
