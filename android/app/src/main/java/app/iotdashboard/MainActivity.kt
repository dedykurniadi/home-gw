package app.iotdashboard

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.viewModels
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.material3.Icon
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.delay
import java.util.Locale

internal val Bg = Color(0xFF0D1117)
internal val Panel = Color(0xFF161B22)
internal val Line = Color(0xFF262D38)
internal val Txt = Color(0xFFE6EDF3)
internal val Mut = Color(0xFF8B949E)
internal val Ok = Color(0xFF3FB950)
internal val Bad = Color(0xFFF85149)
internal val Warn = Color(0xFFD29922)
internal val Acc = Color(0xFF58A6FF)
private val OnCardBg = Color(0xFF13201A)
private val OnCardLine = Color(0xFF2A4A33)
private val TileAc = Color(0xFF12301F)
private val TileBulb = Color(0xFF11263F)
internal val TrackOff = Color(0xFF30363D)

internal val ID = Locale("id", "ID")

class MainActivity : ComponentActivity() {
    private val vm: MainViewModel by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) = vm.start()
            override fun onStop(owner: LifecycleOwner) = vm.stop()
        })
        setContent {
            MaterialTheme(
                colorScheme = darkColorScheme(
                    primary = Acc,
                    background = Bg,
                    surface = Panel,
                    onBackground = Txt,
                    onSurface = Txt,
                ),
            ) {
                Screen(vm)
            }
        }
    }
}

/** Ringkas: detik hanya di bawah 1 menit; setelahnya menit/jam/hari (sama dengan di web). */
fun coarse(sec: Long): String {
    val s = maxOf(0L, sec)
    if (s < 60) return "${s}s"
    val d = s / 86400
    val h = (s / 3600) % 24
    val m = (s / 60) % 60
    return when {
        d > 0 -> "${d}hr ${h}j"
        s >= 3600 -> "${s / 3600}j ${m}m"
        else -> "${m}m"
    }
}

internal fun isAc(d: Device) = d.kind == "switches"

internal fun statusText(d: Device, now: Long): String {
    if (!d.online) return "Offline"
    if (d.state == "on" && d.power != null && isAc(d)) return "ON · " + String.format(ID, "%.0f W", d.power)
    val s = when (d.state) {
        "on" -> "ON"
        "off" -> "OFF"
        else -> "—"
    }
    return if (d.since > 0 && (d.state == "on" || d.state == "off")) "$s · ${coarse(now - d.since)}" else s
}

internal fun kwhText(v: Double) = String.format(ID, if (v >= 1) "%.2f" else "%.3f", v)
internal fun rupiah(v: Double) = "Rp " + String.format(ID, "%,.0f", v)

