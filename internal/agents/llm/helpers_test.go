package llm

import "net"

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panic("bad ip " + s)
	}
	return ip
}
