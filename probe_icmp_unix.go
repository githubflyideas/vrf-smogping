//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func icmpRound(host string, packets int, gap, timeout time.Duration) (Round, error) {
	return icmpRoundTarget(TargetCfg{Type: "icmp", Host: host}, packets, gap, timeout)
}

func icmpRoundTarget(t TargetCfg, packets int, gap, timeout time.Duration) (Round, error) {
	t.Type = "icmp"
	source, err := localSource(t)
	if err != nil {
		return probeError(err)
	}
	if err := validateVRF(t.VRF); err != nil {
		return probeError(err)
	}
	host, err := resolveTarget(t, source, timeout)
	if err != nil {
		return probeError(err)
	}
	var ip [4]byte
	copy(ip[:], net.ParseIP(host).To4())

	fd, raw, err := icmpSocket()
	if err != nil {
		return probeError(fmt.Errorf("cannot create ICMP socket (requires ping_group_range or cap_net_raw): %w", err))
	}
	defer syscall.Close(fd)
	if err := bindVRF(uintptr(fd), t.VRF); err != nil {
		return probeError(err)
	}
	if source != nil {
		var addr [4]byte
		copy(addr[:], source.To4())
		if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: addr}); err != nil {
			return probeError(fmt.Errorf("bind source %s: %w", source, err))
		}
	}
	r := Round{T: time.Now().Unix(), S: packets}

	id := os.Getpid() & 0xffff
	var nonce [4]byte
	rand.Read(nonce[:])
	dst := &syscall.SockaddrInet4{Addr: ip}

	sendAt := make([]time.Time, packets)
	got := make([]bool, packets)

	for i := 0; i < packets; i++ {
		pkt := buildEcho(id, i, nonce)
		sendAt[i] = time.Now()
		if err := syscall.Sendto(fd, pkt, 0, dst); err != nil {
			if err == syscall.EADDRNOTAVAIL || err == syscall.ENODEV || err == syscall.EPERM || err == syscall.EACCES {
				return probeError(fmt.Errorf("ICMP send: %w", err))
			}
			continue // 单包发送失败不终止整轮
		}
		collect(fd, raw, id, nonce, sendAt, got, &r, time.Now().Add(gap))
	}
	// 尾窗:等最后一批在途包
	collect(fd, raw, id, nonce, sendAt, got, &r, time.Now().Add(timeout))
	return r, nil
}

func icmpSocket() (fd int, raw bool, err error) {
	fd, err = syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_ICMP)
	if err == nil {
		return fd, false, nil
	}
	fd, err = syscall.Socket(syscall.AF_INET, syscall.SOCK_RAW, syscall.IPPROTO_ICMP)
	return fd, true, err
}

func collect(fd int, raw bool, id int, nonce [4]byte, sendAt []time.Time, got []bool, r *Round, deadline time.Time) {
	buf := make([]byte, 1500)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			return
		}
		wait := remain
		if wait > 50*time.Millisecond {
			wait = 50 * time.Millisecond // 50ms 一跳,保证按时退出
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		tv := syscall.NsecToTimeval(wait.Nanoseconds()) // 可移植:linux/darwin 的 Timeval 字段类型不同
		syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		now := time.Now()
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK || err == syscall.EINTR {
				continue
			}
			return
		}
		pkt := buf[:n]
		if raw && n >= 20 { // raw socket 带 IP 头,剥掉
			ihl := int(pkt[0]&0x0f) * 4
			if n <= ihl {
				continue
			}
			pkt = pkt[ihl:n]
		}
		if len(pkt) < 8+len(icmpMagic)+4 || pkt[0] != 0 { // 只认 echo reply
			continue
		}
		// raw socket 会收到本机所有 ICMP 回包,靠 id+nonce 隔离并发目标
		if raw && int(binary.BigEndian.Uint16(pkt[4:6])) != id {
			continue
		}
		if string(pkt[8:8+len(icmpMagic)]) != icmpMagic ||
			string(pkt[8+len(icmpMagic):8+len(icmpMagic)+4]) != string(nonce[:]) {
			continue
		}
		seq := int(binary.BigEndian.Uint16(pkt[6:8]))
		if seq < 0 || seq >= len(sendAt) || got[seq] {
			continue
		}
		got[seq] = true
		r.R++
		r.MS = append(r.MS, float64(now.Sub(sendAt[seq]).Microseconds())/1000.0)
	}
}
