//go:build linux

package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// Inspect IFLA_LINKINFO/IFLA_INFO_KIND rather than trusting an interface name.
func validateVRF(name string) error {
	if name == "" {
		return nil
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return fmt.Errorf("VRF %q: %w", name, err)
	}
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETLINK, syscall.AF_UNSPEC)
	if err != nil {
		return err
	}
	msgs, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if m.Header.Type != syscall.RTM_NEWLINK || len(m.Data) < syscall.SizeofIfInfomsg {
			continue
		}
		if int(binary.NativeEndian.Uint32(m.Data[4:8])) != iface.Index {
			continue
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(&m)
		if err != nil {
			return err
		}
		for _, a := range attrs {
			if a.Attr.Type&0x3fff != 18 {
				continue
			} // IFLA_LINKINFO
			nested := syscall.NetlinkMessage{Header: syscall.NlMsghdr{Type: syscall.RTM_NEWLINK}, Data: append(make([]byte, syscall.SizeofIfInfomsg), a.Value...)}
			info, err := syscall.ParseNetlinkRouteAttr(&nested)
			if err != nil {
				return err
			}
			for _, v := range info {
				if v.Attr.Type&0x3fff == 1 && strings.TrimRight(string(v.Value), "\x00") == "vrf" {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("interface %q is not a VRF device", name)
}

func bindVRF(fd uintptr, name string) error {
	if name == "" {
		return nil
	}
	if err := validateVRF(name); err != nil {
		return err
	}
	if err := syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, name); err != nil {
		return fmt.Errorf("bind VRF %q: %w", name, err)
	}
	return nil
}
