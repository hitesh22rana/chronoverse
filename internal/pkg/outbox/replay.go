package outbox

import (
	"errors"
	"time"
)

// NormalizeReplayIntervals shares omission and precision rules across env and repository callers.
func NormalizeReplayIntervals(interval, maximum time.Duration) (normalizedInterval, normalizedMaximum time.Duration, err error) {
	if interval == 0 {
		interval = 5 * time.Minute
	}
	if maximum == 0 {
		maximum = time.Hour
	}
	if interval < time.Millisecond || maximum < interval || interval%time.Millisecond != 0 || maximum%time.Millisecond != 0 {
		return 0, 0, errors.New("outbox replay intervals require whole milliseconds and 1ms <= interval <= maximum")
	}
	return interval, maximum, nil
}
