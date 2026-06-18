package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Akshayvij07/clinical-trial-pipeline/config"
	"github.com/Akshayvij07/clinical-trial-pipeline/internal/ingester"
	"github.com/Akshayvij07/clinical-trial-pipeline/internal/models"
	"github.com/Akshayvij07/clinical-trial-pipeline/internal/transformer"
	"github.com/Akshayvij07/clinical-trial-pipeline/internal/warehouse"
	// "github.com/ceresity/clinical-trial-pipeline/warehouse"
)

func main() {

	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ltime)

	printBanner()

	cfg := config.Load()

	db, err := warehouse.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer db.Close()

	// ── Ingesters ─────────────────────────────────────────────────────────────
	ingesters := []ingester.Ingester{
		ingester.NewEUCTRIngester(cfg.EuctrQuery, 1),
		ingester.NewWHOIngester(cfg.WhoQuery),
		ingester.NewISRCTNIngester(cfg.IsrctnQuery),
	}

	// ── Concurrent fetch ──────────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	type result struct {
		name   string
		trials []models.Trial
		err    error
	}

	ch := make(chan result, len(ingesters))
	var wg sync.WaitGroup

	for _, ing := range ingesters {
		wg.Add(1)
		go func(ing ingester.Ingester) {
			defer wg.Done()
			start := time.Now()
			fmt.Printf("\n🔄  [%s] starting...\n", ing.Name())
			trials, err := ing.Fetch(ctx)
			elapsed := time.Since(start).Round(time.Millisecond)
			if err != nil {
				fmt.Printf("❌  [%s] error after %s: %v\n", ing.Name(), elapsed, err)
				ch <- result{name: ing.Name(), err: err}
				return
			}
			fmt.Printf("✅  [%s] fetched %d records in %s\n", ing.Name(), len(trials), elapsed)
			ch <- result{name: ing.Name(), trials: trials}
		}(ing)
	}

	go func() { wg.Wait(); close(ch) }()

	var allRaw []models.Trial
	for r := range ch {
		if r.err == nil {
			allRaw = append(allRaw, r.trials...)
		}
	}

	fmt.Printf("\n📦  Raw records collected: %d\n", len(allRaw))

	for i, t := range allRaw {
		if i > 5 {
			break
		}

		fmt.Printf(
			"ID=%s TITLE=%q STATUS=%s SOURCE=%s\n",
			t.ID,
			t.Title,
			t.Status,
			t.Source,
		)
	}
	// ── Transform ─────────────────────────────────────────────────────────────
	fmt.Println("\n🔧  Transforming (normalise + deduplicate)...")
	cleaned := transformer.Transform(allRaw)
	fmt.Printf("✅  After transform: %d trials\n", len(cleaned))

	// ── Print to terminal ─────────────────────────────────────────────────────
	printTrials(cleaned)

	// ── Write to warehouse ────────────────────────────────────────────────────
	bySource := map[string][]models.Trial{}
	for _, t := range cleaned {
		bySource[t.Source] = append(bySource[t.Source], t)
	}

	if err := db.UpsertTrials(ctx, cleaned); err != nil {
		log.Fatalf("upsert: %v", err)
	}

	// Print summary from DB
	count, _ := db.CountTrials(ctx)
	log.Printf("Total trials in DB: %d", count)

	printSummary(cleaned)
}

// ── Display helpers ───────────────────────────────────────────────────────────

func printBanner() {
	line := strings.Repeat("═", 68)
	fmt.Println(line)
	fmt.Println("  🏥  Ceresity Clinical Trial Ingestion Pipeline")
	fmt.Println("  Sources: EUCTR  |  WHO ICTRP  |  ISRCTN")
	fmt.Println(line)
}

func printTrials(trials []models.Trial) {
	fmt.Printf("\n%s\n", strings.Repeat("─", 68))
	fmt.Printf("  📋  INGESTED TRIALS  (%d total)\n", len(trials))
	fmt.Printf("%s\n\n", strings.Repeat("─", 68))

	limit := 25
	if len(trials) < limit {
		limit = len(trials)
	}
	for i, t := range trials[:limit] {
		printOne(i+1, t)
	}
	if len(trials) > limit {
		fmt.Printf("\n  ... %d more records (all stored in warehouse)\n", len(trials)-limit)
	}
}

func printOne(n int, t models.Trial) {
	src := fmt.Sprintf("[%-10s]", t.Source)
	fmt.Printf("  %3d %s  %s\n", n, src, t.ID)
	fmt.Printf("       Title    : %s\n", trunc(t.Title, 64))
	fmt.Printf("       Status   : %-18s  Phase: %s\n", t.Status, t.Phase)
	if t.Sponsor != "" {
		fmt.Printf("       Sponsor  : %s\n", trunc(t.Sponsor, 60))
	}
	if len(t.Countries) > 0 {
		fmt.Printf("       Countries: %s\n", strings.Join(t.Countries, ", "))
	}
	if len(t.Conditions) > 0 {
		fmt.Printf("       Conditions: %s\n", trunc(strings.Join(t.Conditions, "; "), 64))
	}
	if t.StartDate != nil {
		fmt.Printf("       Start    : %s\n", t.StartDate.Format("2006-01-02"))
	}
	if t.SourceURL != "" {
		fmt.Printf("       URL      : %s\n", t.SourceURL)
	}
	fmt.Println()
}

func printSummary(trials []models.Trial) {
	line := strings.Repeat("═", 68)
	fmt.Printf("\n%s\n  📊  SUMMARY\n%s\n", line, strings.Repeat("─", 68))

	bySource := map[string]int{}
	byStatus := map[string]int{}
	for _, t := range trials {
		bySource[t.Source]++
		byStatus[t.Status]++
	}
	fmt.Println("\n  By Source:")
	for s, c := range bySource {
		fmt.Printf("    %-14s %d\n", s, c)
	}
	fmt.Println("\n  By Status:")
	for s, c := range byStatus {
		fmt.Printf("    %-25s %d\n", s, c)
	}
	fmt.Printf("\n  Total : %d trials\n", len(trials))
	fmt.Printf("  Time  : %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(line)
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
