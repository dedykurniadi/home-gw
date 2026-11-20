package app.iotdashboard

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlin.math.ceil
import kotlin.math.max
import kotlin.math.roundToInt

private val CompColor = Acc
private val FanColor = Color(0xFF2DD4BF)
private val IdleColor = Color(0xFF484F58)
private val CardBg = Panel

/** Hasil pemuatan data dari dashboard. */
internal sealed interface Loaded<out T> {
    data object Loading : Loaded<Nothing>
    data class Ok<T>(val v: T) : Loaded<T>
    data class Err(val msg: String) : Loaded<Nothing>
}

/** Memuat data dan menyegarkannya tiap [periodMs]; dimuat ulang saat [key] berubah. */
@Composable
internal fun <T> polled(key: Any, periodMs: Long, load: suspend () -> T): Loaded<T> {
    val st = produceState<Loaded<T>>(Loaded.Loading, key) {
        while (true) {
            try {
                value = Loaded.Ok(load())
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                if (value !is Loaded.Ok) value = Loaded.Err(e.message ?: "gagal memuat")
            }
            delay(periodMs)
        }
    }
    return st.value
}

private fun clock(t: Long) = SimpleDateFormat("HH.mm", Locale.US).format(Date(t * 1000))
private fun clockSec(t: Long) = SimpleDateFormat("HH.mm.ss", Locale.US).format(Date(t * 1000))
private fun dayClock(t: Long) = SimpleDateFormat("dd/MM HH.mm", Locale.US).format(Date(t * 1000))
private fun hms(t: Long) = SimpleDateFormat("HH:mm:ss", Locale.US).format(Date(t * 1000))
private fun dateKey(t: Long) = SimpleDateFormat("yyyy/MM/dd", Locale.US).format(Date(t * 1000))

private fun signalLabel(rssi: Int) = when {
    rssi > -55 -> "Sangat baik"
    rssi > -65 -> "Baik"
    rssi > -75 -> "Cukup"
    else -> "Lemah"
}

@Composable
internal fun DetailScreen(
    vm: MainViewModel,
    d: Device,
    now: Long,
    energy: Energy?,
    pending: Boolean,
    onBack: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var tab by remember { mutableIntStateOf(0) }
    val haptic = LocalHapticFeedback.current
    val fresh by vm.fresh.collectAsStateWithLifecycle()
    val on = d.state == "on" && d.online
    Column(modifier.padding(horizontal = 16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(Modifier.heightIn(min = 48.dp), verticalAlignment = Alignment.CenterVertically) {
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
            Spacer(Modifier.width(4.dp))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(d.name, color = Txt, fontSize = 20.sp, fontWeight = FontWeight.Bold, maxLines = 1)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(
                        Modifier
                            .size(8.dp)
                            .clip(CircleShape)
                            .background(if (!d.online) Bad else if (on) Ok else Color(0xFF6E7681)),
                    )
                    Spacer(Modifier.width(6.dp))
                    Text(
                        statusText(d, now),
                        color = if (!d.online) Bad else if (on) Ok else Mut,
                        fontSize = 13.sp,
                        style = androidx.compose.ui.text.TextStyle(fontFeatureSettings = "tnum"),
                        maxLines = 1,
                    )
                }
            }
            SwitchToggle(
                on = on,
                enabled = d.online && fresh,
                pending = pending,
                label = (if (on) "Matikan " else "Nyalakan ") + d.name,
                onToggle = {
                    haptic.performHapticFeedback(HapticFeedbackType.LongPress)
                    vm.toggle(d)
                },
            )
        }
        OfflineNotice(vm)

        Row(Modifier.fillMaxWidth()) {
            listOf("Usage", "Detail", "Log").forEachIndexed { i, label ->
                val sel = i == tab
                Column(
                    Modifier
                        .weight(1f)
                        .clickable(role = Role.Tab) { tab = i },
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    Box(Modifier.heightIn(min = 44.dp), contentAlignment = Alignment.Center) {
                        Text(
                            label,
                            color = if (sel) Acc else Mut,
                            fontSize = 15.sp,
                            fontWeight = if (sel) FontWeight.SemiBold else FontWeight.Medium,
                        )
                    }
                    Box(
                        Modifier
                            .fillMaxWidth()
                            .height(2.dp)
                            .background(if (sel) Acc else Line),
                    )
                }
            }
        }

        Column(
            Modifier
                .weight(1f)
                .fillMaxWidth()
                .verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            when (tab) {
                0 -> UsageTab(vm, d, energy, onOpenLog = { tab = 2 })
                1 -> DetailTab(vm, d, energy, onOpenLog = { tab = 2 })
                else -> LogTab(vm, d)
            }
            Spacer(Modifier.height(16.dp))
        }
    }
}

