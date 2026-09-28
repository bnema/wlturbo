# Canonical core / migration (v0.x)

`protocol/core` replaces root-package and `wl` shim core object types. Bootstrap
`Display`, `Registry`, `Context`, and `BaseProxy` remain in the root/`wl`
transport. Bind globals using `core.NewCompositor(display.Context())`,
`core.NewSeat(display.Context())`, etc. and `display.Registry().Bind(name,
core.CompositorInterface, version, compositor)`. Requests create typed registered
children (`seat.GetKeyboard()`, `surface.Frame()`, `shm.CreatePool()`) and
server-side `new_id` events register concrete objects before invoking handlers.
Do not overwrite registered objects with a second wrapper.

Breaking changes: `wlturbo`/`wl` `Seat`, `Surface`, `Pointer`, `Keyboard`,
`Touch`, `Output`, `Region`, `Compositor`, their constructors, and seat
capability aliases have moved to `protocol/core`. Generated methods accept
concrete core objects and return concrete children, not `wl.Object`. Legacy
raw `Event.Fd()` is not for generated events; generated FD handlers receive
`*wlturbo.OwnedFD` with `Take() (int,error)` or `Close()`; second and later
handlers receive nil FD. On a successful full send a generated FD request
closes the local descriptor; on error the caller keeps it. `Registry.BindID`
remains for legacy callers but cannot register typed event dispatch: prefer
`Bind` and generated constructors. Unrecognized custom-proxy event opcodes
now fail unless declared via `Display.RegisterEventSignature`.

The scanner accepts `-import wl_surface=github.com/bnema/wlturbo/protocol/core`
(repeat for each external type); local XML definitions take precedence, then
explicit external mapping, then only bootstrap display/registry. Generate with
`GOWORK=off go generate ./...`; metadata and XML license are in
`protocol/SOURCE.md`. P3 extensions will use the same scanner and public core
mappings; no generator-owned compositor policy is introduced here.
