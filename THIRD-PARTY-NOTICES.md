# Third-party notices

STM is licensed under the terms in [LICENSE](LICENSE). It distributes the
components below, each under its own licence.

This file covers what is **distributed**: the bundled Spotify engine, the
certificate store, and the libraries linked into the released binaries. Build
tools that never reach a user are not listed.

---

## go-librespot

**What is distributed:** `go-librespot-armv7l`, a separate executable shipped
as a release asset and embedded in the desktop app, which installs it on the
speaker.

**Licence:** GNU General Public License v3.0
(<https://www.gnu.org/licenses/gpl-3.0.html>)

**Upstream:** devgianlu/go-librespot, <https://github.com/devgianlu/go-librespot>

**The source of the binary STM ships:** STM builds it from a fork vendored in
this repository at [`third_party/go-librespot/`](third_party/go-librespot/),
which carries the Ogg passthrough the speaker needs. The fork was created by
Jens Roggenfelder (originally published as `JRpersonal/go-librespot`); the
copy here is its `master` at commit `ca90623d555c`, and
`third_party/go-librespot.base` names the last upstream commit it contains. The
build workflows (`.github/workflows/go-librespot.yml` and the engine job in
`release.yml`) build from that directory and log the STM commit, so the
complete corresponding source of every binary STM distributes is in this
repository under the same licence (`third_party/go-librespot/LICENSE`).

go-librespot is a **separate program**. It is not linked into the STM agent or
the desktop app: STM starts it as its own process and talks to it over a local
socket. Distributing the two together is aggregation, so the GPL applies to
go-librespot and its fork, not to STM.

---

## Mozilla CA certificate store

**What is distributed:** `desktop-app/cacert.pem` and
`internal/tlsgen/extraroots.pem`, the latter embedded in the agent binary.

**Licence:** Mozilla Public License 2.0 (<https://www.mozilla.org/MPL/2.0/>)

**Source:** Mozilla's root certificate authorities as extracted and
redistributed by the curl project,
<https://curl.se/docs/caextract.html>. Both files carry the extraction date in
their header.

`internal/tlsgen/extraroots.pem` is a pinned subset of `desktop-app/cacert.pem`,
regenerated with `make ca-roots`. The selection is in
`internal/tlsgen/extractroots.py`, so every change to the set of authorities
STM carries is a reviewed edit.

**Why STM ships them.** Some SoundTouch speakers were manufactured with a
certificate store that is missing widely used authorities, so they refuse most
https radio stations. That file belongs to the speaker's read-only firmware, a
factory reset cannot change it, and Bose's update servers are gone. The agent
composes a bundle in memory from the speaker's own store plus these roots and
points its own connections at it. Nothing on the speaker is modified, and what
the Bose firmware trusts is left exactly as Bose shipped it. See
`internal/tlsgen/extraroots.go` and [docs/THREAT-MODEL.md](docs/THREAT-MODEL.md).

---

## Libraries linked into the STM binaries

| Component | Licence | Source |
|---|---|---|
| Go standard library and toolchain | BSD-3-Clause | <https://go.dev> |
| Wails | MIT | <https://wails.io> |
| gorilla/websocket | BSD-2-Clause | <https://github.com/gorilla/websocket> |
| grandcat/zeroconf | MIT | <https://github.com/grandcat/zeroconf> |
| golang.org/x/sys | BSD-3-Clause | <https://pkg.go.dev/golang.org/x/sys> |
| Octicons | MIT | <https://github.com/primer/octicons> |

The BSD and MIT licences require their copyright notice and permission notice
to travel with binary distributions, which is what this file is for. The full
licence texts are in each project's repository at the addresses above.

---

## Services STM talks to

Not distributed, listed because the application depends on them:

- **radio-browser.info**, the community station directory,
  <https://www.radio-browser.info>
- **DuckDuckGo icon service**, used for station logos, <https://duckduckgo.com>

---

## Reverse engineering credits

STM ships no code from these projects, but several of its central mechanisms
would not exist without their published work. They are named in the
application's Open Source dialog.
