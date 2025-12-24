package main

import (
	"log"
	"time"

	"github.com/ilya2309548/EventPulse/internal/common"
)

func main() {
	common.Init("ingest")
	log.Println("Telemetry Ingest skeleton — implementation TBD")
	for {
		time.Sleep(60 * time.Second)
	}
}
