//go:build !cgo

package sysman

import "fmt"

// Sysman is unavailable in a build without cgo.
type Sysman struct{}

func Open() (*Sysman, error) {
	return nil, fmt.Errorf("%w: built without cgo", ErrUnavailable)
}

func (s *Sysman) Devices() []Device { return nil }

func (s *Sysman) Sample(int) (Sample, error) { return Sample{}, ErrUnavailable }
