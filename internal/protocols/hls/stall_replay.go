package hls

import (
	"context"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
)

// Cameras on poor networks stay connected but deliver video in bursts with silences of
// several seconds, so viewers see a frozen picture. While someone is watching, StallReplay
// keeps the last few seconds of the camera's own footage and, when video stops, loops it
// into the HLS muxer until the camera has been sending steadily again. A camera that
// disconnects closes the muxer instance, which ends the replay with it.
//
// Every write to the muxer goes through StallReplay so that live and replayed frames are
// serialized and the output timeline only moves forward.

const (
	replayTickPeriod = 50 * time.Millisecond
	// a pause in live video longer than this restarts the "steady" countdown.
	replaySteadyMaxGap = time.Second
	// spacing used after the last frame of the loop and around switches, when the
	// camera's own frame interval is not known yet.
	replayDefaultFrameStep = 40 * time.Millisecond
)

type trackWriter func(ntp time.Time, pts int64, payload any) error

type replayState int

const (
	replayStateLive replayState = iota
	replayStateReplay
	// video is held back until a keyframe so the decoder never sees frames whose
	// references were replaced by replayed footage.
	replayStateWaitKey
)

type replayTrack struct {
	isVideo   bool
	clockRate int64
	isKey     func(any) bool
	write     trackWriter
}

type replayUnit struct {
	track   int
	pts     int64
	in      time.Duration
	payload any
	key     bool
}

// StallReplay loops recent footage into a muxer while a watched camera stalls.
type StallReplay struct {
	// how much footage to keep and loop.
	Buffer time.Duration
	// how long video must be missing before replay starts. Keep it below the
	// viewers' buffer (about 2.4 segments) so they never run dry.
	After time.Duration
	// how long the camera must send without pauses before live video is shown again.
	Steady time.Duration
	// reports whether replay may run: someone is watching and no service is reading.
	Allowed func() bool
	Parent  logger.Writer

	// test hooks
	now        func() time.Time
	manualTick bool

	mutex       sync.Mutex
	tracks      []*replayTrack
	video       int
	unsupported bool
	state       replayState
	shifted     bool
	offset      time.Duration
	maxOut      []time.Duration
	written     []bool
	frameStep   time.Duration
	buf         []replayUnit
	bufStart    int

	lastVideoWall time.Time
	lastVideoIn   time.Duration

	// replay
	anchorWall        time.Time
	anchorOut         time.Duration
	loop              []replayUnit
	loopPos           int
	loopBase          time.Duration
	loopStart         time.Duration
	loopLen           time.Duration
	steadySince       time.Time
	lastLiveVideoWall time.Time

	running   bool
	ctx       context.Context
	ctxCancel func()
	wg        sync.WaitGroup
}

// Initialize initializes StallReplay.
func (sr *StallReplay) Initialize() {
	if sr.now == nil {
		sr.now = time.Now
	}
	sr.video = -1
	sr.frameStep = replayDefaultFrameStep
	sr.ctx, sr.ctxCancel = context.WithCancel(context.Background())
}

// Close stops the replay goroutine. No write may happen after Close.
func (sr *StallReplay) Close() {
	sr.ctxCancel()
	sr.wg.Wait()
}

func (sr *StallReplay) log(level logger.Level, format string, args ...any) {
	if sr.Parent != nil {
		sr.Parent.Log(level, format, args...)
	}
}

// addTrack registers a track and returns the writer FromStream must use for it.
// isKey is nil for video codecs whose keyframes cannot be detected; replay is then disabled.
func (sr *StallReplay) addTrack(isVideo bool, clockRate int, isKey func(any) bool, w trackWriter) trackWriter {
	id := len(sr.tracks)
	sr.tracks = append(sr.tracks, &replayTrack{
		isVideo:   isVideo,
		clockRate: int64(clockRate),
		isKey:     isKey,
		write:     w,
	})
	sr.maxOut = append(sr.maxOut, 0)
	sr.written = append(sr.written, false)

	if isVideo {
		if sr.video >= 0 || isKey == nil {
			sr.unsupported = true
		} else {
			sr.video = id
		}
	}

	return func(ntp time.Time, pts int64, payload any) error {
		return sr.write(id, ntp, pts, payload)
	}
}

