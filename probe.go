package main

import (
	"encoding/binary"
	"log"
	"net"
	"strconv"
	"time"
)

// Round keeps the complete distribution, not merely its average.
type Round struct {
	T     int64     `json:"t"`
	S     int       `json:"s"`
	R     int       `json:"r"`
	MS    []float64 `json:"ms"`
	B     bool      `json:"b,omitempty"`
	Z     float64   `json:"z,omitempty"`
	Error string    `json:"error,omitempty"`
}

func probeParams(t TargetCfg, p ProbeCfg) (interval time.Duration, packets int) {
	iv, pk := p.IntervalSec, p.Packets
	switch t.Pace {
	case "fast":
		iv, pk = 15, 30
	case "slow":
		iv = 300
	}
	if t.IntervalSec > 0 {
		iv = t.IntervalSec
	}
	return time.Duration(iv) * time.Second, pk
}

func probeLoop(t TargetCfg, p ProbeCfg, store *Store, det *Detector, stop chan struct{}) {
	interval, packets := probeParams(t, p)
	gap, timeout := time.Duration(p.GapMs)*time.Millisecond, time.Duration(p.TimeoutMs)*time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		default:
		}
		var r Round
		var err error
		switch t.Type {
		case "icmp":
			r, err = icmpRoundTarget(t, packets, gap, timeout)
		case "tcp":
			r, err = tcpRoundTarget(t, packets, gap, timeout)
		}
		if err != nil {
			log.Printf("[%s] probe error: %v", t.Name, err)
			r = Round{T: time.Now().Unix(), Error: err.Error()}
		}
		select {
		case <-stop:
			return
		default:
		}
		if r.Error == "" {
			r.B, r.Z = det.CheckBurst(t.Name, r)
		}
		if err := store.AppendTarget(t, r); err != nil {
			log.Printf("[%s] write error: %v", t.Name, err)
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

const icmpMagic = "pingpingv1!"

func buildEcho(id, seq int, nonce [4]byte) []byte {
	p := make([]byte, 8+len(icmpMagic)+4)
	p[0] = 8
	binary.BigEndian.PutUint16(p[4:6], uint16(id))
	binary.BigEndian.PutUint16(p[6:8], uint16(seq))
	copy(p[8:], icmpMagic)
	copy(p[8+len(icmpMagic):], nonce[:])
	binary.BigEndian.PutUint16(p[2:4], icmpChecksum(p))
	return p
}
func icmpChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 > 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}
func resolveIPv4(host string) ([4]byte, error) {
	var out [4]byte
	resolved, err := resolveTarget(TargetCfg{Type: "icmp", Host: host}, nil, 5*time.Second)
	if err != nil {
		return out, err
	}
	copy(out[:], net.ParseIP(resolved).To4())
	return out, nil
}
func tcpRound(addr string, packets int, gap, timeout time.Duration) Round {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		r, _ := probeError(err)
		return r
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		r, _ := probeError(err)
		return r
	}
	r, _ := tcpRoundTarget(TargetCfg{Type: "tcp", Host: host, Port: n}, packets, gap, timeout)
	return r
}
