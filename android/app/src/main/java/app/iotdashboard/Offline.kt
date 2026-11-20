package app.iotdashboard

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale

/** Waktu data tersimpan: "14.32" bila hari ini, selain itu "12/11 14.32". */
internal fun snapTime(t: Long): String {
    if (t <= 0) return "—"
    val a = Calendar.getInstance().apply { timeInMillis = t * 1000 }
    val b = Calendar.getInstance()
    val sameDay = a.get(Calendar.YEAR) == b.get(Calendar.YEAR) && a.get(Calendar.DAY_OF_YEAR) == b.get(Calendar.DAY_OF_YEAR)
    return SimpleDateFormat(if (sameDay) "HH.mm" else "dd/MM HH.mm", Locale.US).format(Date(t * 1000))
}

/** Pita peringatan: data yang tampil berasal dari simpanan, bukan langsung dari dashboard. */
@Composable
internal fun OfflineBanner(at: Long, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(10.dp)
    Row(
        modifier
            .fillMaxWidth()
            .clip(shape)
            .background(Color(0xFF2D2208))
            .border(1.dp, Warn.copy(alpha = 0.5f), shape)
            .padding(horizontal = 12.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Box(
            Modifier
                .size(8.dp)
                .clip(CircleShape)
                .background(Warn),
        )
        Text("Tidak terhubung · data per ${snapTime(at)} · hanya baca", color = Warn, fontSize = 12.sp)
    }
}

/** Pita untuk layar di dalam (detail, laporan, scene): tampil hanya bila belum tersambung ke dashboard. */
@Composable
internal fun OfflineNotice(vm: MainViewModel) {
    val fresh by vm.fresh.collectAsStateWithLifecycle()
    val devicesAt by vm.devicesAt.collectAsStateWithLifecycle()
    val cachedAt by vm.cachedAt.collectAsStateWithLifecycle()
    if (fresh || (devicesAt == 0L && cachedAt == 0L)) return
    OfflineBanner(if (cachedAt > 0) cachedAt else devicesAt)
}
