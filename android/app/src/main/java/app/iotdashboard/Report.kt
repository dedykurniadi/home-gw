package app.iotdashboard

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.gestures.detectTapGestures
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import java.util.Locale
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.roundToInt

private val MonthNames = listOf("Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des")
private val MonthFull = listOf("Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember")

private fun monthIdx(key: String) = (key.substring(5, 7).toIntOrNull() ?: 1) - 1
private fun monthShort(key: String) = MonthNames[monthIdx(key).coerceIn(0, 11)]
private fun monthLong(key: String) = MonthFull[monthIdx(key).coerceIn(0, 11)] + " " + key.substring(0, 4)

private fun dur(sec: Long): String {
    val h = sec / 3600
    return if (h >= 24) "${h / 24} hari" else if (h >= 1) "$h jam" else "${sec / 60} menit"
}

/** Laporan energi bulanan: total, perbandingan dengan bulan lalu, dan rincian per perangkat. */
@Composable
internal fun ReportScreen(vm: MainViewModel, devices: List<Device>, onBack: () -> Unit, modifier: Modifier = Modifier) {
    var data by remember { mutableStateOf<Monthly?>(null) }
    var cmp by remember { mutableStateOf<Compare?>(null) }
    var filter by remember { mutableStateOf<String?>(null) } // null = semua perangkat
    var err by remember { mutableStateOf<String?>(null) }
    var sel by remember { mutableIntStateOf(0) } // indeks bulan; 0 = bulan berjalan

    val rt by vm.refreshTick.collectAsStateWithLifecycle()
    LaunchedEffect(rt) {
        while (true) {
            try {
                data = vm.api().monthly(6)
                cmp = vm.api().compare()
                err = null
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                err = e.message ?: "kesalahan"
            }
            delay(60_000)
        }
    }

    Column(modifier.padding(horizontal = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            Box(
                Modifier
                    .size(44.dp)
                    .clip(RoundedCornerShape(12.dp))
                    .clickable(onClick = onBack)
                    .semantics { contentDescription = "Kembali" },
                contentAlignment = Alignment.Center,
            ) {
                Icon(painterResource(R.drawable.ic_back), contentDescription = null, tint = Txt, modifier = Modifier.size(24.dp))
            }
            Text("Laporan energi", color = Txt, fontSize = 20.sp, fontWeight = FontWeight.Bold)
        }
        OfflineNotice(vm)

        Column(
            Modifier
                .weight(1f)
                .verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            val d = data
            if (err != null) Text("Gagal memuat: $err", color = Bad, fontSize = 13.sp)
            if (d == null && err == null) Text("Memuat…", color = Mut, fontSize = 13.sp)
            if (d != null && d.months.isNotEmpty()) {
                // hanya perangkat yang punya data energi (terukur / estimasi) yang bisa difilter
                val withData = devices.filter { dv -> d.months.any { mo -> mo.devices[dv.id]?.basis.let { b -> b == "measured" || b == "estimate" } } }
                val picked = filter
                val f: String? = if (picked != null && withData.any { dv -> dv.id == picked }) picked else null
                DeviceFilter(withData, f, onPick = { filter = it })
                val c = cmp
                if (c != null) CompareCard(c, f)
                val months = d.months.map { it.filtered(f) }
                val idx = sel.coerceIn(0, months.size - 1)
                val m = months[idx]
                val prev = months.getOrNull(idx + 1)
                MonthChart(months, idx, onPick = { sel = it })
                TotalCard(m, prev, d.tariff)
                DeviceList(m, devices)
                Text(
                    "Estimasi = daya (W) × lama ON dari riwayat; Terukur = meter perangkat (POWR). " +
                        "Bulan sebelum dashboard mencatat tidak ikut terhitung.",
                    color = Mut,
                    fontSize = 11.sp,
                )
            }
        }
    }
}

