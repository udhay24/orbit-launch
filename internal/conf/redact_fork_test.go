package conf

import (
	"os"
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

func TestViewTokenOwnerCheckRequirements(t *testing.T) {
	load := func(content string) error {
		tmp, err := os.CreateTemp(t.TempDir(), "mediamtx*.yml")
		require.NoError(t, err)
		_, err = tmp.WriteString(content)
		require.NoError(t, err)
		require.NoError(t, tmp.Close())
		_, _, err = Load(tmp.Name(), nil, nil)
		return err
	}

	require.ErrorContains(t, load("viewTokenOwnerCheck: yes\nauthMethod: jwt\nauthJWTJWKS: http://x\n"), "publishAllowlist")
	require.ErrorContains(t, load("viewTokenOwnerCheck: yes\npublishAllowlist: yes\n"), "authMethod")
	require.ErrorContains(t, load("viewTokenOwnerCheck: yes\npublishAllowlist: yes\nauthMethod: jwt\nauthJWTJWKS: http://x\nplayback: yes\n"), "playback")
	require.NoError(t, load("viewTokenOwnerCheck: yes\npublishAllowlist: yes\nauthMethod: jwt\nauthJWTJWKS: http://x\n"))
}
