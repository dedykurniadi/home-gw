package app.iotdashboard

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlin.math.abs

private val DayNames = listOf("Min", "Sen", "Sel", "Rab", "Kam", "Jum", "Sab")
private val DayOrder = listOf(1, 2, 3, 4, 5, 6, 0) // tampil Senin dulu

private fun hm(t: Long) = SimpleDateFormat("HH.mm", Locale.US).format(Date(t * 1000))
private fun ymd(t: Long) = SimpleDateFormat("yyyyMMdd", Locale.US).format(Date(t * 1000))
private fun dayHm(t: Long) = SimpleDateFormat("dd/MM HH.mm", Locale.US).format(Date(t * 1000))

internal fun daysText(days: List<Int>): String {
    val d = days.toSet()
    return when {
        d.isEmpty() -> "Setiap hari"
        d == setOf(1, 2, 3, 4, 5) -> "Hari kerja"
        d == setOf(0, 6) -> "Akhir pekan"
        else -> DayOrder.filter { it in d }.joinToString(" ") { DayNames[it] }
    }
}

internal fun offsetText(m: Int) = when {
    m == 0 -> ""
    m > 0 -> " +$m mnt"
    else -> " −${abs(m)} mnt"
}

internal fun triggerText(t: Trig) = when (t.type) {
    "sunrise" -> "Terbit" + offsetText(t.offset)
    "sunset" -> "Terbenam" + offsetText(t.offset)
    else -> t.at.replace(':', '.')
}

private fun nextText(next: Long, now: Long): String {
    if (next <= 0) return "—"
    return when (ymd(next)) {
        ymd(now) -> "hari ini " + hm(next)
        ymd(now + 86_400) -> "besok " + hm(next)
        else -> dayHm(next)
    }
}

private fun actText(a: String) = if (a == "on") "ON" else "OFF"

private val SunUp = Warn
private val SunDown = Color(0xFFF0883E)

/** Ikon pemicu: matahari terbit/terbenam; jam memakai ikon jam. */
private fun triggerIcon(t: Trig) = when (t.type) {
    "sunrise" -> R.drawable.ic_sunrise
    "sunset" -> R.drawable.ic_sunset
    else -> R.drawable.ic_clock
}

private fun triggerTint(t: Trig) = when (t.type) {
    "sunrise" -> SunUp
    "sunset" -> SunDown
    else -> Mut
}

@Composable
internal fun ScheduleScreen(vm: MainViewModel, devices: List<Device>, now: Long, onBack: () -> Unit, modifier: Modifier = Modifier) {
    var data by remember { mutableStateOf<Sched?>(null) }
    var loadErr by remember { mutableStateOf<String?>(null) }
    var actionErr by remember { mutableStateOf<String?>(null) }
    var tick by remember { mutableIntStateOf(0) }
    var editing by remember { mutableStateOf<Scene?>(null) }
    var tab by remember { mutableIntStateOf(0) } // 0 = PRESET, 1 = LOG
    val scope = rememberCoroutineScope()
    val fresh by vm.fresh.collectAsStateWithLifecycle()
    val offline = !fresh // belum tersambung: data dari simpanan, tidak bisa mengubah

    val rt by vm.refreshTick.collectAsStateWithLifecycle()
    LaunchedEffect(tick, rt) {
        while (true) {
            try {
                data = vm.api().sched()
                loadErr = null
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                loadErr = e.message ?: "kesalahan"
            }
            delay(30_000)
        }
    }

    fun change(block: suspend () -> Unit) {
        if (offline) {
            actionErr = "Tidak terhubung ke dashboard"
            return
        }
        scope.launch {
            try {
                block()
                actionErr = null
                tick++
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                actionErr = e.message ?: "kesalahan"
            }
        }
    }

    fun nameOf(id: String) = devices.firstOrNull { it.id == id }?.name ?: id

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
            Text("Scene", color = Txt, fontSize = 20.sp, fontWeight = FontWeight.Bold, modifier = Modifier.weight(1f))
            Box(
                Modifier
                    .size(44.dp)
                    .clip(RoundedCornerShape(12.dp))
                    .background(Acc)
                    .alpha(if (offline) 0.4f else 1f)
                    .clickable(enabled = devices.isNotEmpty() && !offline) {
                        editing = Scene(triggers = listOf(Trig("time", "18:00")), actions = emptyList())
                    }
                    .semantics { contentDescription = "Tambah scene" },
                contentAlignment = Alignment.Center,
            ) {
                Text("+", color = Bg, fontSize = 26.sp, fontWeight = FontWeight.Bold)
            }
        }

        OfflineNotice(vm)
        Segmented(listOf("PRESET", "LOG"), tab, { tab = it })

        Column(
            Modifier
                .weight(1f)
                .verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            val d = data
            if (loadErr != null) Text("Gagal memuat: $loadErr", color = Bad, fontSize = 13.sp)
            if (actionErr != null) Text("Gagal menyimpan: $actionErr", color = Bad, fontSize = 13.sp)
            if (d == null && loadErr == null) Text("Memuat…", color = Mut, fontSize = 13.sp)
            if (d != null) {
                if (tab == 0) {
                    SunCard(d)
                    if (d.scenes.isEmpty()) {
                        Text(
                            "Belum ada scene. Ketuk + untuk membuat, misalnya semua lampu menyala saat matahari terbenam.",
                            color = Mut,
                            fontSize = 14.sp,
                            modifier = Modifier.padding(vertical = 8.dp),
                        )
                    }
                    d.scenes.forEach { sc ->
                        SceneCard(
                            sc = sc,
                            now = now,
                            offline = offline,
                            nameOf = ::nameOf,
                            onOpen = { editing = sc },
                            onToggle = { change { vm.api().saveScene(sc.copy(enabled = !sc.enabled)) } },
                        )
                    }
                } else {
                    LogList(d.runs)
                }
            }
        }
    }

    val sc = editing
    val d = data
    if (sc != null) {
        SceneDialog(
            scene = sc,
            devices = devices,
            hasLocation = d?.hasLocation ?: false,
            sunrise = d?.sunrise ?: 0L,
            sunset = d?.sunset ?: 0L,
            onDismiss = { editing = null },
            onSave = { n ->
                editing = null
                change { vm.api().saveScene(n) }
            },
            onDelete = if (sc.id.isEmpty()) null else {
                {
                    editing = null
                    change { vm.api().deleteScene(sc.id) }
                }
            },
        )
    }
}

