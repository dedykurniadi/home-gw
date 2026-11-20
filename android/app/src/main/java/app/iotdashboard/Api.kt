package app.iotdashboard

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/** Satu perangkat IoT (bentuk sama dengan State di dashboard). */
data class Device(
    val id: String,
    val name: String,
    val state: String, // on | off | unknown
    val online: Boolean,
    val kind: String,
    val power: Double?, // watt (POWR316D)
    val rssi: Int?, // dBm
    val since: Long, // unix detik, sejak kapan status ini (0 = tidak tahu)
    val ip: String = "",
    val mac: String = "",
    val fw: String = "",
    val ssid: String = "",
    val startup: String = "", // on | off | stay
    val pulse: String = "", // on | off (inching)
    val pulseWidth: Int = 0, // ms
    val led: String = "", // on | off (Network Indicator)
    val lastSeen: Long = 0,
    val configurable: Boolean = false,
    val voltage: Double? = null,
    val current: Double? = null,
)

/** Satu periode pemakaian listrik. unknownSec = waktu tidak diketahui (tidak dihitung). */
data class PeriodE(val kwh: Double, val cost: Double, val unknownSec: Long)

data class DeviceEnergy(
    val basis: String, // estimate | measured | none
    val watt: Double,
    val standbyW: Double = 0.0, // konsumsi modul Sonoff sendiri
    val note: String = "",
    val today: PeriodE?,
    val week: PeriodE?,
    val month: PeriodE?,
)

/** Estimasi pemakaian listrik. */
data class Energy(
    val totalKwh: Double,
    val totalCost: Double,
    val tariff: Double,
    val devices: Map<String, DeviceEnergy>,
)

data class Seg(val t0: Long, val t1: Long, val state: String)
data class OnOffEvent(val t: Long, val state: String, val src: String = "")

/** Riwayat ON/OFF/tidak terdengar satu perangkat. */
data class OnOffHist(
    val now: Long,
    val from: Long,
    val segs: List<Seg>,
    val events: List<OnOffEvent>,
    val onSec: Long,
    val offSec: Long,
    val gapSec: Long,
    val toggles: Int,
)

data class PowerEvent(val t: Long, val state: String, val w: Double, val prev: String, val dur: Long, val kwh: Double)

/** Riwayat daya perangkat berdaya (POWR316D). minutes: [t, rata-rata W, min, maks]. */
data class PowerHist(
    val now: Long,
    val from: Long,
    val minutes: List<DoubleArray>,
    val segs: List<Seg>,
    val events: List<PowerEvent>,
    val compSec: Long,
    val fanSec: Long,
    val cycles: Int,
    val kwh: Double,
    val maxW: Double,
    val compOnW: Double,
)

class ApiException(message: String) : IOException(message)

