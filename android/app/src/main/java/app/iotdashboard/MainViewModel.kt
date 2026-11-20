package app.iotdashboard

import android.app.Application
import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

const val DEFAULT_URL = "http://10.142.27.1:8081"

class MainViewModel(app: Application) : AndroidViewModel(app) {
    private val prefs = app.getSharedPreferences("iot", Context.MODE_PRIVATE)

    val baseUrl = MutableStateFlow(prefs.getString("url", DEFAULT_URL) ?: DEFAULT_URL)
    val devices = MutableStateFlow<List<Device>>(emptyList())
    val live = MutableStateFlow(false)
    val energy = MutableStateFlow<Energy?>(null)

    /** Simpanan (cache) jawaban dashboard terakhir; di folder yang tidak ikut backup cloud. */
    private val cache = Cache(File(app.noBackupFilesDir, "snap"))

    /** true bila daftar perangkat yang tampil datang langsung dari dashboard (bukan dari simpanan). */
    val fresh = MutableStateFlow(false)

    /** Waktu (unix detik) data perangkat yang tampil; dibekukan saat terputus. 0 = belum ada. */
    val devicesAt = MutableStateFlow(0L)

    /** >0: data layar dalam sedang dari simpanan (waktu ambil); 0 = langsung dari dashboard. */
    val cachedAt = MutableStateFlow(0L)
    val messages = MutableSharedFlow<String>(extraBufferCapacity = 4)

    /** Perangkat yang baru di-toggle dan menunggu status baru dari dashboard (id -> status saat diketuk). */
    val pending = MutableStateFlow<Map<String, String>>(emptyMap())

    /** Database lokal berisi riwayat ON/OFF & daya hasil sinkron dari dashboard. */
    private val db = LocalDb(app)

    /** Waktu (unix detik) sinkron terakhir yang berhasil; 0 = belum pernah. */
    val syncedAt = MutableStateFlow(0L)

    /** Naik tiap sinkron selesai: layar riwayat memuat ulang dari database lokal. */
    val histTick = MutableStateFlow(0)

    /** true selama tarik-untuk-muat-ulang (sambung ulang + sinkron) berjalan. */
    val refreshing = MutableStateFlow(false)

    /** Naik tiap tarik-untuk-muat-ulang: layar memuat ulang datanya dari dashboard. */
    val refreshTick = MutableStateFlow(0)

    private var jobs: List<Job> = emptyList()
    @Volatile private var lastRaw: String? = null
    private var lastSnap = 0L

    private fun key(path: String) = baseUrl.value.trim().trimEnd('/') + path

    init {
        db.bindUrl(baseUrl.value)
        syncedAt.value = db.meta("sync_at")?.toLongOrNull() ?: 0L
        restore()
    }

    /** Tampilkan data simpanan (bila ada) sampai tersambung ke dashboard. */
    private fun restore() {
        cache.get(key("#devices"))?.let { e ->
            try {
                devices.value = parseDevices(JSONArray(e.body))
                devicesAt.value = e.t
            } catch (_: Exception) {
                // simpanan rusak: diabaikan
            }
        }
        cache.get(key("/api/energy"))?.let { e ->
            try {
                energy.value = parseEnergy(JSONObject(e.body))
            } catch (_: Exception) {
            }
        }
    }

    private fun snapshot() {
        val raw = lastRaw ?: return
        cache.put(key("#devices"), raw, devicesAt.value)
    }