@Composable
private fun SunTime(icon: Int, label: String, time: String, tint: Color, modifier: Modifier = Modifier) {
    Row(modifier, verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        Icon(painterResource(icon), contentDescription = null, tint = tint, modifier = Modifier.size(32.dp))
        Column {
            Text(label, color = Mut, fontSize = 12.sp)
            Text(time, color = Txt, fontSize = 18.sp, fontWeight = FontWeight.Bold)
        }
    }
}

@Composable
private fun SunCard(d: Sched) {
    val shape = RoundedCornerShape(16.dp)
    Row(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(Panel)
            .border(1.dp, Line, shape)
            .padding(16.dp),
        horizontalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        if (!d.hasLocation || d.sunrise == 0L) {
            Text(
                "Lokasi belum diatur. Tambahkan \"location\": {\"lat\": …, \"lon\": …} di config dashboard untuk pemicu terbit/terbenam.",
                color = Mut,
                fontSize = 12.sp,
            )
        } else {
            SunTime(R.drawable.ic_sunrise, "Terbit hari ini", hm(d.sunrise), SunUp, Modifier.weight(1f))
            SunTime(R.drawable.ic_sunset, "Terbenam hari ini", hm(d.sunset), SunDown, Modifier.weight(1f))
        }
    }
}

@Composable
private fun StatusChip(action: String) {
    val on = action == "on"
    Box(
        Modifier
            .size(width = 44.dp, height = 24.dp)
            .clip(RoundedCornerShape(8.dp))
            .background(if (on) Ok else TrackOff),
        contentAlignment = Alignment.Center,
    ) {
        Text(actText(action), color = if (on) Bg else Txt, fontSize = 12.sp, fontWeight = FontWeight.SemiBold)
    }
}

