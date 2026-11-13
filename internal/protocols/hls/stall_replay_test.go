package hls

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testPayload struct {
	id  int
	key bool
}

type testOut struct {
	track   int
	pts     int64
	payload testPayload
	live    bool // written as live video, not by the replay
}

const (
	testVideoRate = 90000
	testAudioRate = 48000
)

// testCamera sends 25 fps video with a keyframe every 2s and audio every 20ms,
// moving a fake clock along with the media.
type testCamera struct {
	t       *testing.T
	sr      *StallReplay
	clock   time.Time
	video   trackWriter
	audio   trackWriter
	allowed atomic.Bool
	media   time.Duration
	step    int
	nextID  int
	outs    []testOut
	// ids of payloads sent live, by track
	liveIDs map[int]bool
}

func newTestCamera(t *testing.T, supported bool) *testCamera {
	c := &testCamera{
		t:       t,
		clock:   time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
		liveIDs: map[int]bool{},
	}
	c.allowed.Store(true)
	c.sr = &StallReplay{
		Buffer:     6 * time.Second,
		After:      2 * time.Second,
		Steady:     5 * time.Second,
		Allowed:    c.allowed.Load,
		now:        func() time.Time { return c.clock },
		manualTick: true,
	}
	c.sr.Initialize()

	var isKey func(any) bool
	if supported {
		isKey = func(p any) bool { return p.(testPayload).key }
	}

	c.video = c.sr.addTrack(true, testVideoRate, isKey, func(_ time.Time, pts int64, p any) error {
		c.outs = append(c.outs, testOut{track: 0, pts: pts, payload: p.(testPayload), live: c.sr.state != replayStateReplay})
		return nil
	})
	c.audio = c.sr.addTrack(false, testAudioRate, nil, func(_ time.Time, pts int64, p any) error {
		c.outs = append(c.outs, testOut{track: 1, pts: pts, payload: p.(testPayload), live: c.sr.state != replayStateReplay})
		return nil
	})
	return c
}

// send delivers d of media in real time.
func (c *testCamera) send(d time.Duration) {
	for end := c.media + d; c.media < end; {
		c.nextID++
		require.NoError(c.t, c.audio(c.clock, durationToTimestamp(c.media, testAudioRate), testPayload{id: c.nextID}))
		c.liveIDs[c.nextID] = true

		if c.step%2 == 0 {
			c.nextID++
			key := (c.step/2)%50 == 0
			require.NoError(c.t, c.video(c.clock, durationToTimestamp(c.media, testVideoRate)+1,
				testPayload{id: c.nextID, key: key}))
			c.liveIDs[c.nextID] = true
		}

		c.step++
		c.media += 20 * time.Millisecond
		c.clock = c.clock.Add(20 * time.Millisecond)
		c.sr.tick()
	}
}

// idle lets time pass with nothing arriving from the camera.
func (c *testCamera) idle(d time.Duration) {
	for end := c.clock.Add(d); c.clock.Before(end); {
		c.clock = c.clock.Add(20 * time.Millisecond)
		c.sr.tick()
	}
}

func (c *testCamera) outsSince(n int) []testOut {
	return c.outs[n:]
}

func videoOuts(outs []testOut) []testOut {
	var r []testOut
	for _, o := range outs {
		if o.track == 0 {
			r = append(r, o)
		}
	}
	return r
}

func requireIncreasing(t *testing.T, outs []testOut, track int) {
	t.Helper()
	var last int64
	first := true
	for _, o := range outs {
		if o.track != track {
			continue
		}
		if !first {
			require.Greater(t, o.pts, last, "track %d timestamps must increase", track)
		}
		last = o.pts
		first = false
	}
}