@Composable
private fun MonthChart(months: List<MonthE>, selected: Int, onPick: (Int) -> Unit) {
    val shape = RoundedCornerShape(16.dp)
    val ordered = months.reversed() // lama -> baru, kiri ke kanan
    val maxV = max(ordered.maxOf { it.kwh }, 0.001)
    Column(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(Panel)
            .border(1.dp, Line, shape)
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Text("6 bulan terakhir (kWh)", color = Mut, fontSize = 12.sp)
        Canvas(
            Modifier
                .fillMaxWidth()
                .height(120.dp)
                .pointerInput(ordered.size) {
                    detectTapGestures { p ->
                        val slot = size.width.toFloat() / ordered.size
                        val i = (p.x / slot).toInt().coerceIn(0, ordered.size - 1)
                        onPick(ordered.size - 1 - i)
                    }
                }
                .semantics { contentDescription = "Grafik pemakaian 6 bulan, ketuk batang untuk memilih bulan" },
        ) {
            val slot = size.width / ordered.size
            val bw = slot * 0.56f
            ordered.forEachIndexed { i, m ->
                val h = (size.height * (m.kwh / maxV)).toFloat().coerceAtLeast(2f)
                val isSel = (ordered.size - 1 - i) == selected
                drawRoundRect(
                    color = if (isSel) Acc else Color(0xFF30363D),
                    topLeft = Offset(i * slot + (slot - bw) / 2, size.height - h),
                    size = Size(bw, h),
                    cornerRadius = CornerRadius(4.dp.toPx(), 4.dp.toPx()),
                )
            }
        }
        Row(Modifier.fillMaxWidth()) {
            ordered.forEachIndexed { i, m ->
                val isSel = (ordered.size - 1 - i) == selected
                Text(
                    monthShort(m.key),
                    color = if (isSel) Txt else Mut,
                    fontSize = 11.sp,
                    fontWeight = if (isSel) FontWeight.SemiBold else FontWeight.Normal,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.weight(1f),
                )
            }
        }
    }
}

@Composable
private fun TotalCard(m: MonthE, prev: MonthE?, tariff: Double) {
    val shape = RoundedCornerShape(16.dp)
    Column(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(Panel)
            .border(1.dp, Line, shape)
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(monthLong(m.key), color = Txt, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
            if (m.partial) {
                Text(
                    "berjalan",
                    color = Warn,
                    fontSize = 11.sp,
                    fontWeight = FontWeight.SemiBold,
                    modifier = Modifier
                        .border(1.dp, Warn, RoundedCornerShape(10.dp))
                        .padding(horizontal = 8.dp, vertical = 2.dp),
                )
            }
        }
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.SpaceBetween) {
            Row(verticalAlignment = Alignment.Bottom) {
                Text(kwhText(m.kwh), color = Txt, fontSize = 34.sp, fontWeight = FontWeight.Bold)
                Text(" kWh", color = Mut, fontSize = 16.sp, modifier = Modifier.padding(bottom = 5.dp))
            }
            if (tariff > 0) Text(rupiah(m.cost), color = Acc, fontSize = 18.sp, fontWeight = FontWeight.SemiBold)
        }
        if (prev != null && prev.kwh > 0) {
            if (m.partial) {
                Text("Bulan lalu (penuh): ${kwhText(prev.kwh)} kWh" + if (tariff > 0) " · ${rupiah(prev.cost)}" else "", color = Mut, fontSize = 12.sp)
            } else {
                val diff = m.kwh - prev.kwh
                val pct = (diff / prev.kwh * 100).roundToInt()
                val up = diff > 0
                Text(
                    (if (up) "▲ " else "▼ ") + "${abs(pct)}% dibanding ${monthShort(prev.key)} (${if (up) "+" else "−"}${kwhText(abs(diff))} kWh)",
                    color = if (up) Warn else Ok,
                    fontSize = 12.sp,
                    fontWeight = FontWeight.Medium,
                )
            }
        }
    }
}

@Composable
private fun DeviceList(m: MonthE, devices: List<Device>) {
    val shape = RoundedCornerShape(16.dp)
    val rows = m.devices.entries
        .filter { it.value.known }
        .sortedByDescending { it.value.kwh }
    val maxV = max(rows.firstOrNull()?.value?.kwh ?: 0.0, 0.001)
    Column(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(Panel)
            .border(1.dp, Line, shape)
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Text("Per perangkat", color = Txt, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
        if (rows.isEmpty()) Text("Belum ada data untuk bulan ini.", color = Mut, fontSize = 13.sp)
        rows.forEach { row ->
            val id = row.key
            val v = row.value
            val name = devices.firstOrNull { d -> d.id == id }?.name ?: id
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text(name, color = Txt, fontSize = 14.sp, fontWeight = FontWeight.Medium, modifier = Modifier.weight(1f), maxLines = 1)
                    Text(kwhText(v.kwh) + " kWh", color = Txt, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
                }
                Box(
                    Modifier
                        .fillMaxWidth()
                        .height(6.dp)
                        .clip(RoundedCornerShape(3.dp))
                        .background(Line),
                ) {
                    Box(
                        Modifier
                            .fillMaxWidth((v.kwh / maxV).toFloat().coerceIn(0.01f, 1f))
                            .height(6.dp)
                            .clip(RoundedCornerShape(3.dp))
                            .background(if (v.basis == "measured") Ok else Acc),
                    )
                }
                val note = buildString {
                    append(if (v.basis == "measured") "Terukur" else "Estimasi")
                    if (v.basis == "measured" && v.days > 0) append(" · ${v.days} hari tercatat")
                    if (v.unknownSec > 3600) append(" · tak diketahui ${dur(v.unknownSec)}")
                }
                Row(Modifier.fillMaxWidth()) {
                    Text(note, color = Mut, fontSize = 11.sp, modifier = Modifier.weight(1f))
                    if (v.cost > 0) Text(rupiah(v.cost), color = Acc, fontSize = 12.sp)
                }
            }
        }
    }
}