@Composable
private fun Screen(vm: MainViewModel) {
    val devices by vm.devices.collectAsStateWithLifecycle()
    val live by vm.live.collectAsStateWithLifecycle()
    val energy by vm.energy.collectAsStateWithLifecycle()
    val url by vm.baseUrl.collectAsStateWithLifecycle()
    val pending by vm.pending.collectAsStateWithLifecycle()
    val fresh by vm.fresh.collectAsStateWithLifecycle()
    val devicesAt by vm.devicesAt.collectAsStateWithLifecycle()
    val haptic = LocalHapticFeedback.current
    // Belum tersambung ke dashboard tetapi ada data simpanan: tile dibaca saja, hitungan waktu dibekukan.
    val stale = devices.isNotEmpty() && !fresh

    var now by remember { mutableLongStateOf(System.currentTimeMillis() / 1000) }
    LaunchedEffect(Unit) {
        while (true) {
            delay(1_000)
            now = System.currentTimeMillis() / 1000
        }
    }
    val snack = remember { SnackbarHostState() }
    LaunchedEffect(Unit) { vm.messages.collect { snack.showSnackbar(it) } }
    var showSettings by remember { mutableStateOf(false) }
    var filter by remember { mutableIntStateOf(0) } // 0 semua, 1 ON, 2 offline
    var openId by remember { mutableStateOf<String?>(null) }
    val open = openId?.let { id -> devices.firstOrNull { it.id == id } }
    var showSched by remember { mutableStateOf(false) }
    var showReport by remember { mutableStateOf(false) }
    if (open != null) BackHandler { openId = null } else if (showSched) BackHandler { showSched = false } else if (showReport) BackHandler { showReport = false }

    val shownNow = if (stale && devicesAt > 0) devicesAt else now
    val onCount = devices.count { it.online && it.state == "on" }
    val offlineCount = devices.count { !it.online }
    val shown = when (filter) {
        1 -> devices.filter { it.online && it.state == "on" }
        2 -> devices.filter { !it.online }
        else -> devices
    }

    Scaffold(
        containerColor = Bg,
        snackbarHost = { SnackbarHost(snack) },
    ) { pad ->
        if (open == null && showReport) {
            Pull(vm, Modifier.fillMaxSize().padding(pad)) {
                ReportScreen(
                    vm = vm,
                    devices = devices,
                    onBack = { showReport = false },
                    modifier = Modifier.fillMaxSize(),
                )
            }
            return@Scaffold
        }
        if (open == null && showSched) {
            Pull(vm, Modifier.fillMaxSize().padding(pad)) {
                ScheduleScreen(
                    vm = vm,
                    devices = devices,
                    now = now,
                    onBack = { showSched = false },
                    modifier = Modifier.fillMaxSize(),
                )
            }
            return@Scaffold
        }
        if (open != null) {
            Pull(vm, Modifier.fillMaxSize().padding(pad)) {
                DetailScreen(
                    vm = vm,
                    d = open,
                    now = shownNow,
                    energy = energy,
                    pending = pending.containsKey(open.id),
                    onBack = { openId = null },
                    modifier = Modifier.fillMaxSize(),
                )
            }
            return@Scaffold
        }
        Column(
            Modifier
                .fillMaxSize()
                .padding(pad)
                .padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Header(live = live, onSchedule = { showSched = true }, onSettings = { showSettings = true })
            if (stale) OfflineBanner(devicesAt)
            Summary(energy = energy, active = onCount, total = devices.size, onOpen = { showReport = true })
            FilterChips(
                selected = filter,
                labels = listOf("Semua ${devices.size}", "ON $onCount", "Offline $offlineCount"),
                onSelect = { filter = it },
            )
            if (devices.isEmpty()) {
                Pull(vm, Modifier.weight(1f).fillMaxWidth()) {
                    Box(
                        Modifier.fillMaxSize().verticalScroll(rememberScrollState()),
                        contentAlignment = Alignment.Center,
                    ) {
                        Text(
                            if (live) "Belum ada perangkat" else "Menghubungkan ke $url …\nTarik ke bawah untuk menyambung ulang",
                            color = Mut,
                            textAlign = TextAlign.Center,
                        )
                    }
                }
            } else {
                // Tinggi kartu mengikuti layar agar 10 kartu (5 baris) muat dalam satu halaman.
                Pull(vm, Modifier.weight(1f).fillMaxWidth()) {
                BoxWithConstraints(Modifier.fillMaxSize()) {
                    val cardH = ((maxHeight - 44.dp) / 5).coerceIn(64.dp, 120.dp)
                    LazyVerticalGrid(
                        columns = GridCells.Fixed(2),
                        horizontalArrangement = Arrangement.spacedBy(10.dp),
                        verticalArrangement = Arrangement.spacedBy(10.dp),
                        contentPadding = androidx.compose.foundation.layout.PaddingValues(0.dp),
                    ) {
                        items(shown, key = { it.id }) { d ->
                            DeviceCard(
                                d = d,
                                now = shownNow,
                                height = cardH,
                                stale = stale,
                                pending = pending.containsKey(d.id),
                                onToggle = {
                                    haptic.performHapticFeedback(HapticFeedbackType.LongPress)
                                    vm.toggle(d)
                                },
                                onOpen = { openId = d.id },
                            )
                        }
                    }
                }
                }
            }
        }
    }

    if (showSettings) {
        SettingsDialog(
            current = url,
            onSave = {
                vm.setUrl(it)
                showSettings = false
            },
            onDismiss = { showSettings = false },
        )
    }
}

/** Tarik ke bawah = tutup & buka ulang sambungan ke dashboard, lalu muat ulang data layar. */
@OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class)
@Composable
private fun Pull(vm: MainViewModel, modifier: Modifier, content: @Composable () -> Unit) {
    val r by vm.refreshing.collectAsStateWithLifecycle()
    androidx.compose.material3.pulltorefresh.PullToRefreshBox(
        isRefreshing = r,
        onRefresh = { vm.refresh() },
        modifier = modifier,
    ) { content() }
}

