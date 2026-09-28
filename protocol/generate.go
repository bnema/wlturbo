package protocol

//go:generate go run ../cmd/wlturbo-scanner -p core -o core/wayland_generated.go wayland.xml
//go:generate go run ../cmd/wlturbo-scanner -p xdgshell -o xdgshell/xdg-shell_generated.go -import wl_surface=github.com/bnema/wlturbo/protocol/core -import wl_seat=github.com/bnema/wlturbo/protocol/core -import wl_output=github.com/bnema/wlturbo/protocol/core xdgshell/xdg-shell.xml
//go:generate go run ../cmd/wlturbo-scanner -p linuxdmabuf -o linuxdmabuf/linux-dmabuf-v1_generated.go -import wl_surface=github.com/bnema/wlturbo/protocol/core -import wl_buffer=github.com/bnema/wlturbo/protocol/core linuxdmabuf/linux-dmabuf-v1.xml
//go:generate go run ../cmd/wlturbo-scanner -p drmsyncobj -o drmsyncobj/linux-drm-syncobj-v1_generated.go -import wl_surface=github.com/bnema/wlturbo/protocol/core drmsyncobj/linux-drm-syncobj-v1.xml
//go:generate go run ../cmd/wlturbo-scanner -p viewporter -o viewporter/viewporter_generated.go -import wl_surface=github.com/bnema/wlturbo/protocol/core viewporter/viewporter.xml
//go:generate go run ../cmd/wlturbo-scanner -p fractionalscale -o fractionalscale/fractional-scale-v1_generated.go -import wl_surface=github.com/bnema/wlturbo/protocol/core fractionalscale/fractional-scale-v1.xml
//go:generate go run ../cmd/wlturbo-scanner -p textinput -o textinput/text-input-unstable-v3_generated.go -import wl_surface=github.com/bnema/wlturbo/protocol/core -import wl_seat=github.com/bnema/wlturbo/protocol/core textinput/text-input-unstable-v3.xml
