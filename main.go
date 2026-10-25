// quicssh-punch: fixed-port UDP hole puncher + relay for quicssh.
// One UDP socket per side is used both for STUN/punch and for the QUIC relay,
// so the NAT mapping that gets punched is the one QUIC actually uses.
package main

import (
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	stunMagic  = 0x2112A442
	punchMagic = "PUN0"
)

var (
	port       = flag.Int("port", 0, "fixed UDP port to bind")
	target     = flag.String("target", "", "local host:port to relay to (server mode); empty = client mode")
	topic      = flag.String("topic", "", "rendezvous topic on ntfy.sh")
	role       = flag.String("role", "", "role name, e.g. A or B")
	peerStatic = flag.String("peer", "", "static peer ip:port (skips rendezvous, for testing)")
	stunServer = flag.String("stun", "stun.l.google.com:19302", "STUN server")
)

type endpoint struct {
	ip   string
	port int
}

func (e endpoint) String() string { return fmt.Sprintf("%s:%d", e.ip, e.port) }

func main() {
	flag.Parse()
	if *port == 0 || *role == "" {
		log.Fatal("-port and -role are required")
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: *port})
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	log.Printf("listening on udp/%d", *port)

	var self, peer, clientAddr atomic.Value // string
	if *peerStatic != "" {
		peer.Store(*peerStatic)
	}

	var targetAddr *net.UDPAddr
	if *target != "" {
		if targetAddr, err = net.ResolveUDPAddr("udp", *target); err != nil {
			log.Fatal(err)
		}
	}

	go stunLoop(conn)
	if *topic != "" {
		go rendezvousLoop(&self, &peer)
	}
	go punchLoop(conn, &peer)

	buf := make([]byte, 2000)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Print(err)
			continue
		}
		pkt := buf[:n]

		if isSTUN(pkt) {
			if ep := parseSTUN(pkt); ep != nil {
				s := ep.String()
				if old, _ := self.Load().(string); old != s {
					log.Printf("public endpoint %s", s)
				}
				self.Store(s)
			}
			continue
		}
		if len(pkt) >= 4 && string(pkt[:4]) == punchMagic {
			peer.Store(src.String()) // learn the real mapping from the punch itself
			continue
		}
		if isLocal(src.IP) {
			// local quicssh side (client or server)
			clientAddr.Store(src.String())
			if p, _ := peer.Load().(string); p != "" {
				if a, err := net.ResolveUDPAddr("udp", p); err == nil {
					conn.WriteToUDP(pkt, a)
				}
			}
			continue
		}
		// external: the peer (single-peer relay)
		peer.Store(src.String())
		if targetAddr != nil {
			conn.WriteToUDP(pkt, targetAddr)
		} else if c, _ := clientAddr.Load().(string); c != "" {
			if a, err := net.ResolveUDPAddr("udp", c); err == nil {
				conn.WriteToUDP(pkt, a)
			}
		}
	}
}

func stunLoop(conn *net.UDPConn) {
	raddr, err := net.ResolveUDPAddr("udp", *stunServer)
	if err != nil {
		log.Fatal(err)
	}
	for {
		req := make([]byte, 20)
		binary.BigEndian.PutUint16(req[0:], 0x0001) // binding request
		binary.BigEndian.PutUint32(req[4:], stunMagic)
		rand.Read(req[8:])
		if _, err := conn.WriteToUDP(req, raddr); err != nil {
			log.Print(err)
		}
		time.Sleep(20 * time.Second)
	}
}

func punchLoop(conn *net.UDPConn, peer *atomic.Value) {
	for {
		if p, _ := peer.Load().(string); p != "" {
			if a, err := net.ResolveUDPAddr("udp", p); err == nil {
				conn.WriteToUDP([]byte(punchMagic), a)
			}
		}
		time.Sleep(5 * time.Second)
	}
}

func rendezvousLoop(self, peer *atomic.Value) {
	url := "https://ntfy.sh/" + *topic
	hc := &http.Client{Timeout: 10 * time.Second}
	for {
		// read first, publish second: both peers poll on the same cadence, so
		// each side sees the other's previous post instead of racing its own.
		resp, err := hc.Get(url + "/raw?poll=1&since=10m")
		if err != nil {
			log.Printf("rv get: %v", err)
		} else {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			for i := len(lines) - 1; i >= 0; i-- {
				f := strings.Fields(lines[i])
				if len(f) == 2 && f[0] != *role {
					if _, err := net.ResolveUDPAddr("udp", f[1]); err == nil {
						if cur, _ := peer.Load().(string); cur != f[1] {
							log.Printf("peer endpoint %s", f[1])
						}
						peer.Store(f[1])
					}
					break
				}
			}
		}
		if s, _ := self.Load().(string); s != "" {
			resp, err := hc.Post(url, "text/plain", strings.NewReader(*role+" "+s))
			if err != nil {
				log.Printf("rv post: %v", err)
			} else {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
		time.Sleep(10 * time.Second)
	}
}

func isLocal(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate()
}

func isSTUN(b []byte) bool {
	return len(b) >= 20 && b[0]&0xC0 == 0 && binary.BigEndian.Uint32(b[4:8]) == stunMagic
}

func parseSTUN(b []byte) *endpoint {
	if len(b) < 20 {
		return nil
	}
	total := 20 + int(binary.BigEndian.Uint16(b[2:4]))
	for p := 20; p+4 <= len(b) && p+4 <= total; {
		typ := binary.BigEndian.Uint16(b[p:])
		alen := int(binary.BigEndian.Uint16(b[p+2:]))
		v := b[p+4:]
		if len(v) < alen {
			return nil
		}
		if typ == 0x0020 && alen >= 8 && v[1] == 0x01 { // XOR-MAPPED-ADDRESS
			return &endpoint{
				ip:   net.IPv4(v[4]^0x21, v[5]^0x12, v[6]^0xA4, v[7]^0x42).String(),
				port: int(binary.BigEndian.Uint16(v[2:4]) ^ 0x2112),
			}
		}
		if typ == 0x0001 && alen >= 8 && v[1] == 0x01 { // MAPPED-ADDRESS
			return &endpoint{
				ip:   net.IPv4(v[4], v[5], v[6], v[7]).String(),
				port: int(binary.BigEndian.Uint16(v[2:4])),
			}
		}
		p += 4 + alen + (4-alen%4)%4
	}
	return nil
}
