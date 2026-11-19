#!/bin/sh
# Bangun binary hanya-IoT (tanpa tabel client/kamera) untuk router armv7.
set -e
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -ldflags "-s -w -X main.buildMode=iot" -o iot-dashboard-armv7 .
echo "OK: iot-dashboard-armv7"