/** Klien untuk API dashboard di router (dashboard berperan sebagai gateway ke perangkat). */
class DashboardApi(
    baseUrl: String,
    private val cache: Cache? = null,
    /** Dipanggil tiap GET: 0 = data langsung dari dashboard, >0 = dari simpanan (waktu ambil, unix detik). */
    private val onSource: (Long) -> Unit = {},
) {
    private val base = baseUrl.trim().trimEnd('/')

    private fun open(path: String, method: String, readTimeoutMs: Int): HttpURLConnection {
        val c = URL(base + path).openConnection() as HttpURLConnection
        c.requestMethod = method
        c.connectTimeout = 5_000
        c.readTimeout = readTimeoutMs
        c.setRequestProperty("X-HomeGW", "1")
        c.setRequestProperty("X-Dashboard", "1")
        return c
    }

    private fun errorText(c: HttpURLConnection, code: Int): String {
        val body = try {
            c.errorStream?.bufferedReader()?.use { it.readText() }
        } catch (_: Exception) {
            null
        }
        val msg = try {
            if (body != null) JSONObject(body).optString("error", "") else ""
        } catch (_: Exception) {
            ""
        }
        return if (msg.isNotEmpty()) msg else "HTTP $code"
    }

    /** Ketuk dua kali pada kartu = toggle. */
    suspend fun toggle(id: String) = withContext(Dispatchers.IO) {
        val c = open("/api/iot/" + URLEncoder.encode(id, "UTF-8"), "POST", 10_000)
        try {
            c.doOutput = true
            c.setRequestProperty("Content-Type", "application/json")
            c.outputStream.use { it.write("{\"cmd\":\"toggle\"}".toByteArray()) }
            val code = c.responseCode
            if (code != 200) throw ApiException(errorText(c, code))
        } finally {
            c.disconnect()
        }
    }

    suspend fun compare(): Compare = withContext(Dispatchers.IO) { parseCompare(getJson("/api/energy/compare")) }

    suspend fun monthly(months: Int): Monthly = withContext(Dispatchers.IO) { parseMonthly(getJson("/api/energy/monthly?months=$months")) }

    suspend fun sched(): Sched = withContext(Dispatchers.IO) { parseSched(getJson("/api/sched")) }

    /** Buat (id kosong) atau ganti scene. */
    suspend fun saveScene(r: Scene) = withContext(Dispatchers.IO) {
        val create = r.id.isEmpty()
        val c = open(if (create) "/api/sched" else "/api/sched/" + URLEncoder.encode(r.id, "UTF-8"), if (create) "POST" else "PUT", 10_000)
        try {
            c.doOutput = true
            c.setRequestProperty("Content-Type", "application/json")
            c.outputStream.use { it.write(r.toJson().toString().toByteArray()) }
            val code = c.responseCode
            if (code != 200) throw ApiException(errorText(c, code))
        } finally {
            c.disconnect()
        }
    }

    /** Simpan estimasi energi satu perangkat (daya beban, konsumsi modul Sonoff, catatan). */
    suspend fun setEnergyCfg(id: String, watt: Double, standbyW: Double, note: String) = withContext(Dispatchers.IO) {
        val body = JSONObject().put("watt", watt).put("standby_watt", standbyW).put("note", note)
        val c = open("/api/energy/cfg/" + URLEncoder.encode(id, "UTF-8"), "PUT", 10_000)
        try {
            c.doOutput = true
            c.setRequestProperty("Content-Type", "application/json")
            c.outputStream.use { it.write(body.toString().toByteArray()) }
            val code = c.responseCode
            if (code != 200) throw ApiException(errorText(c, code))
        } finally {
            c.disconnect()
        }
    }

    suspend fun deleteScene(id: String) = withContext(Dispatchers.IO) {
        val c = open("/api/sched/" + URLEncoder.encode(id, "UTF-8"), "DELETE", 10_000)
        try {
            val code = c.responseCode
            if (code != 200) throw ApiException(errorText(c, code))
        } finally {
            c.disconnect()
        }
    }

    /**
     * GET JSON. Berhasil: hasilnya disimpan ke cache. Gagal tersambung (dashboard mati/di luar jangkauan):
     * pakai simpanan terakhir bila ada. Balasan error dari dashboard (HTTP 4xx/5xx) tidak diganti cache.
     */
    private fun getJson(path: String): JSONObject {
        try {
            val c = open(path, "GET", 10_000)
            try {
                val code = c.responseCode
                if (code != 200) throw ApiException(errorText(c, code))
                val text = c.inputStream.bufferedReader().use { it.readText() }
                val j = JSONObject(text)
                cache?.put(base + path, text)
                onSource(0)
                return j
            } finally {
                c.disconnect()
            }
        } catch (e: ApiException) {
            throw e
        } catch (e: IOException) {
            val hit = cache?.get(base + path)
            if (hit != null) {
                try {
                    val j = JSONObject(hit.body)
                    onSource(hit.t)
                    return j
                } catch (_: org.json.JSONException) {
                    // simpanan rusak: teruskan kesalahan koneksi
                }
            }
            throw e
        }
    }

    suspend fun onoff(id: String, hours: Int): OnOffHist = withContext(Dispatchers.IO) {
        parseOnOff(getJson("/api/onoff/" + URLEncoder.encode(id, "UTF-8") + "?hours=$hours"))
    }

    /** Tarik kejadian ON/OFF dan daya sejak [since] (satu halaman); lihat /api/sync/events di dashboard. */
    suspend fun syncEvents(since: Long, limit: Int): JSONObject = withContext(Dispatchers.IO) {
        getJson("/api/sync/events?since=$since&limit=$limit")
    }

    suspend fun power(id: String, hours: Int): PowerHist = withContext(Dispatchers.IO) {
        parsePower(getJson("/api/power/" + URLEncoder.encode(id, "UTF-8") + "?hours=$hours"))
    }

    /** Ubah pengaturan perangkat (hanya Sonoff Mini). Isi hanya yang diubah. */
    suspend fun configure(
        id: String,
        startup: String? = null,
        pulse: String? = null,
        pulseWidth: Int? = null,
        led: String? = null,
    ) = withContext(Dispatchers.IO) {
        val body = JSONObject()
        if (startup != null) body.put("startup", startup)
        if (pulse != null) body.put("pulse", pulse)
        if (pulseWidth != null) body.put("pulse_width", pulseWidth)
        if (led != null) body.put("led", led)
        val c = open("/api/iot/" + URLEncoder.encode(id, "UTF-8") + "/config", "POST", 15_000)
        try {
            c.doOutput = true
            c.setRequestProperty("Content-Type", "application/json")
            c.outputStream.use { it.write(body.toString().toByteArray()) }
            val code = c.responseCode
            if (code != 200) throw ApiException(errorText(c, code))
        } finally {
            c.disconnect()
        }
    }

    suspend fun energy(): Energy = withContext(Dispatchers.IO) { parseEnergy(getJson("/api/energy")) }

    /**
     * Mendengarkan aliran SSE /api/events. Memanggil [onLive] saat tersambung dan [onDevices] untuk
     * tiap event "iot". Fungsi ini hanya selesai dengan melempar exception (koneksi putus) atau
     * dibatalkan.
     */
    suspend fun streamIot(onLive: () -> Unit, onDevices: (List<Device>, String) -> Unit): Unit = coroutineScope {
        val c = open("/api/events", "GET", 45_000) // dashboard mengirim detak tiap 15 detik
        c.setRequestProperty("Accept", "text/event-stream")
        // Membaca baris bersifat blocking: saat dibatalkan, tutup koneksi agar pembacaan berhenti.
        val watcher = launch(Dispatchers.IO) {
            try {
                awaitCancellation()
            } finally {
                c.disconnect()
            }
        }
        try {
            withContext(Dispatchers.IO) {
                val code = c.responseCode
                if (code != 200) throw ApiException(errorText(c, code))
                onLive()
                val r = c.inputStream.bufferedReader() // tanpa use{}: close() akan menguras aliran tak berujung
                var ev = ""
                val data = StringBuilder()
                while (true) {
                    val line = r.readLine() ?: break
                    when {
                        line.isEmpty() -> {
                            if (ev == "iot" && data.isNotEmpty()) {
                                try {
                                    val raw = data.toString()
                                    onDevices(parseDevices(JSONArray(raw)), raw)
                                } catch (_: org.json.JSONException) {
                                    // paket rusak: lewati
                                }
                            }
                            ev = ""
                            data.setLength(0)
                        }
                        line.startsWith("event:") -> ev = line.substring(6).trim()
                        line.startsWith("data:") -> {
                            if (data.isNotEmpty()) data.append('\n')
                            data.append(line.substring(5).trim())
                        }
                    }
                }
            }
            throw ApiException("koneksi ditutup")
        } finally {
            watcher.cancel()
            c.disconnect()
        }
    }
}

