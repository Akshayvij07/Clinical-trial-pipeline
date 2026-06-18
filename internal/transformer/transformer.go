package transformer

// normalize.go — cleans and deduplicates trials from all sources.
//
// Rules:
//  1. Normalise status and phase to controlled vocabulary (done in models layer)
//  2. Deduplicate: if the same trial appears in WHO and EUCTR (WHO aggregates EUCTR),
//     keep the EUCTR record as it has more detail, tag the WHO secondary IDs.
//  3. Drop trials with no ID and no title — they are unparseable fragments.
//  4. Trim all string fields.

import (
	"log"
	"strings"

	"github.com/Akshayvij07/clinical-trial-pipeline/internal/models"
)

// Transform cleans a slice of trials and removes cross-source duplicates.
func Transform(trials []models.Trial) []models.Trial {
	// Step 1: clean individual fields
	cleaned := make([]models.Trial, 0, len(trials))
	for _, t := range trials {
		t = cleanTrial(t)
		if t.ID == "" {
			continue
		}

		if strings.TrimSpace(t.Title) == "" {
			t.Title = "Title unavailable"
		}
		cleaned = append(cleaned, t)
	}

	// Step 2: deduplicate
	deduped := deduplicate(cleaned)

	log.Printf("[transformer] %d → cleaned %d → deduped %d",
		len(trials), len(cleaned), len(deduped))

	return deduped
}

// cleanTrial normalises all string fields in a Trial.
func cleanTrial(t models.Trial) models.Trial {
	t.ID = strings.TrimSpace(t.ID)
	t.Title = strings.TrimSpace(t.Title)
	t.Sponsor = strings.TrimSpace(t.Sponsor)
	t.Status = models.NormaliseStatus(t.Status)
	t.Phase = models.NormalisePhase(t.Phase)
	t.Source = strings.TrimSpace(t.Source)
	t.SourceURL = strings.TrimSpace(t.SourceURL)

	t.Countries = cleanSlice(t.Countries)
	t.Conditions = cleanSlice(t.Conditions)
	t.Interventions = cleanSlice(t.Interventions)

	return t
}

func cleanSlice(ss []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	return out
}

// deduplicate removes duplicate trials using a priority rule:
//
//	EUCTR > ISRCTN > WHO  (most specific wins)
//
// WHO ICTRP aggregates records from EUCTR and ISRCTN, so the same trial
// can appear in multiple sources. We keep the source-native record.
func deduplicate(trials []models.Trial) []models.Trial {
	// Priority map — lower number = higher priority
	priority := map[string]int{
		models.SourceEUCTR:  1,
		models.SourceISRCTN: 2,
		models.SourceWHO:    3,
	}

	// Map from normalised ID → best trial seen so far
	best := make(map[string]models.Trial, len(trials))

	for _, t := range trials {
		key := normaliseID(t.ID)
		existing, exists := best[key]
		if !exists {
			best[key] = t
			continue
		}
		// Keep whichever source has higher priority (lower number)
		if priority[t.Source] < priority[existing.Source] {
			best[key] = t
		}
	}

	out := make([]models.Trial, 0, len(best))
	for _, t := range best {
		out = append(out, t)
	}
	return out
}

// normaliseID strips dashes and lowercases an ID for comparison.
// "2004-000001-11" == "2004000001-11" etc.
func normaliseID(id string) string {
	return strings.ToLower(strings.ReplaceAll(id, "-", ""))
}
