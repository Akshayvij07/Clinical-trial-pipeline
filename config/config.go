package config

import (
	"bufio"
	"log"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL string
	WhoQuery    string
	EuctrQuery  string
	IsrctnQuery string
}

func Load() *Config {
	loadDotEnv()
	return &Config{
		DatabaseURL: getEnv("DATABASE_URL", ""),
		WhoQuery:    getEnv("WHO_QUERY", "cancer"),
		EuctrQuery:  getEnv("EUCTR_QUERY", "cancer"),
		IsrctnQuery: getEnv("ISRCTN_QUERY", "cancer"),
	}
}

// loadDotEnv reads .env file manually — no external dependency.
func loadDotEnv() {
	f, err := os.Open(".env")
	if err != nil {
		return // .env is optional
	}
	defer f.Close()
	log.Println("[config] loading .env")
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if os.Getenv(key) == "" { // don't override real env vars
			os.Setenv(key, val)
		}
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
