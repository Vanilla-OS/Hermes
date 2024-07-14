package main

import (
	"flag"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/vanilla-os/Hermes/pkg/downloader"
	"github.com/vanilla-os/Hermes/pkg/utils"
)

func main() {
	var (
		interval     int
		releaseIndex string
		codename     string
		root         string
	)

	flag.IntVar(&interval, "interval", 0, "time interval in minutes")
	flag.StringVar(&releaseIndex, "releaseIndex", "", "JSON link to use for the instance")
	flag.StringVar(&codename, "codename", "", "codename to use for the path")
	flag.StringVar(&root, "root", "", "root of builds")
	flag.Parse()

	if interval == 0 {
		intervalEnv := os.Getenv("HERMES_INTERVAL")
		if intervalEnv != "" {
			intervalParsed, err := strconv.Atoi(intervalEnv)
			if err != nil {
				log.Fatalf("invalid value for HERMES_INTERVAL: %v", err)
			}
			interval = intervalParsed
		}
	}

	if releaseIndex == "" {
		releaseIndex = os.Getenv("HERMES_RELEASE_INDEX")
	}

	if codename == "" {
		codename = os.Getenv("HERMES_CODENAME")
	}

	if root == "" {
		root = os.Getenv("HERMES_ROOT")
	}

	if interval == 0 || releaseIndex == "" || codename == "" || root == "" {
		log.Fatalf("all flags or environment variables must be set. usage: -interval=30 -releaseIndex=<URL> -codename=<name> -root=<path>")
	}

	buildsPath := utils.GetBuildsPath(root, codename)
	utils.CreateDir(buildsPath)

	ticker := time.NewTicker(time.Duration(interval) * time.Minute)
	defer ticker.Stop()

	for {
		downloader.CheckForNewRelease(releaseIndex, buildsPath)
		<-ticker.C
	}
}
