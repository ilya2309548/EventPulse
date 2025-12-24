package main

import (
	"log"
	"time"

	"github.com/ilya2309548/EventPulse/internal/common"
)

func main() {
	common.Init("action-runner")
	log.Println("Action Runner skeleton — implementation TBD")
	for {
		time.Sleep(60 * time.Second)
	}
}
