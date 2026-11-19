// Dashboard pengganti Home Assistant untuk router OpenWrt: client jaringan, IoT Sonoff, (kamera menyusul).
// Pemakaian: ./dashboard -config config.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"owrt-dashboard/internal/energy"
	"owrt-dashboard/internal/netdata"
	"owrt-dashboard/internal/onoff"
	"owrt-dashboard/internal/powerlog"
	"owrt-dashboard/internal/sched"
	"owrt-dashboard/internal/sonoff"
	"owrt-dashboard/internal/sqlstore"
	"owrt-dashboard/internal/web"
)

type config struct {
	Listen       string               `json:"listen"`
	LANIface     string               `json:"lan_iface"`
	Subnet       string               `json:"subnet"` // kosong = ambil dari lan_iface
	PollSeconds  int                  `json:"poll_seconds"`
	SweepSeconds int                  `json:"sweep_seconds"`
	DevicesFile  string               `json:"devices_file"`
	LeasesFile   string               `json:"leases_file"`
	ARPFile      string               `json:"arp_file"`
	StaticHosts  []netdata.StaticHost `json:"static_hosts"`
	WiredHosts   []string             `json:"wired_hosts"` // IP perangkat kabel yang diperiksa lewat ARP/arping
	WifiEvents   *bool                `json:"wifi_events"` // konek/putus WiFi seketika via ubus subscribe (default true)
	NoPing       bool                 `json:"no_ping"`
	QuerySeconds int                  `json:"query_seconds"` // selang query mDNS ke perangkat Sonoff (0 = 30)
	Power        powerCfg             `json:"power"`
	OnOff        onoffCfg             `json:"onoff"`
	Sched        schedCfg             `json:"sched"`
	// Location: koordinat rumah, hanya dipakai untuk jadwal terbit/terbenam matahari.
	Location *struct {
		Lat float64 `json:"lat"`
		Lon float64 `json:"lon"`
	} `json:"location"`
	// IoTOnly: hanya dashboard IoT (tanpa tabel client, ping, sweep, dan kamera). Binary yang dibangun
	// dengan -ldflags "-X main.buildMode=iot" selalu memakai mode ini.
	IoTOnly bool `json:"iot_only"`
	// TariffPerKWh = tarif listrik (Rp per kWh) untuk perkiraan biaya; 0 = biaya tidak ditampilkan.
	TariffPerKWh float64 `json:"tariff_per_kwh"`
	// UTCOffsetHours: zona waktu untuk batas "hari ini"/"bulan ini" (mis. 7 untuk WIB).
	// Kosong = zona waktu sistem (di OpenWrt bisa jatuh ke UTC bila TZ tidak terbaca).
	UTCOffsetHours *float64 `json:"utc_offset_hours"`
	// DBPath: lokasi berkas SQLite; kosong = <folder devices_file>/data/dashboard.db
	DBPath string `json:"db_path"`
}

// schedCfg mengatur jadwal otomatis (aktif bawaan).
type schedCfg struct {
	Off  bool   `json:"off"`
	File string `json:"file"` // kosong = <folder devices_file>/data/schedules.json
}

// onoffCfg mengatur riwayat ON/OFF perangkat (opsional).
type onoffCfg struct {
	Off bool `json:"off"`
}

// powerCfg mengatur log daya (hanya dari update mDNS). Semua opsional.
type powerCfg struct {
	Off          bool     `json:"off"`
	Devices      []string `json:"devices"` // id perangkat; kosong = semua perangkat kind "switches"
	IdleW        float64  `json:"idle_w"`  // di bawah ini (relay hidup) = AC mati/standby; default 2
	CompOnW      float64  `json:"comp_on_w"`
	CompOffW     float64  `json:"comp_off_w"`
	DebounceS    int      `json:"debounce_s"`
	GapS         int      `json:"gap_s"`
	FlushS       int      `json:"flush_s"`
	RawDays      int      `json:"raw_days"`
	MinuteMonths int      `json:"minute_months"`
}

func loadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ifaceSubnet mengambil CIDR IPv4 dari sebuah interface (mis. br-lan).
func ifaceSubnet(name string) string {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, _ := ifi.Addrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return (&net.IPNet{IP: ipn.IP.Mask(ipn.Mask), Mask: ipn.Mask}).String()
		}
	}
	return ""
}

// buildMode diisi saat build (-X main.buildMode=iot) untuk binary hanya-IoT.
var buildMode string

