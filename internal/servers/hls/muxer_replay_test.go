package hls

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMuxerReplayStaysOffAfterAServiceRead(t *testing.T) {
	m := &muxer{lastServiceRead: &atomic.Int64{}}
	defer m.addSession(false)()

	m.addSession(true)()
	require.False(t, m.replayAllowed(), "a service samples every few minutes; replay stays off between visits")

	m.lastServiceRead.Store(time.Now().Add(-serviceReplayHold - time.Second).UnixNano())
	require.True(t, m.replayAllowed(), "no service has read the camera for the hold time")

	// a muxer created for the same path after the camera reconnects shares the mark
	m.lastServiceRead.Store(time.Now().UnixNano())
	m2 := &muxer{lastServiceRead: m.lastServiceRead}
	defer m2.addSession(false)()
	require.False(t, m2.replayAllowed())
}

func TestMuxerReplayAllowedOnlyForViewersWithoutServices(t *testing.T) {
	m := &muxer{lastServiceRead: &atomic.Int64{}}
	require.False(t, m.replayAllowed(), "nobody is watching")

	releaseViewer := m.addSession(false)
	require.True(t, m.replayAllowed())

	releaseService := m.addSession(true)
	require.False(t, m.replayAllowed(), "a service such as Sentinel is reading")

	releaseService()
	require.False(t, m.replayAllowed())
	m.lastServiceRead.Store(0)
	require.True(t, m.replayAllowed())

	releaseViewer()
	require.False(t, m.replayAllowed(), "the last viewer left")
}
