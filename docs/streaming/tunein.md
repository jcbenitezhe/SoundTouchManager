# TuneIn

STM can browse and search the TuneIn directory and save TuneIn stations
to the speaker's preset keys. TuneIn is a second, independent radio
provider next to Radio Browser; neither depends on the other.

TuneIn is a trademark of TuneIn, Inc. STM is not affiliated with or
endorsed by TuneIn.

## What STM uses, and what it does not

- Only TuneIn's public OPML directory on `opml.radiotime.com`
  (`Browse.ashx`, `Search.ashx`, `Describe.ashx`, `Tune.ashx`, always with
  `render=json`). No login, no private or partner API.
- Streams are played exactly as TuneIn delivers them. STM never
  transcodes, never redistributes, and refuses encrypted HLS, so no DRM
  is ever touched.
- Share links (`tun.in/...`, `tunein.com/...`) are followed for at most
  five redirects, and only on TuneIn hosts.

## The stored reference

A TuneIn station is stored as `tunein:<stationId>` (for example
`tunein:s345726`) in presets, favourites and recently played. The URL
that `Tune.ashx` resolves to is temporary and can carry an `accessKey`,
so it is never persisted.

```mermaid
sequenceDiagram
    participant App as STM app
    participant Agent as Agent (on the speaker)
    participant Box as Speaker player
    participant TI as opml.radiotime.com
    App->>Agent: play / save preset tunein:s345726
    Box->>Agent: GET /stream/raw?u=... or /stream/<slot>
    Agent->>TI: Tune.ashx?id=s345726
    TI-->>Agent: temporary stream URL
    Agent-->>Box: audio, proxied as delivered
```

The speaker's own player only ever sees the agent's proxy URL. Every
fetch resolves a fresh stream URL, so an expired one never sticks to a
preset key.

## Failure handling

- A permanent upstream rejection (for example a 403 from an expired
  URL) re-resolves the station **once** and retries; a second failure is
  reported, never retried in a loop.
- A station with no compatible stream (TuneIn's `notcompatible`
  placeholder, or no MP3/AAC/HLS variant) answers 404 and shows up as
  `gone` in the stream status.
- Logs, stream status and the UI never contain an `accessKey`: the
  agent's log handler redacts token parameters centrally, and the
  resolved URL is never sent to the app.

## Sharing from the TuneIn app

- **Android:** STM accepts `ACTION_SEND` with `text/plain`; the link
  opens in the TuneIn tab.
- **iOS:** a Share Extension stores the link in the App Group
  `group.io.github.jcbenitezhe.soundtouchmanager`; the app picks it up
  the next time it becomes active. The `stmanager://share?text=...` URL
  scheme does the same inline.
- **Desktop:** paste the link into the TuneIn search box.

## Not verified yet

Packed-audio HLS (ID3 plus ADTS segments) is passed through to the
speaker but has only been checked against the fakebox, which does not
decode audio. Confirm on real hardware before relying on HLS-only
stations.