// ---------------------------------------------------------------------------------------------
// Komponen bersama

@Composable
internal fun Segmented(labels: List<String>, selected: Int, onSelect: (Int) -> Unit, background: Color = Panel) {
    val shape = RoundedCornerShape(12.dp)
    Row(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(background)
            .border(1.dp, Line, shape)
            .padding(4.dp),
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        labels.forEachIndexed { i, label ->
            val sel = i == selected
            Box(
                Modifier
                    .weight(1f)
                    .heightIn(min = 44.dp)
                    .clip(RoundedCornerShape(9.dp))
                    .background(if (sel) Line else Color.Transparent)
                    .clickable(role = Role.RadioButton) { onSelect(i) },
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    label,
                    color = if (sel) Txt else Mut,
                    fontSize = 14.sp,
                    fontWeight = if (sel) FontWeight.SemiBold else FontWeight.Medium,
                )
            }
        }
    }
}

@Composable
private fun CardBox(content: @Composable () -> Unit) {
    val shape = RoundedCornerShape(16.dp)
    Column(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(CardBg)
            .border(1.dp, Line, shape)
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) { content() }
}

@Composable
private fun Badge(text: String, color: Color) {
    Text(
        text,
        color = color,
        fontSize = 11.sp,
        fontWeight = FontWeight.SemiBold,
        modifier = Modifier
            .border(1.dp, color, RoundedCornerShape(10.dp))
            .padding(horizontal = 8.dp, vertical = 2.dp),
    )
}

@Composable
private fun Tile(label: String, value: String, color: Color = Txt, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(12.dp)
    Column(
        modifier
            .clip(shape)
            .background(CardBg)
            .border(1.dp, Line, shape)
            .padding(horizontal = 10.dp, vertical = 10.dp),
        verticalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        Text(label, color = Mut, fontSize = 11.sp, maxLines = 1)
        Text(value, color = color, fontSize = 16.sp, fontWeight = FontWeight.Bold, style = androidx.compose.ui.text.TextStyle(fontFeatureSettings = "tnum"), maxLines = 1)
    }
}

private fun stateColor(s: String): Color = when (s) {
    "on" -> Ok
    "off" -> TrackOff
    "comp" -> CompColor
    "fan" -> FanColor
    "idle" -> IdleColor
    else -> Bad // gap
}

/** Garis waktu berwarna per status. */
@Composable
private fun Timeline(from: Long, to: Long, segs: List<Seg>) {
    val span = max(1L, to - from).toFloat()
    Canvas(
        Modifier
            .fillMaxWidth()
            .height(30.dp)
            .clip(RoundedCornerShape(4.dp)),
    ) {
        drawRect(Color(0xFF0D1117), size = size)
        val minW = 2.dp.toPx()
        for (s in segs) {
            val x0 = ((s.t0 - from) / span * size.width).coerceIn(0f, size.width)
            val x1 = ((s.t1 - from) / span * size.width).coerceIn(0f, size.width)
            val w = max(x1 - x0, minW)
            drawRect(stateColor(s.state), topLeft = Offset(x0, 0f), size = Size(w, size.height))
        }
    }
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(if (to - from > 86400) dayClock(from) else clock(from), color = Mut, fontSize = 10.sp)
        Text("sekarang", color = Mut, fontSize = 10.sp)
    }
}

@Composable
private fun Legend(items: List<Pair<String, Color>>) {
    Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
        for ((label, c) in items) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier
                        .size(width = 12.dp, height = 10.dp)
                        .clip(RoundedCornerShape(2.dp))
                        .background(c),
                )
                Spacer(Modifier.width(6.dp))
                Text(label, color = Mut, fontSize = 12.sp)
            }
        }
    }
}

