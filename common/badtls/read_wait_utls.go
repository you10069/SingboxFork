//go:build go1.21 && !without_badtls && with_utls

package badtls

import (
	"net"
	_ "unsafe"

	tls "github.com/metacubex/utls"
	"github.com/sagernet/sing/common"
)

func init() {
	tlsRegistry = append(tlsRegistry, func(conn net.Conn) (loaded bool, tlsReadRecord func() error, tlsHandlePostHandshakeMessage func() error) {
		uConn, loaded := common.Cast[*tls.UConn](conn)
		if loaded {
			return true, func() error {
					return utlsReadRecord(uConn.Conn)
				}, func() error {
					return utlsHandlePostHandshakeMessage(uConn.Conn)
				}
		}
		tlsConn, loaded := common.Cast[*tls.Conn](conn)
		if loaded {
			return true, func() error {
					return utlsReadRecord(tlsConn)
				}, func() error {
					return utlsHandlePostHandshakeMessage(tlsConn)
				}
		}
		return
	})
}

//go:linkname utlsReadRecord github.com/metacubex/utls.(*Conn).readRecord
func utlsReadRecord(c *tls.Conn) error

//go:linkname utlsHandlePostHandshakeMessage github.com/metacubex/utls.(*Conn).handlePostHandshakeMessage
func utlsHandlePostHandshakeMessage(c *tls.Conn) error
