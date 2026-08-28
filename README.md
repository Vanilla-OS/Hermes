# Hermes

Hermes mirrors Vanilla OS images produced by GitHub Actions to the download
server.

It polls the latest successful `build-iso.yml` run in `Vanilla-OS/live-iso`,
downloads the AMD64 and ARM64 artifacts through nightly.link, verifies each ISO
against its SHA256 file, and publishes both architectures together.

Hermes maintains these files in the configured root:

```text
Vanilla-OS-3-stable-amd64.20260826.iso
Vanilla-OS-3-stable-amd64.20260826.sha256.txt
Vanilla-OS-3-stable-arm64.20260826.iso
Vanilla-OS-3-stable-arm64.20260826.sha256.txt
latest-amd64.iso
latest-amd64.sha256.txt
latest-arm64.iso
latest-arm64.sha256.txt
downloads.json
```

`downloads.json` uses the schema consumed by the Vanilla OS website:

```json
[
  {
    "Arch": "amd64",
    "Date": "2026-08-26",
    "Iso": "https://download.vanillaos.org/Vanilla-OS-3-stable-amd64.20260826.iso",
    "Sha256": "https://download.vanillaos.org/Vanilla-OS-3-stable-amd64.20260826.sha256.txt"
  }
]
```

The state file and staging directories are private implementation details. Old
builds recorded in the Hermes state are removed per architecture. Preexisting
and unrecognized files in the download root are left untouched so published
URLs remain valid.

## Run Hermes

Build and run a single synchronization:

```sh
go build -o hermes .
./hermes -once -root ./downloads -public-url https://download.vanillaos.org
```

Run it as a poller:

```sh
./hermes -interval 30 -root /srv/downloads
```

The GitHub token is optional for public repositories. Set
`HERMES_GITHUB_TOKEN` to increase the GitHub API rate limit. The artifact
downloads remain public through nightly.link.

| Flag | Environment variable | Default |
| --- | --- | --- |
| `-interval` | `HERMES_INTERVAL` | `30` minutes |
| `-repository` | `HERMES_REPOSITORY` | `Vanilla-OS/live-iso` |
| `-workflow` | `HERMES_WORKFLOW` | `build-iso.yml` |
| `-branch` | `HERMES_BRANCH` | `orchid` |
| `-root` | `HERMES_ROOT` | `/srv/downloads` |
| `-public-url` | `HERMES_PUBLIC_URL` | `https://download.vanillaos.org` |
| `-api-url` | `HERMES_API_URL` | `https://api.github.com` |
| `-nightly-url` | `HERMES_NIGHTLY_URL` | `https://nightly.link` |
| `-keep` | `HERMES_KEEP` | `2` per architecture |
| `-once` | `HERMES_ONCE` | `false` |

## Install as a service

Install the binary and service files:

```sh
sudo install -Dm755 hermes /usr/local/bin/hermes
sudo useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin hermes
sudo install -d -o hermes -g hermes /srv/downloads
sudo install -Dm644 contrib/hermes.env.example /etc/hermes.env
sudo install -Dm644 contrib/hermes.service /etc/systemd/system/hermes.service
sudo systemctl daemon-reload
sudo systemctl enable --now hermes.service
```

Check the service with `systemctl status hermes.service` and
`journalctl -u hermes.service -f`.

## Container image

`recipe.yml` builds a small runtime image with Vib 1.1. Mount the download
directory at `/srv/downloads` when starting the container.

## Artifact contract

Each successful workflow run must contain exactly one artifact whose name
includes `AMD64` and one whose name includes `ARM64`. Each ZIP must contain one
ISO named like `Vanilla-OS-3-stable-amd64.20260826.iso` and its matching
`.sha256.txt` file. Hermes rejects missing, duplicate, expired, malformed, and
checksum-invalid input without advancing its state.

## Why the name Hermes?

Hermes is the messenger of the gods in Greek mythology. The name reflects the
project's job of delivering Vanilla OS images.
