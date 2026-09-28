// Package core contains canonical Wayland core protocol bindings generated from
// pinned wayland.xml. Bind globals with display.Registry().Bind and the
// corresponding New* constructor. Client-created and server-created children
// are registered with their concrete generated types; never replace them with
// transport wrappers. On* handlers receive typed values. Received OwnedFDs
// are closed after the event unless a handler calls Take; request FDs are
// transferred (and closed locally) only after a complete successful send.
package core
