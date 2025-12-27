package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/ilya2309548/EventPulse/internal/common"
)

func alertmanagerHandler(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("error reading body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// Try to pretty print JSON if possible
	var v any
	if err := json.Unmarshal(body, &v); err == nil {
		pretty, _ := json.MarshalIndent(v, "", "  ")
		log.Printf("Alertmanager webhook:\n%s", string(pretty))
	} else {
		log.Printf("Alertmanager webhook (raw): %s", string(body))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("received"))
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("webhook-debug up"))
}

func main() {
	common.Init("webhook-debug")
	http.HandleFunc("/alertmanager", alertmanagerHandler)
	http.HandleFunc("/", rootHandler)
	addr := ":8080"
	log.Printf("listening on %s", addr)
	srv := &http.Server{Addr: addr, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