@Composable
private fun Header(live: Boolean, onSchedule: () -> Unit, onSettings: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = 44.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text("Rumah", color = Txt, fontSize = 24.sp, fontWeight = FontWeight.Bold)
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                Box(
                    Modifier
                        .size(7.dp)
                        .clip(CircleShape)
                        .background(if (live) Ok else Bad),
                )
                Text(if (live) "Terhubung ke router" else "Terputus", color = Mut, fontSize = 12.sp)
            }
        }
        Spacer(Modifier.weight(1f))
        Box(
            Modifier
                .size(44.dp)
                .clip(RoundedCornerShape(12.dp))
                .background(Panel)
                .border(1.dp, Line, RoundedCornerShape(12.dp))
                .clickable(onClick = onSchedule)
                .semantics { contentDescription = "Jadwal otomatis" },
            contentAlignment = Alignment.Center,
        ) {
            Icon(painterResource(R.drawable.ic_clock), contentDescription = null, tint = Mut, modifier = Modifier.size(22.dp))
        }
        Spacer(Modifier.width(8.dp))
        Box(
            Modifier
                .size(44.dp)
                .clip(RoundedCornerShape(12.dp))
                .background(Panel)
                .border(1.dp, Line, RoundedCornerShape(12.dp))
                .clickable(onClick = onSettings)
                .semantics { contentDescription = "Pengaturan alamat dashboard" },
            contentAlignment = Alignment.Center,
        ) {
            Icon(painterResource(R.drawable.ic_settings), contentDescription = null, tint = Mut, modifier = Modifier.size(22.dp))
        }
    }
}

@Composable
private fun Summary(energy: Energy?, active: Int, total: Int, onOpen: () -> Unit) {
    val shape = RoundedCornerShape(16.dp)
    Row(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(Panel)
            .border(1.dp, Line, shape)
            .clickable(onClick = onOpen)
            .semantics { contentDescription = "Buka laporan energi bulanan" }
            .padding(16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text("Pemakaian hari ini", color = Mut, fontSize = 12.sp)
            Row(verticalAlignment = Alignment.Bottom) {
                Text(
                    if (energy != null) kwhText(energy.totalKwh) else "—",
                    color = Txt,
                    fontSize = 28.sp,
                    fontWeight = FontWeight.Bold,
                )
                Text(" kWh", color = Mut, fontSize = 14.sp, modifier = Modifier.padding(bottom = 4.dp))
            }
            Text(
                if (energy != null && energy.totalCost > 0) rupiah(energy.totalCost) else " ",
                color = Acc,
                fontSize = 14.sp,
            )
        }
        Box(
            Modifier
                .padding(horizontal = 16.dp)
                .width(1.dp)
                .height(56.dp)
                .background(Line),
        )
        Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text("Aktif", color = Mut, fontSize = 12.sp)
            Row(verticalAlignment = Alignment.Bottom) {
                Text("$active", color = Txt, fontSize = 28.sp, fontWeight = FontWeight.Bold)
                Text(" / $total", color = Mut, fontSize = 14.sp, modifier = Modifier.padding(bottom = 4.dp))
            }
        }
    }
}

@Composable
private fun FilterChips(selected: Int, labels: List<String>, onSelect: (Int) -> Unit) {
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        labels.forEachIndexed { i, label ->
            val sel = i == selected
            val shape = RoundedCornerShape(22.dp)
            Box(
                Modifier
                    .heightIn(min = 44.dp)
                    .clip(shape)
                    .background(if (sel) Acc.copy(alpha = 0.14f) else Panel)
                    .border(1.dp, if (sel) Acc else Line, shape)
                    .clickable { onSelect(i) }
                    .padding(horizontal = 16.dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    label,
                    color = if (sel) Acc else Color(0xFFC9D1D9),
                    fontSize = 14.sp,
                    fontWeight = if (sel) FontWeight.SemiBold else FontWeight.Medium,
                )
            }
        }
    }
}

