FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY . .
RUN go mod tidy && \
    go build -o weather-api .

FROM scratch

COPY --from=builder /app/weather-api /weather-api

ENTRYPOINT [ "/weather-api" ]
