package hls_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gohlslib/v2"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/hls"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/bluenviron/mediamtx/internal/unit"
)

func TestFromStreamNoSupportedCodecs(t *testing.T) {
	desc := &description.Session{Medias: []*description.Media{{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{&format.VP8{}},
	}}}

	r := &stream.Reader{
		Parent: test.Logger(func(logger.Level, string, ...any) {
			t.Error("should not happen")
		}),
	}

	m := &gohlslib.Muxer{}

	err := hls.FromStream(desc, desc, r, m)
	require.Equal(t, hls.ErrNoSupportedCodecs, err)
}

func TestFromStreamSkipUnsupportedTracks(t *testing.T) {
	desc := &description.Session{Medias: []*description.Media{
		{
			Type:    description.MediaTypeVideo,
			Formats: []format.Format{&format.VP9{}},
		},
		{
			Type:    description.MediaTypeVideo,
			Formats: []format.Format{&format.VP8{}},
		},
		{
			Type:    description.MediaTypeAudio,
			Formats: []format.Format{&format.MPEG1Audio{}},
		},
	}}

	m := &gohlslib.Muxer{}

	n := 0

	r := &stream.Reader{
		Parent: test.Logger(func(l logger.Level, format string, args ...any) {
			require.Equal(t, logger.Warn, l)
			switch n {
			case 0:
				require.Equal(t, "skipping track 2 (VP8)", fmt.Sprintf(format, args...))
			case 1:
				require.Equal(t, "skipping track 3 (MPEG-1/2 Audio)", fmt.Sprintf(format, args...))
			}
			n++
		}),
	}

	err := hls.FromStream(desc, desc, r, m)
	require.NoError(t, err)

	require.Equal(t, 2, n)
}

func TestFromStreamKLVRequiresMPEGTSVariant(t *testing.T) {
	t.Run("klv only, non-mpegts variant", func(t *testing.T) {
		desc := &description.Session{Medias: []*description.Media{{
			Type:    description.MediaTypeApplication,
			Formats: []format.Format{&format.KLV{PayloadTyp: 96}},
		}}}

		r := &stream.Reader{
			Parent: test.Logger(func(logger.Level, string, ...any) {
				t.Error("should not happen")
			}),
		}

		m := &gohlslib.Muxer{Variant: gohlslib.MuxerVariantFMP4}

		err := hls.FromStream(desc, desc, r, m)
		require.Equal(t, hls.ErrNoSupportedCodecs, err)
	})

	t.Run("klv alongside video, non-mpegts variant", func(t *testing.T) {
		desc := &description.Session{Medias: []*description.Media{
			{
				Type:    description.MediaTypeVideo,
				Formats: []format.Format{&format.H264{PayloadTyp: 96, PacketizationMode: 1}},
			},
			{
				Type:    description.MediaTypeApplication,
				Formats: []format.Format{&format.KLV{PayloadTyp: 97}},
			},
		}}

		n := 0

		r := &stream.Reader{
			Parent: test.Logger(func(l logger.Level, f string, args ...any) {
				require.Equal(t, logger.Warn, l)
				require.Equal(t, "skipping track 2 (KLV)", fmt.Sprintf(f, args...))
				n++
			}),
		}

		m := &gohlslib.Muxer{Variant: gohlslib.MuxerVariantFMP4}

		err := hls.FromStream(desc, desc, r, m)
		require.NoError(t, err)
		require.Equal(t, 1, n)
		require.Equal(t, 1, len(m.Tracks))
	})
}

func TestFromStreamMPEGTSVariantSkipUnsupportedAudio(t *testing.T) {
	for _, ca := range []struct {
		name    string
		variant gohlslib.MuxerVariant
		tracks  int
	}{
		{"mpegts", gohlslib.MuxerVariantMPEGTS, 2},
		{"fmp4", gohlslib.MuxerVariantFMP4, 3},
	} {
		t.Run(ca.name, func(t *testing.T) {
			desc := &description.Session{Medias: []*description.Media{
				test.MediaH264,
				{
					Type:    description.MediaTypeAudio,
					Formats: []format.Format{&format.Opus{PayloadTyp: 96, ChannelCount: 2}},
				},
				test.MediaMPEG4Audio,
			}}

			n := 0

			r := &stream.Reader{
				Parent: test.Logger(func(l logger.Level, f string, args ...any) {
					require.Equal(t, logger.Warn, l)
					require.Equal(t, "skipping track 2 (Opus)", fmt.Sprintf(f, args...))
					n++
				}),
			}

			m := &gohlslib.Muxer{Variant: ca.variant}

			err := hls.FromStream(desc, desc, r, m)
			require.NoError(t, err)
			require.Equal(t, ca.tracks, len(m.Tracks))

			if ca.variant == gohlslib.MuxerVariantMPEGTS {
				require.Equal(t, 1, n)
			} else {
				require.Equal(t, 0, n)
			}

			err = m.Start()
			require.NoError(t, err)
			m.Close()
		})
	}
}

