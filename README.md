# WLTurbo - High-Performance Wayland Client for Go

🚀 **Ultra-fast, zero-allocation Wayland client library optimized for gaming and real-time applications**

## Features

- **🎯 Sub-microsecond latency** - < 500ns input event processing
- **⚡ Zero-allocation hot paths** - No GC pressure during event handling
- **🎮 Gaming optimized** - 360-500+ FPS support, 8000Hz device compatibility
- **🔒 Lock-free architecture** - MPSC queues and atomic operations
- **🏎️ SIMD optimizations** - Vectorized batch operations
- **📊 Cache-friendly** - 64-byte aligned data structures
- **🔧 Go 1.24 ready** - Leverages latest performance features

## Performance Targets

| Metric | Target | Description |
|--------|--------|-------------|
| **Input Latency** | < 125μs | Input-to-dispatch (8000Hz polling rate) |
| **Event Dispatch** | < 50ns | Per-event processing time |
| **Allocations** | 0 | Zero allocations in steady state |
| **GC Pause** | < 10μs | Maximum garbage collection pause |
| **Throughput** | 16,000+ | Events per second capacity |

## Gaming Hardware Support

- **8000Hz Mice**: Razer Viper 8K, Corsair Sabre RGB Pro
- **8000Hz Keyboards**: Wooting 80HE with true analog scanning
- **High-refresh displays**: ASUS ROG Swift 500Hz, BenQ Zowie XL2566K
- **VRR/G-Sync**: Variable refresh rate awareness

## Quick Start

```go
package main

import (
    "github.com/bnema/wlturbo"
)

func main() {
    // Connect to Wayland display
    display, err := wlturbo.Connect("")
    if err != nil {
        panic(err)
    }
    defer display.Close()

    // Ultra-low latency event loop
    for {
        if err := display.Dispatch(); err != nil {
            break
        }
    }
}
```

## Architecture

WLTurbo is built with gaming performance in mind:

- **Lock-free Event Queue**: MPSC ring buffer for zero-contention event handling
- **Object Pool**: Pre-allocated event objects to eliminate malloc overhead
- **Direct Dispatch**: Array-based handler lookup for objects < 1024
- **Batch Processing**: Process multiple events per syscall
- **Memory Pool**: Size-classed allocators for temporary buffers

## Benchmarks

```
BenchmarkEventDispatch-16    20000000    50.2 ns/op    0 B/op    0 allocs/op
BenchmarkMessageSend-16      40000000    25.1 ns/op    0 B/op    0 allocs/op
BenchmarkFDPassing-16         5000000   200.0 ns/op    0 B/op    0 allocs/op
```

## Requirements

- **Go 1.24+** (for latest performance features)
- **Linux** (Wayland compositor required)
- **Modern CPU** (for SIMD optimizations)

## Status

🚧 **Active Development** - Core functionality complete, optimizations ongoing

## License

MIT License - see LICENSE file for details

## Contributing

PRs welcome! Please read CONTRIBUTING.md for guidelines.

Focus areas:
- Performance optimizations
- Gaming-specific features
- Protocol extension support
- Benchmark improvements