@Composable
private fun LoadState(l: Loaded<*>) {
    when (l) {
        is Loaded.Loading -> Text("Memuat…", color = Mut, fontSize = 13.sp)
        is Loaded.Err -> Text(l.msg, color = Bad, fontSize = 13.sp)
        is Loaded.Ok -> {}
    }
}

// ---------------------------------------------------------------------------------------------
// Tab Usage

@Composable
private fun UsageTab(vm: MainViewModel, d: Device, energy: Energy?, onOpenLog: () -> Unit) {
    val e = energy?.devices?.get(d.id)
    var period by remember { mutableIntStateOf(0) }
    val periods = buildList<Pair<String, PeriodE?>> {
        add("Hari ini" to e?.today)
        val wk = e?.week
        if (wk != null) add("7 hari" to wk)
        add("Bulan ini" to e?.month)
    }
    val idx = period.coerceIn(0, periods.size - 1)
    Segmented(periods.map { it.first }, idx, { period = it })

    val p = periods[idx].second
    CardBox {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.Bottom) {
            Text(
                if (p != null) kwhText(p.kwh) else "—",
                color = Txt,
                fontSize = 34.sp,
                fontWeight = FontWeight.Bold,
                style = androidx.compose.ui.text.TextStyle(fontFeatureSettings = "tnum"),
            )
            Text(" kWh", color = Mut, fontSize = 16.sp, modifier = Modifier.padding(bottom = 5.dp))
            Spacer(Modifier.weight(1f))
            if (p != null && p.cost > 0) {
                Text(rupiah(p.cost), color = Acc, fontSize = 18.sp, fontWeight = FontWeight.SemiBold, style = androidx.compose.ui.text.TextStyle(fontFeatureSettings = "tnum"))
            }
        }
        when (e?.basis) {
            "estimate" -> Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Badge("Estimasi", Warn)
                val tariffV = energy?.tariff ?: 0.0
                val tariff = if (tariffV > 0) " · tarif " + rupiah(tariffV) + "/kWh" else ""
                Text(String.format(ID, "%.0f W × lama ON", e?.watt ?: 0.0) + tariff, color = Mut, fontSize = 12.sp)
            }
            "measured" -> Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Badge("Terukur", Ok)
                Text("dari meter perangkat", color = Mut, fontSize = 12.sp)
            }
            else -> Text("Isi \"watt\" di devices.json untuk melihat estimasi.", color = Mut, fontSize = 12.sp)
        }
        if (p != null && p.unknownSec > 0) {
            Text("Waktu tidak diketahui ${coarse(p.unknownSec)} tidak dihitung.", color = Mut, fontSize = 12.sp)
        }
    }

    if (isAc(d)) PowerSection(vm, d) else OnOffSection(vm, d, onOpenLog, periods[idx].first)
}

/** Awal periode (detik): hari ini = 00:00, 7 hari = 168 jam terakhir, bulan ini = tanggal 1 pukul 00:00. */
private fun periodStart(label: String, now: Long): Long {
    if (label == "7 hari") return now - 168 * 3600L
    val c = java.util.Calendar.getInstance()
    c.timeInMillis = now * 1000
    c.set(java.util.Calendar.HOUR_OF_DAY, 0)
    c.set(java.util.Calendar.MINUTE, 0)
    c.set(java.util.Calendar.SECOND, 0)
    c.set(java.util.Calendar.MILLISECOND, 0)
    if (label == "Bulan ini") c.set(java.util.Calendar.DAY_OF_MONTH, 1)
    return c.timeInMillis / 1000
}

private class OnOffStats(val on: Long, val off: Long, val gap: Long, val toggles: Int)

/** Statistik dihitung dari segmen yang dipotong tepat di awal periode. */
private fun onoffStats(h: OnOffHist, from: Long): OnOffStats {
    var on = 0L
    var off = 0L
    var gap = 0L
    for (s in h.segs) {
        val a = maxOf(s.t0, from)
        if (s.t1 <= a) continue
        val dur = s.t1 - a
        when (s.state) {
            "on" -> on += dur
            "off" -> off += dur
            else -> gap += dur
        }
    }
    var tg = 0
    val ev = h.events
    for (i in 1 until ev.size) {
        val e = ev[i]
        val p = ev[i - 1].state
        if (e.t >= from && (e.state == "on" || e.state == "off") && (p == "on" || p == "off")) tg++
    }
    return OnOffStats(on, off, gap, tg)
}

