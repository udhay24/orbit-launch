package hls

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMuxerReplayAllowedOnlyForViewersWithoutServices(t *testing.T) {
	m := &muxer{}
	require.False(t, m.replayAllowed(), "nobody is watching")

	releaseViewer := m.addSession(false)
	require.True(t, m.replayAllowed())

	releaseService := m.addSession(true)
	require.False(t, m.replayAllowed(), "a service such as Sentinel is reading")

	releaseService()
	require.True(t, m.replayAllowed())

	releaseViewer()
	require.False(t, m.replayAllowed(), "the last viewer left")
}