func (sr *StallReplay) allowed() bool {
	return !sr.unsupported && sr.video >= 0 && sr.Allowed != nil && sr.Allowed()
}

func (sr *StallReplay) write(id int, ntp time.Time, pts int64, payload any) error {
	sr.mutex.Lock()
	defer sr.mutex.Unlock()

	t := sr.tracks[id]
	in := timestampToDuration(pts, t.clockRate)
	isVideo := id == sr.video

	if !sr.allowed() {
		if sr.state == replayStateReplay {
			sr.stopReplay("no viewers, or a service is reading")
		}
		sr.clearBuffer()
		return sr.writeLive(id, ntp, pts, in, payload)
	}

	now := sr.now()
	sr.ensureRunning()

	key := isVideo && t.isKey(payload)
	sr.appendBuffer(replayUnit{track: id, pts: pts, in: in, payload: payload, key: key})

	if isVideo {
		if !sr.lastVideoWall.IsZero() && in > sr.lastVideoIn {
			if step := in - sr.lastVideoIn; step < sr.After {
				sr.frameStep = step
			}
		}
		sr.lastVideoIn = in
	}

	if sr.state == replayStateReplay {
		if !isVideo {
			return nil
		}

		if sr.lastLiveVideoWall.IsZero() || now.Sub(sr.lastLiveVideoWall) > replaySteadyMaxGap {
			sr.steadySince = now
		}
		sr.lastLiveVideoWall = now

		if !key || now.Sub(sr.steadySince) < sr.Steady {
			return nil
		}

		sr.offset = sr.nextOut() - in
		sr.state = replayStateLive
		sr.loop = nil
		sr.log(logger.Info, "camera is sending steadily again, back to live video")
	}

	if isVideo {
		sr.lastVideoWall = now
	}

	return sr.writeLive(id, ntp, pts, in, payload)
}

// writeLive writes a live unit, shifted onto the output timeline. With no shift the
// timestamp is passed through unchanged.
func (sr *StallReplay) writeLive(id int, ntp time.Time, pts int64, in time.Duration, payload any) error {
	t := sr.tracks[id]
	isVideo := id == sr.video

	if sr.state == replayStateWaitKey {
		if !isVideo || !t.isKey(payload) {
			return nil
		}
		sr.offset = sr.nextOut() - in
		sr.state = replayStateLive
	}

	return sr.out(id, ntp, pts+durationToTimestamp(sr.offset, t.clockRate), in+sr.offset, payload)
}

func (sr *StallReplay) out(id int, ntp time.Time, outPTS int64, out time.Duration, payload any) error {
	t := sr.tracks[id]

	// once replay has moved the timeline, keep audio and data strictly increasing across the
	// switches. Video may carry B-frames, whose timestamps are not in order, so it is left alone,
	// and so is everything on cameras that never replayed: they pass through exactly as before.
	if sr.shifted && !t.isVideo && sr.written[id] && out <= sr.maxOut[id] {
		return nil
	}

	if !sr.written[id] || out > sr.maxOut[id] {
		sr.maxOut[id] = out
	}
	sr.written[id] = true

	return t.write(ntp, outPTS, payload)
}

// nextOut is the first output time after everything written so far.
func (sr *StallReplay) nextOut() time.Duration {
	var m time.Duration
	for i, w := range sr.written {
		if w && sr.maxOut[i] > m {
			m = sr.maxOut[i]
		}
	}
	return m + sr.frameStep
}

func (sr *StallReplay) appendBuffer(u replayUnit) {
	sr.buf = append(sr.buf, u)

	if u.track != sr.video {
		return
	}

	// keep at most Buffer of footage, starting at a video keyframe.
	start := sr.bufStart
	for start < len(sr.buf) && u.in-sr.buf[start].in > sr.Buffer {
		start++
	}
	for start < len(sr.buf) && !(sr.buf[start].track == sr.video && sr.buf[start].key) {
		start++
	}

	for i := sr.bufStart; i < start; i++ {
		sr.buf[i] = replayUnit{} // release payloads
	}
	sr.bufStart = start

	if sr.bufStart > 256 && sr.bufStart > len(sr.buf)/2 {
		sr.buf = append([]replayUnit(nil), sr.buf[sr.bufStart:]...)
		sr.bufStart = 0
	}
}

