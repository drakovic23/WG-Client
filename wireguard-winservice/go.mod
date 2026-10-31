module github.com/drakovic23/wg-vpn/wireguard-winservice

go 1.27.1

require (
	github.com/Microsoft/go-winio v0.6.2
	github.com/kardianos/service v1.3.0
	golang.zx2c4.com/wireguard v0.0.0-20260522210424-ecfc5a8d5446
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20241231184526-a9ab2273dd10
	golang.zx2c4.com/wireguard/windows v1.1.1
)

require (
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
)
