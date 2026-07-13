# Buzzer as a Service

A small public game-show buzzer service hosted at:

https://stuff.kvanland.com/buzzer-as-a-service/

## Features

- Create a group and share a group code.
- Join as a player with a name and color.
- Live first-buzz and ordered buzz-in list per round.
- Host controls for reset, round-count reset, global lock, individual lock, and player removal.
- Browser reconnect support for host and players.
- JSON-backed group persistence with expiry.
- Mobile and desktop responsive UI.

## Runtime

The Go service listens on loopback by default:

```sh
BUZZER_ADDR=127.0.0.1:8097
BUZZER_DATA=/var/lib/buzzer-as-a-service/groups.json
BUZZER_TTL_HOURS=6
```

Caddy proxies `https://stuff.kvanland.com/buzzer-as-a-service/` to the loopback service through the VPS web front end.

## Development

```sh
go test ./...
go build -o bin/buzzer-as-a-service ./cmd/buzzer
```

## Deploy Files

- `deploy/buzzer-as-a-service.service`: systemd unit.
- `deploy/Caddyfile.example`: sanitized Caddy route example with request body cap.