    /** Mulai (atau mulai ulang) sambungan ke dashboard. Dipanggil saat app terlihat. */
    fun start() {
        stop()
        val api = api()
        jobs = listOf(
            viewModelScope.launch {
                while (isActive) {
                    try {
                        api.streamIot({ live.value = true }, { list, raw ->
                            devices.value = list
                            fresh.value = true
                            val t = System.currentTimeMillis() / 1000
                            devicesAt.value = t
                            lastRaw = raw
                            if (t - lastSnap >= 5) { // simpan paling sering tiap 5 detik
                                lastSnap = t
                                cache.put(key("#devices"), raw, t)
                            }
                            val p = pending.value
                            if (p.isNotEmpty()) {
                                // selesai bila status perangkat sudah berubah dari saat diketuk
                                pending.value = p.filter { (id, st) -> list.firstOrNull { it.id == id }?.state == st }
                            }
                        })
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        // putus: coba lagi di bawah
                    }
                    live.value = false
                    if (fresh.value) {
                        snapshot() // simpan data terakhir sebelum dianggap basi
                        fresh.value = false
                    }
                    delay(3_000)
                }
            },
            viewModelScope.launch {
                // sinkron riwayat tiap kali tersambung, lalu tiap 30 detik selama tersambung
                live.collectLatest { on ->
                    if (on) {
                        val plain = DashboardApi(baseUrl.value)
                        while (isActive) {
                            syncOnce(plain)
                            delay(30_000)
                        }
                    }
                }
            },
            viewModelScope.launch {
                while (isActive) {
                    try {
                        energy.value = api.energy()
                    } catch (e: CancellationException) {
                        throw e
                    } catch (e: Exception) {
                        // dashboard tanpa data energi / belum tersambung
                    }
                    delay(60_000)
                }
            },
        )
    }

    /** Tarik kejadian baru dari dashboard (bertahap, mulai sedikit sebelum kejadian terakhir yang tersimpan). */
    private suspend fun syncOnce(api: DashboardApi) {
        try {
            var since = maxOf(0L, withContext(Dispatchers.IO) { db.maxT() } - 600)
            var first = true
            var pages = 0
            while (pages++ < 200) {
                val j = api.syncEvents(since, 2000)
                withContext(Dispatchers.IO) { db.apply(j, if (first) since else -1L) }
                first = false
                if (j.optBoolean("more")) {
                    val nx = j.optLong("next")
                    if (nx < since) break
                    since = nx + 1 // semua kejadian dengan waktu nx sudah termasuk di halaman ini
                } else {
                    val now = j.optLong("now", System.currentTimeMillis() / 1000)
                    withContext(Dispatchers.IO) { db.setMeta("sync_at", now.toString()) }
                    syncedAt.value = now
                    break
                }
            }
            histTick.value++
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // dashboard belum terjangkau: pakai data lokal yang ada
        }
    }

    /**
     * Tarik-untuk-muat-ulang: tutup lalu buka lagi sambungan ke dashboard (berguna saat VPN baru dinyalakan),
     * tunggu sampai tersambung (maks 12 detik), lalu minta layar memuat ulang datanya.
     *
     * Perbaikan bug: live.value dipastikan false dulu sebelum menunggu transisi false→true,
     * sehingga timeout tidak langsung terlewati jika live sebelumnya sudah true.
     */
    fun refresh() {
        if (refreshing.value) return
        refreshing.value = true
        // Reset state agar live pasti false sebelum start(), sehingga penantian
        // live.first { it } benar-benar menunggu koneksi baru — bukan nilai lama.
        fresh.value = false
        live.value = false
        start() // memanggil stop() di dalam, lalu memulai job-job baru
        viewModelScope.launch {
            // Tunggu live menjadi true (koneksi SSE berhasil), maks 12 detik.
            val ok = withTimeoutOrNull(12_000) { live.first { it } } != null
            if (ok) {
                delay(500) // beri sedikit waktu agar paket iot pertama masuk
            } else {
                messages.tryEmit("Belum tersambung ke dashboard. Cek VPN / Wi-Fi.")
            }
            refreshTick.value++
            histTick.value++
            refreshing.value = false
        }
    }

    /** Riwayat ON/OFF dari database lokal; bila kosong dan tersambung, ambil langsung dari dashboard. */
    suspend fun onoffHist(id: String, hours: Int): OnOffHist {
        val now = if (fresh.value) System.currentTimeMillis() / 1000 else syncedAt.value
        val from = now - hours * 3600L
        val (pre, evs) = withContext(Dispatchers.IO) { db.onoffEvents(id, from) }
        if (now > 0 && (pre != null || evs.isNotEmpty())) return buildOnOffHist(pre, evs, from, now)
        if (fresh.value) return api().onoff(id, hours)
        throw ApiException("Belum ada data lokal. Sambungkan ke dashboard sekali untuk sinkron.")
    }

