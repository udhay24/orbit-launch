// Package hls contains HLS utilities.
package hls

import (
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/bluenviron/gohlslib/v2"
	"github.com/bluenviron/gohlslib/v2/pkg/codecs"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/flac"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"

	"github.com/bluenviron/mediamtx/internal/formatlabel"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/unit"
)

// ErrNoSupportedCodecs is returned by FromStream when there are no supported codecs.
var ErrNoSupportedCodecs = errors.New(
	"the stream doesn't contain any supported codec, which are currently AV1, VP9, H265, H264, Opus, MPEG-4 Audio, KLV")

func findFormatAndIndexInMedia(media *description.Media, forma any) int {
	for i, forma2 := range media.Formats {
		if reflect.TypeOf(forma2) == reflect.TypeOf(forma).Elem() {
			reflect.ValueOf(forma).Elem().Set(reflect.ValueOf(forma2))
			return i
		}
	}

	return -1
}

func findFormatAndIndexes(d *description.Session, forma any) (*description.Media, int, int) {
	for i, media := range d.Medias {
		j := findFormatAndIndexInMedia(media, forma)
		if j >= 0 {
			return media, i, j
		}
	}
	return nil, -1, -1
}

// routeTrack sends a track's writes through the stall replay, when there is one.
func routeTrack(sr *StallReplay, isVideo bool, clockRate int, isKey func(any) bool, w trackWriter) trackWriter {
	if sr == nil {
		return w
	}
	return sr.addTrack(isVideo, clockRate, isKey, w)
}

// Cameras that resume after a gap without a keyframe confuse the muxer's DTS extractor.
// Returning that error would crash the muxer and take the stream away from viewers for 10s or
// more; dropping frames until the extractor accepts them again only holds the picture until the
// next keyframe, which resets the extractor.
func dropOnDTSError(l logger.Writer, w trackWriter) trackWriter {
	dropping := false
	return func(ntp time.Time, pts int64, p any) error {
		err := w(ntp, pts, p)
		if err != nil && strings.Contains(err.Error(), "unable to extract DTS") {
			if !dropping {
				l.Log(logger.Warn, "dropping frames until the next keyframe: %v", err)
				dropping = true
			}
			return nil
		}
		if err == nil {
			dropping = false
		}
		return err
	}
}

