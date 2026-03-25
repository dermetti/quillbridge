# Stage 1: build
FROM golang:1.26-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /quillbridge ./cmd/quillbridge

# Stage 2: minimal runtime image
FROM alpine:3.21

WORKDIR /app

COPY --from=builder /quillbridge /quillbridge

VOLUME ["/app/data"]

EXPOSE 8080

CMD ["/quillbridge"]