@Composable
private fun OnOffSection(vm: MainViewModel, d: Device, onOpenLog: () -> Unit, label: String) {
    val nowSec = System.currentTimeMillis() / 1000
    val hours = (((nowSec - periodStart(label, nowSec)) + 3599) / 3600).toInt().coerceIn(1, 800)
    val rt by vm.refreshTick.collectAsStateWithLifecycle()
    val l = polled(Triple(d.id, hours, rt), 5_000L) { vm.onoffHist(d.id, hours) }
    CardBox {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text("ON / OFF · $label", color = Txt, fontSize = 14.sp, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f))
            Box(Modifier.heightIn(min = 44.dp).clickable(onClick = onOpenLog), contentAlignment = Alignment.Center) {
                Text("Lihat log", color = Acc, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
            }
        }
        LoadState(l)
        if (l is Loaded.Ok) {
            val from = maxOf(l.v.from, periodStart(label, l.v.now))
            Timeline(from, l.v.now, l.v.segs.filter { it.t1 > from }.map { if (it.t0 < from) Seg(from, it.t1, it.state) else it })
            Legend(listOf("ON" to Ok, "OFF" to TrackOff, "Tidak terdengar" to Bad))
        }
    }
    if (l is Loaded.Ok) {
        val h = l.v
        val st = onoffStats(h, maxOf(h.from, periodStart(label, h.now)))
        val known = st.on + st.off
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Tile("Total ON", coarse(st.on), modifier = Modifier.weight(1f))
            Tile("Total OFF", coarse(st.off), modifier = Modifier.weight(1f))
        }
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            Tile("Perpindahan", "${st.toggles}×", modifier = Modifier.weight(1f))
            Tile("Tidak terdengar", coarse(st.gap), color = if (st.gap > 0) Bad else Txt, modifier = Modifier.weight(1f))
        }
        if (known > 0) {
            Text("ON ${(st.on * 100.0 / known).roundToInt()}% dari waktu terpantau", color = Mut, fontSize = 12.sp)
        }
    }
}

@Composable
private fun PowerSection(vm: MainViewModel, d: Device) {
    val ranges = listOf(1, 6, 12, 24, 48)
    var ri by remember { mutableIntStateOf(3) }
    val hours = ranges[ri]
    val rt by vm.refreshTick.collectAsStateWithLifecycle()
    val l = polled(Triple(d.id, hours, rt), 30_000L) { vm.api().power(d.id, hours) }
    CardBox {
        Text("Daya ${hours} jam", color = Txt, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
        LoadState(l)
        if (l is Loaded.Ok) PowerChart(l.v)
        Segmented(ranges.map { "${it}j" }, ri, { ri = it }, background = Bg)
    }
    if (l is Loaded.Ok) {
        val h = l.v
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Tile("Kompresor", coarse(h.compSec), modifier = Modifier.weight(1f))
            Tile("Siklus", "${h.cycles}×", modifier = Modifier.weight(1f))
            Tile("Fan saja", coarse(h.fanSec), modifier = Modifier.weight(1f))
            Tile("Daya maks", "${h.maxW.roundToInt()} W", modifier = Modifier.weight(1f))
        }
    }
}

