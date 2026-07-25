package constant

const (
	ProviderTypeInline = "inline"
	ProviderTypeLocal  = "local"
	ProviderTypeRemote = "remote"
)

func ProviderDisplayName(providerType string) string {
	switch providerType {
	case ProviderTypeInline:
		return "Compatible"
	case ProviderTypeLocal:
		return "File"
	case ProviderTypeRemote:
		return "HTTP"
	default:
		return "Unknown"
	}
}
