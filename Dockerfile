# syntax=docker/dockerfile:1.7
FROM golang:1.27.0-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags='-s -w' \
    -o /out/zenmoney-mcp ./cmd/zenmoney-mcp

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/zenmoney-mcp /zenmoney-mcp
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/zenmoney-mcp"]
