# 3X-UI Telegram Bot

Telegram interface for 3X-UI panel management with moderation workflow and subscription handling.

## Technical Stack

- **Go 1.24.6** - Core language
- **SQLite** - Local state and cache persistence
- **3X-UI API** - VPN panel integration via HTTP
- **Telego** - Telegram Bot API client
- **Zerolog** - Structured logging

## Architecture

```
cmd/bot/              # Application entry point
internal/
├── bot/              # Bot core (handlers, services, middleware)
├── config/           # Configuration management
├── storage/          # SQLite persistence layer
├── logger/           # Structured logging
└── shutdown/         # Graceful shutdown manager
pkg/client/           # 3X-UI HTTP API client
```

Detailed architecture: [`ARCHITECTURE.md`](ARCHITECTURE.md)

## Features

**User Flow:**
- Registration with admin approval
- Multi-tier subscriptions (1/3/6/12 months)
- Trial period support
- Traffic and expiry monitoring
- Subscription renewal requests
- Direct admin messaging

**Admin Functions:**
- Registration moderation
- Client management (block, delete, modify)
- Bulk announcements
- Manual database backups
- Direct user communication

**System:**
- Rate limiting (10 req/min per user)
- Session TTL (24h)
- Client data caching with invalidation
- Automatic periodic backups

## Installation

```bash
git clone https://github.com/0GUrEtZ/x-ui_bot.git
cd x-ui_bot
cp config.yaml.example config.yaml
nano config.yaml
docker-compose up -d
```

**Without Docker:**
```bash
go run ./cmd/bot
```

## Configuration

`config.yaml`:
```yaml
telegram:
  token: "BOT_TOKEN"
  admin_ids: [123456789]
  proxy: ""                    # SOCKS5 proxy (optional)
  api_server: ""               # Custom API endpoint (optional)
  welcome_file: "URL"          # Welcome PDF URL

panel:
  url: "http://host:port/path"
  username: "admin"
  password: "password"
  limit_ip: 5                  # Client IP limit (0 = unlimited)
  traffic_limit_gb: 100        # Traffic limit (0 = unlimited)
  backup_days: 7               # Auto-backup interval (0 = disabled)

payment:
  bank: "Bank Name"
  phone_number: "+1234567890"
  instructions_url: "https://docs.example.com/payment"
  trial_days: 3                # Trial period duration (0 = disabled)
  prices:
    one_month: 300
    three_month: 800
    six_month: 1500
    one_year: 2800
```

## Code Quality

- **0 linting issues** (golangci-lint: errcheck, unused, staticcheck, ineffassign)
- Comprehensive error handling for all API calls
- No dead code or unused functions
- Clean compilation (go build, go vet)
- Optimized dependencies (go mod tidy)

## Requirements

- 3X-UI panel with API access
- Docker + Docker Compose (recommended)
- Go 1.24+ (for native builds)

## Logs

```bash
docker logs x-ui-bot -f
```

## Version

**v1.1.0** - Code quality improvements, architecture documentation, comprehensive error handling