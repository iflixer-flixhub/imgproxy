# -------- build --------
FROM golang:1.25-bookworm AS build
WORKDIR /src

RUN apt-get update && apt-get install -y --no-install-recommends \
    pkg-config \
    libwebp-dev \
    ca-certificates \
  && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=1 go build -o /out/imgproxy .

# -------- runtime --------
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    libwebp7 \
    ca-certificates \
    wget curl \
  && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/imgproxy /app/imgproxy
RUN chmod 0755 /app/imgproxy
EXPOSE 80
CMD ["/app/imgproxy"]