func TestFromStreamMPEGTSVariantNoSupportedAudio(t *testing.T) {
	desc := &description.Session{Medias: []*description.Media{{
		Type:    description.MediaTypeAudio,
		Formats: []format.Format{&format.Opus{PayloadTyp: 96, ChannelCount: 2}},
	}}}

	r := &stream.Reader{
		Parent: test.Logger(func(logger.Level, string, ...any) {
			t.Error("should not happen")
		}),
	}

	m := &gohlslib.Muxer{Variant: gohlslib.MuxerVariantMPEGTS}

	err := hls.FromStream(desc, desc, r, m)
	require.Equal(t, hls.ErrNoSupportedCodecs, err)
}

func TestFromStreamMPEGTSVariantSkipExtraAudio(t *testing.T) {
	for _, ca := range []struct {
		name    string
		variant gohlslib.MuxerVariant
		tracks  int
	}{
		{"mpegts", gohlslib.MuxerVariantMPEGTS, 2},
		{"fmp4", gohlslib.MuxerVariantFMP4, 3},
	} {
		t.Run(ca.name, func(t *testing.T) {
			desc := &description.Session{Medias: []*description.Media{
				test.MediaH264,
				test.MediaMPEG4Audio,
				{
					Type: description.MediaTypeAudio,
					Formats: []format.Format{&format.MPEG4Audio{
						PayloadTyp:       96,
						Config:           test.FormatMPEG4Audio.Config,
						SizeLength:       13,
						IndexLength:      3,
						IndexDeltaLength: 3,
					}},
				},
			}}

			n := 0

			r := &stream.Reader{
				Parent: test.Logger(func(l logger.Level, f string, args ...any) {
					require.Equal(t, logger.Warn, l)
					require.Equal(t, "skipping track 3 (MPEG-4 Audio)", fmt.Sprintf(f, args...))
					n++
				}),
			}

			m := &gohlslib.Muxer{Variant: ca.variant}

			err := hls.FromStream(desc, desc, r, m)
			require.NoError(t, err)
			require.Equal(t, ca.tracks, len(m.Tracks))

			if ca.variant == gohlslib.MuxerVariantMPEGTS {
				require.Equal(t, 1, n)
			} else {
				require.Equal(t, 0, n)
			}

			err = m.Start()
			require.NoError(t, err)
			m.Close()
		})
	}
}

type playlistResponseWriter struct {
	bytes.Buffer
	h          http.Header
	statusCode int
}

func (w *playlistResponseWriter) Header() http.Header { return w.h }

func (w *playlistResponseWriter) WriteHeader(statusCode int) { w.statusCode = statusCode }

func getPlaylist(t *testing.T, m *gohlslib.Muxer, path string) string {
	u, err := url.Parse("http://localhost/" + path)
	require.NoError(t, err)
	w := &playlistResponseWriter{h: make(http.Header)}
	m.Handle(w, &http.Request{URL: u})
	if w.statusCode != http.StatusOK {
		return ""
	}
	return w.String()
}

// A camera that stalls and resumes with timestamps 26s ahead must not produce a 26s segment,
// because the playlist's target duration never decreases and players would keep freezing.
func TestFromStreamRemovesTimestampJumps(t *testing.T) {
	desc := &description.Session{Medias: []*description.Media{test.MediaH264}}

	strm := &stream.Stream{
		OrigDesc:          desc,
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		Parent:            test.NilLogger,
	}
	require.NoError(t, strm.Initialize())
	defer strm.Close()

	subStream := &stream.SubStream{Stream: strm, UseRTPPackets: false}
	require.NoError(t, subStream.Initialize())

	m := &gohlslib.Muxer{
		Variant:            gohlslib.MuxerVariantMPEGTS,
		SegmentCount:       7,
		SegmentMinDuration: time.Second,
	}

	var warnings []string
	r := &stream.Reader{Parent: test.Logger(func(_ logger.Level, f string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(f, args...))
	})}
	require.NoError(t, hls.FromStream(strm.OrigDesc, strm.OutDescCopy(), r, m))
	require.NoError(t, m.Start())
	defer m.Close()

	strm.AddReader(r)
	defer strm.RemoveReader(r)

	// 12 fps with a keyframe every 2s, like the cameras; a 26s jump after 10s.
	const frame = 90000 / 12
	jump := int64(0)
	for i := range 12 * 40 {
		if i == 12*10+3 {
			jump = 26 * 90000
		}
		payload := unit.PayloadH264{{1, 2}}
		if i%24 == 0 {
			payload = unit.PayloadH264{test.FormatH264.SPS, test.FormatH264.PPS, {5, 1}}
		}
		subStream.WriteUnit(test.MediaH264, test.FormatH264, &unit.Unit{
			PTS:     int64(i)*frame + jump,
			NTP:     time.Now(),
			Payload: payload,
		})
	}

	var media string
	require.Eventually(t, func() bool {
		media = getPlaylist(t, m, "main_stream.m3u8")
		return strings.Contains(media, "#EXT-X-MEDIA-SEQUENCE:") &&
			!strings.Contains(media, "#EXT-X-MEDIA-SEQUENCE:0\n")
	}, 5*time.Second, 50*time.Millisecond)

	require.Contains(t, media, "#EXT-X-TARGETDURATION:2\n")
	for _, line := range strings.Split(media, "\n") {
		if v, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
			d, err := strconv.ParseFloat(strings.TrimSuffix(v, ","), 64)
			require.NoError(t, err)
			require.LessOrEqual(t, d, 2.5, line)
		}
	}
	// 26s plus the 1/12s between the two frames.
	require.Equal(t, []string{"timestamps jumped by 26.083333334s, removing the gap"}, warnings)
}

