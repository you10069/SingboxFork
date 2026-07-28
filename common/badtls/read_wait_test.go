//go:build go1.21 && !without_badtls

package badtls

import (
	stdTLS "crypto/tls"
	"net"
	"testing"

	N "github.com/sagernet/sing/common/network"
)

func TestReadWaitConnReaderReplaceable(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	tlsConn := stdTLS.Client(clientConn, &stdTLS.Config{})
	readWaitConn := &ReadWaitConn{Conn: tlsConn}
	unwrappedConn, loaded := N.CastReader[*stdTLS.Conn](readWaitConn)
	if !loaded {
		t.Fatal("ReadWaitConn must expose its transparent upstream reader")
	}
	if unwrappedConn != tlsConn {
		t.Fatal("ReadWaitConn returned an unexpected upstream TLS connection")
	}
}