func setupVideoTrack(
	origDesc *description.Session,
	outDesc *description.Session,
	r *stream.Reader,
	muxer *gohlslib.Muxer,
	newRebaser func() *timestampRebaser,
	sr *StallReplay,
) {
	addTrack := func(
		media *description.Media,
		forma format.Format,
		track *gohlslib.Track,
		onData stream.OnDataFunc,
	) {
		muxer.Tracks = append(muxer.Tracks, track)
		r.OnData(media, forma, onData)
	}

	var videoFormatAV1 *format.AV1
	videoMedia, _, _ := findFormatAndIndexes(origDesc, &videoFormatAV1)

	if videoFormatAV1 != nil {
		track := &gohlslib.Track{
			Codec:     &codecs.AV1{},
			ClockRate: videoFormatAV1.ClockRate(),
		}

		rb := newRebaser()
		write := routeTrack(sr, true, track.ClockRate, nil,
			func(ntp time.Time, pts int64, p any) error {
				return muxer.WriteAV1(track, ntp, pts, p.(unit.PayloadAV1))
			})

		addTrack(
			videoMedia,
			videoFormatAV1,
			track,
			func(u *unit.Unit) error {
				if u.NilPayload() {
					return nil
				}

				err := write(
					u.NTP,
					rb.rebase(u.PTS, track.ClockRate), // track.ClockRate = format.ClockRate, so no other conversion is needed
					u.Payload)
				if err != nil {
					return fmt.Errorf("muxer error: %w", err)
				}

				return nil
			})

		return
	}

	var videoFormatVP9 *format.VP9
	videoMedia, _, _ = findFormatAndIndexes(origDesc, &videoFormatVP9)

	if videoFormatVP9 != nil {
		track := &gohlslib.Track{
			Codec:     &codecs.VP9{},
			ClockRate: videoFormatVP9.ClockRate(),
		}

		rb := newRebaser()
		write := routeTrack(sr, true, track.ClockRate, nil,
			func(ntp time.Time, pts int64, p any) error {
				return muxer.WriteVP9(track, ntp, pts, p.(unit.PayloadVP9))
			})

		addTrack(
			videoMedia,
			videoFormatVP9,
			track,
			func(u *unit.Unit) error {
				if u.NilPayload() {
					return nil
				}

				err := write(
					u.NTP,
					rb.rebase(u.PTS, track.ClockRate), // track.ClockRate = format.ClockRate, so no other conversion is needed
					u.Payload)
				if err != nil {
					return fmt.Errorf("muxer error: %w", err)
				}

				return nil
			})

		return
	}

	var videoFormatH265 *format.H265
	videoMedia, i, j := findFormatAndIndexes(origDesc, &videoFormatH265)

	if videoFormatH265 != nil {
		outFormat := outDesc.Medias[i].Formats[j].(*format.H265)

		track := &gohlslib.Track{
			Codec: &codecs.H265{
				VPS: outFormat.VPS,
				SPS: outFormat.SPS,
				PPS: outFormat.PPS,
			},
			ClockRate: videoFormatH265.ClockRate(),
		}

		rb := newRebaser()
		write := routeTrack(sr, true, track.ClockRate, func(p any) bool { return h265.IsRandomAccess(p.(unit.PayloadH265)) },
			dropOnDTSError(r.Parent, func(ntp time.Time, pts int64, p any) error {
				return muxer.WriteH265(track, ntp, pts, p.(unit.PayloadH265))
			}))

		addTrack(
			videoMedia,
			videoFormatH265,
			track,
			func(u *unit.Unit) error {
				if u.NilPayload() {
					return nil
				}

				err := write(
					u.NTP,
					rb.rebase(u.PTS, track.ClockRate), // track.ClockRate = format.ClockRate, so no other conversion is needed
					u.Payload)
				if err != nil {
					return fmt.Errorf("muxer error: %w", err)
				}

				return nil
			})

		return
	}

	var videoFormatH264 *format.H264
	videoMedia, i, j = findFormatAndIndexes(origDesc, &videoFormatH264)

	if videoFormatH264 != nil {
		outFormat := outDesc.Medias[i].Formats[j].(*format.H264)

		track := &gohlslib.Track{
			Codec: &codecs.H264{
				SPS: outFormat.SPS,
				PPS: outFormat.PPS,
			},
			ClockRate: videoFormatH264.ClockRate(),
		}

		rb := newRebaser()
		write := routeTrack(sr, true, track.ClockRate, func(p any) bool { return h264.IsRandomAccess(p.(unit.PayloadH264)) },
			dropOnDTSError(r.Parent, func(ntp time.Time, pts int64, p any) error {
				return muxer.WriteH264(track, ntp, pts, p.(unit.PayloadH264))
			}))

		addTrack(
			videoMedia,
			videoFormatH264,
			track,
			func(u *unit.Unit) error {
				if u.NilPayload() {
					return nil
				}

				err := write(
					u.NTP,
					rb.rebase(u.PTS, track.ClockRate), // track.ClockRate = format.ClockRate, so no other conversion is needed
					u.Payload)
				if err != nil {
					return fmt.Errorf("muxer error: %w", err)
				}

				return nil
			})

		return
	}
}

