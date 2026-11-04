package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"syscall"
	"time"
)

func probeError(err error) (Round, error) {
	return Round{T: time.Now().Unix(), Error: err.Error()}, err
}

func localSource(t TargetCfg) (net.IP, error) {
	if err := validateRouting(t); err != nil {
		return nil, err
	}
	if t.SourceAddress == "" {
		return nil, nil
	}
	ip := net.ParseIP(t.SourceAddress)
	if ip.IsUnspecified() {
		return nil, fmt.Errorf("source address must be assigned locally, not unspecified")
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range addrs {
		local, _, e := net.ParseCIDR(addr.String())
		if e == nil && local.Equal(ip) {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("source address %s is not assigned locally", t.SourceAddress)
}

func tcpRoundTarget(t TargetCfg, packets int, gap, timeout time.Duration) (Round, error) {
	source, err := localSource(t)
	if err != nil {
		return probeError(err)
	}
	if err := validateVRF(t.VRF); err != nil {
		return probeError(err)
	}
	// Keep the hostname intact: Dialer owns address ordering, fallback and the
	// shared timeout. Pre-resolving to one IP silently discards other answers.
	d := net.Dialer{Timeout: timeout, Control: routingControl(t.VRF), Resolver: targetResolver(t, source, timeout)}
	network := "tcp"
	if source != nil {
		if source.To4() != nil {
			network = "tcp4"
		} else {
			network = "tcp6"
		}
	}
	if source != nil {
		d.LocalAddr = &net.TCPAddr{IP: source}
	}
	r := Round{T: time.Now().Unix(), S: packets}
	for i := 0; i < packets; i++ {
		started := time.Now()
		c, err := d.Dial(network, net.JoinHostPort(t.Host, fmt.Sprint(t.Port)))
		if tcpProbeSetupError(err) {
			return probeError(err)
		}
		if err == nil {
			r.R++
			r.MS = append(r.MS, float64(time.Since(started).Microseconds())/1000)
			c.Close()
		}
		if i < packets-1 {
			time.Sleep(gap)
		}
	}
	return r, nil
}

func tcpProbeSetupError(err error) bool {
	var local *routingError
	var se *os.SyscallError
	var dns *net.DNSError
	if errors.As(err, &dns) || errors.As(err, &local) {
		return true
	}
	if errors.As(err, &se) {
		switch se.Syscall {
		case "socket", "setsockopt", "bind":
			return true
		}
	}
	// Resource and permission failures can also surface during connect rather
	// than socket creation. Refusal, timeout and unreachable remain probe loss.
	for _, cause := range []error{syscall.EMFILE, syscall.ENFILE, syscall.ENOMEM, syscall.ENOBUFS, syscall.EACCES, syscall.EPERM} {
		if errors.Is(err, cause) {
			return true
		}
	}
	return false
}

type routingError struct{ error }

func routingControl(vrf string) func(string, string, syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var bindErr error
		err := c.Control(func(fd uintptr) { bindErr = bindVRF(fd, vrf) })
		if err != nil {
			return &routingError{err}
		}
		if bindErr != nil {
			return &routingError{bindErr}
		}
		return nil
	}
}

// A scoped resolver never delegates sockets to the global resolver. Both UDP
// and TCP DNS bind the same source and routing domain as the actual probe.
func targetResolver(t TargetCfg, source net.IP, timeout time.Duration) *net.Resolver {
	if t.VRF == "" && source == nil {
		return net.DefaultResolver
	}
	return &net.Resolver{PreferGo: true, StrictErrors: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		d := net.Dialer{Timeout: timeout, Control: routingControl(t.VRF)}
		if source != nil {
			if network == "tcp" || network == "tcp4" || network == "tcp6" {
				d.LocalAddr = &net.TCPAddr{IP: source}
			} else {
				d.LocalAddr = &net.UDPAddr{IP: source}
			}
		}
		return d.DialContext(ctx, network, address)
	}}
}

func resolveTarget(t TargetCfg, source net.IP, timeout time.Duration) (string, error) {
	family := "ip"
	if t.Type == "icmp" || (source != nil && source.To4() != nil) {
		family = "ip4"
	} else if source != nil {
		family = "ip6"
	}
	if ip, err := netip.ParseAddr(t.Host); err == nil {
		v4 := ip.Unmap().Is4()
		if (family == "ip4" && !v4) || (family == "ip6" && v4) {
			return "", fmt.Errorf("destination and source/probe address families differ")
		}
		return ip.String(), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ips, err := targetResolver(t, source, timeout).LookupIP(ctx, family, t.Host)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", t.Host, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("no %s address for %s", family, t.Host)
	}
	return ips[0].String(), nil
}