private fun JSONObject.optIntOrNull(key: String): Int? = if (has(key) && !isNull(key)) getInt(key) else null
private fun JSONObject.optDoubleOrNull(key: String): Double? = if (has(key) && !isNull(key)) getDouble(key) else null

fun parseDevices(arr: JSONArray): List<Device> = (0 until arr.length()).map { i ->
    val o = arr.getJSONObject(i)
    Device(
        id = o.getString("id"),
        name = o.optString("name", ""),
        state = o.optString("state", "unknown"),
        online = o.optBoolean("online", false),
        kind = o.optString("kind", "switch"),
        power = o.optDoubleOrNull("power"),
        rssi = o.optIntOrNull("rssi"),
        since = o.optLong("since", 0L),
        ip = o.optString("ip", ""),
        mac = o.optString("mac", ""),
        fw = o.optString("fw", ""),
        ssid = o.optString("ssid", ""),
        startup = o.optString("startup", ""),
        pulse = o.optString("pulse", ""),
        pulseWidth = o.optIntOrNull("pulse_width") ?: 0,
        led = o.optString("led", ""),
        lastSeen = o.optLong("last_seen", 0L),
        configurable = o.optBoolean("configurable", false),
        voltage = o.optDoubleOrNull("voltage"),
        current = o.optDoubleOrNull("current"),
    )
}

