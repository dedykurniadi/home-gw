package app.iotdashboard

import java.io.File
import java.security.MessageDigest

/**
 * Cache snapshot: jawaban GET terakhir yang berhasil dari dashboard, disimpan per kunci (alamat + path)
 * beserta waktu pengambilannya. Dipakai saat dashboard tidak terjangkau supaya data masih bisa dilihat.
 * Satu berkas per kunci; ditulis lewat berkas sementara supaya tidak pernah setengah tertulis.
 */
class Cache(private val dir: File, private val maxFiles: Int = 120) {
    class Entry(val t: Long, val body: String)

    private fun file(key: String): File {
        val h = MessageDigest.getInstance("SHA-1").digest(key.toByteArray())
            .joinToString("") { "%02x".format(it) }.take(24)
        return File(dir, "$h.snap")
    }

    @Synchronized
    fun put(key: String, body: String, tSec: Long = System.currentTimeMillis() / 1000) {
        try {
            dir.mkdirs()
            val f = file(key)
            val tmp = File(dir, f.name + ".tmp")
            tmp.writeText(tSec.toString() + "\n" + body)
            if (!tmp.renameTo(f)) {
                f.delete()
                if (!tmp.renameTo(f)) tmp.delete()
            }
            prune()
        } catch (_: Exception) {
            // cache hanya bonus: gagal menulis tidak boleh mengganggu app
        }
    }

    @Synchronized
    fun get(key: String): Entry? = try {
        val f = file(key)
        if (!f.exists()) {
            null
        } else {
            val s = f.readText()
            val i = s.indexOf('\n')
            if (i <= 0) null else Entry(s.substring(0, i).toLong(), s.substring(i + 1))
        }
    } catch (_: Exception) {
        null
    }

    @Synchronized
    fun clear() {
        dir.listFiles()?.forEach { it.delete() }
    }

    /** Jaga jumlah berkas: buang yang paling lama tidak diperbarui. */
    private fun prune() {
        val all = dir.listFiles { x -> x.name.endsWith(".snap") } ?: return
        if (all.size <= maxFiles) return
        all.sortedBy { it.lastModified() }.take(all.size - maxFiles).forEach { it.delete() }
    }
}
