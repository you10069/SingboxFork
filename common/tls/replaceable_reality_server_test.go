//go:build with_reality_server && with_utls

package tls

import (
	"testing"

	utls "github.com/metacubex/utls"
	N "github.com/sagernet/sing/common/network"
)

func TestRealityServerWrapperReplaceable(t *testing.T) {
	realityConn := new(utls.Conn)
	wrapper := &realityConnWrapper{Conn: realityConn}

	reader, loaded := N.CastReader[*utls.Conn](wrapper)
	if !loaded || reader != realityConn {
		t.Fatal("REALITY server wrapper must expose its transparent upstream reader")
	}
	writer, loaded := N.CastWriter[*utls.Conn](wrapper)
	if !loaded || writer != realityConn {
		t.Fatal("REALITY server wrapper must expose its transparent upstream writer")
	}
}
