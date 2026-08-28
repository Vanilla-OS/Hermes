package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/vanilla-os/Hermes/pkg/downloader"
	"github.com/vanilla-os/Hermes/pkg/release"
)

type config struct {
	interval   time.Duration
	repository string
	workflow   string
	branch     string
	root       string
	publicURL  string
	apiURL     string
	nightlyURL string
	keep       int
	token      string
	once       bool
}

func main() {
	configuration, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, configuration); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() (config, error) {
	interval, err := envInt("HERMES_INTERVAL", 30)
	if err != nil {
		return config{}, err
	}
	keep, err := envInt("HERMES_KEEP", 2)
	if err != nil {
		return config{}, err
	}
	once, err := envBool("HERMES_ONCE", false)
	if err != nil {
		return config{}, err
	}

	intervalFlag := flag.Int("interval", interval, "poll interval in minutes")
	repository := flag.String("repository", envString("HERMES_REPOSITORY", "Vanilla-OS/live-iso"), "GitHub repository")
	workflow := flag.String("workflow", envString("HERMES_WORKFLOW", "build-iso.yml"), "GitHub Actions workflow")
	branch := flag.String("branch", envString("HERMES_BRANCH", "orchid"), "workflow branch")
	root := flag.String("root", envString("HERMES_ROOT", "/srv/downloads"), "download directory")
	publicURL := flag.String("public-url", envString("HERMES_PUBLIC_URL", "https://download.vanillaos.org"), "public download URL")
	apiURL := flag.String("api-url", envString("HERMES_API_URL", "https://api.github.com"), "GitHub API URL")
	nightlyURL := flag.String("nightly-url", envString("HERMES_NIGHTLY_URL", "https://nightly.link"), "nightly.link URL")
	keepFlag := flag.Int("keep", keep, "dated builds to keep per architecture")
	onceFlag := flag.Bool("once", once, "synchronize once and exit")
	flag.Parse()

	if *intervalFlag < 1 {
		return config{}, errors.New("interval must be at least 1 minute")
	}
	if *keepFlag < 1 {
		return config{}, errors.New("keep must be at least 1")
	}
	if *workflow == "" || *branch == "" {
		return config{}, errors.New("workflow and branch are required")
	}

	return config{
		interval:   time.Duration(*intervalFlag) * time.Minute,
		repository: *repository,
		workflow:   *workflow,
		branch:     *branch,
		root:       *root,
		publicURL:  *publicURL,
		apiURL:     *apiURL,
		nightlyURL: *nightlyURL,
		keep:       *keepFlag,
		token:      os.Getenv("HERMES_GITHUB_TOKEN"),
		once:       *onceFlag,
	}, nil
}

func run(ctx context.Context, config config) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	client := &http.Client{Transport: transport}

	syncer, err := downloader.New(downloader.Config{
		Repository: config.repository,
		Root:       config.root,
		PublicURL:  config.publicURL,
		NightlyURL: config.nightlyURL,
		Keep:       config.keep,
	}, client)
	if err != nil {
		return err
	}

	synchronize := func() error {
		latest, err := release.FetchLatest(ctx, client, config.apiURL, config.repository, config.workflow, config.branch, config.token)
		if err != nil {
			return err
		}
		updated, err := syncer.Sync(ctx, latest)
		if err != nil {
			return err
		}
		if updated {
			log.Printf("published workflow run %d for amd64 and arm64", latest.ID)
		} else {
			log.Printf("workflow run %d is already published", latest.ID)
		}
		return nil
	}

	if config.once {
		return synchronize()
	}
	if err := synchronize(); err != nil {
		log.Printf("synchronization failed: %v", err)
	}

	ticker := time.NewTicker(config.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := synchronize(); err != nil {
				log.Printf("synchronization failed: %v", err)
			}
		}
	}
}

func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid value for %s: %w", name, err)
	}
	return parsed, nil
}

func envBool(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("invalid value for %s: %w", name, err)
	}
	return parsed, nil
}
