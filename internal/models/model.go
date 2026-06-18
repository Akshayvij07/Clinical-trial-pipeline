package models

import (
	"strings"
	"time"
)

// Source constants — one per registry
const (
	SourceWHO    = "WHO_ICTRP"
	SourceEUCTR  = "EUCTR"
	SourceISRCTN = "ISRCTN"
)

// Trial is the unified, normalised record written to the warehouse.
// Every ingester maps its raw fields into this struct.
type Trial struct {
	// Identity
	ID     string `db:"id"`     // registry-native ID  e.g. "2004-000001-11"
	Source string `db:"source"` // WHO_ICTRP | EUCTR | ISRCTN

	// Core metadata
	Title     string     `db:"title"`
	Status    string     `db:"status"` // normalised → see NormaliseStatus()
	Phase     string     `db:"phase"`  // normalised → see NormalisePhase()
	Sponsor   string     `db:"sponsor"`
	StartDate *time.Time `db:"start_date"`
	EndDate   *time.Time `db:"end_date"`

	// Classification arrays (stored as text[] in Postgres)
	Conditions    []string `db:"conditions"`
	Interventions []string `db:"interventions"`
	Countries     []string `db:"countries"`

	// Provenance
	SourceURL  string    `db:"source_url"`
	IngestedAt time.Time `db:"ingested_at"`
}

// NormaliseStatus maps free-text status strings to a controlled vocabulary.
func NormaliseStatus(raw string) string {
	r := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(r, "recruit"):
		return "recruiting"
	case strings.Contains(r, "complet"):
		return "completed"
	case strings.Contains(r, "terminat"), strings.Contains(r, "stopped"):
		return "terminated"
	case strings.Contains(r, "not yet"), strings.Contains(r, "planned"):
		return "not_yet_recruiting"
	case strings.Contains(r, "suspend"):
		return "suspended"
	case strings.Contains(r, "withdraw"):
		return "withdrawn"
	default:
		if r == "" {
			return "unknown"
		}
		return r
	}
}

// NormalisePhase maps phase strings to a controlled vocabulary.
func NormalisePhase(raw string) string {
	r := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(r, "phase 1") || strings.Contains(r, "phase i") || r == "1" || r == "i":
		return "phase_1"
	case strings.Contains(r, "phase 2") || strings.Contains(r, "phase ii") || r == "2" || r == "ii":
		return "phase_2"
	case strings.Contains(r, "phase 3") || strings.Contains(r, "phase iii") || r == "3" || r == "iii":
		return "phase_3"
	case strings.Contains(r, "phase 4") || strings.Contains(r, "phase iv") || r == "4" || r == "iv":
		return "phase_4"
	case strings.Contains(r, "1") && strings.Contains(r, "2"):
		return "phase_1_2"
	case strings.Contains(r, "2") && strings.Contains(r, "3"):
		return "phase_2_3"
	default:
		if r == "" || r == "n/a" || r == "not applicable" {
			return "not_applicable"
		}
		return r
	}
}
