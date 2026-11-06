FROM golang:1.26-alpine AS build
WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/backfill ./cmd/backfill \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/identity ./services/identity/cmd/identity \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/workspace ./services/workspace/cmd/workspace \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/gateway ./services/gateway/cmd/gateway

FROM alpine:3.20 AS base
RUN apk add --no-cache ca-certificates tzdata wget && adduser -D -u 10001 app
WORKDIR /app
USER app

FROM base AS api
COPY --from=build /out/api /usr/local/bin/api
COPY --from=build /src/migrations /app/migrations
EXPOSE 8080
ENTRYPOINT ["api"]

FROM base AS worker
COPY --from=build /out/worker /usr/local/bin/worker
EXPOSE 9091
ENTRYPOINT ["worker"]

FROM base AS migrate
COPY --from=build /out/migrate /usr/local/bin/migrate
COPY --from=build /src/migrations /app/migrations
COPY --from=build /src/services/identity/migrations /app/services/identity/migrations
COPY --from=build /src/services/workspace/migrations /app/services/workspace/migrations
ENTRYPOINT ["migrate"]

# One-shot legacy projection job. Opt-in: `DEPLOY_BACKFILL=1 ./scripts/deploy.sh`
# (or `docker compose run --rm backfill`). Reads identity/workspace and writes
# the legacy database, so it only runs when a legacy cutover is intended.
FROM base AS backfill
COPY --from=build /out/backfill /usr/local/bin/backfill
ENTRYPOINT ["backfill"]

FROM base AS identity
COPY --from=build /out/identity /usr/local/bin/identity
COPY --from=build /src/services/identity/migrations /app/migrations
EXPOSE 8080
ENTRYPOINT ["identity"]

FROM base AS workspace
COPY --from=build /out/workspace /usr/local/bin/workspace
COPY --from=build /src/services/workspace/migrations /app/migrations
EXPOSE 8080
ENTRYPOINT ["workspace"]

FROM base AS gateway
COPY --from=build /out/gateway /usr/local/bin/gateway
EXPOSE 8080
ENTRYPOINT ["gateway"]
