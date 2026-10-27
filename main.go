// quicssh-punch: fixed-port UDP hole puncher + relay for quicssh.
// One UDP socket per side is used both for STUN/punch and for the QUIC relay,
// so the NAT mapping that gets punched is the one QUIC actually uses.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	stunMagic = 0x2112A442
)

var (
	port        = flag.Int("port", 0, "fixed UDP port to bind")
	target      = flag.String("target", "", "local host:port to relay to (server mode); empty = client mode")
	topic       = flag.String("topic", "", "rendezvous topic on ntfy.sh")
	role        = flag.String("role", "", "role name, e.g. A or B")
	peerStatic  = flag.String("peer", "", "static peer ip:port (skips rendezvous, for testing)")
	stunServer  = flag.String("stun", "stun.l.google.com:19302", "primary STUN server")
	stunServer2 = flag.String("stun2", "stun1.l.google.com:19302", "secondary STUN server for NAT-type detection")
	relay       = flag.String("relay", "", "relay address used when direct punch stalls")
	secret      = flag.String("secret", "", "shared secret for rendezvous auth and punch obfuscation")
	metricsAddr = flag.String("metrics", "", "HTTP listen address for health/metrics (empty disables)")
)

// punchMagic is derived from secret when set, otherwise the legacy value.
var punchMagic = "PUN0"

const probeReqMagic = "PNG0"
const probeRespMagic = "PON0"

var lastPeerProbe atomic.Int64

type endpoint struct {
	ip   string
	port int
}

func (e endpoint) String() string { return fmt.Sprintf("%s:%d", e.ip, e.port) }

// ---- shared state for features (NAT type, peers, metrics) ----
var (
	natType        atomic.Value
	usingRelay     atomic.Bool
	pendingProbe   atomic.Int64
	peerTableMu    sync.Mutex
	peerTable      = map[string]string{}
	metricPeerPkts atomic.Int64
	metricToPeer   atomic.Int64
	metricLastRTT  atomic.Int64 // microseconds
	metricRVFail   atomic.Int64
)

func main() {
	flag.Parse()
	if *port == 0 || *role == "" {
		log.Fatal("-port and -role are required")
	}
	if *secret != "" {
		h := sha256.Sum256([]byte(*secret))
		punchMagic = string(h[:4])
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: *port})
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	log.Printf("listening on udp/%d", *port)
	natType.Store("unknown")

	var self, peer, clientAddr atomic.Value
	if *peerStatic != "" {
		peer.Store(*peerStatic)
	}

	var targetAddr *net.UDPAddr
	if *target != "" {
		if targetAddr, err = net.ResolveUDPAddr("udp", *target); err != nil {
			log.Fatal(err)
		}
	}

	if *metricsAddr != "" {
		go startMetrics(*metricsAddr)
	}

	go stunLoop(conn)
	if *topic != "" {
		go rendezvousLoop(&self, &peer)
	}
	go punchLoop(conn, &peer)
	go detectNAT(&self)

	buf := make([]byte, 2000)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Print(err)
			continue
		}
		pkt := buf[:n]

		// probe reply (peer -> us)
		if len(pkt) >= 12 && string(pkt[:4]) == probeRespMagic {
			if p := pendingProbe.Swap(0); p > 0 {
				metricLastRTT.Store(time.Since(time.Unix(0, p)).Microseconds())
			}
			lastPeerProbe.Store(time.Now().UnixNano())
			continue
		}
		// probe request (peer -> us): echo timestamp
		if len(pkt) >= 12 && string(pkt[:4]) == probeReqMagic {
			resp := make([]byte, 12)
			copy(resp[:4], probeRespMagic)
			copy(resp[4:], pkt[4:12])
			conn.WriteToUDP(resp, src)
			lastPeerProbe.Store(time.Now().UnixNano())
			continue
		}

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
			if usingRelay.Load() && *relay != "" && src.String() != *relay {
				usingRelay.Store(false)
				log.Printf("direct peer resumed: %s", src.String())
			}
			continue
		}
		if isLocal(src.IP) {
			// local quicssh side (client or server)
			clientAddr.Store(src.String())
			metricToPeer.Add(1)
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
		metricPeerPkts.Add(1)
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

func stunProbe(server string) (*endpoint, error) {
	raddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	req := make([]byte, 20)
	binary.BigEndian.PutUint16(req[0:], 0x0001)
	binary.BigEndian.PutUint32(req[4:], stunMagic)
	rand.Read(req[8:])
	if _, err := conn.WriteToUDP(req, raddr); err != nil {
		return nil, err
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2000)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		return nil, err
	}
	if !isSTUN(buf[:n]) {
		return nil, fmt.Errorf("bad stun response")
	}
	ep := parseSTUN(buf[:n])
	if ep == nil {
		return nil, fmt.Errorf("no mapped address")
	}
	return ep, nil
}

func detectNAT(self *atomic.Value) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := self.Load().(string); s != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	s, _ := self.Load().(string)
	if s == "" {
		natType.Store("unknown")
		return
	}
	sec, err := stunProbe(*stunServer2)
	if err != nil {
		log.Printf("nat detect: %v", err)
		natType.Store("unknown")
		return
	}
	if sec.String() == s {
		natType.Store("cone")
	} else {
		natType.Store("symmetric/restricted")
	}
	log.Printf("NAT type: %s (primary=%s secondary=%s)", natType.Load(), s, sec.String())
}

