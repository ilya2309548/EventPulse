package main

import (
	"log"
	"time"

	"github.com/ilya2309548/EventPulse/internal/common"
)

func main() {
	common.Init("incident-api")
	log.Println("Incident Store API skeleton — implementation TBD")
	for {
		time.Sleep(60 * time.Second)
	}
}
