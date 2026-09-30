// Package wl provides type aliases for easy migration from neurlang/wayland
package wl

import (
	"github.com/bnema/wlturbo"
)

// Type aliases for compatibility
type (
	Display                     = wlturbo.Display
	Registry                    = wlturbo.Registry
	Context                     = wlturbo.Context
	Fixed                       = wlturbo.Fixed
	Object                      = wlturbo.Object
	Proxy                       = wlturbo.Proxy
	BaseProxy                   = wlturbo.BaseProxy
	Event                       = wlturbo.Event
	Global                      = wlturbo.Global
	GlobalHandler               = wlturbo.GlobalHandler
	RegistryGlobalHandler       = wlturbo.RegistryGlobalHandler
	RegistryGlobalRemoveHandler = wlturbo.RegistryGlobalRemoveHandler
	RegistryGlobalEvent         = wlturbo.RegistryGlobalEvent
	RegistryGlobalRemoveEvent   = wlturbo.RegistryGlobalRemoveEvent
	OwnedFD                     = wlturbo.OwnedFD
	Request                     = wlturbo.Request
	Arg                         = wlturbo.Arg

	// Transport failures. Without these, a caller that imports this shim has to
	// import the root package as well just to classify an error.
	DisplayError  = wlturbo.DisplayError
	ProtocolError = wlturbo.ProtocolError
)

// Function aliases
var (
	Connect             = wlturbo.Connect
	NewFixed            = wlturbo.NewFixed
	NewContext          = wlturbo.NewContext
	CloseSentFD         = wlturbo.CloseSentFD
	CheckVersion        = wlturbo.CheckVersion
	CreateAnonymousFile = wlturbo.CreateAnonymousFile
	MapMemory           = wlturbo.MapMemory
	UnmapMemory         = wlturbo.UnmapMemory
)

// ArgUint constructs a uint argument.
func ArgUint(v uint32) Arg { return wlturbo.ArgUint(v) }

// ArgInt constructs an int argument.
func ArgInt(v int32) Arg { return wlturbo.ArgInt(v) }

// ArgFixed constructs a fixed-point argument.
func ArgFixed(v Fixed) Arg { return wlturbo.ArgFixed(v) }

// ArgString constructs a string argument borrowed until the request returns.
func ArgString(v string) Arg { return wlturbo.ArgString(v) }

// ArgArray constructs an array argument borrowed until the request returns.
func ArgArray(v []byte) Arg { return wlturbo.ArgArray(v) }

// ArgObject constructs an object or new_id argument; nil is sent as 0.
func ArgObject(v Object) Arg { return wlturbo.ArgObject(v) }

// ArgFD marks a descriptor position; the descriptor travels in Request.FDs.
func ArgFD() Arg { return wlturbo.ArgFD() }

// Sentinels for the transport's error classes, so errors.Is works through the
// shim as it does through the root package.
var (
	ErrMalformedFrame = wlturbo.ErrMalformedFrame
	ErrUnknownObject  = wlturbo.ErrUnknownObject
	ErrUnknownOpcode  = wlturbo.ErrUnknownOpcode
	ErrDisplayError   = wlturbo.ErrDisplayError
	ErrVersionTooLow  = wlturbo.ErrVersionTooLow
)
