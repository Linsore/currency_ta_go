# build
FROM golang:1.23 AS build
WORKDIR /app
COPY . .
RUN go mod tidy \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /currency_ta_go ./cmd/server

# run
FROM gcr.io/distroless/base-debian12
ENV ADDR=":8080"
ENV DATABASE_URL="postgres://postgres:postgres@db:5432/quotes?sslmode=disable"
ENV FX_BASE_URL="https://api.frankfurter.dev/v1/"
ENV FX_PAIRS="USD/EUR,EUR/USD,EUR/MXN,USD/MXN"
ENV CACHE_TTL="10m"
ENV RATE_LIMIT="60"
ENV RATE_WINDOW="1m"
COPY --from=build /currency_ta_go /currency_ta_go
EXPOSE 8080
ENTRYPOINT ["/currency_ta_go"]