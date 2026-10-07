// Package summary defines the shared vocabulary for summary requests.
package summary

import "errors"

type Style string

type Result struct {
	TLDR   []string
	Title  string
	Points []string
}

const (
	Standard Style = ""
	Concrete Style = "concrete"
	Detailed Style = "detailed"
	Simple   Style = "simple"
)

var ErrInvalidStyle = errors.New("invalid summary style")

func (s Style) Valid() bool {
	switch s {
	case Standard, Concrete, Detailed, Simple:
		return true
	default:
		return false
	}
}