// Some cameras keep their audio timestamps tens of seconds away from video. That steady offset
// is not a jump: removing it on every switch between the two tracks scrambled the video
// timestamps until the muxer failed and viewers lost the stream for 10s at a time.
func TestFromStreamKeepsTracksThatRunApart(t *testing.T) {
	desc := &description.Session{Medias: []*description.Media{test.MediaH264, test.MediaMPEG4Audio}}

	strm := &stream.Stream{
		OrigDesc:          desc,
		WriteQueueSize:    4096, // 40s of video and AAC are written at once
		RTPMaxPayloadSize: 1450,
		Parent:            test.NilLogger,
	}
	require.NoError(t, strm.Initialize())
	defer strm.Close()

	subStream := &stream.SubStream{Stream: strm, UseRTPPackets: false}
	require.NoError(t, subStream.Initialize())

	m := &gohlslib.Muxer{
		Variant:            gohlslib.MuxerVariantMPEGTS,
		SegmentCount:       7,
		SegmentMinDuration: time.Second,
	}

	var mutex sync.Mutex
	var warnings []string
	r := &stream.Reader{Parent: test.Logger(func(_ logger.Level, f string, args ...any) {
		mutex.Lock()
		defer mutex.Unlock()
		warnings = append(warnings, fmt.Sprintf(f, args...))
	})}
	require.NoError(t, hls.FromStream(strm.OrigDesc, strm.OutDescCopy(), r, m))
	require.NoError(t, m.Start())
	defer m.Close()

	strm.AddReader(r)
	defer strm.RemoveReader(r)

	// 12 fps with a keyframe every 2s; audio runs 32.6s ahead of video, and after 10s both
	// tracks jump 26s forward together, which must still be removed once on each track.
	const frame = 90000 / 12
	const audioAhead = 326 * 44100 / 10
	jump := int64(0)
	audio := int64(0)
	for i := range 12 * 40 {
		if i == 12*10+3 {
			jump = 26
		}
		payload := unit.PayloadH264{{1, 2}}
		if i%24 == 0 {
			payload = unit.PayloadH264{test.FormatH264.SPS, test.FormatH264.PPS, {5, 1}}
		}
		subStream.WriteUnit(test.MediaH264, test.FormatH264, &unit.Unit{
			PTS:     int64(i)*frame + jump*90000,
			NTP:     time.Now(),
			Payload: payload,
		})
		// AAC frames of 1024 samples covering this video frame.
		for ; audio*1024 < int64(i+1)*44100/12; audio++ {
			subStream.WriteUnit(test.MediaMPEG4Audio, test.FormatMPEG4Audio, &unit.Unit{
				PTS:     audio*1024 + audioAhead + jump*44100,
				NTP:     time.Now(),
				Payload: unit.PayloadMPEG4Audio{{1, 2}},
			})
		}
	}

	var media string
	require.Eventually(t, func() bool {
		media = getPlaylist(t, m, "main_stream.m3u8")
		return strings.Contains(media, "#EXT-X-MEDIA-SEQUENCE:") &&
			!strings.Contains(media, "#EXT-X-MEDIA-SEQUENCE:0\n")
	}, 5*time.Second, 50*time.Millisecond)

	select {
	case err := <-r.Error():
		t.Fatalf("muxer failed: %v", err)
	default:
	}

	for _, line := range strings.Split(media, "\n") {
		if v, ok := strings.CutPrefix(line, "#EXTINF:"); ok {
			d, err := strconv.ParseFloat(strings.TrimSuffix(v, ","), 64)
			require.NoError(t, err)
			require.LessOrEqual(t, d, 2.5, line)
		}
	}

	mutex.Lock()
	defer mutex.Unlock()
	require.Len(t, warnings, 2, warnings)
	for _, w := range warnings {
		require.True(t, strings.HasPrefix(w, "timestamps jumped by 26."), w)
	}
}
