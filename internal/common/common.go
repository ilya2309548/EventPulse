package common

import (
	"log"
)

// Init logs basic startup message; extend with config/env parsing later.
func Init(service string) {
	log.Printf("%s starting (EventPulse skeleton)", service)
}
