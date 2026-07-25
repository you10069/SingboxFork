//go:build with_utls

package tls

import "testing"

// REALITY owns the ClientHello session ID because it carries authentication
// material. ShadowTLS must therefore not be able to replace it through the
// generic WithSessionIDGenerator interface.
func TestRealityClientConfigDoesNotExposeSessionIDGenerator(t *testing.T) {
	var config any = (*RealityClientConfig)(nil)
	if _, loaded := config.(WithSessionIDGenerator); loaded {
		t.Fatal("REALITY client must not implement WithSessionIDGenerator")
	}
}