/** Salinan bulan yang hanya memuat perangkat f (null = semua), total dihitung ulang. */
private fun MonthE.filtered(f: String?): MonthE {
    if (f == null) return this
    val dv = devices.filter { it.key == f }
    var kwh = 0.0
    var cost = 0.0
    for (e in dv.entries) if (e.value.known) {
        kwh += e.value.kwh
        cost += e.value.cost
    }
    return copy(kwh = kwh, cost = cost, devices = dv)
}

@Composable
private fun DeviceFilter(devices: List<Device>, selected: String?, onPick: (String?) -> Unit) {
    if (devices.size < 2) return
    var open by remember { mutableStateOf(false) }
    val shape = RoundedCornerShape(14.dp)
    val label = devices.firstOrNull { it.id == selected }?.name ?: "Semua perangkat"
    Box(Modifier.fillMaxWidth()) {
        Row(
            Modifier
                .fillMaxWidth()
                .heightIn(min = 48.dp)
                .clip(shape)
                .background(Panel)
                .border(1.dp, Line, shape)
                .clickable { open = true }
                .semantics { contentDescription = "Pilih perangkat: $label" }
                .padding(horizontal = 16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("Perangkat", color = Mut, fontSize = 12.sp, modifier = Modifier.padding(end = 12.dp))
            Text(label, color = Txt, fontSize = 15.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f), maxLines = 1)
            Text("▾", color = Mut, fontSize = 16.sp)
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }, modifier = Modifier.background(Panel)) {
            DropdownMenuItem(
                text = { Text("Semua perangkat", color = if (selected == null) Acc else Txt, fontWeight = if (selected == null) FontWeight.SemiBold else FontWeight.Normal) },
                onClick = {
                    onPick(null)
                    open = false
                },
            )
            devices.forEach { dv ->
                val on = dv.id == selected
                DropdownMenuItem(
                    text = { Text(dv.name, color = if (on) Acc else Txt, fontWeight = if (on) FontWeight.SemiBold else FontWeight.Normal) },
                    onClick = {
                        onPick(dv.id)
                        open = false
                    },
                )
            }
        }
    }
}

/** Hari ini vs kemarin, minggu ini (mulai Senin) vs minggu lalu, bulan ini vs bulan lalu. */
@Composable
private fun CompareCard(c: Compare, filter: String?) {
    val shape = RoundedCornerShape(16.dp)
    Column(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(Panel)
            .border(1.dp, Line, shape)
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Text("Perbandingan", color = Txt, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
        CompareRow("Hari ini", "Kemarin", c.sum("today", filter), c.sum("yesterday", filter), c.tariff)
        CompareRow("Minggu ini", "Minggu lalu", c.sum("week", filter), c.sum("last_week", filter), c.tariff)
        CompareRow("Bulan ini", "Bulan lalu", c.sum("month", filter), c.sum("last_month", filter), c.tariff)
        Text("Periode berjalan dibanding periode sebelumnya yang penuh. Minggu dimulai Senin.", color = Mut, fontSize = 11.sp)
    }
}

@Composable
private fun CompareRow(label: String, prevLabel: String, cur: Pair<Double, Double>, prev: Pair<Double, Double>, tariff: Double) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(label, color = Mut, fontSize = 12.sp)
            Row(verticalAlignment = Alignment.Bottom) {
                Text(kwhText(cur.first), color = Txt, fontSize = 22.sp, fontWeight = FontWeight.Bold)
                Text(" kWh", color = Mut, fontSize = 13.sp, modifier = Modifier.padding(bottom = 3.dp))
            }
            if (tariff > 0) Text(rupiah(cur.second), color = Acc, fontSize = 12.sp)
        }
        Column(horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text("$prevLabel ${kwhText(prev.first)} kWh", color = Mut, fontSize = 12.sp)
            if (prev.first > 0) {
                val diff = cur.first - prev.first
                val pct = (diff / prev.first * 100).roundToInt()
                val up = diff > 0
                Text(
                    (if (up) "▲ " else if (diff < 0) "▼ " else "") + "${abs(pct)}%",
                    color = if (up) Warn else Ok,
                    fontSize = 13.sp,
                    fontWeight = FontWeight.SemiBold,
                )
            }
        }
    }
}