func (sr *StallReplay) buffered() []replayUnit {
	return sr.buf[sr.bufStart:]
}

func (sr *StallReplay) clearBuffer() {
	sr.buf = nil
	sr.bufStart = 0
	sr.lastVideoWall = time.Time{}
}

func (sr *StallReplay) ensureRunning() {
	if sr.running || sr.manualTick {
		return
	}
	sr.running = true
	sr.wg.Add(1)
	go sr.run()
}

func (sr *StallReplay) run() {
	defer sr.wg.Done()

	t := time.NewTicker(replayTickPeriod)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			if !sr.tick() {
				return
			}
		case <-sr.ctx.Done():
			return
		}
	}
}

// tick starts or advances the replay. It returns false when the goroutine can stop
// because nobody is watching.
func (sr *StallReplay) tick() bool {
	sr.mutex.Lock()
	defer sr.mutex.Unlock()

	if !sr.allowed() {
		if sr.state == replayStateReplay {
			sr.stopReplay("no viewers, or a service is reading")
		}
		sr.clearBuffer()
		sr.running = false
		return false
	}

	now := sr.now()

	switch sr.state {
	case replayStateLive:
		if !sr.lastVideoWall.IsZero() && now.Sub(sr.lastVideoWall) >= sr.After && sr.hasFootage() {
			sr.startReplay(now)
		}

	case replayStateReplay:
		sr.emit(now)
	}

	return true
}

func (sr *StallReplay) hasFootage() bool {
	b := sr.buffered()
	return len(b) > 0 && b[0].track == sr.video && b[0].key
}

func (sr *StallReplay) startReplay(now time.Time) {
	sr.state = replayStateReplay
	sr.shifted = true
	sr.anchorWall = sr.lastVideoWall
	sr.anchorOut = sr.maxOut[sr.video]
	sr.steadySince = time.Time{}
	sr.lastLiveVideoWall = time.Time{}
	sr.newLoop(sr.nextOut())

	sr.log(logger.Info, "no video for %v, replaying the last %v of footage",
		now.Sub(sr.lastVideoWall).Truncate(time.Millisecond), sr.loopLen.Truncate(time.Millisecond))

	sr.emit(now)
}

// newLoop takes the current buffer as the next pass of the loop, so footage that
// arrives in bursts during the replay is shown on the following pass.
func (sr *StallReplay) newLoop(start time.Duration) {
	sr.loop = append([]replayUnit(nil), sr.buffered()...)
	sr.loopPos = 0
	sr.loopStart = start
	sr.loopBase = sr.loop[0].in

	end := sr.loopBase
	for _, u := range sr.loop {
		if u.in > end {
			end = u.in
		}
	}
	sr.loopLen = end - sr.loopBase + sr.frameStep
}

func (sr *StallReplay) emit(now time.Time) {
	target := sr.anchorOut + now.Sub(sr.anchorWall)

	for {
		if sr.loopPos >= len(sr.loop) {
			if !sr.hasFootage() {
				return
			}
			sr.newLoop(sr.loopStart + sr.loopLen)
		}

		u := sr.loop[sr.loopPos]
		out := sr.loopStart + (u.in - sr.loopBase)
		if out > target {
			return
		}

		ntp := sr.anchorWall.Add(out - sr.anchorOut)
		shift := durationToTimestamp(sr.loopStart-sr.loopBase, sr.tracks[u.track].clockRate)
		err := sr.out(u.track, ntp, u.pts+shift, out, u.payload)
		if err != nil {
			sr.log(logger.Warn, "replay stopped: %v", err)
			sr.stopReplay("muxer error")
			return
		}

		sr.loopPos++
	}
}

func (sr *StallReplay) stopReplay(reason string) {
	sr.state = replayStateWaitKey
	sr.loop = nil
	sr.log(logger.Info, "replay stopped (%s)", reason)
}
