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

## Features

### Performance Optimizations

- **Zero-allocation event handling**: For messages under 4KB
- **Lock-free dispatcher**: For object IDs < 1024 using atomic operations  
- **Pre-allocated buffers**: Reusable buffers to minimize allocations
- **Direct array indexing**: Fast opcode lookup for common cases
- **Buffer pooling**: sync.Pool for temporary allocations
- **File descriptor passing**: Efficient shared memory via SCM_RIGHTS

### Design Goals

- Minimal allocations in hot paths
- Efficient event dispatching
- Clean API for protocol extensions
- Foundation for high-performance Wayland applications

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

    // Basic event loop
    for {
        if err := display.Dispatch(); err != nil {
            break
        }
    }
}
```

For device control and input injection, use [libwldevices-go](https://github.com/bnema/libwldevices-go) which builds on top of WLTurbo.

## Implementation Status

### Core Protocol ✅
- Display, Registry, Compositor, Surface, Seat, Region
- Event dispatching and handler registration
- File descriptor passing for shared memory
- Context-based proxy management

### Optimizations ✅
- Zero-allocation event handling (messages < 4KB)
- Lock-free dispatcher for common cases
- Pre-allocated and pooled buffers
- Efficient event routing

### Future Improvements
- Performance benchmarks and validation
- Additional optimizations based on profiling
- Extended protocol object support as needed

## Requirements

- **Go 1.21+**
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