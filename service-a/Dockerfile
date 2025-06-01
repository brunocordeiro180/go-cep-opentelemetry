FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY . .
RUN go mod tidy && \
    go build -o service-a .

FROM scratch

COPY --from=builder /app/service-a /service-a

ENTRYPOINT [ "/service-a" ]
