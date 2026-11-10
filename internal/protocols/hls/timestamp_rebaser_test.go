package hls

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTimestampRebaser(t *testing.T) {
	const second = 90000

	for _, ca := range []struct {
		name string
		in   []int64
		out  []int64
		gaps []time.Duration
	}{
		{
			"continuous",
			[]int64{0, second, 2 * second},
			[]int64{0, second, 2 * second},
			nil,
		},
		{
			"small gap is kept",
			[]int64{0, 2 * second},
			[]int64{0, 2 * second},
			nil,
		},
		{
			"forward jump is removed",
			[]int64{0, second, 27 * second, 28 * second},
			[]int64{0, second, second + second/10, 2*second + second/10},
			[]time.Duration{26 * time.Second},
		},
		{
			"backward reset is removed",
			[]int64{10 * second, 11 * second, 0, second},
			[]int64{10 * second, 11 * second, 11*second + second/10, 12*second + second/10},
			[]time.Duration{-11 * time.Second},
		},
		{
			"repeated jumps accumulate",
			[]int64{0, 30 * second, 31 * second, 90 * second},
			[]int64{0, second / 10, second + second/10, second + 2*second/10},
			[]time.Duration{30 * time.Second, 59 * time.Second},
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			var gaps []time.Duration
			r := &timestampRebaser{onRebase: func(gap time.Duration) { gaps = append(gaps, gap) }}

			var out []int64
			for _, v := range ca.in {
				out = append(out, r.rebase(v, second))
			}

			require.Equal(t, ca.out, out)
			require.Equal(t, ca.gaps, gaps)
		})
	}
}

func TestTimestampRebaserSharedAcrossTracks(t *testing.T) {
	r := &timestampRebaser{}

	// video at 90kHz and audio at 44.1kHz jump by the same 26s; the offset is applied once.
	require.Equal(t, int64(0), r.rebase(0, 90000))
	require.Equal(t, int64(0), r.rebase(0, 44100))
	video := r.rebase(26*90000, 90000)
	audio := r.rebase(26*44100+4410, 44100)

	require.Equal(t, int64(9000), video) // lands 100ms after the last sample
	require.Equal(t, int64(8820), audio) // keeps its 100ms distance to video
}

func TestTimestampRebaserIgnoresOneTrackCatchingUp(t *testing.T) {
	r := &timestampRebaser{}

	// audio keeps flowing while video stalls; video resumes near audio, so nothing is shifted.
	r.rebase(0, 90000)
	for i := range 30 {
		r.rebase(int64(i)*44100, 44100)
	}
	require.Equal(t, int64(29*90000), r.rebase(29*90000, 90000))
}

func TestTimestampConversion(t *testing.T) {
	for _, cr := range []int64{90000, 48000, 44100} {
		for _, v := range []int64{0, 1, cr - 1, cr, 3600*cr + 7, 30 * 24 * 3600 * cr} {
			// a duration has nanosecond precision, so the round trip loses at most one tick.
			require.InDelta(t, v, durationToTimestamp(timestampToDuration(v, cr), cr), 1)
		}
	}
}
