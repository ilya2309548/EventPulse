package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/ilya2309548/EventPulse/internal/common"
)

func cpuBurn(d time.Duration) {
	end := time.Now().Add(d)
	var x float64 = 0
	for time.Now().Before(end) {
		// some meaningless floating point ops to keep the CPU busy
		x += math.Sqrt(rand.Float64())
		if x > 1e9 {
			x = 0
		}
	}
}

func workHandler(w http.ResponseWriter, r *http.Request) {
	// Burn ~50–100ms CPU per request
	d := 50 + rand.Intn(50)
	cpuBurn(time.Duration(d) * time.Millisecond)
	fmt.Fprintf(w, "ok %dms\n", d)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func main() {
	service := os.Getenv("SERVICE")
	if service == "" {
		service = "app"
	}
	common.Init("app-service")
	// Seed randomness
	rand.Seed(time.Now().UnixNano())

	http.HandleFunc("/work", workHandler)
	http.HandleFunc("/healthz", healthHandler)

	addr := ":8080"
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
