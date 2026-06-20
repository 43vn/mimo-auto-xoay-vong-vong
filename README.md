# mimo-ss-proxy

HTTP proxy server cung cấp endpoint tương thích OpenAI API, forward request đến Xiaomi MiMo AI API (`api.xiaomimimo.com`). Hỗ trợ tunnel qua Shadowsocks pool để xoay IP, bypass rate limit và geo-restriction.

## Features

- OpenAI-compatible `/v1/chat/completions` endpoint (streaming + non-streaming)
- Hỗ trợ đa giao thức proxy: `ss://`, `socks5://`, `http://`, `https://`
- Shadowsocks proxy pool với tự động xoay IP round-robin
- Lấy SS server tự động từ [shadowmere.xyz](https://shadowmere.xyz)
- SmartRouter: chọn proxy tốt nhất dựa trên latency (cho custom/fallback-proxy mode)
- Fallback chain: user pool → shadowmere SS pool → direct
- Health check background mỗi 60s trên tất cả SS server
- Fast concurrent health check (`--fast-check`) với cancellation, debounce, max 20 goroutines
- Auto-refresh pool mỗi 300s (auto/fallback mode)
- Rate limiter local (sliding window: 8 req/60s, min 2s interval)
- JWT bootstrap tự động từ upstream API (route qua SS tunnel)
- Retry logic: timeout (5 retries), 401 (refresh JWT), 429 (rotate proxy + delay + refresh JWT)
- SSE streaming proxy với thinking timeout 120s + idle read timeout 5 phút
- CORS support
- Docker image (distroless, multi-arch)
- Graceful shutdown (SIGINT/SIGTERM)
- `--version` flag với build metadata

## Proxy Modes

| Mode | Behavior |
|------|----------|
| `direct` | Kết nối trực tiếp lên upstream, không qua proxy |
| `auto` | Luôn xoay qua SS pool từ shadowmere.xyz (rate limit tắt, rotate on 429) |
| `custom` | Dùng proxy cụ thể qua `--proxy` hoặc `--proxy-file` (SmartRouter chọn best latency) |
| `fallback` | Thử direct trước, nếu fail thì fallback sang SS pool |
| `fallback-proxy` | Thử user pool trước, fallback → shadowmere SS pool → direct |

## Quick Start

```bash
# Build
make build

# Chạy direct mode (không cần proxy)
./mimo-ss-proxy --mode=direct --port=18084

# Chạy auto mode (tự lấy SS pool từ shadowmere.xyz)
./mimo-ss-proxy --mode=auto --port=18084

# Chạy với SS cụ thể
./mimo-ss-proxy --mode=custom --proxy="ss://aes-256-gcm:password@1.2.3.4:8388"

# Chạy với proxy-file (hỗ trợ ss://, socks5://, http://, https://)
./mimo-ss-proxy --mode=custom --proxy-file=proxies.txt

# Chạy fallback-proxy mode (user pool → shadowmere → direct)
./mimo-ss-proxy --mode=fallback-proxy --proxy-file=proxies.txt --port=18084

# Docker
docker run -p 18084:18084 ghcr.io/<user>/mimo-xoay:latest --mode=auto
```

## Proxy File Format

`--proxy-file` hỗ trợ nhiều giao thức, mỗi dòng một URI. Dòng trống và dòng `#` comment bị bỏ qua:

```
# Shadowsocks
ss://aes-256-gcm:password@server1:8388
ss://chacha20-ietf-poly1305:secret@server2:8388

# SOCKS5 (có hoặc không auth)
socks5://user:pass@proxy.example.com:1080
socks5://10.0.0.1:1080

# HTTP/HTTPS CONNECT proxy (có hoặc không auth)
http://user:pass@http-proxy:8080
https://secure-proxy:443

# Có thể bỏ qua ss:// prefix (tự động thêm)
aes-256-gcm:password@server3:8388
```

## Usage

### OpenAI Client

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:18084/v1",
    api_key="any-string"
)

