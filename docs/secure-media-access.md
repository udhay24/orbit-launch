# Secure media access (Orbit fork)

Everything here is off until a host opts in, so a release alone changes nothing.
Settings live in `/etc/orbit-play/media.env`, because `mediamtx.yml` ships as a public
release asset. Templates: `orbit_play_state/scripts/systemd/`.

## What each part does

| Goal | Where | Setting |
|---|---|---|
| Watching needs a token | MediaMTX | `MTX_AUTHMETHOD=jwt`, `MTX_AUTHJWTJWKS` (the orchestrator's `/load-balancer/api/media-access/jwks`, because media servers take one URL and several applications run at once), `MTX_AUTHJWTISSUER`, `MTX_AUTHJWTAUDIENCE` |
| Issue tokens | Orbit-NestJS `POST /api/v1/media-access/view-tokens`, each application with its own key | `MEDIA_JWT_PRIVATE_KEY_BASE64`, `MEDIA_JWT_KEY_ID` (the database name), set by the orchestrator on deploy, `MEDIA_VIEW_TOKEN_TTL_SECONDS` (default 12h), `MEDIA_VIEW_TOKEN_MAX_TTL_SECONDS` (default 24h) |
| Redis kept by the orchestrator | MediaMTX, for the three rows below | `MTX_BACKENDREDISADDRESS`, `MTX_BACKENDREDISPASSWORD` |
| Blocked cameras cannot publish | MediaMTX reading Redis set `camera:blocked_stream_ids`, which admins fill from the Orbit Play portal | `MTX_PUBLISHBLOCKLIST=yes` |
| Know which application owns each camera | Orbit-NestJS claims cameras from the orchestrator, which writes `camera:stream_ids:owned:<db>` | `ORCHESTRATOR_URL`, `ENABLE_REDIS_FILTER=true`, `RUN_CAMERA_STREAM_WATCHER=true` on one process per application |
| Browsers play only from the web apps | MediaMTX reading Redis set `media:allowed_origins`, kept by the orchestrator | `MTX_DYNAMICALLOWORIGINS=yes`, plus `MTX_HLSALLOWORIGINS` and `MTX_WEBRTCALLOWORIGINS` set to the admin's address only, since the default allows everything |

Tokens above the default lifetime need a CEO or ADMINCONFIG login. A session ends when
its token expires, so the lifetime is also the longest someone can watch on one token.
The web app renews tokens by itself; share links just stop.

## Rollout order

Do these in order. Nothing is enforced until steps 9 and 10.

1. Rotate what is exposed: the Redis password, the orchestrator `API_KEY`, every secret
   hardcoded in `services/api_server_service.py`, and the TLS certificate.
2. Deploy the orchestrator and Orbit-Play-FrontEnd together, because the old admin login
   stops working. Set `ADMIN_USERS` (from `scripts/hash_admin_password.py`),
   `ADMIN_SESSION_SECRET`, `MEDIA_KEY_ENCRYPTION_KEY`, its own
   `MEDIA_JWT_PRIVATE_KEY_BASE64`, `ORCHESTRATOR_PUBLIC_URL`, and
   `MEDIA_SERVER_ENV_BASE64` so new media servers are protected. Update
   `fetch-ssl-certs.sh` on media servers with the new `API_KEY`. Check
   `/load-balancer/api/media-access/jwks` returns the orchestrator's key.
3. On each media server install `media.env` with the new Redis password and `ORBIT_API_KEY`,
   plus both systemd drop-ins, `orbit-play-secrets.conf` and
   `orbit-play-state-secrets.conf`. Do this before the release: the new `mediamtx.yml` has
   no password in it.
4. For each existing application, run `scripts/issue_app_media_key.py <db_name>` on the
   orchestrator. Set the printed lines, `ORCHESTRATOR_URL`, `ENABLE_REDIS_FILTER=true` and
   `ADMIN_API_KEY` in its `.env`, make sure `RUN_CAMERA_STREAM_WATCHER=true` on one process,
   and deploy Orbit-NestJS. Check its log for stream IDs refused as another application's:
   each one is a camera two applications both claim.
5. Deploy Orbit-FrontEnd. It plays with or without enforcement.
6. Deploy Sentinel and sentinel-ai-pipeline with `MEDIA_READ_TOKEN` from
   `scripts/issue_service_view_token.py sentinel <days>` on the orchestrator, plus
   `MEDIA_TOKEN_HOST_SUFFIX` and `ADMIN_API_KEY`. Renew the token before it expires: nothing
   does this automatically.
7. Publish the release and let media servers pull it. Deploy orbit-play-state.
8. Lock the two APIs. Deploy Orbit-API-Admin and Orbit-API-Manager, confirm every caller
   sends its key, then set `API_KEYS` on each, last. Media servers ignore upload errors,
   so a missing `ORBIT_API_KEY` would silently lose recording logs.
9. On one media server turn on `MTX_PUBLISHBLOCKLIST=yes`. Block a test stream ID from the
   portal, confirm it is refused and disconnected, and that other cameras, registered or not,
   still connect. Watch for `allowing ... without a blocklist check`. Then roll out.
10. On one media server turn on `MTX_AUTHMETHOD=jwt`, `MTX_AUTHJWTJWKS`, the issuer and
    audience, `MTX_VIEWTOKENOWNERCHECK=yes`, `MTX_DYNAMICALLOWORIGINS=yes`, and set
    `MTX_HLSALLOWORIGINS` and `MTX_WEBRTCALLOWORIGINS` to the admin's address. Check every
    application plays, then roll out.

Rollback: remove the lines from `media.env` and restart `orbit-play`.

## Signing keys

Each application signs with its own key, created when it is deployed and stored encrypted
in the orchestrator (`MEDIA_KEY_ENCRYPTION_KEY`). The JWKS at
`/load-balancer/api/media-access/jwks` lists them all, and media servers refresh it every
60 seconds, keeping the last good copy if the orchestrator is down.

With `MTX_VIEWTOKENOWNERCHECK=yes`, a token is refused unless its signing key owns the
camera, so a key stolen from one application cannot open another's. The orchestrator's own
key may read any camera, so it never leaves the orchestrator. Sentinel needs a token made
with it, because it reads every application: see step 6.

Camera ownership is written only by the orchestrator. Applications claim cameras with a
request signed by their own key, and the first owner keeps a camera, so an application
cannot take over another's even with its server fully compromised. Applications do not
hold the shared Redis password. If Redis loses its data, the next sync of each application
restores it within 10 minutes.

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

- The CIS app signs in with a supervisor ID and no password.
- CORS binds browsers only. A copied token works in VLC or curl until it expires.
- There is no way to cancel a token before it expires.
- A camera removed from the database keeps streaming until it reconnects.
- The blocklist lets cameras in when Redis is unreachable, and logs an error. Only listed
  cameras are refused: any other stream ID can publish.
- Blocking disconnects a live camera through the control API on port 9997, so the
  orchestrator must be able to reach it.
- View tokens never fail open. During a Redis outage only key and camera pairs seen in the
  last day work.
- A new media server is only protected if `MEDIA_SERVER_ENV_BASE64` is set.
- Anyone who knows a camera's stream ID can publish to it, and `overridePublisher` lets
  them replace the camera.
- Application signing keys and `ADMIN_API_KEY` reach API servers through cloud-init, so
  anything that can read instance metadata can read them.
- RTSP, RTMP and SRT reads need tokens too once `authMethod` is `jwt`.
- The control API on port 9997 stays open by design for the orchestrator. Firewall it.
