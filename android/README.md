# IoT Dashboard untuk Android

App native (Kotlin + Jetpack Compose) untuk dashboard IoT di router OpenWrt. Dashboard di router berperan sebagai gateway: app hanya berbicara dengan API-nya dan tidak pernah langsung ke perangkat Sonoff.

## Fitur v1
- Kartu semua perangkat (kapan pun `devices.json` berubah, daftar ikut berubah), diperbarui langsung lewat SSE `/api/events`.
- **Toggle hanya lewat ketuk dua kali** (ketuk sekali menampilkan pengingat).
- Status ON/OFF dengan lama (detik hanya di bawah 1 menit), offline, kekuatan sinyal, daya (POWR316D), kWh hari ini per perangkat, total kWh dan biaya hari ini.
- Alamat dashboard bisa diganti di ikon ⚙ (mis. alamat router lewat VPN). Bawaan `http://10.142.27.1:8081`.
- Koneksi dihentikan saat app tidak terlihat (hemat baterai) dan disambung ulang otomatis.

Belum ada: popup pengaturan perangkat, grafik, notifikasi.

## Membangun
1. Pasang Android Studio (versi terbaru), buka folder ini (`File > Open`). Biarkan Gradle sync (butuh internet; pertama kali agak lama).
2. Sambungkan HP (USB debugging) lalu tekan Run, atau `Build > Build APK(s)`. Lewat terminal: `./gradlew assembleDebug`, hasilnya `app/build/outputs/apk/debug/app-debug.apk`.
3. Izinkan pemasangan dari sumber luar bila APK dipasang manual.

Di sisi router, jalankan dashboard IoT (`iot-dashboard`, port 8081) atau dashboard penuh (port 8080 — ganti URL di app).

## Catatan keamanan
Dashboard tidak memakai login. App memakai HTTP biasa (cleartext diizinkan di manifest). Batasi port dashboard ke LAN/VPN saja.
