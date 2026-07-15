FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /opensave-relay ./cmd/opensave-relay

FROM alpine:3.20
RUN adduser -D relay
USER relay
COPY --from=build /opensave-relay /usr/local/bin/opensave-relay
ENV PORT=10000
EXPOSE 10000
CMD ["opensave-relay"]
