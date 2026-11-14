package hls

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/test"
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

func TestTimestampConversion(t *testing.T) {
	for _, cr := range []int64{90000, 48000, 44100} {
		for _, v := range []int64{0, 1, cr - 1, cr, 3600*cr + 7, 30 * 24 * 3600 * cr} {
			// a duration has nanosecond precision, so the round trip loses at most one tick.
			require.InDelta(t, v, durationToTimestamp(timestampToDuration(v, cr), cr), 1)
		}
	}
}

func TestDropOnDTSError(t *testing.T) {
	var warnings []string
	l := test.Logger(func(_ logger.Level, f string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(f, args...))
	})

	results := []error{
		nil,
		fmt.Errorf("unable to extract DTS: too many reordered frames (14)"),
		fmt.Errorf("unable to extract DTS: too many reordered frames (15)"),
		nil, // next keyframe accepted
		fmt.Errorf("unable to extract DTS: too many reordered frames (3)"),
		errors.New("segment too big"),
	}
	i := 0
	w := dropOnDTSError(l, func(time.Time, int64, any) error {
		err := results[i]
		i++
		return err
	})

	for range 5 {
		require.NoError(t, w(time.Time{}, 0, nil))
	}
	require.EqualError(t, w(time.Time{}, 0, nil), "segment too big")

	// one warning per run of dropped frames.
	require.Equal(t, []string{
		"dropping frames until the next keyframe: unable to extract DTS: too many reordered frames (14)",
		"dropping frames until the next keyframe: unable to extract DTS: too many reordered frames (3)",
	}, warnings)
}