private fun parsePeriod(o: JSONObject?): PeriodE? =
    if (o == null) null else PeriodE(o.optDouble("kwh", 0.0), o.optDouble("cost", 0.0), o.optLong("unknown_sec", 0L))

fun parseEnergy(o: JSONObject): Energy {
    val today = o.optJSONObject("total")?.optJSONObject("today")
    val per = HashMap<String, DeviceEnergy>()
    val ds = o.optJSONObject("devices")
    if (ds != null) {
        val keys = ds.keys()
        while (keys.hasNext()) {
            val k = keys.next()
            val r = ds.optJSONObject(k) ?: continue
            per[k] = DeviceEnergy(
                basis = r.optString("basis", "none"),
                watt = r.optDouble("watt", 0.0),
                standbyW = r.optDouble("standby_watt", 0.0),
                note = r.optString("note", ""),
                today = parsePeriod(r.optJSONObject("today")),
                week = parsePeriod(r.optJSONObject("week")),
                month = parsePeriod(r.optJSONObject("month")),
            )
        }
    }
    return Energy(
        totalKwh = today?.optDouble("kwh", 0.0) ?: 0.0,
        totalCost = today?.optDouble("cost", 0.0) ?: 0.0,
        tariff = o.optDouble("tariff", 0.0),
        devices = per,
    )
}

private fun parseSegs(a: JSONArray?): List<Seg> {
    if (a == null) return emptyList()
    return (0 until a.length()).map { i ->
        val o = a.getJSONObject(i)
        Seg(o.getLong("t0"), o.getLong("t1"), o.optString("state", ""))
    }
}

fun parseOnOff(o: JSONObject): OnOffHist {
    val ev = o.optJSONArray("events")
    val st = o.optJSONObject("stats")
    return OnOffHist(
        now = o.optLong("now", 0L),
        from = o.optLong("from", 0L),
        segs = parseSegs(o.optJSONArray("segs")),
        events = if (ev == null) emptyList() else (0 until ev.length()).map { i ->
            val e = ev.getJSONObject(i)
            OnOffEvent(e.getLong("t"), e.optString("state", ""), e.optString("src", ""))
        },
        onSec = st?.optLong("on_sec", 0L) ?: 0L,
        offSec = st?.optLong("off_sec", 0L) ?: 0L,
        gapSec = st?.optLong("gap_sec", 0L) ?: 0L,
        toggles = st?.optInt("toggles", 0) ?: 0,
    )
}

fun parsePower(o: JSONObject): PowerHist {
    val mins = o.optJSONArray("minutes")
    val ev = o.optJSONArray("events")
    val st = o.optJSONObject("stats")
    val live = o.optJSONObject("live")
    return PowerHist(
        now = o.optLong("now", 0L),
        from = o.optLong("from", 0L),
        minutes = if (mins == null) emptyList() else (0 until mins.length()).map { i ->
            val r = mins.getJSONArray(i)
            DoubleArray(r.length()) { k -> r.getDouble(k) }
        },
        segs = parseSegs(o.optJSONArray("segs")),
        events = if (ev == null) emptyList() else (0 until ev.length()).map { i ->
            val e = ev.getJSONObject(i)
            PowerEvent(
                e.getLong("t"), e.optString("state", ""), e.optDouble("w", 0.0),
                e.optString("prev", ""), e.optLong("dur", 0L), e.optDouble("kwh", 0.0),
            )
        },
        compSec = st?.optLong("comp_sec", 0L) ?: 0L,
        fanSec = st?.optLong("fan_sec", 0L) ?: 0L,
        cycles = st?.optInt("cycles", 0) ?: 0,
        kwh = st?.optDouble("kwh", 0.0) ?: 0.0,
        maxW = st?.optDouble("max_w", 0.0) ?: 0.0,
        compOnW = live?.optDouble("comp_on_w", 300.0) ?: 300.0,
    )
}

