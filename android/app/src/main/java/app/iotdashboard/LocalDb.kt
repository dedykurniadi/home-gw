package app.iotdashboard

import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import org.json.JSONObject
import java.io.File

/**
 * Database lokal app: kejadian ON/OFF dan status daya yang ditarik dari dashboard (sinkron bertahap).
 * Tetap ada saat terputus, jadi riwayat/log bisa dilihat tanpa dashboard. Tidak ikut backup cloud.
 * Kunci (id perangkat, waktu) → upsert aman diulang. Berisi data satu alamat dashboard; ganti alamat = kosongkan.
 */
class LocalDb(ctx: Context) : SQLiteOpenHelper(ctx, File(ctx.noBackupFilesDir, "iot-local.db").path, null, 1) {
    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL("CREATE TABLE onoff(id TEXT NOT NULL, t INTEGER NOT NULL, state TEXT NOT NULL, src TEXT NOT NULL DEFAULT '', PRIMARY KEY(id,t))")
        db.execSQL(
            "CREATE TABLE power_ev(id TEXT NOT NULL, t INTEGER NOT NULL, state TEXT NOT NULL, w REAL NOT NULL DEFAULT 0, " +
                "prev TEXT NOT NULL DEFAULT '', dur INTEGER NOT NULL DEFAULT 0, kwh REAL NOT NULL DEFAULT 0, PRIMARY KEY(id,t))",
        )
        db.execSQL("CREATE TABLE meta(k TEXT PRIMARY KEY, v TEXT NOT NULL)")
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) {}

    @Synchronized
    fun meta(k: String): String? = readableDatabase.rawQuery("SELECT v FROM meta WHERE k=?", arrayOf(k)).use {
        if (it.moveToFirst()) it.getString(0) else null
    }

    @Synchronized
    fun setMeta(k: String, v: String) {
        val cv = ContentValues().apply { put("k", k); put("v", v) }
        writableDatabase.insertWithOnConflict("meta", null, cv, SQLiteDatabase.CONFLICT_REPLACE)
    }

    /** Kosongkan bila data berasal dari alamat dashboard lain. */
    @Synchronized
    fun bindUrl(url: String) {
        if (meta("url") == url) return
        val db = writableDatabase
        db.delete("onoff", null, null)
        db.delete("power_ev", null, null)
        db.delete("meta", null, null)
        setMeta("url", url)
    }

    /** Waktu kejadian terbaru yang tersimpan (0 = kosong). */
    @Synchronized
    fun maxT(): Long = readableDatabase.rawQuery(
        "SELECT MAX(a) FROM (SELECT MAX(t) AS a FROM onoff UNION ALL SELECT MAX(t) FROM power_ev)", null,
    ).use { if (it.moveToFirst() && !it.isNull(0)) it.getLong(0) else 0L }

    /**
     * Simpan satu halaman jawaban /api/sync/events. [replaceFrom] >= 0: hapus dulu semua kejadian sejak waktu itu
     * (dashboard membersihkan "tidak terdengar" yang singkat, jadi jendela terakhir ditarik ulang utuh).
     */
    @Synchronized
    fun apply(j: JSONObject, replaceFrom: Long) {
        val db = writableDatabase
        db.beginTransaction()
        try {
            if (replaceFrom >= 0) {
                val a = arrayOf(replaceFrom.toString())
                db.delete("onoff", "t>=?", a)
                db.delete("power_ev", "t>=?", a)
            }
            j.optJSONArray("onoff")?.let { arr ->
                for (i in 0 until arr.length()) {
                    val e = arr.getJSONObject(i)
                    val cv = ContentValues().apply {
                        put("id", e.getString("id"))
                        put("t", e.getLong("t"))
                        put("state", e.optString("state", ""))
                        put("src", e.optString("src", ""))
                    }
                    db.insertWithOnConflict("onoff", null, cv, SQLiteDatabase.CONFLICT_REPLACE)
                }
            }
            j.optJSONArray("power")?.let { arr ->
                for (i in 0 until arr.length()) {
                    val e = arr.getJSONObject(i)
                    val cv = ContentValues().apply {
                        put("id", e.getString("id"))
                        put("t", e.getLong("t"))
                        put("state", e.optString("state", ""))
                        put("w", e.optDouble("w", 0.0))
                        put("prev", e.optString("prev", ""))
                        put("dur", e.optLong("dur", 0L))
                        put("kwh", e.optDouble("kwh", 0.0))
                    }
                    db.insertWithOnConflict("power_ev", null, cv, SQLiteDatabase.CONFLICT_REPLACE)
                }
            }
            db.setTransactionSuccessful()
        } finally {
            db.endTransaction()
        }
    }

    /** Kejadian ON/OFF sejak [from] (urut naik) dan kejadian terakhir sebelum [from] (status awal), bila ada. */
    @Synchronized
    fun onoffEvents(id: String, from: Long): Pair<OnOffEvent?, List<OnOffEvent>> {
        val db = readableDatabase
        val pre = db.rawQuery("SELECT t,state,src FROM onoff WHERE id=? AND t<? ORDER BY t DESC LIMIT 1", arrayOf(id, from.toString())).use {
            if (it.moveToFirst()) OnOffEvent(it.getLong(0), it.getString(1), it.getString(2)) else null
        }
        val list = ArrayList<OnOffEvent>()
        db.rawQuery("SELECT t,state,src FROM onoff WHERE id=? AND t>=? ORDER BY t", arrayOf(id, from.toString())).use {
            while (it.moveToNext()) list.add(OnOffEvent(it.getLong(0), it.getString(1), it.getString(2)))
        }
        return pre to list
    }

    @Synchronized
    fun powerEvents(id: String, from: Long): List<PowerEvent> {
        val list = ArrayList<PowerEvent>()
        readableDatabase.rawQuery(
            "SELECT t,state,w,prev,dur,kwh FROM power_ev WHERE id=? AND t>=? ORDER BY t", arrayOf(id, from.toString()),
        ).use {
            while (it.moveToNext()) list.add(PowerEvent(it.getLong(0), it.getString(1), it.getDouble(2), it.getString(3), it.getLong(4), it.getDouble(5)))
        }
        return list
    }
}

/** Susun riwayat ON/OFF dari kejadian lokal: tiap status berlaku sampai kejadian berikutnya, yang terakhir sampai [now]. */
fun buildOnOffHist(pre: OnOffEvent?, evs: List<OnOffEvent>, from: Long, now: Long): OnOffHist {
    val pts = ArrayList<OnOffEvent>()
    if (pre != null) pts.add(OnOffEvent(from, pre.state, pre.src))
    pts.addAll(evs)
    val segs = ArrayList<Seg>()
    for (i in pts.indices) {
        val t0 = pts[i].t
        val t1 = if (i + 1 < pts.size) pts[i + 1].t else now
        if (t1 > t0) segs.add(Seg(t0, t1, pts[i].state))
    }
    return OnOffHist(now, from, segs, evs, 0, 0, 0, 0)
}