func main() {
	cfgPath := flag.String("config", "config.json", "file konfigurasi")
	flag.Parse()

	cfg := config{Listen: ":8080", LANIface: "br-lan", PollSeconds: 5, SweepSeconds: 60, DevicesFile: "devices.json"}
	if err := loadJSON(*cfgPath, &cfg); err != nil {
		log.Fatalf("config %s: %v", *cfgPath, err)
	}
	if buildMode == "iot" {
		cfg.IoTOnly = true
	}
	if cfg.IoTOnly {
		// Collector hanya dipakai membaca ARP/DHCP untuk mencari IP perangkat: tanpa ping dan sweep.
		cfg.NoPing, cfg.Subnet = true, ""
		if cfg.PollSeconds < 30 {
			cfg.PollSeconds = 30
		}
	}
	var devs []sonoff.Device
	if err := loadJSON(cfg.DevicesFile, &devs); err != nil {
		log.Printf("devices %s: %v (IoT kosong)", cfg.DevicesFile, err)
	}
	if cfg.Subnet == "" && cfg.LANIface != "" && !cfg.IoTOnly {
		cfg.Subnet = ifaceSubnet(cfg.LANIface)
	}
	if cfg.Subnet == "" && !cfg.IoTOnly {
		log.Printf("subnet tidak diketahui: sweep dinonaktifkan")
	}

	aliases := map[string]string{}
	for _, d := range devs {
		if d.MAC != "" {
			aliases[strings.ToLower(d.MAC)] = d.Name
		}
	}

	loc := time.Local
	if cfg.UTCOffsetHours != nil {
		loc = time.FixedZone("lokal", int(*cfg.UTCOffsetHours*3600))
	}

	col := netdata.New(netdata.Config{
		LANIface:      cfg.LANIface,
		Subnet:        cfg.Subnet,
		PollInterval:  time.Duration(cfg.PollSeconds) * time.Second,
		SweepInterval: time.Duration(cfg.SweepSeconds) * time.Second,
		LeasesFile:    cfg.LeasesFile,
		ARPFile:       cfg.ARPFile,
		StaticHosts:   cfg.StaticHosts,
		WiredHosts:    cfg.WiredHosts,
		WifiEvents:    cfg.WifiEvents == nil || *cfg.WifiEvents,
		Aliases:       aliases,
		NoPing:        cfg.NoPing,
		ResolveOnly:   cfg.IoTOnly,
	})
	mgr := sonoff.NewManager(devs, col.IPByMAC)
	mgr.QueryEvery = time.Duration(cfg.QuerySeconds) * time.Second
	hub := web.NewHub()

	dbPath := cfg.DBPath
	if dbPath == "" {
		dbPath = filepath.Join(filepath.Dir(cfg.DevicesFile), "data", "dashboard.db")
	}
	sdb, err := sqlstore.Open(dbPath)
	if err != nil {
		log.Fatalf("SQLite %s: %v", dbPath, err)
	}
	log.Printf("penyimpanan: SQLite di %s", dbPath)

	// riwayat ON/OFF semua perangkat (kecil: hanya perubahan yang disimpan)
	var olog *onoff.Log
	if !cfg.OnOff.Off {
		var err error
		olog, err = onoff.OpenStore(sdb.OnOff(), nil)
		if err != nil {
			log.Printf("riwayat ON/OFF nonaktif: %v", err)
			olog = nil
		} else {
			mgr.OnSwitch = func(id string, t time.Time, st string) { olog.Add(id, t, st) }
			mgr.SinceOf = func(id string) int64 {
				t, _ := olog.Since(id)
				return t
			}
			olog.Touch()
		}
	}

	var plog *powerlog.Registry
	if !cfg.Power.Off {
		ids := cfg.Power.Devices
		if len(ids) == 0 {
			for _, d := range devs {
				if d.Kind == "switches" {
					ids = append(ids, d.ID)
				}
			}
		}
		if len(ids) > 0 {
			pc := cfg.Power
			var err error
			plog, err = powerlog.OpenAll(ids, powerlog.Config{
				NewStore: func(id string) (powerlog.Store, error) {
					return sdb.Power(id), nil
				},
				IdleW: pc.IdleW, CompOnW: pc.CompOnW, CompOffW: pc.CompOffW,
				Debounce:     time.Duration(pc.DebounceS) * time.Second,
				Gap:          time.Duration(pc.GapS) * time.Second,
				RawDays:      pc.RawDays,
				MinuteMonths: pc.MinuteMonths,
			})
			if err != nil {
				log.Printf("log daya nonaktif: %v", err)
				plog = nil
			} else {
				plog.OnUpdate = func(l map[string]powerlog.Live) { hub.Publish("power", l) }
				mgr.OnPower = plog.Record
				log.Printf("log daya aktif untuk %v", ids)
			}
		}
	}
	if !cfg.IoTOnly {
		col.OnUpdate = func(c []netdata.Client) { hub.Publish("net", c) }
	}
	mgr.OnChange = func(s []sonoff.State) { hub.Publish("iot", s) }

	var netFn func() any
	if !cfg.IoTOnly {
		netFn = func() any { return col.Snapshot() }
	}
	// pengaturan estimasi energi di SQLite. Nilai watt/standby_watt di devices.json
	// hanya menjadi isian awal (diimpor sekali); sesudahnya diubah dari tab Detail.
	ecStore := sdb.EnergyCfg()
	seed := map[string]energy.Cfg{}
	for _, d := range devs {
		seed[d.ID] = energy.Cfg{Watt: d.Watt, StandbyW: d.StandbyW}
	}
	ecfg, ecerr := energy.OpenConfig(ecStore, seed)
	if ecerr != nil {
		log.Printf("pengaturan energi: %v (memakai devices.json)", ecerr)
	}
	energyDevs := func() []energy.Dev {
		var ds []energy.Dev
		for _, d := range devs {
			c := energy.Cfg{Watt: d.Watt, StandbyW: d.StandbyW}
			if ecerr == nil {
				c = ecfg.Get(d.ID)
			}
			ds = append(ds, energy.Dev{ID: d.ID, Name: d.Name, Watt: c.Watt, StandbyW: c.StandbyW, Note: c.Note, Metered: d.Kind == "switches"})
		}
		return ds
	}
	var secsFn energy.Seconds
	if olog != nil {
		secsFn = olog.Seconds
	}
	meterFn := func(id string) (*float64, *float64) {
		for _, s := range mgr.States() {
			if s.ID == id {
				return s.DayKWh, s.MonthKWh
			}
		}
		return nil, nil
	}
	// catatan kWh harian dari meter perangkat (POWR) untuk rekap bulanan
	var daily *energy.Daily
	{
		var err error
		if daily, err = energy.OpenDaily(sdb.EnergyDaily()); err != nil {
			log.Printf("energi harian: %v", err)
		}
	}
	var sch *sched.Engine
	if !cfg.Sched.Off {
		file := cfg.Sched.File
		if file == "" {
			file = filepath.Join(filepath.Dir(cfg.DevicesFile), "data", "schedules.json")
		}
		var lat, lon *float64
		if cfg.Location != nil {
			lat, lon = &cfg.Location.Lat, &cfg.Location.Lon
		}
		has := func(id string) (string, bool) {
			for _, s := range mgr.States() {
				if s.ID == id {
					return s.Name, true
				}
			}
			return "", false
		}
		var err error
		sch, err = sched.New(file, loc, lat, lon, mgr.Set, has)
		if err != nil {
			log.Printf("jadwal %s: %v (jadwal dimulai kosong; file tidak ditimpa sampai ada perubahan)", file, err)
		}
		if olog != nil {
			sch.SetNote(func(id, scene string) { olog.NoteCmd(id, "Scene: "+scene) })
		}
		sch.SetStore(sdb.SchedLog()) // log eksekusi scene disimpan di DB (tahan restart)
	}
	var schH http.Handler
	if sch != nil {
		schH = sch.Handler()
	}
	handler := web.New(web.Handlers{
		Sched:   schH,
		Net:     netFn,
		IoTOnly: cfg.IoTOnly,
		IoT:     func() any { return mgr.States() },
		Power: func() any {
			if plog == nil {
				return map[string]powerlog.Live{}
			}
			return plog.Lives()
		},
		PowerHistory: func(id string, hours int) (any, bool) {
			if plog == nil {
				return nil, false
			}
			return plog.History(id, hours)
		},
		SetEnergyCfg: func(id string, watt, standby float64, note string) error {
			if ecerr != nil {
				return errors.New("penyimpanan pengaturan energi tidak tersedia")
			}
			found := false
			for _, d := range devs {
				found = found || d.ID == id
			}
			if !found {
				return web.ErrNotFound
			}
			if err := ecfg.Set(id, energy.Cfg{Watt: watt, StandbyW: standby, Note: note}); err != nil {
				if errors.Is(err, energy.ErrCfg) {
					return fmt.Errorf("%w: %s", web.ErrInvalid, err)
				}
				return err
			}
			return nil
		},
		Energy: func() any {
			return energy.Compute(energyDevs(), secsFn, meterFn, cfg.TariffPerKWh, time.Now(), loc)
		},
		EnergyCompare: func() any {
			var dr energy.DailyRange
			if daily != nil {
				dr = daily.Range
			}
			return energy.Compare(energyDevs(), secsFn, dr, meterFn, cfg.TariffPerKWh, time.Now(), loc)
		},
		EnergyMonthly: func(n int) any {
			var dk energy.DailyKWh
			if daily != nil {
				dk = daily.Month
			}
			return energy.Monthly(energyDevs(), secsFn, dk, meterFn, cfg.TariffPerKWh, time.Now(), loc, n)
		},
		OnOffHistory: func(id string, hours int) (any, bool) {
			if olog == nil {
				return nil, false
			}
			return olog.History(id, hours)
		},
		Configure: func(id string, r web.ConfigReq) error {
			err := mgr.Configure(id, sonoff.ConfigReq{Startup: r.Startup, Pulse: r.Pulse, PulseWidth: r.PulseWidth, LED: r.LED})
			switch {
			case errors.Is(err, sonoff.ErrUnknown):
				return web.ErrNotFound
			case errors.Is(err, sonoff.ErrInvalid):
				return fmt.Errorf("%w: %s", web.ErrInvalid, strings.TrimPrefix(err.Error(), sonoff.ErrInvalid.Error()+": "))
			case err != nil:
				log.Printf("iot %s config: %v", id, err)
			}
			return err
		},
		Control: func(id, cmd string) error {
			if olog != nil {
				olog.NoteCmd(id, "Dashboard")
			}
			err := mgr.Set(id, cmd)
			if errors.Is(err, sonoff.ErrUnknown) {
				return web.ErrNotFound
			}
			if err != nil {
				log.Printf("iot %s %s: %v", id, cmd, err)
			}
			return err
		},
	}, hub)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go col.Run(ctx)
	go mgr.Run(ctx, cfg.LANIface)
	if sch != nil {
		go sch.Run(ctx)
	}
	if daily != nil {
		go func() { // catat kWh harian dari meter tiap menit, simpan tiap 5 menit
			rec := time.NewTicker(time.Minute)
			fl := time.NewTicker(5 * time.Minute)
			defer rec.Stop()
			defer fl.Stop()
			for {
				select {
				case <-ctx.Done():
					daily.Flush(time.Now(), loc)
					return
				case <-rec.C:
					for _, d := range energyDevs() {
						if !d.Metered {
							continue
						}
						if day, _ := meterFn(d.ID); day != nil {
							daily.Record(d.ID, time.Now(), loc, *day)
						}
					}
				case <-fl.C:
					if err := daily.Flush(time.Now(), loc); err != nil {
						log.Printf("energi harian: %v", err)
					}
				}
			}
		}()
	}
	if daily != nil && plog != nil {
		go func() { // isi riwayat kWh harian POWR dari rekap menit (hari yang belum tercatat), lalu segarkan tiap 6 jam
			fill := func() {
				since := time.Now().In(loc).AddDate(0, 0, -62)
				for _, d := range energyDevs() {
					if !d.Metered {
						continue
					}
					if per, ok := plog.DailyKWh(d.ID, since, loc); ok {
						daily.Fill(d.ID, per)
					}
				}
			}
			fill()
			tk := time.NewTicker(6 * time.Hour)
			defer tk.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tk.C:
					fill()
				}
			}
		}()
	}
	if olog != nil {
		go func() { // penanda "dashboard hidup" agar selang saat mati tercatat sebagai gap
			tk := time.NewTicker(5 * time.Minute)
			defer tk.Stop()
			for {
				select {
				case <-ctx.Done():
					olog.Touch()
					return
				case <-tk.C:
					olog.Touch()
				}
			}
		}()
	}
	flushDone := make(chan struct{})
	if plog != nil {
		go func() {
			plog.Run(ctx, time.Duration(cfg.Power.FlushS)*time.Second)
			close(flushDone)
		}()
	} else {
		close(flushDone)
	}

	srv := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sc)
	}()
	if cfg.IoTOnly {
		log.Printf("dashboard IoT di %s (%d perangkat)", cfg.Listen, len(devs))
	} else {
		log.Printf("dashboard di %s (client: %s, IoT: %d perangkat)", cfg.Listen, cfg.Subnet, len(devs))
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-ctx.Done()
	<-flushDone // tunggu log daya selesai menulis ke disk
	if olog != nil {
		olog.Touch()
	}
	sdb.Close()
}
