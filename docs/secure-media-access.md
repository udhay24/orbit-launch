# Secure media access (Orbit fork)

Everything here is off until a host opts in, so a release alone changes nothing.
Settings live in `/etc/orbit-play/media.env`, because `mediamtx.yml` ships as a public
release asset. Templates: `orbit_play_state/scripts/systemd/`.

## What each part does

| Goal | Where | Setting |
|---|---|---|
| Watching needs a token | MediaMTX | `MTX_AUTHMETHOD=jwt`, `MTX_AUTHJWTJWKS`, `MTX_AUTHJWTISSUER`, `MTX_AUTHJWTAUDIENCE` |
| Issue tokens | Orbit-NestJS `POST /api/v1/media-access/view-tokens` | `MEDIA_JWT_PRIVATE_KEY_BASE64`, `MEDIA_VIEW_TOKEN_TTL_SECONDS` (default 12h), `MEDIA_VIEW_TOKEN_MAX_TTL_SECONDS` (default 24h) |
| Only registered cameras publish | MediaMTX reading Redis set `camera:stream_ids` | `MTX_PUBLISHALLOWLIST=yes`, `MTX_PUBLISHALLOWLISTADDRESS`, `MTX_PUBLISHALLOWLISTPASSWORD` |
| Keep that set current | Orbit-NestJS | `ENABLE_REDIS_FILTER=true`, `RUN_CAMERA_STREAM_WATCHER=true` on one process |
| Browsers play only from the web app | MediaMTX | `MTX_HLSALLOWORIGINS`, `MTX_WEBRTCALLOWORIGINS` |

Tokens above the default lifetime need a CEO or ADMINCONFIG login. A session ends when
its token expires, so the lifetime is also the longest someone can watch on one token.
The web app renews tokens by itself; share links just stop.

## Rollout order

1. Rotate the stream registry Redis password. The old one is in git history and past
   release assets. Update Redis, the orchestrator's `STREAM_REGISTRY_PASSWORD` and the
   registry dashboard.
2. On each media server install `media.env` with the new password and the systemd
   drop-in `orbit-play-secrets.conf`. Do this before the release: the new
   `mediamtx.yml` has no password in it.
3. Deploy Orbit-NestJS with a new signing key, set `REDIS_PASSWORD` if its Redis has
   one, and check `GET /api/v1/media-access/jwks` and
   `GET /api/v1/cameras/debug/redis-stream-id-count`.
4. Deploy Orbit-FrontEnd. It plays with or without enforcement.
5. Issue a Sentinel token with `node scripts/issue-media-service-token.js sentinel 30`
   (from Orbit-NestJS) and set `MEDIA_READ_TOKEN` and `MEDIA_TOKEN_HOST_SUFFIX`.
6. Publish the release and let servers pull it.
7. Deploy the orchestrator and Orbit-Play-FrontEnd together, since the old admin login
   stops working. Set on the orchestrator: `ADMIN_USERS` (from
   `scripts/hash_admin_password.py`), `ADMIN_SESSION_SECRET`, and the same
   `MEDIA_JWT_PRIVATE_KEY_BASE64` as Nest. Rotate `API_KEY` and update
   `fetch-ssl-certs.sh` on the media servers at the same time.
8. Turn on `MTX_PUBLISHALLOWLIST=yes` on one server. Watch for
   `is not registered` in the log against cameras you expect to be valid.
9. Turn on `MTX_AUTHMETHOD=jwt` and the allowed origins on one server, then the rest.

Rollback: remove the lines from `media.env` and restart `orbit-play`.

## Known limits

- CORS binds browsers only. A copied token works in VLC or curl until it expires.
- There is no way to cancel a token before it expires.
- A camera removed from the database keeps streaming until it reconnects.
- `publishAllowlistFailOpen: yes` (default) lets cameras in when Redis is unreachable or
  the set is missing, and logs a warning. Set it to `no` to reject them instead.
- RTSP, RTMP and SRT reads need tokens too once `authMethod` is `jwt`.
- The control API on port 9997 stays open by design for the orchestrator. Firewall it.