@Composable
private fun PowerChart(h: PowerHist) {
    val yMax = max(450.0, ceil(h.maxW / 50.0) * 50.0).toFloat()
    val span = max(1L, h.now - h.from).toFloat()
    Row(Modifier.fillMaxWidth().height(150.dp)) {
        Column(Modifier.width(34.dp).fillMaxSize(), verticalArrangement = Arrangement.SpaceBetween, horizontalAlignment = Alignment.End) {
            Text("${yMax.roundToInt()}", color = Mut, fontSize = 9.sp)
            Text("${(yMax / 2).roundToInt()}", color = Mut, fontSize = 9.sp)
            Text("0 W", color = Mut, fontSize = 9.sp)
        }
        Spacer(Modifier.width(4.dp))
        Canvas(Modifier.weight(1f).fillMaxSize()) {
            val w = size.width
            val hh = size.height
            fun yOf(v: Double) = hh - (v.toFloat() / yMax) * hh
            fun xOf(t: Double) = ((t - h.from) / span * w).toFloat().coerceIn(0f, w)
            for (f in listOf(0f, 0.5f, 1f)) {
                drawLine(Line, Offset(0f, hh * f), Offset(w, hh * f), strokeWidth = 1.dp.toPx())
            }
            drawLine(
                Warn.copy(alpha = 0.8f),
                Offset(0f, yOf(h.compOnW)),
                Offset(w, yOf(h.compOnW)),
                strokeWidth = 1.dp.toPx(),
                pathEffect = PathEffect.dashPathEffect(floatArrayOf(10f, 10f)),
            )
            // Garis daya; terputus bila ada selang > 150 detik (tidak ada data).
            val line = Path()
            val fill = Path()
            var lastT = -1.0
            var lastX = 0f
            var open = false
            fun closeRun() {
                if (open) {
                    fill.lineTo(lastX, hh)
                    fill.close()
                    open = false
                }
            }
            for (m in h.minutes) {
                if (m.size < 2) continue
                val t = m[0]
                val x = xOf(t)
                val y = yOf(m[1])
                if (!open || t - lastT > 150) {
                    closeRun()
                    line.moveTo(x, y)
                    fill.moveTo(x, hh)
                    fill.lineTo(x, y)
                    open = true
                } else {
                    line.lineTo(x, y)
                    fill.lineTo(x, y)
                }
                lastT = t
                lastX = x
            }
            closeRun()
            drawPath(fill, Acc.copy(alpha = 0.18f))
            drawPath(line, Acc, style = Stroke(width = 1.5.dp.toPx()))
        }
    }
    Row(Modifier.fillMaxWidth().padding(start = 38.dp), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(if (h.now - h.from > 86400) dayClock(h.from) else clock(h.from), color = Mut, fontSize = 10.sp)
        Text(clock((h.from + h.now) / 2), color = Mut, fontSize = 10.sp)
        Text(clock(h.now), color = Mut, fontSize = 10.sp)
    }
    Text("Garis putus-putus: ambang kompresor ${h.compOnW.roundToInt()} W", color = Warn, fontSize = 11.sp)
}

// ---------------------------------------------------------------------------------------------
// Tab Detail

@Composable
private fun FormRow(label: String, content: @Composable RowScope.() -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = 56.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(label, color = Txt, fontSize = 14.sp, modifier = Modifier.weight(0.42f))
        Row(
            Modifier.weight(0.58f),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) { content() }
    }
}

@Composable
private fun FormValue(v: String) {
    Text(v, color = Txt, fontSize = 14.sp, style = androidx.compose.ui.text.TextStyle(fontFeatureSettings = "tnum"))
}

/** Kolom pilihan (dropdown) bergaya eWeLink. */
@Composable
private fun DropField(text: String, enabled: Boolean, options: List<Pair<String, String>>, onPick: (String) -> Unit, modifier: Modifier = Modifier) {
    var open by remember { mutableStateOf(false) }
    val shape = RoundedCornerShape(10.dp)
    Box(modifier) {
        Row(
            Modifier
                .fillMaxWidth()
                .heightIn(min = 44.dp)
                .clip(shape)
                .background(Panel)
                .border(1.dp, Line, shape)
                .clickable(enabled = enabled) { open = true }
                .semantics { contentDescription = "Pilih: $text" }
                .padding(horizontal = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(text, color = if (enabled) Txt else Mut, fontSize = 14.sp, modifier = Modifier.weight(1f), maxLines = 1)
            Text("▾", color = Mut, fontSize = 16.sp)
        }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }, modifier = Modifier.background(Panel)) {
            options.forEach { (v, l) ->
                DropdownMenuItem(text = { Text(l, color = Txt) }, onClick = {
                    open = false
                    onPick(v)
                })
            }
        }
    }
}

private val PulseWidths = listOf(500, 1000, 2000, 3000, 5000, 10000, 30000, 60000)
private fun widthLabel(ms: Int) = if (ms < 60000) String.format(ID, "%.1f dtk", ms / 1000.0).replace(",0 dtk", " dtk") else "${ms / 60000} mnt"

