# build
FROM golang:1.23 AS build
WORKDIR /app
COPY . .
RUN go mod tidy \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /currency_ta_go ./cmd/server

# run
FROM gcr.io/distroless/base-debian12
COPY --from=build /currency_ta_go /currency_ta_go
EXPOSE 8080
ENTRYPOINT ["/currency_ta_go"]