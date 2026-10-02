# WLTurbo

A performance-focused, low-level Wayland client runtime for Go.

## Overview

WLTurbo is the transport layer: connection, wire framing, descriptor passing, object lifecycle and the bootstrap registry. It is not a complete Wayland client. It has no windows, surfaces, seats, input handling or rendering, and it knows no protocol beyond the bootstrap objects. Protocol bindings are provided separately (see [Protocol bindings](#protocol-bindings)).

## Where it fits

```
Application
└─ neferclient             connection helpers, surfaces, seat, DMA-BUF presentation
   └─ go-wayland-bindings  one package per protocol
      └─ wlturbo           transport and objects  (this module)
```

Use WLTurbo directly to speak raw Wayland or to write bindings. For an application, start with [neferclient](https://github.com/bnema/neferclient).

## Architecture

WLTurbo provides the foundational Wayland client infrastructure:

- **Bootstrap Objects**: Display and Registry
- **Event Dispatching**: Efficient routing of Wayland events to handlers
- **Connection Management**: Unix socket communication with the compositor
- **Memory Management**: Shared memory support via file descriptor passing

Applications own rendering, input interpretation, capability fallbacks and desktop policy. WLTurbo is client-side only, not a compositor-side server implementation.

## Protocol bindings

Generated protocol bindings are not part of this module. They live in [github.com/bnema/go-wayland-bindings](https://github.com/bnema/go-wayland-bindings), one package per protocol under `client/<pkg>`, for example `client/wayland`, `client/xdgshell` and `client/wlrlayershell`:

```go
import "github.com/bnema/go-wayland-bindings/client/xdgshell"
```

Those packages are built on this runtime. A binding's presence does not imply that the compositor implements it.

After `display.Roundtrip()` discovers globals, call `display.Registry().BindNegotiated(interfaceName, supportedVersion, proxy)` to bind at the lesser of the advertised and supported versions. It returns the negotiated version; use `errors.Is(err, wlturbo.ErrGlobalNotFound)` to handle absent optional globals.

## Features

- **Stream framing**: messages split across or coalesced within socket reads are framed exactly; event bodies are not copied.
- **Descriptor ownership**: received FDs belong to the event that declares them and are closed if a handler does not take them.
- **Object lifecycle**: destroyed objects stay as zombies until `delete_id`, so events already in flight are dropped instead of failing the connection.
- **Version checks**: requests newer than the bound object version return `ErrVersionTooLow` before anything is sent.
- **Allocation-free numeric paths**: numeric requests sent with `Context.RequestArgs` without object creation or descriptors, and typed numeric events, do not allocate after warm-up. Object creation, received strings and descriptors have separate allocation costs.

## Quick Start

This opens a connection and dispatches events. Binding globals needs the protocol packages from `go-wayland-bindings`.

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
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
GOWORK=off go test -run '^$' -bench . -benchmem
```

Socketpair tests exercise wire messages, versions, object lifecycle and file descriptors without a compositor.

## Requirements

- **Go 1.27+**
- **Linux** with Wayland compositor
- **Unix sockets** support

## License

MIT; see [LICENSE](LICENSE).

## Contributing

Contributions welcome! Areas of focus:
- Performance improvements
- Transport correctness and hardening
- Documentation and examples
- Benchmark suite