func TestStallReplayPassesThroughWhenNotWatched(t *testing.T) {
	c := newTestCamera(t, true)
	c.allowed.Store(false)

	c.send(10 * time.Second)
	c.idle(10 * time.Second)

	require.Len(t, c.outs, 750) // 500 audio + 250 video, nothing replayed
	for _, o := range c.outs {
		require.True(t, c.liveIDs[o.payload.id])
	}
	// timestamps are passed through untouched, including the odd +1 tick on video
	require.Equal(t, int64(1), c.outs[1].pts)
	require.Equal(t, durationToTimestamp(40*time.Millisecond, testVideoRate)+1, videoOuts(c.outs)[1].pts)
	require.Empty(t, c.sr.buffered())
}

func TestStallReplayPassesOutOfOrderAudioWhenItNeverReplayed(t *testing.T) {
	for _, watched := range []bool{false, true} {
		c := newTestCamera(t, true)
		c.allowed.Store(watched)
		c.send(4 * time.Second)

		// a camera clock stepping back by less than 2s gets past the rebaser unchanged
		back := durationToTimestamp(c.media-time.Second, testAudioRate)
		require.NoError(t, c.audio(c.clock, back, testPayload{id: -1}))

		last := c.outs[len(c.outs)-1]
		require.Equal(t, testPayload{id: -1}, last.payload, "watched=%v", watched)
		require.Equal(t, back, last.pts)
	}
}

func TestStallReplayHealthyCameraIsUntouched(t *testing.T) {
	c := newTestCamera(t, true)

	c.send(60 * time.Second)

	require.Equal(t, replayStateLive, c.sr.state)
	require.Len(t, c.outs, 4500)
	for i, o := range videoOuts(c.outs) {
		require.Equal(t, durationToTimestamp(time.Duration(i)*40*time.Millisecond, testVideoRate)+1, o.pts)
	}
}

func TestStallReplayStartsAfterStall(t *testing.T) {
	c := newTestCamera(t, true)
	c.send(10 * time.Second)
	lastLive := videoOuts(c.outs)[len(videoOuts(c.outs))-1]
	n := len(c.outs)

	c.idle(1900 * time.Millisecond)
	require.Equal(t, n, len(c.outs), "nothing is replayed before 2s")

	c.idle(200 * time.Millisecond)
	require.Equal(t, replayStateReplay, c.sr.state)

	replayed := videoOuts(c.outsSince(n))
	require.NotEmpty(t, replayed)
	require.True(t, replayed[0].payload.key, "replay starts on a keyframe")
	require.Greater(t, replayed[0].pts, lastLive.pts)

	// the missed seconds are caught up at once, so the output keeps pace with the clock
	caughtUp := timestampToDuration(replayed[len(replayed)-1].pts-lastLive.pts, testVideoRate)
	require.InDelta(t, (2100 * time.Millisecond).Seconds(), caughtUp.Seconds(), 0.1)
}

func TestStallReplayLoopsWithIncreasingTimestamps(t *testing.T) {
	c := newTestCamera(t, true)
	c.send(10 * time.Second)
	lastLive := videoOuts(c.outs)[len(videoOuts(c.outs))-1]
	n := len(c.outs)

	c.idle(32 * time.Second) // 5 passes of the 6s loop

	outs := c.outsSince(n)
	requireIncreasing(t, outs, 0)
	requireIncreasing(t, outs, 1)

	keys := 0
	for _, o := range videoOuts(outs) {
		require.True(t, c.liveIDs[o.payload.id], "only the camera's own footage is replayed")
		if o.payload.key {
			keys++
		}
	}
	require.GreaterOrEqual(t, keys, 10)

	// output video keeps pace with the clock
	last := videoOuts(outs)[len(videoOuts(outs))-1]
	require.InDelta(t, 32.0, timestampToDuration(last.pts-lastLive.pts, testVideoRate).Seconds(), 0.1)
}

