// SPDX-License-Identifier: Apache-2.0
package device

import "context"

type Device struct {
	Path       string
	Label      string
	StableID   string
	Connection string
	Retries    int
}

type Status string

const (
	Accepted       Status = "accepted"
	Invalid        Status = "invalid"
	Blocked        Status = "blocked"
	TemporaryBlock Status = "temporary-block"
	Unavailable    Status = "unavailable"
)

type Result struct {
	Status  Status
	Retries int
	Message string
}
type Backend interface {
	Discover(context.Context) ([]Device, error)
	Verify(context.Context, Device, []byte) Result
}
