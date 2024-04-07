package main

import (
	"flag"
	"log"
)

func main() {
	flag.Parse()
	switch flag.Arg(0) {
	case "app":
		runApp()
	case "cdc":
		runCDC()
	default:
		log.Fatalln("invalid argument", flag.Args())
	}
}
