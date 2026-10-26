package main

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestIsSTUN(t *testing.T) {
	b := make([]byte, 20)
	binary.BigEndian.PutUint16(b[0:], 0x0001)
	binary.BigEndian.PutUint32(b[4:], stunMagic)
	if !isSTUN(b) {
		t.Fatal("valid binding request not recognized")
	}
	if isSTUN([]byte{1, 2, 3}) {
		t.Fatal("short bytes reported as STUN")
	}
}

func makeSTUN(attrType uint16, port int, ip net.IP, xor bool) []byte {
	b := make([]byte, 32)
	binary.BigEndian.PutUint16(b[0:], 0x0101)
	binary.BigEndian.PutUint16(b[2:], 8)
	binary.BigEndian.PutUint32(b[4:], stunMagic)
	binary.BigEndian.PutUint16(b[20:], attrType)
	binary.BigEndian.PutUint16(b[22:], 8)
	b[24] = 0
	b[25] = 0x01
	p := port
	i := ip.To4()
	if xor {
		binary.BigEndian.PutUint16(b[26:], uint16(p)^0x2112)
		b[28] = i[0] ^ 0x21
		b[29] = i[1] ^ 0x12
		b[30] = i[2] ^ 0xA4
		b[31] = i[3] ^ 0x42
	} else {
		binary.BigEndian.PutUint16(b[26:], uint16(p))
		copy(b[28:], i)
	}
	return b
}

func TestParseSTUN(t *testing.T) {
	xor := makeSTUN(0x0020, 12345, net.IPv4(203, 0, 113, 9), true)
	ep := parseSTUN(xor)
	if ep == nil || ep.ip != "203.0.113.9" || ep.port != 12345 {
		t.Fatalf("xor-mapped: got %+v", ep)
	}
	mapped := makeSTUN(0x0001, 54321, net.IPv4(198, 51, 100, 7), false)
	ep = parseSTUN(mapped)
	if ep == nil || ep.ip != "198.51.100.7" || ep.port != 54321 {
		t.Fatalf("mapped: got %+v", ep)
	}
	if parseSTUN([]byte{1, 2, 3}) != nil {
		t.Fatal("short bytes should return nil")
	}
}

func TestIsLocal(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"192.168.1.1", true},
		{"8.8.8.8", false},
	}
	for _, c := range cases {
		if got := isLocal(net.ParseIP(c.ip)); got != c.want {
			t.Errorf("isLocal(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestPeerFromLines(t *testing.T) {
	body := "A 1.1.1.1:1000\nB 2.2.2.2:2000\nA 3.3.3.3:3000\n"
	if got := peerFromLines(body, "A"); got != "2.2.2.2:2000" {
		t.Fatalf("got %q", got)
	}
	if got := peerFromLines("A 1.1.1.1:1000\n", "A"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := peerFromLines("A not-an-addr\nB 4.4.4.4:4000\n", "A"); got != "4.4.4.4:4000" {
		t.Fatalf("got %q", got)
	}
	// latest valid entry for a peer role wins
	body2 := "B 5.5.5.5:5000\nB 6.6.6.6:6000\n"
	if got := peerFromLines(body2, "A"); got != "6.6.6.6:6000" {
		t.Fatalf("got %q", got)
	}
}

func TestNeedPost(t *testing.T) {
	now := time.Unix(1000, 0)
	hb := time.Hour
	if !needPost("self", "", time.Time{}, now, hb) {
		t.Fatal("first publish should post")
	}
	if needPost("self", "self", now, now, hb) {
		t.Fatal("unchanged within heartbeat should not post")
	}
	if !needPost("new", "self", now, now, hb) {
		t.Fatal("changed endpoint should post")
	}
	if !needPost("self", "self", now.Add(-2*time.Hour), now, hb) {
		t.Fatal("heartbeat elapsed should post")
	}
	if needPost("", "self", now, now, hb) {
		t.Fatal("empty self should not post")
	}
}

func TestFailureShouldWarn(t *testing.T) {
	for _, f := range []int{1, 2, 29, 31, 32} {
		if failureShouldWarn(f) {
			t.Errorf("should not warn at %d", f)
		}
	}
	for _, f := range []int{3, 30, 60} {
		if !failureShouldWarn(f) {
			t.Errorf("should warn at %d", f)
		}
	}
}

func TestPeerSilenceAndPunchWarn(t *testing.T) {
	now := time.Unix(1000, 0)
	peerSince := now.Add(-30 * time.Second)
	if punchShouldWarn(peerSilence(peerSince, 0, now), time.Time{}, now) {
		t.Fatal("should not warn under 2min")
	}
	probe := now.Add(-3 * time.Minute).UnixNano()
	silent := peerSilence(peerSince, probe, now)
	if silent != 3*time.Minute {
		t.Fatalf("silent = %v", silent)
	}
	lastWarn := now.Add(-10 * time.Minute)
	if !punchShouldWarn(silent, lastWarn, now) {
		t.Fatal("should warn after 2min silence")
	}
	if punchShouldWarn(silent, now.Add(-1*time.Minute), now) {
		t.Fatal("should respect 5min re-warn cadence")
	}
}