func TestStallReplayReturnsToLiveWhenSteady(t *testing.T) {
	c := newTestCamera(t, true)
	c.send(10 * time.Second)
	c.idle(5 * time.Second)
	require.Equal(t, replayStateReplay, c.sr.state)

	n := len(c.outs)
	c.send(4900 * time.Millisecond)
	for _, o := range c.outsSince(n) {
		require.False(t, o.live, "no live frame is shown before the camera has been steady for 5s")
	}
	require.Equal(t, replayStateReplay, c.sr.state)

	c.send(3 * time.Second) // reaches the next keyframe after 5s of steady video
	require.Equal(t, replayStateLive, c.sr.state)

	outs := c.outsSince(n)
	requireIncreasing(t, outs, 0)
	requireIncreasing(t, outs, 1)

	// the first live frame after the replay is a keyframe and continues the timeline
	var lastReplay, firstLive *testOut
	for i := range outs {
		o := &outs[i]
		if o.track != 0 {
			continue
		}
		if o.live {
			firstLive = o
			break
		}
		lastReplay = o
	}
	require.NotNil(t, firstLive)
	require.NotNil(t, lastReplay)
	require.True(t, firstLive.payload.key)
	gap := timestampToDuration(firstLive.pts-lastReplay.pts, testVideoRate)
	require.Greater(t, gap, time.Duration(0))
	require.LessOrEqual(t, gap, 200*time.Millisecond)

	// from then on live video passes straight through, at a constant shift
	live := videoOuts(c.outsSince(n))
	for i := 1; i < len(live); i++ {
		if live[i-1].live && live[i].live {
			require.Equal(t, durationToTimestamp(40*time.Millisecond, testVideoRate), live[i].pts-live[i-1].pts)
		}
	}
}

func TestStallReplayBurstyCameraKeepsReplayingWithNewFootage(t *testing.T) {
	c := newTestCamera(t, true)
	c.send(10 * time.Second)
	c.idle(3 * time.Second)
	require.Equal(t, replayStateReplay, c.sr.state)

	firstBurstID := c.nextID + 1
	for i := 0; i < 6; i++ {
		c.send(2 * time.Second) // starts on a keyframe: 2s = one GOP
		c.idle(3 * time.Second)
	}
	require.Equal(t, replayStateReplay, c.sr.state, "bursts are never steady for 5s")

	sawBurst := false
	for _, o := range c.outs {
		if o.payload.id >= firstBurstID {
			sawBurst = true
		}
	}
	require.True(t, sawBurst, "footage from the bursts is shown on later passes of the loop")
	requireIncreasing(t, c.outs, 1)
	for _, o := range c.outs {
		if o.payload.id >= firstBurstID {
			require.False(t, o.live, "bursts are only shown through the replay")
		}
	}
}

func TestStallReplayStopsWhenAServiceReads(t *testing.T) {
	c := newTestCamera(t, true)
	c.send(10 * time.Second)
	c.idle(3 * time.Second)
	require.Equal(t, replayStateReplay, c.sr.state)

	c.allowed.Store(false)
	c.idle(time.Second)
	n := len(c.outs)
	c.idle(5 * time.Second)
	require.Equal(t, n, len(c.outs), "nothing is replayed once replay is not allowed")

	c.send(5 * time.Second)
	live := videoOuts(c.outsSince(n))
	require.NotEmpty(t, live)
	require.True(t, live[0].payload.key, "video resumes on a keyframe")
	requireIncreasing(t, c.outs, 1)
	require.Empty(t, c.sr.buffered())
}

func TestStallReplayUnsupportedCodecNeverReplays(t *testing.T) {
	c := newTestCamera(t, false)
	c.send(10 * time.Second)
	n := len(c.outs)
	c.idle(10 * time.Second)
	require.Equal(t, n, len(c.outs))
	require.Equal(t, replayStateLive, c.sr.state)
}

func TestStallReplayBufferIsBounded(t *testing.T) {
	c := newTestCamera(t, true)
	c.send(120 * time.Second)

	b := c.sr.buffered()
	span := b[len(b)-1].in - b[0].in
	require.LessOrEqual(t, span, c.sr.Buffer)
	require.GreaterOrEqual(t, span, c.sr.Buffer-2*time.Second)
	require.True(t, b[0].key)
	require.Less(t, len(c.sr.buf), 2*len(b)+300, "dropped units are compacted away")
}
