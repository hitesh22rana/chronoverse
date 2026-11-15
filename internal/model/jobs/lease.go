package jobs

import (
	"errors"
	"math"
	"time"
)

// ValidateLeaseTiming keeps local timing consistent with the jobs RPC's int32 seconds.
func ValidateLeaseTiming(duration, renewalInterval time.Duration) error {
	if duration < time.Second || duration%time.Second != 0 || duration/time.Second > math.MaxInt32 {
		return errors.New("LEASE_DURATION: must be whole seconds between 1 and 2147483647 seconds")
	}
	if renewalInterval <= 0 || (duration-renewalInterval)/3 <= 0 {
		return errors.New("LEASE_RENEW_INTERVAL: must be positive and leave a positive renewal RPC budget below LEASE_DURATION")
	}
	return nil
}
