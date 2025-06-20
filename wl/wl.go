// Package wl provides type aliases for easy migration from neurlang/wayland
package wl

import (
	"github.com/bnema/wlturbo"
)


// Type aliases for compatibility
type (
	Display                    = wlturbo.Display
	Registry                   = wlturbo.Registry
	Context                    = wlturbo.Context
	Fixed                      = wlturbo.Fixed
	Object                     = wlturbo.Object
	Proxy                      = wlturbo.Proxy
	BaseProxy                  = wlturbo.BaseProxy
	Event                      = wlturbo.Event
	Global                     = wlturbo.Global
	GlobalHandler              = wlturbo.GlobalHandler
	RegistryGlobalHandler      = wlturbo.RegistryGlobalHandler
	RegistryGlobalRemoveHandler = wlturbo.RegistryGlobalRemoveHandler
	RegistryGlobalEvent        = wlturbo.RegistryGlobalEvent
	RegistryGlobalRemoveEvent  = wlturbo.RegistryGlobalRemoveEvent
	Seat                       = wlturbo.Seat
	Surface                    = wlturbo.Surface
	Pointer                    = wlturbo.Pointer
	Keyboard                   = wlturbo.Keyboard
	Touch                      = wlturbo.Touch
	Output                     = wlturbo.Output
	Region                     = wlturbo.Region
	Compositor                 = wlturbo.Compositor
)

// Function aliases
var (
	Connect             = wlturbo.Connect
	NewFixed            = wlturbo.NewFixed
	NewSeat             = wlturbo.NewSeat
	NewContext          = wlturbo.NewContext
	NewSurface          = wlturbo.NewSurface
	NewCompositor       = wlturbo.NewCompositor
	CreateAnonymousFile = wlturbo.CreateAnonymousFile
	MapMemory           = wlturbo.MapMemory
	UnmapMemory         = wlturbo.UnmapMemory
)

// Seat capability constants
const (
	SeatCapabilityPointer  = wlturbo.SeatCapabilityPointer
	SeatCapabilityKeyboard = wlturbo.SeatCapabilityKeyboard
	SeatCapabilityTouch    = wlturbo.SeatCapabilityTouch
)