@Composable
private fun DeviceCard(
    d: Device,
    now: Long,
    height: androidx.compose.ui.unit.Dp,
    stale: Boolean,
    pending: Boolean,
    onToggle: () -> Unit,
    onOpen: (() -> Unit)?,
) {
    val on = d.state == "on" && d.online
    val shape = RoundedCornerShape(16.dp)
    val ac = isAc(d)
    Box(
        Modifier
            .fillMaxWidth()
            .height(height)
            .clip(shape)
            .background(if (on) OnCardBg else Panel)
            .border(1.dp, if (on) OnCardLine else Line, shape)
            .alpha(if (d.online && !stale) 1f else 0.6f),
    ) {
        Column(
            Modifier
                .fillMaxSize()
                .then(if (onOpen != null) Modifier.clickable(onClick = onOpen) else Modifier)
                .padding(horizontal = 10.dp, vertical = 8.dp),
            verticalArrangement = Arrangement.SpaceBetween,
        ) {
            Icon(
                painterResource(
                    if (ac) (if (on) R.drawable.ic_ac_on else R.drawable.ic_ac_off)
                    else (if (on) R.drawable.ic_bulb_on else R.drawable.ic_bulb_off),
                ),
                contentDescription = null,
                tint = Color.Unspecified,
                modifier = Modifier.size(34.dp),
            )
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(
                    d.name,
                    color = Txt,
                    fontSize = 15.sp,
                    fontWeight = FontWeight.SemiBold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                val statusColor = if (!d.online) Bad else if (on) Ok else Mut
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(
                        Modifier
                            .size(8.dp)
                            .clip(CircleShape)
                            .background(if (!d.online) Bad else if (on) Ok else Color(0xFF6E7681)),
                    )
                    Spacer(Modifier.width(6.dp))
                    // Teks mengisi sisa lebar, sinyal selalu di ujung kanan dengan lebar tetap.
                    Text(
                        statusText(d, now),
                        color = statusColor,
                        fontSize = 12.sp,
                        style = androidx.compose.ui.text.TextStyle(fontFeatureSettings = "tnum"),
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f),
                    )
                    Box(Modifier.size(width = 18.dp, height = 14.dp), contentAlignment = Alignment.BottomEnd) {
                        if (d.online) SignalBars(d.rssi)
                    }
                }
            }
        }
        Box(Modifier.align(Alignment.TopEnd).padding(top = 2.dp, end = 4.dp)) {
            SwitchToggle(
                on = on,
                enabled = d.online && !stale,
                pending = pending,
                label = (if (on) "Matikan " else "Nyalakan ") + d.name,
                onToggle = onToggle,
            )
        }
    }
}

/** Saklar seperti di eWeLink: ketuk sekali. Area sentuh 56x44. */
@Composable
internal fun SwitchToggle(on: Boolean, enabled: Boolean, pending: Boolean, label: String, onToggle: () -> Unit) {
    val track = when {
        !enabled -> Color(0xFF21262D)
        on -> Acc
        else -> TrackOff
    }
    val thumb = if (enabled) Color.White else Color(0xFF484F58)
    Box(
        Modifier
            .size(width = 56.dp, height = 44.dp)
            .clip(RoundedCornerShape(12.dp))
            .toggleable(
                value = on,
                enabled = enabled && !pending,
                role = Role.Switch,
                onValueChange = { onToggle() },
            )
            .semantics { contentDescription = label },
        contentAlignment = Alignment.Center,
    ) {
        Box(
            Modifier
                .size(width = 48.dp, height = 28.dp)
                .alpha(if (pending) 0.55f else 1f)
                .clip(RoundedCornerShape(14.dp))
                .background(track)
                .padding(3.dp),
            contentAlignment = if (on) Alignment.CenterEnd else Alignment.CenterStart,
        ) {
            Box(
                Modifier
                    .size(22.dp)
                    .clip(CircleShape)
                    .background(thumb),
            )
        }
    }
}

@Composable
internal fun SignalBars(rssi: Int?) {
    if (rssi == null) return
    val n = if (rssi > -55) 4 else if (rssi > -65) 3 else if (rssi > -75) 2 else 1
    val col = if (n >= 3) Ok else if (n == 2) Warn else Bad
    Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(2.dp)) {
        for (i in 1..4) {
            Box(
                Modifier
                    .width(3.dp)
                    .height((3 + 3 * i).dp)
                    .clip(RoundedCornerShape(1.dp))
                    .background(if (i <= n) col else TrackOff),
            )
        }
    }
}

@Composable
private fun SettingsDialog(current: String, onSave: (String) -> Unit, onDismiss: () -> Unit) {
    var text by remember { mutableStateOf(current) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Alamat dashboard") },
        text = {
            Column {
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it },
                    singleLine = true,
                    label = { Text("URL") },
                )
                Spacer(Modifier.height(8.dp))
                Text(
                    "Contoh: http://10.142.27.1:8081 (di rumah) atau alamat router lewat VPN.",
                    fontSize = 12.sp,
                    color = Mut,
                )
            }
        },
        confirmButton = { TextButton(onClick = { onSave(text) }) { Text("Simpan") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Batal") } },
    )
}
