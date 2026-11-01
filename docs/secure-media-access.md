# Secure media access (Orbit fork)

Everything here is off until a host opts in, so a release alone changes nothing.
Settings live in `/etc/orbit-play/media.env`, because `mediamtx.yml` ships as a public
release asset. Templates: `orbit_play_state/scripts/systemd/`.

## What each part does

| Goal | Where | Setting |
|---|---|---|
| Watching needs a token | MediaMTX | `MTX_AUTHMETHOD=jwt`, `MTX_AUTHJWTJWKS` (the orchestrator's `/load-balancer/api/media-access/jwks`, because media servers take one URL and several applications run at once), `MTX_AUTHJWTISSUER`, `MTX_AUTHJWTAUDIENCE` |
| Issue tokens | Orbit-NestJS `POST /api/v1/media-access/view-tokens`, each application with its own key | `MEDIA_JWT_PRIVATE_KEY_BASE64`, `MEDIA_JWT_KEY_ID` (the database name), set by the orchestrator on deploy, `MEDIA_VIEW_TOKEN_TTL_SECONDS` (default 12h), `MEDIA_VIEW_TOKEN_MAX_TTL_SECONDS` (default 24h) |
| Only registered cameras publish | MediaMTX reading Redis set `camera:stream_ids` | `MTX_PUBLISHALLOWLIST=yes`, `MTX_PUBLISHALLOWLISTADDRESS`, `MTX_PUBLISHALLOWLISTPASSWORD` |
| Keep that set current | Orbit-NestJS | `ENABLE_REDIS_FILTER=true`, `CAMERA_ALLOWLIST_REDIS_URL` (the Redis the media servers read), `RUN_CAMERA_STREAM_WATCHER=true` on one process per application |
| Browsers play only from the web apps | MediaMTX reading Redis set `media:allowed_origins`, kept by the orchestrator | `MTX_DYNAMICALLOWORIGINS=yes`, plus `MTX_HLSALLOWORIGINS` and `MTX_WEBRTCALLOWORIGINS` set to the admin's address only, since the default allows everything |

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
   (from Orbit-NestJS, using the orchestrator's key and `MEDIA_JWT_KEY_ID=orchestrator`) and set `MEDIA_READ_TOKEN` and `MEDIA_TOKEN_HOST_SUFFIX`.
6. Publish the release and let servers pull it.
7. Deploy the orchestrator and Orbit-Play-FrontEnd together, since the old admin login
   stops working. Set on the orchestrator: `ADMIN_USERS` (from
   `scripts/hash_admin_password.py`), `ADMIN_SESSION_SECRET`, and the same
   `MEDIA_JWT_PRIVATE_KEY_BASE64` for its own key, plus `MEDIA_KEY_ENCRYPTION_KEY`. Rotate `API_KEY` and update
   `fetch-ssl-certs.sh` on the media servers at the same time.
   Give the orchestrator's `ADMIN_API_TOKEN` a value that is also in api-admin's `API_KEYS`.
8. Lock api-admin. Deploy Orbit-API-Admin, then give each caller its own key:
   Orbit-NestJS, Sentinel and Orbit-API-Manager through `ADMIN_API_KEY`, the Lambda
   through its `AdminApiKey` parameter. Put all of them in `API_KEYS` last: until then
   nothing is enforced.
   Lock api-router the same way. Deploy Orbit-API-Manager, set `ORBIT_API_KEY` on every
   media server and deploy orbit-play-state, then set api-router's `API_KEYS`. In that
   order: media servers ignore upload errors, so recording logs would stop silently.
9. Ship the CIS app update. Older versions keep working.
10. Check `GET /api/v1/cameras/debug/redis-stream-id-count` matches your camera count,
   then turn on `MTX_PUBLISHALLOWLIST=yes` and `MTX_LOGLEVEL=warn` on one server. Watch
   for `is not registered` against cameras you expect to be valid, publish a made-up
   stream ID to confirm it is refused, and look for `allowing ... without a check`,
   which means the list is missing or Redis is unreachable and nothing is being checked.
11. Give every existing application its own key (see Signing keys), and check its cameras
    are in `camera:stream_ids:owned:<db_name>`.
12. Turn on `MTX_AUTHMETHOD=jwt`, `MTX_VIEWTOKENOWNERCHECK=yes` and the allowed origins on one server, then the rest.

Rollback: remove the lines from `media.env` and restart `orbit-play`.

## Signing keys

Each application signs with its own key, created when it is deployed and stored encrypted
in the orchestrator (`MEDIA_KEY_ENCRYPTION_KEY`). The JWKS at
`/load-balancer/api/media-access/jwks` lists them all, and media servers refresh it every
60 seconds, keeping the last good copy if the orchestrator is down.

With `MTX_VIEWTOKENOWNERCHECK=yes`, a token is refused unless its signing key owns the
camera, so a key stolen from one application cannot open another's. The orchestrator's own
key may read any camera, so it never leaves the orchestrator. Sentinel needs a token made
with that key, because it reads every application.

Applications deployed before this: run `scripts/issue_app_media_key.py <db_name>` on the
orchestrator and set the two printed lines in that application's `.env`. Add `--rotate` to
replace a key. Tokens signed with the old one stop working within a minute.

## Launching a new application

Nothing to do on media servers. `deploy_application` adds `https://<ui_domain>` to
`media:allowed_origins`, which media servers pick up within 15 seconds, whatever the
domain. `delete_application` removes it, and that application's cameras. If Redis was
unreachable, the deploy result has `media_access_error`: add the origin by hand. Existing
applications are added when the orchestrator starts.

## Known limits

- Installation: validating a stream ID in the CIS app adds it to the allowlist until the
  next sync (10 minutes), because a device must be online before it can be installed.
- The CIS app signs in with a supervisor ID and no password.
- CORS binds browsers only. A copied token works in VLC or curl until it expires.
- There is no way to cancel a token before it expires.
- A camera removed from the database keeps streaming until it reconnects.
- `publishAllowlistFailOpen: yes` (default) lets cameras in when Redis is unreachable or
  the set is missing, and logs a warning. Set it to `no` to reject them instead.
- RTSP, RTMP and SRT reads need tokens too once `authMethod` is `jwt`.
- The control API on port 9997 stays open by design for the orchestrator. Firewall it.
