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

// lastPeerProbe is set whenever any packet is received from the peer, so
// punchLoop can tell "punch forming" apart from "punch silently blocked".
var lastPeerProbe atomic.Int64

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
			lastPeerProbe.Store(time.Now().UnixNano())
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
		lastPeerProbe.Store(time.Now().UnixNano())
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
	var seenPeer string
	var peerSince, lastWarn time.Time
	for {
		p, _ := peer.Load().(string)
		if p == "" {
			time.Sleep(5 * time.Second)
			continue
		}
		if p != seenPeer {
			seenPeer, peerSince = p, time.Now()
		}
		if a, err := net.ResolveUDPAddr("udp", p); err == nil {
			conn.WriteToUDP([]byte(punchMagic), a)
		}
		// A known peer with no packets back for minutes means the tunnel is
		// never going to form on its own (NAT/hairpin blocking, peer down).
		silent := time.Since(peerSince)
		if lp := lastPeerProbe.Load(); lp > 0 {
			silent = time.Since(time.Unix(0, lp))
		}
		if silent > 2*time.Minute && time.Since(lastWarn) > 5*time.Minute {
			log.Printf("no packets from peer %s for %v — peer is down or its NAT is dropping our probes", p, silent.Round(time.Minute))
			lastWarn = time.Now()
		}
		time.Sleep(5 * time.Second)
	}
}

func rendezvousLoop(self, peer *atomic.Value) {
	url := "https://ntfy.sh/" + *topic
	hc := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			ForceAttemptHTTP2: false, // pin HTTP/1.1
			DialContext:       (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		},
	}
	var failures int
	var firstFail time.Time
	var lastPost time.Time
	var lastSelf string
	const heartbeat = 1 * time.Hour
	// ntfy.sh free tier: 250 messages/day/visitor (IP) and 1 req/5s after a
	// 60 burst. Posting every 10s burns the daily cap in ~40 min, which is
	// what silenced rendezvous in the first place. Publish only on change or a
	// slow heartbeat, and pull cached messages (12h) so a peer that is
	// currently rate-limited still learns the last published endpoint.
	for {
		good := true
		resp, err := hc.Get(url + "/raw?poll=1&since=12h")
		if err != nil {
			log.Printf("rv get: %v", err)
			good = false
			time.Sleep(30 * time.Second)
		} else if resp.StatusCode == http.StatusTooManyRequests {
			log.Printf("rv get: HTTP 429 (rate limited) — backing off")
			resp.Body.Close()
			good = false
			time.Sleep(60 * time.Second)
		} else if resp.StatusCode != http.StatusOK {
			log.Printf("rv get: HTTP %d", resp.StatusCode)
			resp.Body.Close()
			good = false
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

		s, _ := self.Load().(string)
		if s != "" {
			needPost := s != lastSelf || time.Since(lastPost) >= heartbeat
			if needPost {
				resp, err := hc.Post(url, "text/plain", strings.NewReader(*role+" "+s))
				if err != nil {
					log.Printf("rv post: %v", err)
					good = false
				} else if resp.StatusCode == http.StatusTooManyRequests {
					log.Printf("rv post: HTTP 429 (rate limited) — backing off")
					resp.Body.Close()
					good = false
					time.Sleep(60 * time.Second)
				} else if resp.StatusCode != http.StatusOK {
					log.Printf("rv post: HTTP %d", resp.StatusCode)
					resp.Body.Close()
					good = false
				} else {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					lastSelf, lastPost = s, time.Now()
				}
			}
		}

		if good {
			if failures > 0 {
				log.Printf("rendezvous recovered after %v (%d failed cycles)", time.Since(firstFail).Round(time.Second), failures)
				failures = 0
			}
		} else {
			if failures == 0 {
				firstFail = time.Now()
			}
			failures++
			if failures == 3 || failures%30 == 0 {
				log.Printf("rendezvous failing for %v (%d consecutive cycles)", time.Since(firstFail).Round(time.Second), failures)
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
