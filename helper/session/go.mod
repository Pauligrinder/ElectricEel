module electric-eel-session

go 1.26.5

require (
	github.com/godbus/dbus v0.0.0-20190726142602-4481cbc300e2
	github.com/teslamotors/vehicle-command v0.4.1
	google.golang.org/protobuf v1.34.2
)

// Local patch of vehicle-command v0.4.1 with upstream PR #443
// (navigation_waypoints_request, tag 90) applied — see
// docs/vehicle-command-patch.md. The proxy/command.go
// hunk of that PR is intentionally NOT included: this app is BLE-only,
// and the proxy is the internet path.
replace github.com/teslamotors/vehicle-command => ./thirdparty/vehicle-command

require (
	github.com/JuulLabs-OSS/cbgo v0.0.1 // indirect
	github.com/cronokirby/saferith v0.33.0 // indirect
	github.com/go-ble/ble v0.0.0-20240122180141-8c5522f54333 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/konsorten/go-windows-terminal-sequences v1.0.1 // indirect
	github.com/mattn/go-colorable v0.1.6 // indirect
	github.com/mattn/go-isatty v0.0.12 // indirect
	github.com/mgutz/ansi v0.0.0-20170206155736-9520e82c474b // indirect
	github.com/mgutz/logxi v0.0.0-20161027140823-aebf8a7d67ab // indirect
	github.com/pkg/errors v0.8.1 // indirect
	github.com/raff/goble v0.0.0-20190909174656-72afc67d6a99 // indirect
	github.com/sirupsen/logrus v1.5.0 // indirect
	github.com/stretchr/testify v1.8.1 // indirect
	golang.org/x/sys v0.8.0 // indirect
)