@Composable
private fun SceneCard(sc: Scene, now: Long, offline: Boolean, nameOf: (String) -> String, onOpen: () -> Unit, onToggle: () -> Unit) {
    val shape = RoundedCornerShape(16.dp)
    val title = sc.label.ifEmpty { sc.name.ifEmpty { "Scene" } }
    val trig = sc.triggers.firstOrNull()
    Column(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(Panel)
            .border(1.dp, Line, shape)
            .clickable(onClick = onOpen)
            .padding(start = 16.dp, top = 6.dp, bottom = 12.dp, end = 8.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                title,
                color = if (sc.enabled) Txt else Mut,
                fontSize = 16.sp,
                fontWeight = FontWeight.SemiBold,
                maxLines = 1,
                modifier = Modifier.weight(1f),
            )
            SwitchToggle(
                on = sc.enabled,
                enabled = !offline,
                pending = false,
                label = (if (sc.enabled) "Nonaktifkan scene " else "Aktifkan scene ") + title,
                onToggle = onToggle,
            )
        }
        if (trig != null) {
            Row(Modifier.padding(end = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Jika", color = Mut, fontSize = 12.sp, modifier = Modifier.width(36.dp))
                Icon(painterResource(triggerIcon(trig)), contentDescription = null, tint = triggerTint(trig), modifier = Modifier.size(20.dp))
                Text(triggerText(trig), color = Txt, fontSize = 14.sp, fontWeight = FontWeight.Medium, modifier = Modifier.weight(1f))
            }
        }
        Row(Modifier.padding(end = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("Maka", color = Mut, fontSize = 12.sp, modifier = Modifier.width(36.dp))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                sc.actions.forEach { a ->
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text(nameOf(a.device), color = Txt, fontSize = 14.sp, maxLines = 1, modifier = Modifier.weight(1f))
                        StatusChip(a.action)
                    }
                }
            }
        }
        Column(Modifier.padding(end = 8.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(
                daysText(sc.days) + if (sc.enabled) " · berikutnya " + nextText(sc.next, now) else "",
                color = Mut,
                fontSize = 12.sp,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                if (sc.lastOk == 0L && sc.lastFail == 0L) Text("Belum pernah dijalankan", color = Mut, fontSize = 11.sp)
                if (sc.lastOk > 0) Text("Berhasil " + dayHm(sc.lastOk), color = Ok, fontSize = 11.sp)
                if (sc.lastFail > 0) Text("Gagal " + dayHm(sc.lastFail), color = Bad, fontSize = 11.sp)
            }
        }
    }
}

