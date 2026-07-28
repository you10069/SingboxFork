//go:build with_utls

package tls

import (
	"net"
	"testing"

	utls "github.com/metacubex/utls"
	N "github.com/sagernet/sing/common/network"
)

func TestUTLSClientWrappersReplaceable(t *testing.T) {
	uConn := new(utls.UConn)
	testUTLSClientWrapperReplaceable(t, "uTLS", &utlsConnWrapper{UConn: uConn}, uConn)
	testUTLSClientWrapperReplaceable(t, "REALITY client", &realityClientConnWrapper{UConn: uConn}, uConn)
}

func testUTLSClientWrapperReplaceable(t *testing.T, name string, wrapper net.Conn, expected *utls.UConn) {
	t.Helper()

	reader, loaded := N.CastReader[*utls.UConn](wrapper)
	if !loaded || reader != expected {
		t.Fatalf("%s wrapper must expose its transparent upstream reader", name)
	}
	writer, loaded := N.CastWriter[*utls.UConn](wrapper)
	if !loaded || writer != expected {
		t.Fatalf("%s wrapper must expose its transparent upstream writer", name)
	}
}
