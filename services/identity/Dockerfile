FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/identity ./services/identity/cmd/identity

FROM alpine:3.20 AS base
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
USER 10001

FROM base AS identity
COPY --from=build /out/identity /usr/local/bin/identity
COPY --from=build /src/services/identity/migrations /app/migrations
EXPOSE 8080
ENTRYPOINT ["identity"]