/** Tab LOG: tiap eksekusi scene (berhasil/gagal); bila gagal, perangkat yang gagal disebut satu per baris. */
@Composable
private fun LogList(runs: List<SchedRun>) {
    if (runs.isEmpty()) {
        Text(
            "Belum ada log. Catatan muncul setelah scene dijalankan.",
            color = Mut,
            fontSize = 14.sp,
            modifier = Modifier.padding(vertical = 8.dp),
        )
        return
    }
    // dikelompokkan per tanggal (terbaru di atas), seperti log perangkat
    val groups = ArrayList<Pair<String, MutableList<SchedRun>>>()
    for (x in runs) {
        val k = SimpleDateFormat("yyyy/MM/dd", Locale.US).format(Date(x.t * 1000))
        if (groups.isEmpty() || groups.last().first != k) groups.add(k to mutableListOf())
        groups.last().second.add(x)
    }
    Column(Modifier.fillMaxWidth().padding(horizontal = 4.dp)) {
        for ((k, list) in groups) {
            Row(Modifier.heightIn(min = 36.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(12.dp).clip(CircleShape).border(3.dp, Acc, CircleShape))
                Spacer(Modifier.width(12.dp))
                Text(k, color = Acc, fontSize = 17.sp, fontWeight = FontWeight.Bold)
            }
            Row(Modifier.height(IntrinsicSize.Min)) {
                Spacer(Modifier.width(4.dp))
                Box(Modifier.width(3.dp).fillMaxHeight().background(Line))
                Spacer(Modifier.width(20.dp))
                Column(Modifier.weight(1f).padding(bottom = 8.dp)) {
                    for (x in list) {
                        Row(
                            Modifier.fillMaxWidth().heightIn(min = 44.dp).padding(vertical = 6.dp),
                            verticalAlignment = Alignment.Top,
                        ) {
                            Text(
                                if (x.ok) "Berhasil" else "Gagal",
                                color = if (x.ok) Ok else Bad,
                                fontSize = 15.sp,
                                fontWeight = FontWeight.SemiBold,
                                modifier = Modifier.width(78.dp),
                            )
                            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                                Text(x.name.ifEmpty { "Scene dihapus" }, color = Txt, fontSize = 15.sp, maxLines = 1)
                                val sub = listOf(x.label, if (x.fire > 0) "dijadwalkan " + hm(x.fire) else "").filter { it.isNotEmpty() }.joinToString(" · ")
                                if (sub.isNotEmpty()) Text(sub, color = Mut, fontSize = 12.sp)
                                if (!x.ok) {
                                    if (x.fails.isNotEmpty()) {
                                        x.fails.forEach { f -> Text("• " + f.name.ifEmpty { f.device } + ": " + f.err, color = Bad, fontSize = 12.sp) }
                                    } else if (x.err.isNotEmpty()) {
                                        Text(x.err, color = Bad, fontSize = 12.sp)
                                    }
                                }
                            }
                            Text(
                                SimpleDateFormat("HH:mm:ss", Locale.US).format(Date(x.t * 1000)),
                                color = Txt,
                                fontSize = 15.sp,
                                style = androidx.compose.ui.text.TextStyle(fontFeatureSettings = "tnum"),
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun Label(text: String) {
    Text(text, color = Mut, fontSize = 12.sp, fontWeight = FontWeight.Medium, modifier = Modifier.padding(top = 6.dp))
}

@Composable
private fun Stepper(label: String, value: String, onMinus: () -> Unit, onPlus: () -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(label, color = Txt, fontSize = 14.sp, modifier = Modifier.weight(1f))
        StepBtn("−", "Kurangi $label", onMinus)
        Text(value, color = Txt, fontSize = 18.sp, fontWeight = FontWeight.Bold, textAlign = TextAlign.Center, modifier = Modifier.width(84.dp))
        StepBtn("+", "Tambah $label", onPlus)
    }
}

@Composable
private fun StepBtn(sym: String, desc: String, onClick: () -> Unit) {
    Box(
        Modifier
            .size(44.dp)
            .clip(RoundedCornerShape(12.dp))
            .background(Line)
            .clickable(role = Role.Button, onClick = onClick)
            .semantics { contentDescription = desc },
        contentAlignment = Alignment.Center,
    ) {
        Text(sym, color = Txt, fontSize = 22.sp, fontWeight = FontWeight.Medium)
    }
}

@Composable
private fun DialogBtn(text: String, color: Color, filled: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(12.dp)
    Box(
        modifier
            .heightIn(min = 44.dp)
            .clip(shape)
            .background(if (filled) color else Color.Transparent)
            .border(1.dp, if (filled) color else Line, shape)
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 16.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(text, color = if (filled) Bg else color, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
    }
}

/** Pilihan 3 arah untuk satu perangkat: tidak diubah / ON / OFF. */
@Composable
private fun ActionPicker(current: String?, onPick: (String?) -> Unit) {
    val shape = RoundedCornerShape(10.dp)
    Row(
        Modifier
            .clip(shape)
            .background(Bg)
            .border(1.dp, Line, shape)
            .padding(3.dp),
        horizontalArrangement = Arrangement.spacedBy(3.dp),
    ) {
        listOf<Pair<String?, String>>(null to "—", "on" to "ON", "off" to "OFF").forEach { (v, text) ->
            val sel = v == current
            val bg = when {
                !sel -> Color.Transparent
                v == "on" -> Ok
                v == "off" -> TrackOff
                else -> Line
            }
            Box(
                Modifier
                    .size(width = 46.dp, height = 40.dp)
                    .clip(RoundedCornerShape(8.dp))
                    .background(bg)
                    .clickable(role = Role.RadioButton) { onPick(v) },
                contentAlignment = Alignment.Center,
            ) {
                Text(text, color = if (sel) (if (v == "on") Bg else Txt) else Mut, fontSize = 13.sp, fontWeight = FontWeight.SemiBold)
            }
        }
    }
}

@Composable
private fun TriggerEditor(t: Trig, hasLocation: Boolean, sunrise: Long, sunset: Long, onChange: (Trig) -> Unit) {
    val shape = RoundedCornerShape(12.dp)
    val parts = t.at.split(":")
    val hour = parts.getOrNull(0)?.toIntOrNull() ?: 18
    val min = parts.getOrNull(1)?.toIntOrNull() ?: 0
    fun time(h: Int, m: Int) = Trig("time", String.format(Locale.US, "%02d:%02d", ((h % 24) + 24) % 24, ((m % 60) + 60) % 60))
    val kind = when (t.type) {
        "sunrise" -> 1
        "sunset" -> 2
        else -> 0
    }
    Column(
        Modifier
            .fillMaxWidth()
            .clip(shape)
            .border(1.dp, Line, shape)
            .padding(10.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            Box(Modifier.weight(1f)) {
                Segmented(
                    listOf("Jam", "Terbit", "Terbenam"),
                    kind,
                    {
                        when (it) {
                            0 -> onChange(Trig("time", t.at))
                            1 -> if (hasLocation) onChange(Trig("sunrise", offset = t.offset))
                            else -> if (hasLocation) onChange(Trig("sunset", offset = t.offset))
                        }
                    },
                    Bg,
                )
            }
        }
        if (kind == 0) {
            Stepper("Jam", String.format(Locale.US, "%02d", hour), { onChange(time(hour - 1, min)) }, { onChange(time(hour + 1, min)) })
            Stepper("Menit", String.format(Locale.US, "%02d", min), { onChange(time(hour, min - 5)) }, { onChange(time(hour, min + 5)) })
        } else {
            val sunTs = if (kind == 1) sunrise else sunset
            if (sunTs > 0) {
                SunTime(
                    if (kind == 1) R.drawable.ic_sunrise else R.drawable.ic_sunset,
                    if (kind == 1) "Terbit hari ini" else "Terbenam hari ini",
                    hm(sunTs),
                    if (kind == 1) SunUp else SunDown,
                )
            }
            val txt = when {
                t.offset == 0 -> "tepat"
                t.offset > 0 -> "+${t.offset} mnt"
                else -> "−${abs(t.offset)} mnt"
            }
            Stepper(
                "Selisih",
                txt,
                { onChange(t.copy(offset = (t.offset - 5).coerceAtLeast(-180))) },
                { onChange(t.copy(offset = (t.offset + 5).coerceAtMost(180))) },
            )
        }
    }
}

@Composable
private fun SceneDialog(
    scene: Scene,
    devices: List<Device>,
    hasLocation: Boolean,
    sunrise: Long,
    sunset: Long,
    onDismiss: () -> Unit,
    onSave: (Scene) -> Unit,
    onDelete: (() -> Unit)?,
) {
    var name by remember { mutableStateOf(scene.name) }
    var trig by remember { mutableStateOf(scene.triggers.firstOrNull() ?: Trig("time", "18:00")) }
    var acts by remember { mutableStateOf(scene.actions) }
    var days by remember { mutableStateOf(scene.days) }
    val shape = RoundedCornerShape(20.dp)

    Dialog(onDismissRequest = onDismiss) {
        Column(
            Modifier
                .fillMaxWidth()
                .heightIn(max = 680.dp)
                .clip(shape)
                .background(Panel)
                .border(1.dp, Line, shape)
                .verticalScroll(rememberScrollState())
                .padding(20.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(if (scene.id.isEmpty()) "Scene baru" else "Ubah scene", color = Txt, fontSize = 18.sp, fontWeight = FontWeight.Bold)

            OutlinedTextField(
                value = name,
                onValueChange = { if (it.length <= 40) name = it },
                singleLine = true,
                label = { Text("Nama (opsional)") },
                modifier = Modifier.fillMaxWidth(),
            )

            Label("Jika")
            if (!hasLocation) Text("Terbit/terbenam butuh lokasi di config dashboard.", color = Warn, fontSize = 12.sp)
            TriggerEditor(
                t = trig,
                hasLocation = hasLocation,
                sunrise = sunrise,
                sunset = sunset,
                onChange = { trig = it },
            )

            Label("Maka (pilih ON atau OFF per perangkat)")
            Column(
                Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(12.dp))
                    .border(1.dp, Line, RoundedCornerShape(12.dp)),
            ) {
                devices.forEach { dv ->
                    val cur = acts.firstOrNull { it.device == dv.id }?.action
                    Row(
                        Modifier
                            .fillMaxWidth()
                            .heightIn(min = 52.dp)
                            .padding(horizontal = 12.dp, vertical = 4.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(dv.name, color = if (cur != null) Txt else Mut, fontSize = 14.sp, modifier = Modifier.weight(1f), maxLines = 1)
                        ActionPicker(cur) { v ->
                            val rest = acts.filter { it.device != dv.id }
                            acts = if (v == null) rest else rest + Act(dv.id, v)
                        }
                    }
                }
            }

            Label("Hari (kosong = setiap hari)")
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                DayOrder.forEach { d ->
                    val sel = d in days
                    Box(
                        Modifier
                            .weight(1f)
                            .heightIn(min = 44.dp)
                            .clip(RoundedCornerShape(10.dp))
                            .background(if (sel) Acc else Bg)
                            .border(1.dp, if (sel) Acc else Line, RoundedCornerShape(10.dp))
                            .clickable(role = Role.Checkbox) { days = if (sel) days - d else days + d },
                        contentAlignment = Alignment.Center,
                    ) {
                        Text(DayNames[d], color = if (sel) Bg else Mut, fontSize = 12.sp, fontWeight = FontWeight.SemiBold)
                    }
                }
            }

            if (acts.isEmpty()) Text("Pilih minimal satu perangkat.", color = Warn, fontSize = 12.sp)
            Spacer(Modifier.size(6.dp))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (onDelete != null) DialogBtn("Hapus", Bad, false, onDelete)
                Spacer(Modifier.weight(1f))
                DialogBtn("Batal", Mut, false, onDismiss)
                DialogBtn(
                    "Simpan",
                    if (acts.isEmpty()) TrackOff else Acc,
                    true,
                    { if (acts.isNotEmpty()) onSave(scene.copy(name = name.trim(), triggers = listOf(trig), actions = acts, days = days)) },
                )
            }
        }
    }
}
