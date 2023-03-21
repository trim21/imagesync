package main

import (
	"log"
	"os"

	"github.com/trim21/imagesync"
)

func main() {
	if err := imagesync.Execute(); err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}
