package main

import (
	"flag"
	"log"
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

	if interval == 0 || releaseIndex == "" || codename == "" || root == "" {
		log.Fatalf("all flags must be set. usage: -interval=30 -releaseIndex=<URL> -codename=<name> -root=<path>")
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
