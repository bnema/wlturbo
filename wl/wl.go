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
	ArgUint             = wlturbo.ArgUint
	ArgInt              = wlturbo.ArgInt
	ArgFixed            = wlturbo.ArgFixed
	ArgString           = wlturbo.ArgString
	ArgArray            = wlturbo.ArgArray
	ArgObject           = wlturbo.ArgObject
	ArgFD               = wlturbo.ArgFD
	NewContext          = wlturbo.NewContext
	CloseSentFD         = wlturbo.CloseSentFD
	CheckVersion        = wlturbo.CheckVersion
	CreateAnonymousFile = wlturbo.CreateAnonymousFile
	MapMemory           = wlturbo.MapMemory
	UnmapMemory         = wlturbo.UnmapMemory
)

// Sentinels for the transport's error classes, so errors.Is works through the
// shim as it does through the root package.
var (
	ErrMalformedFrame = wlturbo.ErrMalformedFrame
	ErrUnknownObject  = wlturbo.ErrUnknownObject
	ErrUnknownOpcode  = wlturbo.ErrUnknownOpcode
	ErrDisplayError   = wlturbo.ErrDisplayError
	ErrVersionTooLow  = wlturbo.ErrVersionTooLow
)
