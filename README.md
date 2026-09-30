# WLTurbo - Wayland Client Library for Go

A performance-focused Wayland client library that provides the foundational protocol implementation for building Wayland applications and libraries.

## Overview

WLTurbo is a low-level Wayland client library with generated core and extension bindings. Applications and higher-level libraries use it to communicate with any compositor that advertises the required protocols.

## Architecture

WLTurbo provides the foundational Wayland client infrastructure:

- **Core Protocol Objects**: Display, Registry, Compositor, Surface, Seat, Region
- **Event Dispatching**: Efficient routing of Wayland events to handlers
- **Connection Management**: Unix socket communication with the compositor
- **Memory Management**: Shared memory support via file descriptor passing

Bindings handle wire messages and object lifecycle. Applications own rendering, input interpretation, capability fallbacks and desktop policy. WLTurbo provides client bindings, not compositor-side server implementations.

## Generated protocols

Packages under `protocol/` cover:

| Area | Packages |
|---|---|
| Core and windows | `core`, `xdgshell`, `xdgdecoration`, `xdgactivation` |
| Buffers and scaling | `linuxdmabuf`, `drmsyncobj`, `viewporter`, `fractionalscale` |
| Presentation and color | `presentation`, `tearingcontrol`, `fifo`, `committiming`, `contenttype`, `alphamodifier`, `colormanagement`, `colorrepresentation`, `drmlease` |
| Input | `cursorshape`, `tablet`, `textinput`, `relativepointer`, `pointerconstraints`, `pointerwarp`, `shortcutsinhibit`, `virtualkeyboard`, `inputmethod` |
| Clipboard | Core data-device, `primaryselection`, `datacontrol` (ext-data-control) |
| Desktop and outputs | `layershell`, `xdgoutput`, `outputmanagement`, `outputpower`, `workspace`, `foreigntoplevel` (wlr), `extforeigntoplevel`, `extsessionlock`, `kdeserverdecoration` |
| Idle | `idleinhibit`, `idlenotify` |
| Capture | `screencopy` (wlr), `imagecapturesource`, `imagecopycapture` |

Each package contains the upstream XML, generated Go bindings and a generation command. [Protocol sources](protocol/SOURCE.md) records pinned upstream revisions and checksums. A binding's presence does not imply that the compositor implements it.

After `display.Roundtrip()` discovers globals, call `display.Registry().BindNegotiated(interfaceName, supportedVersion, proxy)` to bind at the lesser of the advertised and supported versions. It returns the negotiated version; use `errors.Is(err, wlturbo.ErrGlobalNotFound)` to handle absent optional globals.

## Features

- **Stream framing**: messages split across or coalesced within socket reads are framed exactly; event bodies are not copied.
- **Descriptor ownership**: received FDs belong to the event that declares them and are closed if a handler does not take them.
- **Object lifecycle**: destroyed objects stay as zombies until `delete_id`, so events already in flight are dropped instead of failing the connection.
- **Version checks**: generated requests newer than the bound object version return `ErrVersionTooLow` before anything is sent.
- **Allocation-free numeric paths**: numeric generated requests without object creation or descriptors, and typed numeric events, do not allocate after warm-up; representative cases have allocation regression tests. Object creation, received strings and descriptors have separate allocation costs.

## Quick Start

```go
package main

import (
    "github.com/bnema/wlturbo/wl"
)

func main() {
    // Connect to Wayland display
    display, err := wl.Connect("")
    if err != nil {
        panic(err)
    }
    defer display.Close()

    // Basic event loop. Handlers run inside Dispatch and must not call
    // Dispatch or Roundtrip on the same display.
    for {
        if err := display.Dispatch(); err != nil {
            break
        }
    }
}
```

For higher-level device control, [libwldevices-go](https://github.com/bnema/libwldevices-go) builds on WLTurbo.

## Development checks

```sh
GOWORK=off go generate ./...
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
GOWORK=off go test ./protocol -run '^$' -bench . -benchmem
```

Tests verify that checked-in bindings reproduce from the vendored XML. Socketpair tests exercise wire messages, versions, object lifecycle and file descriptors without a compositor.

For isolated integration tests, build [NeferWL](https://github.com/bnema/neferwl) with its headless backend and pass its executable path:

```sh
env GOWORK=off WLTURBO_HEADLESS=/path/to/neferwl go test ./protocol -run Headless -v -count=1 -timeout=90s
```

The tests start a separate compositor with temporary runtime/config directories and no terminal or Xwayland. They do not use the running desktop session. Headless tests do not validate physical display timing, DRM leasing or hardware HDR output.

## Requirements

- **Go 1.27+**
- **Linux** with Wayland compositor
- **Unix sockets** support

## License

The transport and scanner are MIT-licensed; see [LICENSE](LICENSE).
Vendored protocol XML and generated bindings retain upstream notices and terms.
In particular, `kdeserverdecoration` is LGPL-2.1-or-later. See
[protocol sources](protocol/SOURCE.md), the generated file headers and the
[included LGPL text](LICENSES/LGPL-2.1-or-later.txt).

## Contributing

Contributions welcome! Areas of focus:
- Performance improvements
- Additional protocol object support
- Documentation and examples
- Benchmark suite