@Composable
private fun DetailTab(vm: MainViewModel, d: Device, energy: Energy?, onOpenLog: () -> Unit) {
    val rt by vm.refreshTick.collectAsStateWithLifecycle()
    val scenes = polled(d.id to rt, 60_000L) { vm.api().sched() }
    CardBox {
        FormRow("Nama perangkat") { FormValue(d.name) }
        FormRow("Versi firmware") { FormValue(d.fw.ifEmpty { "-" }) }
    }
    CardBox {
        FormRow("Scene") {
            val mine = if (scenes is Loaded.Ok) scenes.v.scenes.filter { s -> s.actions.any { it.device == d.id } } else emptyList()
            if (mine.isEmpty()) {
                Text("—", color = Mut, fontSize = 14.sp)
            } else {
                Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    mine.forEach { s ->
                        val shape = RoundedCornerShape(10.dp)
                        Text(
                            s.name,
                            color = Acc,
                            fontSize = 14.sp,
                            maxLines = 1,
                            modifier = Modifier
                                .clip(shape)
                                .background(Color(0xFF0F2744))
                                .border(1.dp, Acc, shape)
                                .padding(horizontal = 14.dp, vertical = 8.dp),
                        )
                    }
                }
            }
        }
    }
    SettingsCard(vm, d, energy, onOpenLog)
    EnergyCard(vm, d, energy)
    CardBox {
        FormRow("ID perangkat") { FormValue(d.id) }
        FormRow("Model") { FormValue(if (isAc(d)) "POWR3" else "MINI") }
        FormRow("Produsen") { FormValue("SONOFF") }
        if (d.mac.isNotEmpty()) FormRow("Alamat MAC") { FormValue(d.mac.uppercase()) }
        if (d.ip.isNotEmpty()) FormRow("Alamat IP") { FormValue(d.ip) }
        if (d.ssid.isNotEmpty()) FormRow("WiFi") { FormValue(d.ssid) }
        if (d.rssi != null) {
            FormRow("Sinyal") {
                SignalBars(d.rssi)
                FormValue("${d.rssi} dBm · ${signalLabel(d.rssi)}")
            }
        }
        if (d.lastSeen > 0) FormRow("Terakhir terdengar") { FormValue(clockSec(d.lastSeen)) }
        if (d.voltage != null) FormRow("Tegangan") { FormValue(String.format(ID, "%.0f V", d.voltage)) }
        if (d.current != null) FormRow("Arus") { FormValue(String.format(ID, "%.2f A", d.current)) }
    }
}

@Composable
private fun SettingsCard(vm: MainViewModel, d: Device, energy: Energy?, onOpenLog: () -> Unit) {
    var busy by remember { mutableStateOf(false) }
    // Nilai yang baru dikirim tetap tampil sampai perangkat melaporkan nilai baru: kunci -> (baru, lama).
    var ov by remember { mutableStateOf<Map<String, Pair<String, String>>>(emptyMap()) }

    fun shown(key: String, reported: String): String {
        val o = ov[key]
        return if (o != null && o.second == reported) o.first else reported
    }

    fun send(key: String, newV: String, reported: String, startup: String? = null, pulse: String? = null, pw: Int? = null, led: String? = null) {
        if (busy) return
        busy = true
        ov = ov + (key to (newV to reported))
        vm.configure(d, startup, pulse, pw, led) { ok ->
            busy = false
            if (!ok) ov = ov - key
        }
    }

    val cfg = d.configurable
    val startup = shown("startup", d.startup)
    val pulse = shown("pulse", d.pulse)
    val led = shown("led", d.led)
    val width = shown("pw", (if (d.pulseWidth > 0) d.pulseWidth else 1000).toString()).toIntOrNull() ?: 1000
    val widths = if (width in PulseWidths) PulseWidths else (PulseWidths + width).sorted()

    CardBox {
        FormRow("Mode inching") {
            SwitchToggle(
                on = pulse == "on",
                enabled = cfg && pulse.isNotEmpty() && !busy,
                pending = false,
                label = "Inching",
                onToggle = {
                    val nv = if (pulse == "on") "off" else "on"
                    send("pulse", nv, d.pulse, pulse = nv, pw = width)
                },
            )
            DropField(
                widthLabel(width),
                cfg && pulse == "on" && !busy,
                widths.map { it.toString() to widthLabel(it) },
                { v -> send("pw", v, (if (d.pulseWidth > 0) d.pulseWidth else 1000).toString(), pulse = "on", pw = v.toInt()) },
                Modifier.weight(1f),
            )
        }
        FormRow("Status saat daya menyala") {
            val names = mapOf("on" to "Menyala (ON)", "off" to "Mati (OFF)", "stay" to "Status terakhir")
            DropField(
                names[startup] ?: "—",
                cfg && !busy,
                names.toList(),
                { v -> if (v != startup) send("startup", v, d.startup, startup = v) },
                Modifier.weight(1f),
            )
        }
        FormRow("Indikator jaringan") {
            SwitchToggle(
                on = led == "on",
                enabled = cfg && led.isNotEmpty() && !busy,
                pending = false,
                label = "Indikator jaringan",
                onToggle = {
                    val nv = if (led == "on") "off" else "on"
                    send("led", nv, d.led, led = nv)
                },
            )
        }
        FormRow("Log") {
            Text(
                "Lihat",
                color = Acc,
                fontSize = 14.sp,
                modifier = Modifier
                    .heightIn(min = 44.dp)
                    .clickable(onClick = onOpenLog)
                    .padding(vertical = 12.dp),
            )
        }
        if (!cfg) {
            Text("Pengaturan perangkat POWR belum didukung dari dashboard; nilai hanya dibaca.", color = Mut, fontSize = 12.sp)
        } else if (busy) {
            Text("mengirim…", color = Mut, fontSize = 11.sp)
        }
    }
}

