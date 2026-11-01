package conf

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactRedisPasswords(t *testing.T) {
	c, _, err := Load("", nil, nil)
	require.NoError(t, err)

	c.StreamRegistryPassword = "registry-secret"
	c.PublishAllowlistPassword = "allowlist-secret"

	redacted := Redact(c)
	require.Equal(t, redactedCredential, redacted.StreamRegistryPassword)
	require.Equal(t, redactedCredential, redacted.PublishAllowlistPassword)
	require.Equal(t, "registry-secret", c.StreamRegistryPassword)

	// sending back the output of /config/global/get must not overwrite the real passwords.
	var optional OptionalGlobal
	require.NoError(t, optional.UnmarshalJSON(
		[]byte(`{"streamRegistryPassword":"<redacted>","publishAllowlistPassword":"<redacted>"}`)))
	require.NoError(t, c.PatchGlobal(&optional))
	require.Equal(t, "registry-secret", c.StreamRegistryPassword)
	require.Equal(t, "allowlist-secret", c.PublishAllowlistPassword)
}