/** Satu pemicu scene: type = time | sunrise | sunset (at "HH:MM" untuk time, offset menit untuk matahari). */
data class Trig(val type: String, val at: String = "18:00", val offset: Int = 0)

/** Satu aksi scene: action = on | off. */
data class Act(val device: String, val action: String)

/** Scene otomatis di dashboard: bila pemicunya tiba, semua aksi dijalankan (satu pemicu per scene). days: 0=Min..6=Sab, kosong = setiap hari. */
data class Scene(
    val id: String = "",
    val name: String = "",
    val label: String = "",
    val triggers: List<Trig>,
    val actions: List<Act>,
    val days: List<Int> = emptyList(),
    val enabled: Boolean = true,
    val next: Long = 0,
    val lastOk: Long = 0, // terakhir berhasil dijalankan (unix, 0 = belum pernah)
    val lastFail: Long = 0, // terakhir gagal
) {
    fun toJson(): JSONObject {
        val o = JSONObject()
        o.put("name", name)
        val ta = JSONArray()
        triggers.forEach {
            val t = JSONObject()
            t.put("type", it.type)
            if (it.type == "time") t.put("at", it.at) else t.put("offset", it.offset)
            ta.put(t)
        }
        o.put("triggers", ta)
        val aa = JSONArray()
        actions.forEach {
            val a = JSONObject()
            a.put("device", it.device)
            a.put("action", it.action)
            aa.put(a)
        }
        o.put("actions", aa)
        val da = JSONArray()
        days.forEach { da.put(it) }
        o.put("days", da)
        o.put("enabled", enabled)
        return o
    }
}

/** Perangkat yang gagal pada satu eksekusi scene. */
data class RunFail(val device: String, val name: String, val err: String)

/** Satu baris log scene: t = waktu selesai, fire = waktu terjadwal. */
data class SchedRun(
    val t: Long,
    val fire: Long,
    val scene: String,
    val name: String = "",
    val label: String,
    val ok: Boolean,
    val err: String,
    val fails: List<RunFail>,
)

data class Sched(val hasLocation: Boolean, val sunrise: Long, val sunset: Long, val scenes: List<Scene>, val runs: List<SchedRun>)

fun parseSched(o: JSONObject): Sched {
    val scenes = ArrayList<Scene>()
    val sa = o.optJSONArray("scenes")
    if (sa != null) for (i in 0 until sa.length()) {
        val r = sa.getJSONObject(i)
        val days = ArrayList<Int>()
        val da = r.optJSONArray("days")
        if (da != null) for (j in 0 until da.length()) days.add(da.getInt(j))
        val trigs = ArrayList<Trig>()
        val ta = r.optJSONArray("triggers")
        if (ta != null) for (j in 0 until ta.length()) {
            val t = ta.getJSONObject(j)
            trigs.add(Trig(t.getString("type"), t.optString("at", "18:00"), t.optInt("offset", 0)))
        }
        val acts = ArrayList<Act>()
        val aa = r.optJSONArray("actions")
        if (aa != null) for (j in 0 until aa.length()) {
            val a = aa.getJSONObject(j)
            acts.add(Act(a.getString("device"), a.getString("action")))
        }
        scenes.add(
            Scene(
                id = r.getString("id"),
                name = r.optString("name", ""),
                label = r.optString("label", ""),
                triggers = trigs,
                actions = acts,
                days = days,
                enabled = r.optBoolean("enabled", true),
                next = r.optLong("next", 0),
                lastOk = r.optLong("last_ok", 0),
                lastFail = r.optLong("last_fail", 0),
            ),
        )
    }
    val runs = ArrayList<SchedRun>()
    val xa = o.optJSONArray("runs")
    if (xa != null) for (i in 0 until xa.length()) {
        val r = xa.getJSONObject(i)
        val fails = ArrayList<RunFail>()
        val fa = r.optJSONArray("fails")
        if (fa != null) for (j in 0 until fa.length()) {
            val f = fa.getJSONObject(j)
            fails.add(RunFail(f.optString("device", ""), f.optString("name", ""), f.optString("err", "")))
        }
        runs.add(
            SchedRun(
                t = r.getLong("t"),
                fire = r.optLong("fire", 0),
                scene = r.optString("scene", ""),
                name = r.optString("name", ""),
                label = r.optString("label", ""),
                ok = r.optBoolean("ok", false),
                err = r.optString("err", ""),
                fails = fails,
            ),
        )
    }
    val sun = o.optJSONObject("sun")
    return Sched(o.optBoolean("has_location", false), sun?.optLong("sunrise", 0L) ?: 0L, sun?.optLong("sunset", 0L) ?: 0L, scenes, runs)
}

