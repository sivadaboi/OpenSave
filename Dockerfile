# ─────────────────────────────────────────────────────
# OpenSave WAN Relay — root Dockerfile
#
# Lives at the repo root because the Render service builds
# from here (dockerfilePath: ./Dockerfile). Builds only the
# relay binary; the desktop app is not part of this image.
# ─────────────────────────────────────────────────────

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /opensave-relay ./cmd/opensave-relay

FROM alpine:3.20
RUN apk add --no-cache wget && adduser -D relay
USER relay
COPY --from=build /opensave-relay /usr/local/bin/opensave-relay

ENV PORT=10000
ENV MAX_PER_ROOM=20
EXPOSE 10000

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://localhost:${PORT}/health || exit 1

CMD ["opensave-relay"]
