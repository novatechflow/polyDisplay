# Copyright 2026ff novatechflow (Alexander Alten)
# SPDX-License-Identifier: PolyForm-Shield-1.0.0
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod server.go ./
RUN CGO_ENABLED=0 go build -o /polydisplayd .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /polydisplayd /app/polydisplayd
COPY index.html /app/index.html
EXPOSE 8080
CMD ["/app/polydisplayd"]
