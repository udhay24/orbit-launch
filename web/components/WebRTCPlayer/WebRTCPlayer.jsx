import React, { useRef, useState, useEffect, useCallback } from 'react';
import WebRTCReader from './WebRTCReader';
import './WebRTCPlayer.css';

const ZOOM_STEPS = [1, 1.2, 1.5, 2, 5];

const H5_SDK_SCRIPTS = [
  'https://orbit-play-ptz-files.s3.ap-south-1.amazonaws.com/decoder_def.js',
  'https://orbit-play-ptz-files.s3.ap-south-1.amazonaws.com/jadecoder.js',
  'https://orbit-play-ptz-files.s3.ap-south-1.amazonaws.com/hevcdec.js',
  'https://orbit-play-ptz-files.s3.ap-south-1.amazonaws.com/glutils.js',
  'https://orbit-play-ptz-files.s3.ap-south-1.amazonaws.com/connector.js',
  'https://orbit-play-ptz-files.s3.ap-south-1.amazonaws.com/audioplay.js',
  'https://orbit-play-ptz-files.s3.ap-south-1.amazonaws.com/md5.js',
  'https://orbit-play-ptz-files.s3.ap-south-1.amazonaws.com/play.js',
];

let h5SdkPromise = null;

function loadH5Sdk() {
  if (h5SdkPromise) return h5SdkPromise;
  h5SdkPromise = (async () => {
    for (const src of H5_SDK_SCRIPTS) {
      await new Promise((resolve, reject) => {
        const script = document.createElement('script');
        script.src = src;
        script.onload = resolve;
        script.onerror = reject;
        document.head.appendChild(script);
      });
    }
    // play.js declares `const Player = {}` which isn't on window.
    // Expose it globally via an inline script evaluated in the same global scope.
    await new Promise((resolve) => {
      const patch = document.createElement('script');
      patch.textContent = 'window.Player = Player;';
      document.head.appendChild(patch);
      resolve();
    });
  })();
  return h5SdkPromise;
}

/**
 * WebRTCPlayer - React component for Orbit Play WebRTC playback with PTZ controls.
 *
 * Props:
 *   url        - (required) WHEP endpoint URL, e.g. "http://host:8889/mystream/whep"
 *   streamId   - (required) stream identifier for PTZ control
 *   controls   - (optional, default: true) show bottom bar controls
 *   ptz        - (optional, default: true) show PTZ directional buttons
 *   muted      - (optional, default: true) start muted
 *   autoPlay   - (optional, default: true) auto-play video
 *   user       - (optional) WHEP auth username
 *   pass       - (optional) WHEP auth password
 *   token      - (optional) WHEP auth bearer token
 *   ptzUser    - (optional, default: "ptz") H5 SDK PTZ username
 *   ptzPass    - (optional, default: "ptz@sst") H5 SDK PTZ password
 *   channel    - (optional, default: 0) PTZ channel
 *   onError    - (optional) error callback
 */