func setupAudioTracks(
	origDesc *description.Session,
	_ *description.Session,
	r *stream.Reader,
	muxer *gohlslib.Muxer,
	newRebaser func() *timestampRebaser,
	sr *StallReplay,
) error {
	addTrack := func(
		medi *description.Media,
		forma format.Format,
		track *gohlslib.Track,
		onData stream.OnDataFunc,
	) {
		muxer.Tracks = append(muxer.Tracks, track)
		r.OnData(medi, forma, onData)
	}

	for _, media := range origDesc.Medias {
		for _, forma := range media.Formats {
			// the MPEG-TS variant supports a single MPEG-4 Audio track
			if muxer.Variant == gohlslib.MuxerVariantMPEGTS {
				switch forma.(type) {
				case *format.MPEG4Audio, *format.MPEG4AudioLATM:
				default:
					continue
				}

				if slices.ContainsFunc(muxer.Tracks, func(track *gohlslib.Track) bool {
					return !track.Codec.IsVideo()
				}) {
					continue
				}
			}

			switch forma := forma.(type) {
			case *format.Opus:
				track := &gohlslib.Track{
					Codec: &codecs.Opus{
						ChannelCount: forma.ChannelCount,
					},
					ClockRate: forma.ClockRate(),
				}

				rb := newRebaser()
				write := routeTrack(sr, false, track.ClockRate, nil,
					func(ntp time.Time, pts int64, p any) error {
						return muxer.WriteOpus(track, ntp, pts, p.(unit.PayloadOpus))
					})

				addTrack(
					media,
					forma,
					track,
					func(u *unit.Unit) error {
						err := write(
							u.NTP,
							rb.rebase(u.PTS, track.ClockRate), // track.ClockRate = format.ClockRate, so no other conversion is needed
							u.Payload)
						if err != nil {
							return fmt.Errorf("muxer error: %w", err)
						}

						return nil
					})

			case *format.Generic:
				if strings.HasPrefix(strings.ToLower(forma.RTPMap()), "flac/") {
					enc, err := hex.DecodeString(forma.FMT["streaminfo"])
					if err != nil {
						return err
					}

					var streamInfo flac.StreamInfo
					err = streamInfo.Unmarshal(enc)
					if err != nil {
						return err
					}

					track := &gohlslib.Track{
						Codec: &codecs.FLAC{
							StreamInfo: &streamInfo,
						},
						ClockRate: forma.ClockRate(),
					}

					rb := newRebaser()
					write := routeTrack(sr, false, track.ClockRate, nil,
						func(ntp time.Time, pts int64, p any) error {
							return muxer.WriteFLAC(track, ntp, pts, p.(unit.PayloadFLAC))
						})

					addTrack(
						media,
						forma,
						track,
						func(u *unit.Unit) error {
							if u.NilPayload() {
								return nil
							}

							err2 := write(
								u.NTP,
								rb.rebase(u.PTS, track.ClockRate), // track.ClockRate = format.ClockRate, so no other conversion is needed
								u.Payload)
							if err2 != nil {
								return fmt.Errorf("muxer error: %w", err2)
							}

							return nil
						})
				}

			case *format.MPEG4Audio:
				track := &gohlslib.Track{
					Codec: &codecs.MPEG4Audio{
						Config: *forma.Config,
					},
					ClockRate: forma.ClockRate(),
				}

				rb := newRebaser()
				write := routeTrack(sr, false, track.ClockRate, nil,
					func(ntp time.Time, pts int64, p any) error {
						return muxer.WriteMPEG4Audio(track, ntp, pts, p.(unit.PayloadMPEG4Audio))
					})

				addTrack(
					media,
					forma,
					track,
					func(u *unit.Unit) error {
						if u.NilPayload() {
							return nil
						}

						err := write(
							u.NTP,
							rb.rebase(u.PTS, track.ClockRate), // track.ClockRate = format.ClockRate, so no other conversion is needed
							u.Payload)
						if err != nil {
							return fmt.Errorf("muxer error: %w", err)
						}

						return nil
					})

			case *format.MPEG4AudioLATM:
				if !forma.CPresent {
					track := &gohlslib.Track{
						Codec: &codecs.MPEG4Audio{
							Config: *forma.StreamMuxConfig.Programs[0].Layers[0].AudioSpecificConfig,
						},
						ClockRate: forma.ClockRate(),
					}

					rb := newRebaser()
					write := routeTrack(sr, false, track.ClockRate, nil,
						func(ntp time.Time, pts int64, p any) error {
							return muxer.WriteMPEG4Audio(track, ntp, pts, p.([][]byte))
						})

					addTrack(
						media,
						forma,
						track,
						func(u *unit.Unit) error {
							if u.NilPayload() {
								return nil
							}

							var ame mpeg4audio.AudioMuxElement
							ame.StreamMuxConfig = forma.StreamMuxConfig
							err := ame.Unmarshal(u.Payload.(unit.PayloadMPEG4AudioLATM))
							if err != nil {
								return err
							}

							return write(
								u.NTP,
								rb.rebase(u.PTS, track.ClockRate), // track.ClockRate = format.ClockRate, so no other conversion is needed
								[][]byte{ame.Payloads[0][0][0]})
						})
				}
			}
		}
	}

	return nil
}

