package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
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
	// Tunable CPU burn via query params: ms (duration) and workers (parallel goroutines)
	// Defaults: 100ms, 1 worker
	ms := 100
	workers := 1
	if v := r.URL.Query().Get("ms"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ms = n
		}
	} else {
		// If not provided, randomize lightly to create variance
		ms = 100 + rand.Intn(200) // 100–300ms
	}
	if v := r.URL.Query().Get("workers"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workers = n
		}
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			cpuBurn(time.Duration(ms) * time.Millisecond)
		}()
	}
	wg.Wait()
	fmt.Fprintf(w, "ok %dms workers=%d\n", ms, workers)
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

	// Optionally set GOMAXPROCS via env APP_GOMAXPROCS to burn multiple cores inside container
	if v := os.Getenv("APP_GOMAXPROCS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			runtime.GOMAXPROCS(n)
			log.Printf("GOMAXPROCS set to %d", n)
		}
	}

	http.HandleFunc("/work", workHandler)
	http.HandleFunc("/healthz", healthHandler)

	addr := ":8080"
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}
