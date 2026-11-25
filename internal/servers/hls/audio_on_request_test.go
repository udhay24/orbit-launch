package hls

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/gohlslib/v2"
	"github.com/bluenviron/gohlslib/v2/pkg/codecs"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/bluenviron/mediamtx/internal/unit"
)

// a camera that sends video and audio, read through a server set up like the fleet:
// always remux, MPEG-TS, audio left out of HLS.
func newVideoOnlyServer(t *testing.T) (*Server, func()) {
	strm := &stream.Stream{
		OrigDesc: &description.Session{Medias: []*description.Media{
			test.MediaH264,
			test.MediaMPEG4Audio,
		}},
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		Parent:            test.NilLogger,
	}
	require.NoError(t, strm.Initialize())

	subStream := &stream.SubStream{Stream: strm}
	require.NoError(t, subStream.Initialize())

	s := &Server{
		Address:         "127.0.0.1:8888",
		AlwaysRemux:     true,
		VideoOnly:       true,
		Variant:         conf.HLSVariant(gohlslib.MuxerVariantMPEGTS),
		SegmentCount:    7,
		SegmentDuration: conf.Duration(1 * time.Second),
		PartDuration:    conf.Duration(200 * time.Millisecond),
		SegmentMaxSize:  50 * 1024 * 1024,
		MuxerCloseAfter: conf.Duration(1 * time.Minute),
		ReadTimeout:     conf.Duration(10 * time.Second),
		WriteTimeout:    conf.Duration(10 * time.Second),
		PathManager: &dummyPathManager{
			addReaderImpl: func(_ defs.PathAddReaderReq) (*defs.PathAddReaderRes, error) {
				return &defs.PathAddReaderRes{Path: &dummyPath{}, Stream: strm, AuthRes: &auth.Result{}}, nil
			},
		},
		Parent: test.NilLogger,
	}
	require.NoError(t, s.Initialize())

	s.PathReady(&dummyPath{})
	strm.WaitForReaders()

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := int64(0); ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			subStream.WriteUnit(test.MediaH264, test.FormatH264, &unit.Unit{
				PTS:     i * 90000,
				Payload: unit.PayloadH264{{5, 1}},
			})
			subStream.WriteUnit(test.MediaMPEG4Audio, test.FormatMPEG4Audio, &unit.Unit{
				PTS:     i * 44100,
				Payload: unit.PayloadMPEG4Audio{{1, 2}},
			})
		}
	}()

	return s, func() {
		close(stop)
		<-done
	}
}

func readTracks(t *testing.T, uri string) []*gohlslib.Track {
	recv := make(chan []*gohlslib.Track, 1)
	c := &gohlslib.Client{URI: uri, StartDistance: 1}
	c.OnTracks = func(tracks []*gohlslib.Track) error {
		recv <- tracks
		return nil
	}
	require.NoError(t, c.Start())
	defer c.Close()

	select {
	case tracks := <-recv:
		return tracks
	case <-time.After(10 * time.Second):
		t.Fatalf("no tracks from %s", uri)
		return nil
	}
}

func TestServerVideoOnlyGivesAudioOnRequest(t *testing.T) {
	s, stopCamera := newVideoOnlyServer(t)
	defer s.Close()
	defer stopCamera()

	tracks := readTracks(t, "http://127.0.0.1:8888/teststream/index.m3u8")
	require.Len(t, tracks, 1, "tiles are muted, so they get no audio")
	require.IsType(t, &codecs.H264{}, tracks[0].Codec)

	tracks = readTracks(t, "http://127.0.0.1:8888/teststream/index.m3u8?audio=1")
	require.Len(t, tracks, 2, "a viewer who asks for audio gets it")
	require.IsType(t, &codecs.H264{}, tracks[0].Codec)
	require.IsType(t, &codecs.MPEG4Audio{}, tracks[1].Codec)

	tracks = readTracks(t, "http://127.0.0.1:8888/teststream/index.m3u8")
	require.Len(t, tracks, 1, "the stream with audio does not replace the one without")

	list, err := s.APIMuxersList()
	require.NoError(t, err)
	require.Len(t, list.Items, 1, "metrics label muxers by path, so a path is listed once")
}

func TestServerVideoOnlyOffIgnoresAudioRequest(t *testing.T) {
	require.False(t, (&Server{VideoOnly: false}).wantsAudio(map[string][]string{"audio": {"1"}}),
		"every stream already has audio, so there is no second muxer to pick")
	require.True(t, (&Server{VideoOnly: true}).wantsAudio(map[string][]string{"audio": {"1"}}))
	require.False(t, (&Server{VideoOnly: true}).wantsAudio(map[string][]string{"audio": {"0"}}))
	require.False(t, (&Server{VideoOnly: true}).wantsAudio(nil))
}

func TestServerClosesWhileAudioMuxerIsAlive(t *testing.T) {
	s, stopCamera := newVideoOnlyServer(t)
	defer stopCamera()

	require.Len(t, readTracks(t, "http://127.0.0.1:8888/teststream/index.m3u8?audio=1"), 2)

	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for the audio muxer's idle timeout")
	}
}

// One browser can play a camera twice, once with audio (a tile and the opened player).
// Both sessions keep their secret in a cookie on the same path, so each kind has its own
// cookie name; with a shared one the later session would take over the earlier one's requests.
func TestSessionSecretCookieIsSeparateForAudio(t *testing.T) {
	plain := uuid.New()
	audio := uuid.New()

	newCtx := func(query string) *gin.Context {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodGet, "/teststream/stream.m3u8"+query, nil)
		ctx.Request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: plain.String()})
		ctx.Request.AddCookie(&http.Cookie{Name: audioSessionCookieName, Value: audio.String()})
		return ctx
	}

	require.Equal(t, plain, *sessionGetSecret(newCtx(""), false))
	require.Equal(t, audio, *sessionGetSecret(newCtx("?audio=1"), true))
}

func TestServerAudioSessionGetsItsOwnCookie(t *testing.T) {
	s, stopCamera := newVideoOnlyServer(t)
	defer s.Close()
	defer stopCamera()

	sessionCookie := func(query string) string {
		req, err := http.NewRequest(http.MethodGet,
			"http://127.0.0.1:8888/teststream/index.m3u8?cookieCheck=1"+query, nil)
		require.NoError(t, err)
		req.AddCookie(&http.Cookie{Name: "cookieCheck", Value: "1"})

		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		_, err = io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, res.StatusCode)

		var names []string
		for _, c := range res.Cookies() {
			names = append(names, c.Name)
		}
		return strings.Join(names, ",")
	}

	require.Equal(t, sessionCookieName, sessionCookie(""))
	require.Equal(t, audioSessionCookieName, sessionCookie("&audio=1"))
}