export default function WebRTCPlayer({
  url,
  streamId,
  controls = true,
  ptz = true,
  muted: initialMuted = true,
  autoPlay = true,
  user,
  pass,
  token,
  ptzUser = 'ptz',
  ptzPass = 'ptz@sst',
  channel = 0,
  onError,
}) {
  const videoRef = useRef(null);
  const canvasRef = useRef(null);
  const containerRef = useRef(null);
  const readerRef = useRef(null);

  const [message, setMessage] = useState('');
  const [isMuted, setIsMuted] = useState(initialMuted);
  const [volume, setVolume] = useState(0);
  const [zoomLevel, setZoomLevel] = useState(1);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [sdkLoaded, setSdkLoaded] = useState(false);

  // Load H5 Camera SDK for PTZ
  useEffect(() => {
    if (!ptz) return;
    loadH5Sdk()
      .then(() => setSdkLoaded(true))
      .catch((err) => console.error('Failed to load H5 SDK:', err));
  }, [ptz]);

  // Init H5 SDK PTZ connection (poll for Player global since SDK may init async)
  useEffect(() => {
    if (!sdkLoaded || !ptz || !canvasRef.current) return;

    let cancelled = false;
    let pollTimer = null;

    const tryInit = () => {
      if (cancelled) return;
      if (!window.Player) {
        pollTimer = setTimeout(tryInit, 200);
        return;
      }
      window.Player.init([canvasRef.current]);
      window.Player.ConnectDevice(streamId, '', ptzUser, ptzPass, 0, 80, 0, channel, 0, 'wss');
    };

    tryInit();

    return () => {
      cancelled = true;
      if (pollTimer) clearTimeout(pollTimer);
    };
  }, [sdkLoaded, ptz, streamId, ptzUser, ptzPass, channel]);

  // Start WebRTC reader
  useEffect(() => {
    if (!url) return;

    const reader = new WebRTCReader({
      url,
      user,
      pass,
      token,
      onError: (err) => {
        setMessage(err);
        if (onError) onError(err);
      },
      onTrack: (evt) => {
        setMessage('');
        if (videoRef.current) {
          videoRef.current.srcObject = evt.streams[0];
        }
      },
    });

    readerRef.current = reader;
    return () => reader.close();
  }, [url, user, pass, token, onError]);

  // Sync muted/volume to video element
  useEffect(() => {
    if (!videoRef.current) return;
    videoRef.current.muted = isMuted;
    videoRef.current.volume = volume / 100;
  }, [isMuted, volume]);

  // Fullscreen change listener
  useEffect(() => {
    const handler = () => {
      setIsFullscreen(!!document.fullscreenElement);
    };
    document.addEventListener('fullscreenchange', handler);
    return () => document.removeEventListener('fullscreenchange', handler);
  }, []);

  // PTZ handlers
  const ptzMove = useCallback((direction, speed) => {
    if (!window.Player) return;
    window.Player.ptz_ctrl(streamId, '', channel, direction, speed);
    setTimeout(() => window.Player.ptz_ctrl(streamId, '', channel, 0, 0), 1000);
  }, [streamId, channel]);

  const handleZoomIn = useCallback(() => {
    setZoomLevel((prev) => {
      const idx = ZOOM_STEPS.indexOf(prev);
      return idx < ZOOM_STEPS.length - 1 ? ZOOM_STEPS[idx + 1] : prev;
    });
  }, []);

  const handleZoomOut = useCallback(() => {
    setZoomLevel((prev) => {
      const idx = ZOOM_STEPS.indexOf(prev);
      return idx > 0 ? ZOOM_STEPS[idx - 1] : prev;
    });
  }, []);

  const handleResetZoom = useCallback(() => {
    setZoomLevel(1);
  }, []);

  const handleFullscreen = useCallback(() => {
    if (!containerRef.current) return;
    if (document.fullscreenElement) {
      document.exitFullscreen();
    } else {
      containerRef.current.requestFullscreen();
    }
  }, []);

  const handleMuteToggle = useCallback(() => {
    setIsMuted((prev) => {
      if (prev) {
        setVolume((v) => (v === 0 ? 50 : v));
      }
      return !prev;
    });
  }, []);

  const handleVolumeChange = useCallback((e) => {
    const val = Number(e.target.value);
    setVolume(val);
    setIsMuted(val === 0);
  }, []);

  return (
    <div className="webrtc-player" ref={containerRef}>
      {/* Hidden canvas for H5 SDK - parent div hidden, canvas itself must not be display:none */}
      <div style={{ display: 'none' }}>
        <canvas ref={canvasRef} id="canvas" />
      </div>

      <video
        ref={videoRef}
        className="webrtc-player__video"
        autoPlay={autoPlay}
        playsInline
        muted={isMuted}
        style={{ transform: `scale(${zoomLevel})` }}
      />

      {message && (
        <div className="webrtc-player__message">{message}</div>
      )}

      {controls && (
        <div className="webrtc-player__bottom-bar">
          {/* Audio Controls */}
          <div className="webrtc-player__audio-controls">
            <button className="webrtc-player__btn" onClick={handleMuteToggle}>
              <span className="material-icons">
                {isMuted || volume === 0 ? 'volume_off' : 'volume_up'}
              </span>
            </button>
            <input
              type="range"
              className="webrtc-player__volume-slider"
              min="0"
              max="100"
              value={isMuted ? 0 : volume}
              onChange={handleVolumeChange}
            />
          </div>

          <div className="webrtc-player__separator" />

          {/* PTZ Controls */}
          {ptz && (
            <>
              <div className="webrtc-player__ptz-controls">
                <button className="webrtc-player__ptz-btn" onClick={() => ptzMove(4, 6)}>
                  <span className="material-icons">arrow_back</span>
                </button>
                <button className="webrtc-player__ptz-btn" onClick={() => ptzMove(2, 6)}>
                  <span className="material-icons">arrow_upward</span>
                </button>
                <button className="webrtc-player__ptz-btn" onClick={() => ptzMove(3, 6)}>
                  <span className="material-icons">arrow_downward</span>
                </button>
                <button className="webrtc-player__ptz-btn" onClick={() => ptzMove(5, 6)}>
                  <span className="material-icons">arrow_forward</span>
                </button>
              </div>

              <div className="webrtc-player__separator" />
            </>
          )}

          {/* Zoom Controls */}
          <div className="webrtc-player__zoom-controls">
            <button className="webrtc-player__btn" onClick={handleZoomIn}>
              <span className="material-icons">zoom_in</span>
            </button>
            <button className="webrtc-player__btn" onClick={handleZoomOut}>
              <span className="material-icons">zoom_out</span>
            </button>
            <button className="webrtc-player__btn" onClick={handleResetZoom}>
              <span className="material-icons">center_focus_strong</span>
            </button>
            <button className="webrtc-player__btn" onClick={handleFullscreen}>
              <span className="material-icons">
                {isFullscreen ? 'fullscreen_exit' : 'fullscreen'}
              </span>
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