response = client.chat.completions.create(
    model="mimo-auto",
    messages=[{"role": "user", "content": "Xin chào"}],
    stream=True
)

for chunk in response:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

### cURL

```bash
curl http://localhost:18084/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mimo-auto",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | `auto` | Proxy mode: `direct`, `auto`, `custom`, `fallback`, `fallback-proxy` |
| `--proxy` | `""` | Proxy URI (`ss://`, `socks5://`, `http://`, `https://`) |
| `--proxy-file` | `""` | File chứa proxy URIs (mỗi dòng 1 URI, multi protocol) |
| `--port` | `18084` | HTTP listen port |
| `--proxy-timeout` | `60` | Upstream timeout (giây, 0 = mặc định 60s) |
| `--save-proxy` | `""` | Lưu proxy pool ra file |
| `--disable-rate-limit` | `false` | Tắt rate limiter local (chỉ upstream 429) |
| `--fast-check` | `false` | Bật health check nhanh concurrency (cancellation + debounce, max 20 goroutines) |
| `--version` | `false` | Hiển thị version + build metadata |

## Development

```bash
make test           # Chạy tests
make test-race      # Chạy tests với race detector
make cover          # Xem coverage
make vet            # Go vet
make build-win      # Cross-compile cho Windows
make run MODE=auto  # Build + chạy
```

## Systemd User Service

```bash
make install-service     # Cài systemd user service
make enable-service      # Enable + start service
make stop-service        # Stop service
make disable-service     # Stop + disable service
make uninstall-service   # Stop + disable + xóa service file
make rebuild-service     # Stop → rebuild → reinstall → restart
make service-status      # Xem service status
make service-logs        # Xem service logs (follow mode)
```

## Docker

### Build

```bash
docker build \
  --build-arg VERSION=$(git describe --tags 2>/dev/null || echo dev) \
  --build-arg COMMIT=$(git rev-parse HEAD) \
  --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  -t mimo-ss-proxy .
```

### Run

```bash
docker run -p 18084:18084 --rm mimo-ss-proxy --mode=auto
docker run -p 18084:18084 --rm -v $PWD/proxies.txt:/proxies.txt mimo-ss-proxy --mode=custom --proxy-file=/proxies.txt
```

GitHub Actions tự động build Docker image và push lên GHCR khi push tag `v*` hoặc master.

## Project Structure

```
cmd/mimo-ss-proxy/    # Entry point (CLI flags, wiring, mixed proxy loader)
proxy/                # HTTP handlers, JWT, rate limiter, SSE streaming
  ├── server.go       # Core server (forward request, retry, timeout mgmt)
  ├── handler.go      # Model list, CORS, system message injection
  ├── router.go       # SmartRouter (latency-based proxy selection)
  ├── fallback.go     # FallbackHandler (user pool → shadowmere → direct)
  ├── stream.go       # SSE streaming proxy (thinking timeout, [DONE])
  ├── ratelimit.go    # Sliding window rate limiter
  └── jwt.go          # JWT bootstrap + cache
rotator/              # Round-robin address rotator (Update, Trigger)
sspool/               # SS pool management
  ├── pool.go         # Thread-safe SS server pool (Snapshot, ReplaceAll)
  ├── config.go       # URI parsers (ss://, socks5://, http://, https://)
  ├── dialer_iface.go # ProxyDialer interface
  ├── dialer.go       # Shadowsocks dialer
  ├── socks5_dialer.go# SOCKS5 dialer
  ├── http_dialer.go  # HTTP CONNECT dialer
  ├── fetcher.go      # Shadowmere API fetcher
  └── health.go       # TCP health check (sequential + fast concurrent)
```

## Release

```bash
git tag v1.0.0
git push origin v1.0.0
```

GitHub Actions tự build binary cho linux/amd64, linux/arm64, windows/amd64, windows/arm64 và Docker image lên GHCR.

## License

MIT
