package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"owrt-dashboard/internal/energy"
	"owrt-dashboard/internal/onoff"
	"owrt-dashboard/internal/powerlog"
	"owrt-dashboard/internal/sqlstore"
)

// runImport menggabungkan data CSV lama (folder srcData, mis. /opt/dashboard/data) ke SQLite ini.
// Hanya mengisi riwayat yang lebih tua dari data yang sudah ada; tidak menghapus/menimpa apa pun.
// Mengembalikan kode keluar proses.
func runImport(sdb *sqlstore.DB, srcData, dailyDst string, dry bool) int {
	mode := "GABUNG"
	if dry {
		mode = "SIMULASI (tidak ada yang ditulis)"
	}
	fmt.Printf("%s dari %s\n", mode, srcData)
	bad := 0

	if dir := filepath.Join(srcData, "onoff"); isDir(dir) {
		fs, err := onoff.NewFileStore(dir)
		if err != nil {
			fmt.Printf("ON/OFF: %v\n", err)
			bad++
		} else if res, err := sdb.MergeOnOff(fs, dry); err != nil {
			fmt.Printf("ON/OFF gagal: %v\n", err)
			bad++
		} else {
			total := 0
			ids := make([]string, 0, len(res))
			for id := range res {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				fmt.Printf("  ON/OFF %-12s +%d kejadian\n", id, res[id])
				total += res[id]
			}
			fmt.Printf("ON/OFF total: %d kejadian\n", total)
		}
	} else {
		fmt.Printf("ON/OFF: folder %s tidak ada, dilewati\n", dir)
	}

	pdir := filepath.Join(srcData, "power")
	ents, _ := os.ReadDir(pdir)
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		fs, err := powerlog.NewFileStore(filepath.Join(pdir, e.Name()))
		if err != nil {
			fmt.Printf("daya %s: %v\n", e.Name(), err)
			bad++
			continue
		}
		r, err := sdb.MergePower(e.Name(), fs, dry)
		if err != nil {
			fmt.Printf("daya %s gagal: %v\n", e.Name(), err)
			bad++
			continue
		}
		fmt.Printf("  daya %-12s +%d kejadian, +%d menit, +%d mentah\n", e.Name(), r.Events, r.Minutes, r.Raw)
	}
	if len(ents) == 0 {
		fmt.Printf("daya: folder %s kosong / tidak ada, dilewati\n", pdir)
	}

	if src := filepath.Join(srcData, "energy-daily.csv"); fileExists(src) {
		s, err1 := energy.OpenDaily(src)
		d, err2 := energy.OpenDaily(dailyDst)
		if err1 != nil || err2 != nil {
			fmt.Printf("kWh harian: %v %v\n", err1, err2)
			bad++
		} else {
			n := d.MergeFrom(s)
			if !dry && n > 0 {
				if err := d.Flush(time.Now(), time.Local); err != nil {
					fmt.Printf("kWh harian gagal disimpan: %v\n", err)
					bad++
				}
			}
			fmt.Printf("kWh harian: %d hari baru/lebih besar\n", n)
		}
	}
	fmt.Println("Catatan: schedules.json tidak digabung (salin manual bila perlu).")
	if bad > 0 {
		fmt.Printf("SELESAI dengan %d kesalahan\n", bad)
		return 1
	}
	fmt.Println("SELESAI")
	return 0
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
