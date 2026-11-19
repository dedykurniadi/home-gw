# home-gw

Dashboard untuk router OpenWrt (armv7) sebagai pengganti Home Assistant. Isinya:

- **Client jaringan**: daftar perangkat dari DHCP lease, ARP, dan WiFi (hostapd), lengkap dengan status ping dan lama terhubung.
- **IoT Sonoff**: status, kontrol on/off, konfigurasi perangkat, dan pemantauan daya (mDNS).
- **Energi**: estimasi pemakaian listrik per perangkat, rekap harian dan bulanan, serta biaya bila tarif diisi.
- **Penjadwalan**: scene dengan jadwal waktu, termasuk terbit dan terbenam matahari.
- Kamera direncanakan (belum ada).

Frontend ditanam ke dalam binary (`go:embed`), sehingga cukup satu berkas untuk di-deploy.

## Mode

| Mode | Cara aktif | Isi |
|---|---|---|
| Penuh | `dashboard` biasa | Client jaringan, ping, sweep subnet, IoT, energi |
| Hanya IoT | `"iot_only": true` di config, atau build dengan `-X main.buildMode=iot` | IoT dan energi, tanpa tabel client, ping, dan sweep |

## Build

Build di Mac/PC, bukan di router. Tidak butuh CGO. Butuh Go 1.26 atau lebih baru, dan internet saat
pertama kali mengunduh driver SQLite (`modernc.org/sqlite`, murni Go).

```sh
# Penuh
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -ldflags "-s -w" -o dashboard-armv7 .

# Hanya IoT
./build-iot.sh
```

Uji:

```sh
go vet ./...
go test ./...
```

## Deploy

Mode penuh (`/opt/dashboard`):

```sh
mkdir -p /opt/dashboard
cp dashboard-armv7 /opt/dashboard/dashboard
cp config.json devices.json /opt/dashboard/
cp dashboard.init /etc/init.d/dashboard
chmod +x /etc/init.d/dashboard
/etc/init.d/dashboard enable && /etc/init.d/dashboard start
```

Mode hanya IoT (`/opt/iot-dashboard`) memakai `iot-dashboard.init` dan `iot-config.json` dengan cara yang sama. Kedua init script memakai procd dengan `respawn`.

Jalankan manual:

```sh
./dashboard -config config.json
```

Catatan upgrade: data CSV dari versi lama tidak dibaca lagi. Impor dulu dengan binary versi lama
(commit `72ac288`, yang masih punya `-import-csv`) sebelum mengganti binary. Versi ini sudah tidak
menyediakan flag itu.

## Konfigurasi

Berkas JSON, dibaca lewat `-config`. Nilai yang tidak diisi memakai bawaan.

| Kunci | Bawaan | Keterangan |
|---|---|---|
| `listen` | `:8080` | Alamat HTTP |
| `lan_iface` | `br-lan` | Interface LAN untuk ARP dan WiFi |
| `subnet` | dari `lan_iface` | CIDR untuk sweep, `/22` sampai `/30` |
| `poll_seconds` | `5` | Selang siklus client (minimal 30 di mode IoT) |
| `sweep_seconds` | `60` | Selang sweep ping subnet |
| `devices_file` | `devices.json` | Daftar perangkat Sonoff. Folder `data/` dibuat di sampingnya |
| `leases_file`, `arp_file` | `/tmp/dhcp.leases`, `/proc/net/arp` | Sumber data client |
| `static_hosts` | - | Host yang selalu tampil, mis. `{"name": "...", "ip": "..."}` |
| `wired_hosts` | - | IP perangkat kabel, dicek lewat ARP (`arping` bila ada) |
| `wifi_events` | `true` | Lihat bagian WiFi di bawah |
| `no_ping` | `false` | Anggap semua client hidup (tanpa ICMP) |
| `query_seconds` | `30` | Selang query mDNS ke perangkat Sonoff |
| `power`, `onoff`, `sched` | aktif | Objek opsional; `off: true` menonaktifkan. `sched` memakai `data/schedules.json` |
| `location` | - | `{"lat", "lon"}` untuk jadwal terbit dan terbenam |
| `tariff_per_kwh` | `0` | Tarif (Rp/kWh). Nol berarti biaya tidak ditampilkan |
| `utc_offset_hours` | zona sistem | Mis. `7` untuk WIB. Dipakai untuk batas "hari ini" dan "bulan ini" |
| `db_path` | `data/dashboard.db` | Lokasi berkas SQLite |
| `iot_only` | `false` | Mode hanya IoT |

