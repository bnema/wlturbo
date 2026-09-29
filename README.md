# WLTurbo - Wayland Client Library for Go

A performance-focused Wayland client library that provides the foundational protocol implementation for building Wayland applications and libraries.

## Overview

WLTurbo is a low-level Wayland client library that handles core protocol communication. It serves as the base layer for higher-level libraries like [libwldevices-go](https://github.com/bnema/libwldevices-go) which implement specific Wayland protocol extensions.

## Architecture

WLTurbo provides the foundational Wayland client infrastructure:

- **Core Protocol Objects**: Display, Registry, Compositor, Surface, Seat, Region
- **Event Dispatching**: Efficient routing of Wayland events to handlers
- **Connection Management**: Unix socket communication with the compositor
- **Memory Management**: Shared memory support via file descriptor passing

Higher-level protocol implementations (virtual input devices, output management, etc.) are intentionally left to specialized libraries that build on top of WLTurbo.

## Generated protocols

`protocol/core` provides core Wayland (including data-device); `protocol/xdgshell` provides xdg-shell; `protocol/linuxdmabuf` provides linux-dmabuf v4 feedback; `protocol/drmsyncobj` provides linux-drm-syncobj v1; `protocol/viewporter` provides viewporter; `protocol/fractionalscale` provides fractional-scale v1; and `protocol/textinput` provides text-input v3. Bindings handle wire messages and object lifecycle; applications decide compositor policy.

After `display.Roundtrip()` discovers globals, call `display.Registry().BindNegotiated(interfaceName, supportedVersion, proxy)` to bind at the lesser of the advertised and supported versions. It returns the negotiated version; use `errors.Is(err, wlturbo.ErrGlobalNotFound)` to handle absent optional globals.

## Features

- **Stream framing**: messages split across or coalesced within socket reads are framed exactly; event bodies are not copied.
- **Descriptor ownership**: received FDs belong to the event that declares them and are closed if a handler does not take them.
- **Object lifecycle**: destroyed objects stay as zombies until `delete_id`, so events already in flight are dropped instead of failing the connection.
- **Version checks**: generated requests newer than the bound object version return `ErrVersionTooLow` before anything is sent.
- **Allocation-free requests**: marshaling fixed-size requests does not allocate.

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

For device control and input injection, use [libwldevices-go](https://github.com/bnema/libwldevices-go) which builds on top of WLTurbo.

## Requirements

- **Go 1.27+**
- **Linux** with Wayland compositor
- **Unix sockets** support

## License

MIT License - see LICENSE file for details

## Contributing

Contributions welcome! Areas of focus:
- Performance improvements
- Additional protocol object support
- Documentation and examples
- Benchmark suite