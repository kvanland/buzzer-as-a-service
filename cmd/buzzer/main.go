package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"buzzer-as-a-service/internal/buzzer"
	"buzzer-as-a-service/web"
)

func main() {
	addr := getenv("BUZZER_ADDR", "127.0.0.1:8097")
	dataPath := getenv("BUZZER_DATA", "/var/lib/buzzer-as-a-service/groups.json")
	ttl := durationFromHours("BUZZER_TTL_HOURS", 6)

	store, err := buzzer.NewStore(dataPath, ttl)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	go store.RunJanitor(5 * time.Minute)

	server := buzzer.NewServer(store, web.FS)
	log.Printf("buzzer service listening on %s", addr)
	if err := http.ListenAndServe(addr, server); err != nil {
		log.Fatal(err)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationFromHours(key string, fallback int) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return time.Duration(fallback) * time.Hour
	}
	hours, err := strconv.Atoi(value)
	if err != nil || hours < 1 {
		return time.Duration(fallback) * time.Hour
	}
	return time.Duration(hours) * time.Hour
}
