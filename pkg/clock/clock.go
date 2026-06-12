package clock

import "time"

// System is the production Clock implementation (satisfies ports.Clock).
type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

// Fixed is a deterministic Clock for tests.
type Fixed struct{ T time.Time }

func (f Fixed) Now() time.Time { return f.T }