@Composable
private fun EnergyCard(vm: MainViewModel, d: Device, energy: Energy?) {
    val e = energy?.devices?.get(d.id)
    val metered = isAc(d)
    fun num(v: Double) = if (v > 0) String.format(java.util.Locale.US, "%s", if (v % 1.0 == 0.0) v.toInt().toString() else v.toString()) else ""
    var watt by remember(d.id, e?.watt) { mutableStateOf(num(e?.watt ?: 0.0)) }
    var standby by remember(d.id, e?.standbyW) { mutableStateOf(num(e?.standbyW ?: 0.0)) }
    var note by remember(d.id, e?.note) { mutableStateOf(e?.note ?: "") }
    var busy by remember { mutableStateOf(false) }
    val num2 = { s: String -> s.replace(',', '.').toDoubleOrNull() ?: 0.0 }
    CardBox {
        Text("Estimasi energi", color = Txt, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
        if (metered) {
            Text("Terukur oleh meter perangkat.", color = Mut, fontSize = 13.sp)
        } else {
            OutlinedTextField(
                value = watt,
                onValueChange = { if (it.length <= 7) watt = it },
                singleLine = true,
                label = { Text("Daya beban saat ON (W)") },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Decimal),
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = standby,
                onValueChange = { if (it.length <= 6) standby = it },
                singleLine = true,
                label = { Text("Konsumsi modul Sonoff (W)") },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Decimal),
                modifier = Modifier.fillMaxWidth(),
            )
        }
        OutlinedTextField(
            value = note,
            onValueChange = { if (it.length <= 200) note = it },
            label = { Text("Catatan") },
            modifier = Modifier.fillMaxWidth(),
        )
        if (!metered) {
            Text(
                "kWh = beban × lama ON + konsumsi modul Sonoff × lama perangkat terdengar (ON maupun OFF). Modul Sonoff sendiri memakai energi tambahan walau relay OFF.",
                color = Mut,
                fontSize = 12.sp,
            )
        }
        val shape = RoundedCornerShape(12.dp)
        Box(
            Modifier
                .heightIn(min = 44.dp)
                .clip(shape)
                .background(if (busy) TrackOff else Acc)
                .clickable(enabled = !busy) {
                    busy = true
                    vm.saveEnergyCfg(d, if (metered) (e?.watt ?: 0.0) else num2(watt), if (metered) (e?.standbyW ?: 0.0) else num2(standby), note.trim()) { busy = false }
                }
                .padding(horizontal = 24.dp),
            contentAlignment = Alignment.Center,
        ) { Text(if (busy) "Menyimpan…" else "Simpan", color = Bg, fontWeight = FontWeight.SemiBold) }
    }
}

// ---------------------------------------------------------------------------------------------
// Tab Log

