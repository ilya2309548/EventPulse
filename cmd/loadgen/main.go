package main

import (
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	target := getenv("TARGET_URL", "http://traefik/work")
	rateStr := getenv("RATE", "50") // req/s
	concurrencyStr := getenv("CONCURRENCY", "10")

	rate, err := strconv.Atoi(rateStr)
	if err != nil || rate <= 0 {
		rate = 10
	}
	conc, err := strconv.Atoi(concurrencyStr)
	if err != nil || conc <= 0 {
		conc = runtime.NumCPU()
	}

	interval := time.Second / time.Duration(rate)
	client := &http.Client{Timeout: 5 * time.Second}

	log.Printf("loadgen: target=%s rate=%d/s conc=%d", target, rate, conc)

	sem := make(chan struct{}, conc)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var sent, okCount, errCount int64
	start := time.Now()

	for {
		<-ticker.C
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			sent++
			resp, err := client.Get(target)
			if err != nil {
				errCount++
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				okCount++
			} else {
				errCount++
			}
		}()

		if sent%500 == 0 {
			el := time.Since(start).Round(time.Second)
			log.Printf("loadgen stats: sent=%d ok=%d err=%d elapsed=%s", sent, okCount, errCount, el)
		}
	}
}
