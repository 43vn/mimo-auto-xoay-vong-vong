# mimo-ss-proxy

HTTP proxy server cung cấp endpoint tương thích OpenAI API, forward request đến Xiaomi MiMo AI API (`api.xiaomimimo.com`). Hỗ trợ tunnel qua Shadowsocks pool để xoay IP, bypass rate limit và geo-restriction.

## Features

- OpenAI-compatible `/v1/chat/completions` endpoint (streaming + non-streaming)
- Shadowsocks proxy pool với tự động xoay IP round-robin
- Lấy SS server tự động từ [shadowmere.xyz](https://shadowmere.xyz)
- Health check background mỗi 60s trên tất cả SS server
- Auto-refresh pool mỗi 300s (auto/fallback mode)
- Rate limiter local (sliding window: 8 req/60s, min 2s interval)
- JWT bootstrap tự động từ upstream API
- Retry logic: timeout (5 retries), 401 (refresh JWT), 429 (rotate proxy + delay 120s)
- SSE streaming proxy với thinking timeout 120s
- CORS support

## Proxy Modes

| Mode | Behavior |
|------|----------|
| `direct` | Kết nối trực tiếp lên upstream, không qua SS |
| `auto` | Luôn xoay qua SS pool từ shadowmere.xyz |
| `custom` | Dùng SS server cụ thể qua `--proxy` hoặc `--proxy-file` |
| `fallback` | Thử direct trước, nếu fail thì fallback sang SS pool |
| `fallback-proxy` | Thử SS cụ thể trước, fallback sang pool |

## Quick Start

```bash
# Build
make build

# Chạy direct mode (không cần SS)
./mimo-ss-proxy --mode=direct --port=18084

# Chạy auto mode (tự lấy SS pool từ shadowmere.xyz)
./mimo-ss-proxy --mode=auto --port=18084

# Chạy với SS cụ thể
./mimo-ss-proxy --mode=custom --proxy="ss://aes-256-gcm:password@1.2.3.4:8388"
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
| `--mode` | `direct` | Proxy mode: `direct`, `auto`, `custom`, `fallback`, `fallback-proxy` |
| `--proxy` | `""` | SS server URI (`ss://method:password@host:port`) |
| `--proxy-file` | `""` | File chứa SS servers (mỗi dòng 1 URI) |
| `--port` | `18084` | HTTP listen port |
| `--proxy-timeout` | `60` | Proxy timeout (giây) |
| `--save-proxy` | `""` | Lưu proxy pool ra file |
| `--disable-rate-limit` | `false` | Tắt rate limiter local |

## Development

```bash
make test           # Chạy tests
make test-race      # Chạy tests với race detector
make cover          # Xem coverage
make vet            # Go vet
make build-win      # Cross-compile cho Windows
```

## Project Structure

```
cmd/mimo-ss-proxy/   # Entry point (CLI flags, wiring)
proxy/               # HTTP handlers, JWT bootstrap, rate limiter, SSE streaming
rotator/             # Round-robin address rotator
sspool/              # SS pool management (config, dialer, fetcher, health check)
```

## Release

```bash
git tag v1.0.0
git push origin v1.0.0
```

GitHub Actions tự build binary cho linux/amd64, linux/arm64, windows/amd64, windows/arm64.

## License

MIT
