package foreigntoplevel

//go:generate go run ../../cmd/wlturbo-scanner -p foreigntoplevel -o wlr-foreign-toplevel-management-unstable-v1_generated.go -import wl_output=github.com/bnema/wlturbo/protocol/core -import wl_seat=github.com/bnema/wlturbo/protocol/core -import wl_surface=github.com/bnema/wlturbo/protocol/core wlr-foreign-toplevel-management-unstable-v1.xml
