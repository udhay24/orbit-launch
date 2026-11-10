package hls

import "time"

// Cheap 4G cameras can stall and then resume with timestamps far ahead (or reset backwards).
// Written as-is, the jump becomes a single segment tens of seconds long, and gohlslib never
// lowers a playlist's target duration, so players freeze every few seconds until the stream
// restarts. Jumps larger than maxTimestampGap are therefore removed before muxing.
const (
	maxTimestampGap = 2 * time.Second
	// where the first sample after a jump lands, after the last one, so timestamps keep increasing.
	resumeStep = 100 * time.Millisecond
)

// timestampRebaser is shared by all tracks of a stream, so a jump seen on one track is not
// applied twice when the other tracks catch up. Callbacks of a stream.Reader run on a single
// goroutine, so it needs no lock.
type timestampRebaser struct {
	onRebase func(gap time.Duration)

	offset      time.Duration
	last        time.Duration
	initialized bool
}

func timestampToDuration(v int64, clockRate int64) time.Duration {
	return time.Duration(v/clockRate)*time.Second +
		time.Duration((v%clockRate)*int64(time.Second)/clockRate)
}

func durationToTimestamp(d time.Duration, clockRate int64) int64 {
	sec := int64(d / time.Second)
	rem := int64(d % time.Second)
	return sec*clockRate + rem*clockRate/int64(time.Second)
}

func (r *timestampRebaser) rebase(pts int64, clockRate int) int64 {
	cr := int64(clockRate)
	d := timestampToDuration(pts, cr) + r.offset

	if r.initialized && (d > r.last+maxTimestampGap || d < r.last-maxTimestampGap) {
		shift := r.last + resumeStep - d
		r.offset += shift
		d += shift
		if r.onRebase != nil {
			r.onRebase(-shift + resumeStep)
		}
	}

	if !r.initialized || d > r.last {
		r.last = d
		r.initialized = true
	}

	return pts + durationToTimestamp(r.offset, cr)
}