func setupDataTracks(
	origDesc *description.Session,
	_ *description.Session,
	r *stream.Reader,
	muxer *gohlslib.Muxer,
	newRebaser func() *timestampRebaser,
	sr *StallReplay,
) {
	addTrack := func(
		media *description.Media,
		forma format.Format,
		track *gohlslib.Track,
		onData stream.OnDataFunc,
	) {
		muxer.Tracks = append(muxer.Tracks, track)
		r.OnData(media, forma, onData)
	}

	for _, media := range origDesc.Medias {
		for _, forma := range media.Formats {
			if forma, ok := forma.(*format.KLV); ok && muxer.Variant == gohlslib.MuxerVariantMPEGTS {
				track := &gohlslib.Track{
					Codec:     &codecs.KLV{Synchronous: true},
					ClockRate: forma.ClockRate(),
				}

				rb := newRebaser()
				write := routeTrack(sr, false, track.ClockRate, nil,
					func(ntp time.Time, pts int64, p any) error {
						return muxer.WriteKLV(track, ntp, pts, p.(unit.PayloadKLV))
					})

				addTrack(
					media,
					forma,
					track,
					func(u *unit.Unit) error {
						if u.NilPayload() {
							return nil
						}

						err := write(
							u.NTP,
							rb.rebase(u.PTS, track.ClockRate), // track.ClockRate = format.ClockRate, so no other conversion is needed
							u.Payload)
						if err != nil {
							return fmt.Errorf("muxer error: %w", err)
						}

						return nil
					})
			}
		}
	}
}

// FromStream maps a MediaMTX stream to a HLS muxer.
func FromStream(
	origDesc *description.Session,
	outDesc *description.Session,
	r *stream.Reader,
	muxer *gohlslib.Muxer,
) error {
	return FromStreamWithReplay(origDesc, outDesc, r, muxer, nil, false)
}

// FromStreamWithReplay is FromStream with every write passing through a stall replay.
// sr must be initialized; it can be nil. With videoOnly, audio is left out of streams
// that have video.
func FromStreamWithReplay(
	origDesc *description.Session,
	outDesc *description.Session,
	r *stream.Reader,
	muxer *gohlslib.Muxer,
	sr *StallReplay,
	videoOnly bool,
) error {
	// one rebaser per track: some cameras keep audio seconds away from video, and a shared one
	// takes every switch between the two for a jump, scrambling timestamps until the muxer fails.
	newRebaser := func() *timestampRebaser {
		return &timestampRebaser{
			onRebase: func(gap time.Duration) {
				r.Parent.Log(logger.Warn, "timestamps jumped by %v, removing the gap", gap)
			},
		}
	}

	setupVideoTrack(
		origDesc,
		outDesc,
		r,
		muxer,
		newRebaser,
		sr,
	)

	// some cameras stamp audio tens of seconds away from video, and the gap grows while they
	// stay connected. hls.js waits for the two to line up before playing, so such a tile takes
	// as long as the gap to start, or never starts. Muted viewers lose nothing without audio.
	if !videoOnly || len(muxer.Tracks) == 0 {
		err := setupAudioTracks(
			origDesc,
			outDesc,
			r,
			muxer,
			newRebaser,
			sr,
		)
		if err != nil {
			return err
		}
	}

	setupDataTracks(
		origDesc,
		outDesc,
		r,
		muxer,
		newRebaser,
		sr,
	)

	if len(muxer.Tracks) == 0 {
		return ErrNoSupportedCodecs
	}

	setuppedFormats := r.Formats()

	n := 1
	for _, media := range origDesc.Medias {
		for _, forma := range media.Formats {
			if !slices.Contains(setuppedFormats, forma) {
				r.Parent.Log(logger.Warn, "skipping track %d (%s)", n, formatlabel.FormatToLabel(forma))
			}
			n++
		}
	}

	return nil
}
