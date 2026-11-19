# home-gw

Dashboard untuk router OpenWrt (armv7) sebagai pengganti Home Assistant. Isinya:

- **Client jaringan**: daftar perangkat dari DHCP lease, ARP, dan WiFi (hostapd), lengkap dengan status ping dan lama terhubung.
- **IoT Sonoff**: status, kontrol on/off, konfigurasi perangkat, dan pemantauan daya (mDNS).
- **Energi**: estimasi pemakaian listrik per perangkat, rekap harian dan bulanan, serta biaya bila tarif diisi.
- **Penjadwalan**: scene dengan jadwal waktu, termasuk terbit dan terbenam matahari.
- Kamera direncanakan (belum ada).

Frontend ditanam ke dalam binary (`go:embed`), sehingga cukup satu berkas untuk di-deploy.

## Build

Build di Mac/PC, bukan di router. Tidak butuh CGO. Butuh Go 1.26 atau lebih baru, dan internet saat
pertama kali mengunduh driver SQLite (`modernc.org/sqlite`, murni Go).

```sh
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -ldflags "-s -w" -o dashboard-armv7 .
```

Uji:

```sh
go vet ./...
go test ./...
```

## Deploy

Lokasi instalasi di router (`/opt/dashboard`):

```sh
mkdir -p /opt/dashboard
cp dashboard-armv7 /opt/dashboard/dashboard
```

Contoh konfigurasi `/opt/dashboard/config.json`:

```json
{
  "listen": ":8080",
  "lan_iface": "br-lan",
  "tariff_per_kwh": 1444.7,
  "utc_offset_hours": 7
}
```

Contoh init script procd di `/etc/init.d/dashboard`:

```sh
#!/bin/sh /etc/rc.common
START=95
STOP=10
USE_PROCD=1

start_service() {
	procd_open_instance
	procd_set_param command /opt/dashboard/dashboard -config /opt/dashboard/config.json
	procd_set_param respawn
	procd_set_param stderr 1
	procd_set_param stdout 1
	procd_close_instance
}
```

Aktifkan service:

```sh
chmod +x /etc/init.d/dashboard
/etc/init.d/dashboard enable && /etc/init.d/dashboard start
```

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
| `poll_seconds` | `5` | Selang siklus client |
| `sweep_seconds` | `60` | Selang sweep ping subnet |
| `devices_file` | `devices.json` | Opsi impor awal perangkat Sonoff ke SQLite bila ada |
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

Pengaturan energi (watt, konsumsi standby, catatan) dan daftar perangkat disimpan di SQLite.

## Data

Semua data disimpan di SQLite (`data/dashboard.db`): konfigurasi perangkat Sonoff, riwayat ON/OFF,
sumber perubahannya, log daya (sampel mentah, rekap menit, kejadian), kWh harian terukur,
pengaturan energi, dan log eksekusi jadwal.

Yang tetap berupa berkas:

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
| GET, POST | `/api/devices` | Daftar perangkat / tambah perangkat baru |
| DELETE | `/api/devices/{id}` | Hapus perangkat dari SQLite |
| GET | `/api/devices/discovered` | Daftar perangkat baru terdeteksi mDNS |
| POST | `/api/devices/discover` | Picu query probe mDNS baru |
| `/api/sched` | | Penjadwalan |

Endpoint yang mengubah state (POST, PUT, DELETE) wajib mengirim header `X-Dashboard: 1`. Header ini memicu preflight CORS, sehingga halaman web lain tidak bisa mengirim perintah dari browser.

## Keamanan

- **Tidak ada autentikasi.** Server mendengarkan di semua interface (`:8080`). Siapa pun di LAN yang bisa mengirim header `X-Dashboard: 1` dapat menyalakan atau mematikan perangkat dan mengubah konfigurasi. Bind ke IP LAN saja, misalnya `"listen": "10.142.27.1:8080"`, atau batasi lewat firewall. Jangan izinkan akses dari luar LAN.

## Catatan dan masalah yang diketahui

- **Flush kWh harian bisa hilang saat shutdown.** Goroutine energi harian memanggil `daily.Flush` saat `ctx` dibatalkan (`main.go`, sekitar baris 509), tetapi `main` hanya menunggu `flushDone` untuk log daya (sekitar baris 595). Proses bisa selesai sebelum flush selesai, sehingga data kWh hingga 5 menit bisa hilang. Perbaikan: tambahkan channel `dailyDone` seperti `flushDone`, lalu tunggu sebelum keluar.
