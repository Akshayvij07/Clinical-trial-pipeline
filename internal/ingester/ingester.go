package ingester

import (
	"context"

	"github.com/Akshayvij07/clinical-trial-pipeline/internal/models"
)

// Ingester is the contract every source scraper must satisfy.
// Adding a new registry = implementing this interface.
type Ingester interface {
	// Name returns the human-readable source name used in logs.
	Name() string

	// Fetch fetches, parses and returns raw (un-normalised) trials.
	// The transformer layer normalises fields afterwards.
	Fetch(ctx context.Context) ([]models.Trial, error)
}
