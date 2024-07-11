# Hermes

Hermes automates downloading releases from GitHub and uploading them to a CDN.

## How It Works

Hermes periodically checks for new releases on GitHub by looking at a remote JSON manifest. When a new release is found, it downloads the release, uploads it to the specified CDN, maintains a limited number of builds locally, and creates a symbolic link to the latest downloaded build so that it can be easily accessed using the `latest.zip` link.

## Usage

To use Hermes, you need to specify the interval, release index URL, codename, and root path. 

Example usage:

```bash
hermes -interval=30 \
       -releaseIndex="https://raw.githubusercontent.com/Vanilla-OS/info/main/devBuilds.json" \
       -codename="orchid" \
       -root="./builds"
```

### Building from Source

To build the Hermes binary from source, run:

```sh
go build -o hermes .
```

### Why the name Hermes?

Hermes, in Greek mythology, is known as the swift-footed messenger of the gods, often depicted with winged sandals and a caduceus (a staff entwined with two snakes). This symbolism makes Hermes a highly suitable name for a tool that automates the distribution of software releases.