private class LogRow(val t: Long, val status: String, val mid: String, val color: Color)

private fun onoffRows(h: OnOffHist): List<LogRow> = h.events.asReversed().map { e ->
    when (e.state) {
        "on" -> LogRow(e.t, "On", e.src.ifEmpty { "—" }, Ok)
        "off" -> LogRow(e.t, "Off", e.src.ifEmpty { "—" }, Txt)
        else -> LogRow(e.t, "Hilang", "Tidak terdengar", Bad)
    }
}

private fun powerRows(evs: List<PowerEvent>): List<LogRow> = evs.asReversed().map { e ->
    val w = if (e.w > 0) "${e.w.roundToInt()} W" else ""
    when (e.state) {
        "comp" -> LogRow(e.t, "Kompresor", w, CompColor)
        "fan" -> LogRow(e.t, "Fan", w, FanColor)
        "idle" -> LogRow(e.t, "Siaga", w, Txt)
        "off" -> LogRow(e.t, "Mati", w, Txt)
        else -> LogRow(e.t, "Hilang", "Tidak terdengar", Bad)
    }
}

@Composable
private fun LogTab(vm: MainViewModel, d: Device) {
    val ac = isAc(d)
    val ranges = if (ac) listOf(6, 12, 24, 48) else listOf(6, 24, 72, 168)
    val labels = ranges.map { if (it <= 48) "${it}j" else "${it / 24} hari" }
    var ri by remember { mutableIntStateOf(if (ac) 2 else 1) }
    val hours = ranges[ri]
    Segmented(labels, ri, { ri = it })

    val rt by vm.refreshTick.collectAsStateWithLifecycle()
    if (ac) {
        val l = polled(Triple(d.id, hours, rt), 5_000L) { vm.powerLog(d.id, hours) }
        LoadState(l)
        if (l is Loaded.Ok) DateLog(powerRows(l.v))
    } else {
        val l = polled(Triple(d.id, hours, rt), 5_000L) { vm.onoffHist(d.id, hours) }
        LoadState(l)
        if (l is Loaded.Ok) DateLog(onoffRows(l.v))
    }
}

/** Log per tanggal (terbaru di atas): titik + garis waktu, baris "status - sumber - jam". */
@Composable
private fun DateLog(rows: List<LogRow>) {
    if (rows.isEmpty()) {
        Text("Belum ada kejadian pada rentang ini.", color = Mut, fontSize = 13.sp)
        return
    }
    val groups = ArrayList<Pair<String, MutableList<LogRow>>>()
    for (r in rows) {
        val k = dateKey(r.t)
        if (groups.isEmpty() || groups.last().first != k) groups.add(k to mutableListOf())
        groups.last().second.add(r)
    }
    Column(Modifier.fillMaxWidth().padding(horizontal = 4.dp)) {
        for ((k, list) in groups) {
            Row(Modifier.heightIn(min = 36.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier
                        .size(12.dp)
                        .clip(CircleShape)
                        .border(3.dp, Acc, CircleShape),
                )
                Spacer(Modifier.width(12.dp))
                Text(k, color = Acc, fontSize = 17.sp, fontWeight = FontWeight.Bold)
            }
            Row(Modifier.height(IntrinsicSize.Min)) {
                Spacer(Modifier.width(4.dp))
                Box(
                    Modifier
                        .width(3.dp)
                        .fillMaxHeight()
                        .background(Line),
                )
                Spacer(Modifier.width(20.dp))
                Column(Modifier.weight(1f).padding(bottom = 8.dp)) {
                    for (r in list) {
                        Row(Modifier.fillMaxWidth().heightIn(min = 38.dp), verticalAlignment = Alignment.CenterVertically) {
                            Text(r.status, color = r.color, fontSize = 15.sp, modifier = Modifier.width(76.dp), maxLines = 1)
                            Text(r.mid, color = if (r.mid == "—") Mut else if (r.color == Bad) Bad else Txt, fontSize = 15.sp, modifier = Modifier.weight(1f), maxLines = 1)
                            Text(hms(r.t), color = Txt, fontSize = 15.sp, style = androidx.compose.ui.text.TextStyle(fontFeatureSettings = "tnum"))
                        }
                    }
                }
            }
        }
    }
}
