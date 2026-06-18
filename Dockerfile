# Stage 1: Build
FROM docker.io/library/golang:1.24.2-alpine AS builder

# Install git for metadata
RUN apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build arguments for metadata
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ARG REPO_NAME=unknown
ARG REPO_VISIBILITY=unknown

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w \
    -X main.version=${VERSION} \
    -X main.commit=${COMMIT} \
    -X main.buildTime=${BUILD_TIME} \
    -X main.repoName=${REPO_NAME} \
    -X main.repoVisibility=${REPO_VISIBILITY}" \
    -o /mimo-ss-proxy ./cmd/mimo-ss-proxy

# Create metadata file in builder
RUN mkdir -p /etc/mimo && \
    printf '{"name":"%s","visibility":"%s","version":"%s","commit":"%s","build_time":"%s"}\n' \
      "${REPO_NAME}" "${REPO_VISIBILITY}" "${VERSION}" "${COMMIT}" "${BUILD_TIME}" \
      > /etc/mimo/repo.json

# Stage 2: Runtime
FROM gcr.io/distroless/static-debian12

# Copy binary from builder
COPY --from=builder /mimo-ss-proxy /mimo-ss-proxy

# Copy metadata from builder
COPY --from=builder /etc/mimo/repo.json /etc/mimo/repo.json

# Set entrypoint
ENTRYPOINT ["/mimo-ss-proxy"]