    /** Log status daya (AC) dari database lokal; bila kosong dan tersambung, ambil dari dashboard. */
    suspend fun powerLog(id: String, hours: Int): List<PowerEvent> {
        val now = if (fresh.value) System.currentTimeMillis() / 1000 else syncedAt.value
        val evs = withContext(Dispatchers.IO) { db.powerEvents(id, now - hours * 3600L) }
        if (now > 0 && evs.isNotEmpty()) return evs
        if (fresh.value) return api().power(id, hours).events
        throw ApiException("Belum ada data lokal. Sambungkan ke dashboard sekali untuk sinkron.")
    }

    /** Hentikan sambungan saat app tidak terlihat (hemat baterai). */
    fun stop() {
        snapshot()
        jobs.forEach { it.cancel() }
        jobs = emptyList()
        live.value = false
        pending.value = emptyMap()
    }

    fun setUrl(raw: String) {
        var u = raw.trim().trimEnd('/')
        if (u.isEmpty()) u = DEFAULT_URL
        if (!u.startsWith("http://") && !u.startsWith("https://")) u = "http://$u"
        prefs.edit().putString("url", u).apply()
        baseUrl.value = u
        db.bindUrl(u)
        syncedAt.value = 0L
        histTick.value++
        devices.value = emptyList()
        energy.value = null
        fresh.value = false
        devicesAt.value = 0L
        cachedAt.value = 0L
        lastRaw = null
        restore()
        start()
    }

    fun toggle(d: Device) {
        if (!fresh.value) {
            messages.tryEmit("Tidak terhubung ke dashboard")
            return
        }
        if (pending.value.containsKey(d.id)) return
        pending.value = pending.value + (d.id to d.state)
        val api = DashboardApi(baseUrl.value)
        viewModelScope.launch {
            try {
                api.toggle(d.id)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                messages.tryEmit("Gagal toggle ${d.name}: ${e.message ?: "kesalahan"}")
                pending.value = pending.value - d.id
                return@launch
            }
            delay(5_000) // batas tunggu bila status baru tidak sampai
            pending.value = pending.value - d.id
        }
    }

    fun api() = DashboardApi(baseUrl.value, cache) { cachedAt.value = it }

    /** Ubah pengaturan perangkat; [onDone] dipanggil di thread utama dengan hasilnya. */
    fun configure(
        d: Device,
        startup: String? = null,
        pulse: String? = null,
        pulseWidth: Int? = null,
        led: String? = null,
        onDone: (Boolean) -> Unit,
    ) {
        if (!fresh.value) {
            messages.tryEmit("Tidak terhubung ke dashboard")
            onDone(false)
            return
        }
        viewModelScope.launch {
            var ok = true
            try {
                api().configure(d.id, startup, pulse, pulseWidth, led)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                ok = false
                messages.tryEmit("Gagal mengubah pengaturan ${d.name}: ${e.message ?: "kesalahan"}")
            }
            onDone(ok)
        }
    }

    /** Simpan estimasi energi perangkat, lalu muat ulang laporan energi. */
    fun saveEnergyCfg(d: Device, watt: Double, standbyW: Double, note: String, onDone: (Boolean) -> Unit) {
        if (!fresh.value) {
            messages.tryEmit("Tidak terhubung ke dashboard")
            onDone(false)
            return
        }
        viewModelScope.launch {
            var ok = true
            try {
                api().setEnergyCfg(d.id, watt, standbyW, note)
                energy.value = api().energy()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                ok = false
                messages.tryEmit("Gagal menyimpan estimasi ${d.name}: ${e.message ?: "kesalahan"}")
            }
            onDone(ok)
        }
    }

    override fun onCleared() {
        snapshot()
        stop()
    }
}
