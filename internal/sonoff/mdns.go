package sonoff

import (
	"net"
	"strings"
)

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// openMulticast bergabung ke grup mDNS di interface LAN dan mengarahkan paket keluar ke interface itu
// (tanpa ini multicast bisa keluar lewat WAN di router).
func openMulticast(name string) (*net.UDPConn, error) {
	var ifi *net.Interface
	if name != "" {
		var err error
		if ifi, err = net.InterfaceByName(name); err != nil {
			return nil, err
		}
	}
	conn, err := net.ListenMulticastUDP("udp4", ifi, mdnsGroup)
	if err != nil {
		return nil, err
	}
	if ifi != nil {
		setMulticastIF(conn, ifi)
	}
	return conn, nil
}

func setMulticastIF(conn *net.UDPConn, ifi *net.Interface) {
	addrs, _ := ifi.Addrs()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip4 := ipn.IP.To4()
		if ip4 == nil {
			continue
		}
		rc, err := conn.SyscallConn()
		if err != nil {
			return
		}
		rc.Control(func(fd uintptr) {
			var b [4]byte
			copy(b[:], ip4)
			setMcastIF(fd, b)
		})
		return
	}
}

// sendQuery menanyakan PTR _ewelink._tcp.local supaya semua perangkat menjawab dengan state terkini.
func sendQuery(conn *net.UDPConn) {
	b := []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0} // header: 1 pertanyaan
	for _, l := range []string{"_ewelink", "_tcp", "local"} {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	b = append(b, 0, 0, 12, 0, 1) // akhir nama, QTYPE=PTR, QCLASS=IN
	conn.WriteToUDP(b, mdnsGroup)
}

type txtRec struct {
	Name string
	Txt  []string
}

// readName membaca nama DNS (mendukung kompresi pointer), mengembalikan nama dan offset berikutnya.
func readName(m []byte, off int) (string, int, bool) {
	var parts []string
	next, jumped := 0, false
	for hops := 0; hops < 32; hops++ {
		if off >= len(m) {
			return "", 0, false
		}
		l := int(m[off])
		switch {
		case l == 0:
			if !jumped {
				next = off + 1
			}
			return strings.Join(parts, "."), next, true
		case l&0xC0 == 0xC0:
			if off+1 >= len(m) {
				return "", 0, false
			}
			if !jumped {
				next = off + 2
				jumped = true
			}
			off = (l&0x3F)<<8 | int(m[off+1])
		default:
			if off+1+l > len(m) {
				return "", 0, false
			}
			parts = append(parts, string(m[off+1:off+1+l]))
			off += 1 + l
		}
	}
	return "", 0, false
}

// txtRecords mengambil semua record TXT dari paket mDNS.
func txtRecords(m []byte) []txtRec {
	if len(m) < 12 {
		return nil
	}
	qd := int(m[4])<<8 | int(m[5])
	rrs := (int(m[6])<<8 | int(m[7])) + (int(m[8])<<8 | int(m[9])) + (int(m[10])<<8 | int(m[11]))
	off := 12
	for i := 0; i < qd; i++ {
		_, n, ok := readName(m, off)
		if !ok {
			return nil
		}
		off = n + 4
	}
	var out []txtRec
	for i := 0; i < rrs; i++ {
		name, n, ok := readName(m, off)
		if !ok || n+10 > len(m) {
			return out
		}
		typ := int(m[n])<<8 | int(m[n+1])
		rdlen := int(m[n+8])<<8 | int(m[n+9])
		start := n + 10
		if start+rdlen > len(m) {
			return out
		}
		if typ == 16 { // TXT
			var strs []string
			for p := start; p < start+rdlen; {
				l := int(m[p])
				if p+1+l > start+rdlen {
					break
				}
				strs = append(strs, string(m[p+1:p+1+l]))
				p += 1 + l
			}
			out = append(out, txtRec{Name: name, Txt: strs})
		}
		off = start + rdlen
	}
	return out
}

func parseTXT(txt []string) map[string]string {
	m := map[string]string{}
	for _, s := range txt {
		if i := strings.IndexByte(s, '='); i > 0 {
			m[s[:i]] = s[i+1:]
		}
	}
	return m
}
