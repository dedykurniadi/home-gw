#!/bin/sh
# Bangun binary hanya-IoT DENGAN SQLite (butuh internet untuk mengunduh driver, jalankan di Mac/PC, bukan di router).
set -e
go get modernc.org/sqlite@latest
go mod tidy
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -tags sqlite -ldflags "-s -w -X main.buildMode=iot" -o iot-dashboard-armv7 .
echo "OK: iot-dashboard-armv7 (SQLite)"
echo "Uji (opsional): go test -tags sqlite ./internal/sqlstore"
