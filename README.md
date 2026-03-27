# Artoo

A lightweight desktop app for browsing and managing Cloudflare R2 buckets.

## Features

- Browse objects across multiple R2 buckets
- Upload files and folders with drag-and-drop support
- Download objects with parallel transfers
- Preview images, text, JSON, and CSV files inline
- Search objects by prefix
- View bucket statistics
- List and grid view modes
- Configurable download concurrency

## Requirements

- [Go 1.24+](https://go.dev/dl/)
- [Node.js 22+](https://nodejs.org/)
- [Wails CLI](https://wails.io/docs/gettingstarted/installation)

Install Wails:

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@latest
```

## Development

Run the app in development mode with hot reload:

```sh
wails dev
```

Or using mise:

```sh
mise run dev
```

## Building

Build a production binary:

```sh
wails build
```

The binary will be output to `build/bin/`.

## Installation (Linux)

```sh
mise run install
```

This installs the binary to `/usr/local/bin` and adds a desktop entry.

## Configuration

On first launch, enter your Cloudflare R2 credentials:

- **Account ID**: Your Cloudflare account ID
- **Access Key ID**: R2 API token access key
- **Secret Access Key**: R2 API token secret key
- **API Token** (optional): For bucket statistics via Cloudflare API

Credentials are stored at `~/.config/artoo/config.json`.

## License

MIT