func punchLoop(conn *net.UDPConn, peer *atomic.Value) {
	var seenPeer string
	var peerSince, lastWarn, lastProbe time.Time
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
		now := time.Now()
		// periodic latency probe
		if now.Sub(lastProbe) >= 30*time.Second {
			if a, err := net.ResolveUDPAddr("udp", p); err == nil {
				ts := now.UnixNano()
				buf := make([]byte, 12)
				copy(buf[:4], probeReqMagic)
				binary.BigEndian.PutUint64(buf[4:], uint64(ts))
				if _, err := conn.WriteToUDP(buf, a); err == nil {
					pendingProbe.Store(ts)
					lastProbe = now
				}
			}
		}
		// relay fallback
		silent := peerSilence(peerSince, lastPeerProbe.Load(), now)
		if *relay != "" && !usingRelay.Load() && silent > 2*time.Minute {
			log.Printf("direct punch stalled; falling back to relay %s", *relay)
			peer.Store(*relay)
			usingRelay.Store(true)
		}
		if punchShouldWarn(silent, lastWarn, now) {
			log.Printf("no packets from peer %s for %v — peer is down or its NAT is dropping our probes", p, silent.Round(time.Minute))
			lastWarn = now
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
			updatePeerTable(string(body), *role, *secret)
			if ep := peerFromLines(string(body), *role, *secret); ep != "" {
				if cur, _ := peer.Load().(string); cur != ep {
					log.Printf("peer endpoint %s", ep)
				}
				peer.Store(ep)
			}
		}

		s, _ := self.Load().(string)
		if s != "" {
			if needPost(s, lastSelf, lastPost, time.Now(), heartbeat) {
				msg := *role + " " + s
				if *secret != "" {
					msg += " " + signMessage(*secret, *role, s)
				}
				resp, err := hc.Post(url, "text/plain", strings.NewReader(msg))
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
				metricRVFail.Store(0)
			}
		} else {
			if failures == 0 {
				firstFail = time.Now()
			}
			failures++
			metricRVFail.Store(int64(failures))
			if failureShouldWarn(failures) {
				log.Printf("rendezvous failing for %v (%d consecutive cycles)", time.Since(firstFail).Round(time.Second), failures)
			}
		}
		time.Sleep(10 * time.Second)
	}
}

func startMetrics(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snap := map[string]any{
			"nat_type":            natType.Load(),
			"peer":                "",
			"using_relay":         usingRelay.Load(),
			"packets_from_peer":   metricPeerPkts.Load(),
			"packets_to_peer":     metricToPeer.Load(),
			"last_peer_packet":    lastPeerProbe.Load(),
			"last_rtt_us":         metricLastRTT.Load(),
			"peer_count":          len(peerTableSnapshot()),
			"rendezvous_failures": metricRVFail.Load(),
		}
		_ = json.NewEncoder(w).Encode(snap)
	})
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("metrics: %v", err)
	}
}

func peerTableSnapshot() map[string]string {
	peerTableMu.Lock()
	defer peerTableMu.Unlock()
	out := make(map[string]string, len(peerTable))
	for k, v := range peerTable {
		out[k] = v
	}
	return out
}

func updatePeerTable(body, role, secret string) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	peerTableMu.Lock()
	defer peerTableMu.Unlock()
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == role {
			continue
		}
		if _, err := net.ResolveUDPAddr("udp", f[1]); err != nil {
			continue
		}
		if secret != "" {
			if len(f) < 3 || !validSig(secret, f[0], f[1], f[2]) {
				continue
			}
		}
		peerTable[f[0]] = f[1]
	}
}

func signMessage(secret, role, ep string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(role + " " + ep))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

func validSig(secret, role, ep, sig string) bool {
	if len(sig) != 16 {
		return false
	}
	return hmac.Equal([]byte(signMessage(secret, role, ep)), []byte(sig))
}

func peerFromLines(body, role, secret string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		f := strings.Fields(lines[i])
		if len(f) < 2 || f[0] == role {
			continue
		}
		if _, err := net.ResolveUDPAddr("udp", f[1]); err != nil {
			continue
		}
		if secret != "" {
			if len(f) < 3 || !validSig(secret, f[0], f[1], f[2]) {
				continue
			}
		}
		return f[1]
	}
	return ""
}

func needPost(self, lastSelf string, lastPost, now time.Time, heartbeat time.Duration) bool {
	return self != "" && (self != lastSelf || now.Sub(lastPost) >= heartbeat)
}

func failureShouldWarn(failures int) bool {
	return failures == 3 || (failures > 0 && failures%30 == 0)
}

func peerSilence(peerSince time.Time, lastProbe int64, now time.Time) time.Duration {
	silent := now.Sub(peerSince)
	if lastProbe > 0 {
		silent = now.Sub(time.Unix(0, lastProbe))
	}
	return silent
}

func punchShouldWarn(silent time.Duration, lastWarn, now time.Time) bool {
	return silent > 2*time.Minute && now.Sub(lastWarn) > 5*time.Minute
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
