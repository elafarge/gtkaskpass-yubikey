// SPDX-License-Identifier: Apache-2.0
package dialog

import (
	"github.com/elafarge/gtkaskpass-yubikey/internal/askpass"
	"github.com/elafarge/gtkaskpass-yubikey/internal/device"
	"time"
)

type View struct {
	Request      askpass.Request
	Stage        string // input, choose, wait, error, busy, close
	Message      string
	TTL          time.Duration
	Devices      []device.Device
	Selected     int
	ChangeDevice bool
	PassiveTouch bool
}
type Action struct {
	Kind     string
	Value    string
	Selected int
	Remember bool
}
type Result struct {
	Value    string
	Accepted bool
	Remember bool
}