Pengaturan energi (watt, konsumsi standby, catatan) disimpan di SQLite. Nilai `watt` dan `standby_watt` di `devices.json` hanya dipakai sebagai isian awal.

## Data

Semua data disimpan di SQLite (`data/dashboard.db` di samping `devices.json`): riwayat ON/OFF,
sumber perubahannya, log daya (sampel mentah, rekap menit, kejadian), kWh harian terukur,
pengaturan energi, dan log eksekusi jadwal.

Yang tetap berupa berkas:

- `devices.json`: daftar perangkat Sonoff
- `data/schedules.json`: scene dan jadwal

## WiFi

Data client WiFi tidak di-poll. Dashboard:

1. Membaca data WiFi sekali saat start.
2. Menjalankan `ubus subscribe hostapd.<iface>` untuk tiap interface hostapd. Interface baru dicari tiap 60 detik.
3. Membaca ulang data WiFi hanya saat event `assoc`, `disassoc`, `deauth`, `local-deauth`, atau `inactive-deauth`.

Bila `wifi_events` bernilai `false`, data WiFi dibaca ulang tiap siklus sebagai fallback. Perilaku `ubus subscribe` di perangkat nyata belum diuji sepenuhnya.

## API

| Method | Path | Keterangan |
|---|---|---|
| GET | `/api/state` | State IoT, client, dan daya |
| GET | `/api/events` | Aliran SSE: `iot`, `net`, `power` |
| POST | `/api/iot/{id}` | Body `{"cmd": "on" \| "off" \| "toggle"}` |
| POST | `/api/iot/{id}/config` | Startup, pulse, lebar pulse, LED |
| PUT | `/api/energy/cfg/{id}` | Watt, standby, dan catatan |
| GET | `/api/energy`, `/api/energy/compare`, `/api/energy/monthly?months=N` | Estimasi energi |
| GET | `/api/power/{id}?hours=N` | Grafik daya (1 sampai 48 jam) |
| GET | `/api/onoff/{id}?hours=N` | Riwayat ON/OFF (1 sampai 800 jam) |
| `/api/sched` | | Penjadwalan |

Endpoint yang mengubah state (POST, PUT) wajib mengirim header `X-Dashboard: 1`. Header ini memicu preflight CORS, sehingga halaman web lain tidak bisa mengirim perintah dari browser.

## Keamanan

- **Tidak ada autentikasi.** Server mendengarkan di semua interface (`:8080`). Siapa pun di LAN yang bisa mengirim header `X-Dashboard: 1` dapat menyalakan atau mematikan perangkat dan mengubah konfigurasi. Bind ke IP LAN saja, misalnya `"listen": "10.142.27.1:8080"`, atau batasi lewat firewall. Jangan izinkan akses dari luar LAN.

## Catatan dan masalah yang diketahui

- **Flush kWh harian bisa hilang saat shutdown.** Goroutine energi harian memanggil `daily.Flush` saat `ctx` dibatalkan (`main.go`, sekitar baris 509), tetapi `main` hanya menunggu `flushDone` untuk log daya (sekitar baris 595). Proses bisa selesai sebelum flush selesai, sehingga data kWh hingga 5 menit bisa hilang. Perbaikan: tambahkan channel `dailyDone` seperti `flushDone`, lalu tunggu sebelum keluar.
- **Penggantian judul bergantung pada teks HTML.** Mode hanya IoT mengganti `🌐 Network Dashboard` menjadi `⚡ IoT Dashboard` di `internal/web/web.go` (sekitar baris 130). Bila judul di `index.html` berubah, penggantian diam-diam tidak berlaku.
