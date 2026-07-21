package costing

import (
	"errors"
	"math"
	"testing"
)

func TestRateParsingFormattingAndCostRounding(t *testing.T) {
	rate, err := ParseRate("3.25")
	if err != nil || rate != 3_250_000_000 || FormatRate(rate) != "3.25" {
		t.Fatalf("rate round trip = %d, %q, %v", rate, FormatRate(rate), err)
	}
	cost, err := Calculate(1, rate)
	if err != nil || cost != 3250 {
		t.Fatalf("Calculate() = %d, %v", cost, err)
	}
	if _, err := ParseRate("1.0000000001"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ParseRate() error = %v", err)
	}
	if validCurrency("AAA") || !validCurrency("CNY") || !validCurrency("USD") {
		t.Fatal("ISO 4217 currency validation did not preserve the supported contract")
	}
	if _, err := Calculate(math.MaxInt64, MaximumRateNanosPerMillion); !errors.Is(err, ErrOverflow) {
		t.Fatalf("Calculate() overflow error = %v", err)
	}
}
