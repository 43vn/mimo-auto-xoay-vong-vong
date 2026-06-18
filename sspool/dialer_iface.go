package sspool

import (
	"context"
	"net"
)

// ProxyDialer is the common interface for all proxy dialers (SS, HTTP, SOCKS5).
// It provides DialContext compatible with http.Transport.DialContext.
type ProxyDialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}