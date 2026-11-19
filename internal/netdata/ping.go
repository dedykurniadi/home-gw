package netdata

import (
	"net"
	"os"
	"sync"
	"time"
)

// Pinger mengirim ICMP echo lewat satu raw socket (butuh root).
type Pinger struct {
	mu    sync.Mutex
	conn  net.PacketConn
	calls int
}

func NewPinger() (*Pinger, error) {
	c, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, err
	}
	return &Pinger{conn: c}, nil
}

func (p *Pinger) Close() error { return p.conn.Close() }

func checksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return ^uint16(s)
}

func echo(id, seq int) []byte {
	b := []byte{8, 0, 0, 0, byte(id >> 8), byte(id), byte(seq >> 8), byte(seq), 's', 'o', 'n', 'o', 'f', 'f'}
	c := checksum(b)
	b[2], b[3] = byte(c>>8), byte(c)
	return b
}

// Ping mengirim echo ke semua ip. Pada tiap putaran hanya host yang belum menjawab dicoba lagi,
// lalu menunggu paling lama `wait`. Hasil: ip -> RTT untuk yang menjawab.
// Identifier ICMP diganti per panggilan supaya balasan telat dari panggilan lama diabaikan.
func (p *Pinger) Ping(ips []string, rounds int, wait time.Duration) map[string]time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	id := (os.Getpid() + p.calls) & 0xffff
	res := map[string]time.Duration{}
	if rounds < 1 {
		rounds = 1
	}
	if rounds > 15 {
		rounds = 15
	}
	sentAt := make([]map[string]time.Time, rounds)
	for r := range sentAt {
		sentAt[r] = map[string]time.Time{}
	}
	buf := make([]byte, 1500)
	for r := 0; r < rounds; r++ {
		for i, ip := range ips {
			if _, ok := res[ip]; ok {
				continue
			}
			dst := net.ParseIP(ip)
			if dst == nil {
				continue
			}
			sentAt[r][ip] = time.Now()
			p.conn.WriteTo(echo(id, r<<12|(i&0xfff)), &net.IPAddr{IP: dst})
		}
		p.conn.SetReadDeadline(time.Now().Add(wait))
		for len(res) < len(ips) {
			n, from, err := p.conn.ReadFrom(buf)
			if err != nil {
				break
			}
			b := buf[:n]
			if n >= 20 && b[0]>>4 == 4 { // buang header IP kalau ikut terbawa
				b = b[int(b[0]&0x0f)*4:]
			}
			if len(b) < 8 || b[0] != 0 || int(b[4])<<8|int(b[5]) != id {
				continue
			}
			round := (int(b[6])<<8 | int(b[7])) >> 12
			if round >= rounds {
				continue
			}
			ip := from.String()
			if t, ok := sentAt[round][ip]; ok {
				if _, done := res[ip]; !done {
					res[ip] = time.Since(t)
				}
			}
		}
		if len(res) == len(ips) {
			break
		}
	}
	return res
}
