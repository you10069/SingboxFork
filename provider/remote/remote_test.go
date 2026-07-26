package remote

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/stretchr/testify/require"
)

type replacementTestOutbound struct {
	tag       string
	dialCount int
}

func (*replacementTestOutbound) Type() string           { return "test" }
func (o *replacementTestOutbound) Tag() string          { return o.tag }
func (*replacementTestOutbound) Network() []string      { return []string{"tcp", "udp"} }
func (*replacementTestOutbound) Dependencies() []string { return nil }
func (o *replacementTestOutbound) DialContext(context.Context, string, M.Socksaddr) (net.Conn, error) {
	o.dialCount++
	return nil, errors.New(o.tag)
}
func (*replacementTestOutbound) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("not implemented")
}

type replacementTestOutboundManager struct {
	byTag           map[string]adapter.Outbound
	defaultOutbound adapter.Outbound
}

func (*replacementTestOutboundManager) Start(adapter.StartStage) error { return nil }
func (*replacementTestOutboundManager) Close() error                   { return nil }
func (m *replacementTestOutboundManager) Outbounds() []adapter.Outbound {
	outbounds := make([]adapter.Outbound, 0, len(m.byTag))
	for _, outbound := range m.byTag {
		outbounds = append(outbounds, outbound)
	}
	return outbounds
}
func (m *replacementTestOutboundManager) Outbound(tag string) (adapter.Outbound, bool) {
	outbound, loaded := m.byTag[tag]
	return outbound, loaded
}
func (m *replacementTestOutboundManager) Default() adapter.Outbound { return m.defaultOutbound }
func (*replacementTestOutboundManager) Remove(string) error         { return errors.New("not implemented") }
func (*replacementTestOutboundManager) Create(context.Context, adapter.Router, log.ContextLogger, string, string, any) error {
	return errors.New("not implemented")
}

func TestDownloadDialerTracksExplicitOutboundReplacement(t *testing.T) {
	first := &replacementTestOutbound{tag: "first"}
	second := &replacementTestOutbound{tag: "second"}
	manager := &replacementTestOutboundManager{
		byTag: map[string]adapter.Outbound{"download": first},
	}
	provider := &ProviderRemote{
		outbound:       manager,
		downloadDetour: "download",
	}

	_, err := provider.dialDownloadContext(context.Background(), "tcp", M.Socksaddr{})
	require.EqualError(t, err, "first")
	manager.byTag["download"] = second
	_, err = provider.dialDownloadContext(context.Background(), "tcp", M.Socksaddr{})
	require.EqualError(t, err, "second")
	require.Equal(t, 1, first.dialCount)
	require.Equal(t, 1, second.dialCount)
}

func TestDownloadDialerTracksDefaultOutboundReplacement(t *testing.T) {
	first := &replacementTestOutbound{tag: "first"}
	second := &replacementTestOutbound{tag: "second"}
	manager := &replacementTestOutboundManager{defaultOutbound: first}
	provider := &ProviderRemote{outbound: manager}

	_, err := provider.dialDownloadContext(context.Background(), "tcp", M.Socksaddr{})
	require.EqualError(t, err, "first")
	manager.defaultOutbound = second
	_, err = provider.dialDownloadContext(context.Background(), "tcp", M.Socksaddr{})
	require.EqualError(t, err, "second")
	require.Equal(t, 1, first.dialCount)
	require.Equal(t, 1, second.dialCount)
}

func TestProviderCacheKeyBindsURLWithoutPlaintext(t *testing.T) {
	first := providerCacheKey("subscription", "https://example.com/sub?token=secret")
	second := providerCacheKey("subscription", "https://example.com/sub?token=other")
	require.NotEqual(t, first, second)
	require.NotContains(t, first, "secret")
	require.NotContains(t, first, "example.com")
}

func TestReadProviderResponseLimit(t *testing.T) {
	_, err := readProviderResponse(bytes.NewReader(make([]byte, maxProviderResponseSize+1)), -1)
	require.Error(t, err)

	content, err := readProviderResponse(bytes.NewReader(make([]byte, maxProviderResponseSize)), maxProviderResponseSize)
	require.NoError(t, err)
	require.Len(t, content, maxProviderResponseSize)
}

func TestScrubProviderURLError(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://example.com/sub?token=secret", Err: errors.New("failed")}
	require.Same(t, err, scrubProviderURLError(err))
	require.Equal(t, "<provider-url>", err.URL)
}

func TestUpdateCachedSubscriptionInfoPreservesBodyWithoutOldInfo(t *testing.T) {
	content := []byte("proxies:\n  - name: node\n    type: vless")
	infoString := "upload=1; download=2; total=3; expire=4"

	updated := updateCachedSubscriptionInfo(content, infoString)

	require.Equal(t, infoString+"\n"+string(content), string(updated))
}

func TestUpdateCachedSubscriptionInfoReplacesOnlyOldInfo(t *testing.T) {
	content := []byte("upload=10; download=20; total=30; expire=40\nproxies:\n  - name: node")
	infoString := "upload=1; download=2; total=3; expire=4"

	updated := updateCachedSubscriptionInfo(content, infoString)

	require.Equal(t, infoString+"\nproxies:\n  - name: node", string(updated))
}

func TestSelectSubscriptionInfoResponseSemantics(t *testing.T) {
	current := adapter.SubscriptionInfo{Upload: 10, Download: 20, Total: 30, Expire: 40}
	next := adapter.SubscriptionInfo{Upload: 1, Download: 2, Total: 3, Expire: 4}

	require.Equal(t, next, selectSubscriptionInfo(current, next, true, false))
	require.Equal(t, current, selectSubscriptionInfo(current, adapter.SubscriptionInfo{}, false, true))
	require.Equal(t, adapter.SubscriptionInfo{}, selectSubscriptionInfo(current, adapter.SubscriptionInfo{}, false, false))
}

func TestNextProviderUpdateDelayUsesLastUpdated(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	updateInterval := 24 * time.Hour

	require.Zero(t, nextProviderUpdateDelay(now, time.Time{}, updateInterval))
	require.Equal(t, updateInterval, nextProviderUpdateDelay(now, now, updateInterval))
	require.Equal(t, time.Minute, nextProviderUpdateDelay(now, now.Add(-23*time.Hour-59*time.Minute), updateInterval))
	require.Zero(t, nextProviderUpdateDelay(now, now.Add(-updateInterval), updateInterval))
	require.Zero(t, nextProviderUpdateDelay(now, now.Add(-48*time.Hour), updateInterval))
	require.Equal(t, updateInterval, nextProviderUpdateDelay(now, now.Add(time.Hour), updateInterval))
}

func TestProviderUpdateRetryDelayBackoffAndLimit(t *testing.T) {
	updateInterval := 24 * time.Hour

	require.Equal(t, time.Minute, providerUpdateRetryDelay(updateInterval, 1))
	require.Equal(t, 2*time.Minute, providerUpdateRetryDelay(updateInterval, 2))
	require.Equal(t, 4*time.Minute, providerUpdateRetryDelay(updateInterval, 3))
	require.Equal(t, 30*time.Minute, providerUpdateRetryDelay(updateInterval, 6))
	require.Equal(t, 30*time.Minute, providerUpdateRetryDelay(updateInterval, 20))
	require.Equal(t, 5*time.Minute, providerUpdateRetryDelay(5*time.Minute, 10))
}
