package netdata

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

/* ---------- WiFi: konek/putus seketika lewat `ubus subscribe hostapd.<iface>` ---------- */

// wifiChangeEvents: kejadian hostapd yang berarti daftar client WiFi berubah. "probe" dan "auth" diabaikan
// (sangat sering, tidak mengubah daftar).
var wifiChangeEvents = map[string]bool{"assoc": true, "disassoc": true, "deauth": true, "local-deauth": true, "inactive-deauth": true}

// isWifiChange: obj adalah satu objek JSON dari `ubus subscribe`, mis. {"assoc":{"address":"..."}}.
func isWifiChange(obj map[string]json.RawMessage) bool {
	for k := range obj {
		if wifiChangeEvents[k] {
			return true
		}
	}
	return false
}

// kick meminta siklus baca segera (tidak memblokir; permintaan beruntun digabung).
func (c *Collector) kick() {
	select {
	case c.kickCh <- struct{}{}:
	default:
	}
}

// watchWifi menjaga satu proses `ubus subscribe` per interface hostapd. Interface baru ditemukan tiap menit.
func (c *Collector) watchWifi(ctx context.Context) {
	running := map[string]bool{}
	var mu sync.Mutex
	for {
		if out, err := sh("ubus", "list"); err == nil {
			for _, name := range strings.Fields(out) {
				if !strings.HasPrefix(name, "hostapd.") {
					continue
				}
				mu.Lock()
				ok := running[name]
				running[name] = true
				mu.Unlock()
				if !ok {
					go func(name string) {
						c.subscribe(ctx, name)
						mu.Lock()
						delete(running, name)
						mu.Unlock()
					}(name)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(60 * time.Second):
		}
	}
}

// subscribe berjalan sampai proses berakhir (mis. hostapd restart) atau ctx dibatalkan.
func (c *Collector) subscribe(ctx context.Context, name string) {
	cmd := exec.CommandContext(ctx, "ubus", "subscribe", name)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("netdata: ubus subscribe %s: %v", name, err)
		return
	}
	c.kick() // klien yang sudah konek sebelum berlangganan belum tercatat
	dec := json.NewDecoder(bufio.NewReader(out))
	for {
		var obj map[string]json.RawMessage
		if err := dec.Decode(&obj); err != nil {
			break
		}
		if isWifiChange(obj) {
			c.kick()
		}
	}
	cmd.Wait()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
}

/* ---------- Kabel: periksa host tetap lewat ARP ---------- */

// probeWired memeriksa tiap IP di WiredHosts lewat ARP dan mengembalikan IP yang menjawab.
// Bila program `arping` ada, dipakai (jawaban seketika). Jika tidak: kirim paket UDP kecil supaya kernel
// menanyakan ARP, lalu baca /proc/net/arp (entri yang gagal diperbarui turun ke flag 0x0 dalam beberapa detik).
func (c *Collector) probeWired() map[string]bool {
	up := map[string]bool{}
	ips := c.cfg.WiredHosts
	if len(ips) == 0 {
		return up
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	if c.arping != "" {
		for _, ip := range ips {
			wg.Add(1)
			go func(ip string) {
				defer wg.Done()
				args := []string{"-q", "-c", "1", "-w", "1"}
				if c.cfg.LANIface != "" {
					args = append(args, "-I", c.cfg.LANIface)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if exec.CommandContext(ctx, c.arping, append(args, ip)...).Run() == nil {
					mu.Lock()
					up[ip] = true
					mu.Unlock()
				}
			}(ip)
		}
		wg.Wait()
		return up
	}
	for _, ip := range ips {
		if conn, err := net.DialTimeout("udp", net.JoinHostPort(ip, "9"), 200*time.Millisecond); err == nil {
			conn.Write([]byte{0})
			conn.Close()
		}
	}
	time.Sleep(600 * time.Millisecond)
	raw, _ := os.ReadFile(c.cfg.ARPFile)
	for _, e := range parseARP(string(raw), c.cfg.LANIface) {
		up[e.IP] = true
	}
	for _, ip := range ips { // hanya yang diminta
		if !up[ip] {
			delete(up, ip)
		}
	}
	return up
}