/** Rekap energi satu perangkat pada satu bulan. basis: estimate | measured | none. */
data class MonthDev(val basis: String, val kwh: Double, val cost: Double, val unknownSec: Long, val days: Int, val known: Boolean)

data class MonthE(val key: String, val partial: Boolean, val kwh: Double, val cost: Double, val devices: Map<String, MonthDev>)

data class Monthly(val tariff: Double, val months: List<MonthE>)

fun parseMonthly(o: JSONObject): Monthly {
    val months = ArrayList<MonthE>()
    val ma = o.optJSONArray("months")
    if (ma != null) for (i in 0 until ma.length()) {
        val m = ma.getJSONObject(i)
        val devs = LinkedHashMap<String, MonthDev>()
        val dj = m.optJSONObject("devices")
        if (dj != null) {
            val it = dj.keys()
            while (it.hasNext()) {
                val id = it.next()
                val d = dj.getJSONObject(id)
                devs[id] = MonthDev(
                    d.optString("basis", "none"),
                    d.optDouble("kwh", 0.0),
                    d.optDouble("cost", 0.0),
                    d.optLong("unknown_sec", 0L),
                    d.optInt("days", 0),
                    d.optBoolean("known", false),
                )
            }
        }
        val t = m.optJSONObject("total")
        months.add(MonthE(m.getString("key"), m.optBoolean("partial", false), t?.optDouble("kwh", 0.0) ?: 0.0, t?.optDouble("cost", 0.0) ?: 0.0, devs))
    }
    return Monthly(o.optDouble("tariff", 0.0), months)
}

/** Perbandingan periode per perangkat. Periode: today, yesterday, week, last_week, month, last_month (minggu mulai Senin). */
data class CmpDev(val basis: String, val periods: Map<String, MonthDev>)

data class Compare(val tariff: Double, val devices: Map<String, CmpDev>) {
    /** Jumlah kWh dan Rp satu periode; filter = id perangkat, atau null untuk semua. */
    fun sum(period: String, filter: String?): Pair<Double, Double> {
        var kwh = 0.0
        var cost = 0.0
        for (e in devices.entries) {
            if (filter != null && e.key != filter) continue
            val p = e.value.periods[period] ?: continue
            if (p.known) {
                kwh += p.kwh
                cost += p.cost
            }
        }
        return Pair(kwh, cost)
    }
}

fun parseCompare(o: JSONObject): Compare {
    val devs = LinkedHashMap<String, CmpDev>()
    val dj = o.optJSONObject("devices")
    if (dj != null) {
        val ids = dj.keys()
        while (ids.hasNext()) {
            val id = ids.next()
            val d = dj.getJSONObject(id)
            val ps = LinkedHashMap<String, MonthDev>()
            val pj = d.optJSONObject("periods")
            if (pj != null) {
                val ks = pj.keys()
                while (ks.hasNext()) {
                    val k = ks.next()
                    val p = pj.getJSONObject(k)
                    ps[k] = MonthDev(
                        p.optString("basis", "none"),
                        p.optDouble("kwh", 0.0),
                        p.optDouble("cost", 0.0),
                        p.optLong("unknown_sec", 0L),
                        p.optInt("days", 0),
                        p.optBoolean("known", false),
                    )
                }
            }
            devs[id] = CmpDev(d.optString("basis", "none"), ps)
        }
    }
    return Compare(o.optDouble("tariff", 0.0), devs)
}
