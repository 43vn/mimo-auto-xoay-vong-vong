package sspool

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/shadowsocks/go-shadowsocks2/core"
	"github.com/shadowsocks/go-shadowsocks2/socks"
)

// SSDialer connects to target addresses through a Shadowsocks server.
type SSDialer struct {
	cipher     core.Cipher
	serverAddr string // host:port of the SS server
}

// NewSSDialer creates an SSDialer from an SSConfig.
func NewSSDialer(cfg SSConfig) (*SSDialer, error) {
	cipher, err := core.PickCipher(cfg.Method, nil, cfg.Password)
	if err != nil {
		return nil, fmt.Errorf("pick cipher: %w", err)
	}
	return &SSDialer{
		cipher:     cipher,
		serverAddr: net.JoinHostPort(cfg.Server, strconv.Itoa(cfg.Port)),
	}, nil
}

// DialContext connects to addr through the Shadowsocks server.
// It is compatible with http.Transport.DialContext.
func (d *SSDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// Connect to the SS server
	var nd net.Dialer
	rawConn, err := nd.DialContext(ctx, network, d.serverAddr)
	if err != nil {
		return nil, fmt.Errorf("dial SS server %s: %w", d.serverAddr, err)
	}

	// Wrap with SS encryption
	conn := d.cipher.StreamConn(rawConn)

	// Send target address (Shadowsocks protocol)
	tgt := socks.ParseAddr(addr)
	if tgt == nil {
		conn.Close()
		return nil, fmt.Errorf("invalid target address: %s", addr)
	}
	if _, err := conn.Write(tgt); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send target address: %w", err)
	}

	return conn, nil
}
