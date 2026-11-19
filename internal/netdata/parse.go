package netdata

import (
	"encoding/json"
	"net"
	"sort"
	"strconv"
	"strings"
)

type lease struct{ MAC, IP, Host string }

type arpEntry struct{ IP, MAC string }

type wifiInfo struct {
	Band   string
	Signal int
}

// parseLeases membaca /tmp/dhcp.leases: "expiry mac ip hostname clientid".
func parseLeases(s string) []lease {
	var out []lease
	for _, ln := range strings.Split(s, "\n") {
		f := strings.Fields(ln)
		if len(f) < 4 {
			continue
		}
		h := f[3]
		if h == "*" {
			h = ""
		}
		out = append(out, lease{MAC: strings.ToLower(f[1]), IP: f[2], Host: h})
	}
	return out
}

// parseARP membaca /proc/net/arp. Entri valid punya flag 0x2 (dinamis) atau 0x6 (statis).
// Jika iface diisi, hanya entri di interface itu yang dipakai (membuang modem/WAN).
func parseARP(s, iface string) []arpEntry {
	var out []arpEntry
	for i, ln := range strings.Split(s, "\n") {
		f := strings.Fields(ln)
		if i == 0 || len(f) < 4 {
			continue
		}
		fl, err := strconv.ParseUint(f[2], 0, 32)
		mac := strings.ToLower(f[3])
		if err != nil || fl&2 == 0 || mac == "00:00:00:00:00:00" {
			continue
		}
		if iface != "" && len(f) >= 6 && f[5] != iface {
			continue
		}
		out = append(out, arpEntry{IP: f[0], MAC: mac})
	}
	return out
}

// parseHostapd membaca keluaran `ubus call hostapd.<iface> get_clients`.
func parseHostapd(b []byte) (int, map[string]int, error) {
	var r struct {
		Freq    int `json:"freq"`
		Clients map[string]struct {
			Signal int `json:"signal"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return 0, nil, err
	}
	m := map[string]int{}
	for mac, c := range r.Clients {
		m[strings.ToLower(mac)] = c.Signal
	}
	return r.Freq, m, nil
}

func band(f int) string {
	switch {
	case f >= 5925:
		return "6G"
	case f >= 4900:
		return "5G"
	default:
		return "2.4G"
	}
}

// parseIw membaca `iw dev <if> station dump`, mengembalikan MAC -> detik terhubung.
func parseIw(s string) map[string]int64 {
	m := map[string]int64{}
	cur := ""
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(ln, "Station "):
			if f := strings.Fields(ln); len(f) >= 2 {
				cur = strings.ToLower(f[1])
			}
		case strings.HasPrefix(ln, "connected time:") && cur != "":
			if f := strings.Fields(ln); len(f) >= 3 {
				if n, err := strconv.ParseInt(f[2], 10, 64); err == nil {
					m[cur] = n
				}
			}
		}
	}
	return m
}

func ipKey(s string) uint32 {
	ip := net.ParseIP(s).To4()
	if ip == nil {
		return ^uint32(0)
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

// subnetHosts memberi semua alamat host di CIDR (batas /22 sampai /30).
func subnetHosts(cidr string) []string {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil
	}
	ones, bits := n.Mask.Size()
	if bits != 32 || ones < 22 || ones > 30 {
		return nil
	}
	base := ipKey(n.IP.String())
	size := uint32(1) << uint(32-ones)
	out := make([]string, 0, size-2)
	for i := uint32(1); i < size-1; i++ {
		v := base + i
		out = append(out, net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).String())
	}
	return out
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return -1
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

type buildInput struct {
	Leases  []lease
	ARP     []arpEntry
	Wifi    map[string]wifiInfo
	Conn    map[string]int64
	Static  []StaticHost
	Aliases map[string]string
}

// buildRows menyusun satu baris per IP. Satu MAC boleh punya beberapa IP (mis. Intel AMT, VM, alias).
func buildRows(in buildInput) []Client {
	rows := map[string]*Client{}
	hosts := map[string]lease{}
	get := func(ip string) *Client {
		c := rows[ip]
		if c == nil {
			c = &Client{IP: ip, Conn: "LAN", Src: "Static", Connected: -1, RTT: -1}
			rows[ip] = c
		}
		return c
	}
	for _, a := range in.ARP {
		get(a.IP).MAC = a.MAC
	}
	for _, l := range in.Leases {
		c := get(l.IP)
		if c.MAC == "" {
			c.MAC = l.MAC
		}
		c.Src = "DHCP"
		hosts[l.IP] = l
	}
	for _, s := range in.Static {
		c := get(s.IP)
		c.Name = s.Name
		c.Fixed = true
	}
	out := make([]Client, 0, len(rows))
	for ip, c := range rows {
		if c.MAC != "" {
			if w, ok := in.Wifi[c.MAC]; ok {
				c.Conn = "WiFi " + w.Band
				c.Signal, c.HasSignal = w.Signal, true
				if t, ok := in.Conn[c.MAC]; ok {
					c.Connected = t
				}
			}
		}
		if c.Name == "" {
			if n := in.Aliases[c.MAC]; n != "" {
				c.Name = n
			} else if l, ok := hosts[ip]; ok && l.Host != "" && (c.MAC == "" || l.MAC == c.MAC) {
				c.Name = l.Host
			}
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return ipKey(out[i].IP) < ipKey(out[j].IP) })
	return out
}
