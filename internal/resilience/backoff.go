package resilience

import (
	"math"
	"time"
)

type BackoffConfig struct {
	Initial               time.Duration
	Maximum               time.Duration
	MultiplierNumerator   int64
	MultiplierDenominator int64
	JitterPermille        int32
}

func (c BackoffConfig) validate(random Random) error {
	if c.Initial <= 0 || c.Maximum < c.Initial {
		return newError(ErrorInvalidConfiguration, "backoff durations must be positive and ordered")
	}
	if c.MultiplierDenominator <= 0 || c.MultiplierDenominator > 1000 ||
		c.MultiplierNumerator < c.MultiplierDenominator || c.MultiplierNumerator > 1000 ||
		c.MultiplierNumerator > c.MultiplierDenominator*100 {
		return newError(ErrorInvalidConfiguration, "backoff multiplier must be between one and one hundred")
	}
	if c.JitterPermille < 0 || c.JitterPermille > 1000 {
		return newError(ErrorInvalidConfiguration, "backoff jitter must be a permille value")
	}
	if c.Maximum > time.Duration(math.MaxInt64/2000) {
		return newError(ErrorInvalidConfiguration, "maximum backoff exceeds the fixed-point range")
	}
	if c.JitterPermille > 0 && random == nil {
		return newError(ErrorInvalidConfiguration, "backoff jitter requires a random source")
	}
	return nil
}

func backoffDelay(config BackoffConfig, retryOrdinal int, random Random) (time.Duration, error) {
	base := config.Initial
	for ordinal := 1; ordinal < retryOrdinal && base < config.Maximum; ordinal++ {
		if int64(base) > int64(config.Maximum)*config.MultiplierDenominator/config.MultiplierNumerator {
			base = config.Maximum
			break
		}
		next := time.Duration((int64(base)*config.MultiplierNumerator + config.MultiplierDenominator - 1) / config.MultiplierDenominator)
		if next > config.Maximum {
			next = config.Maximum
		}
		base = next
	}
	if config.JitterPermille == 0 {
		return base, nil
	}
	spread := time.Duration(int64(base) * int64(config.JitterPermille) / 1000)
	lower := base - spread
	upper := base + spread
	if upper > config.Maximum {
		upper = config.Maximum
	}
	width := int64(upper-lower) + 1
	offset := random.Int63n(width)
	if offset < 0 || offset >= width {
		return 0, newError(ErrorRandomSource, "random source returned a value outside its requested range")
	}
	return lower + time.Duration(offset), nil
}
