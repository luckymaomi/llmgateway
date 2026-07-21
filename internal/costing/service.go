package costing

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"golang.org/x/text/currency"
)

var decimalRatePattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,6})(?:\.([0-9]{1,9}))?$`)

func (s *Service) CreatePriceVersion(ctx context.Context, actor identity.Principal, input NewPriceVersion, mutation MutationRequest) (PriceVersion, error) {
	if !canManageCosts(actor) {
		return PriceVersion{}, ErrForbidden
	}
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	input.InputPricePerMillionTokens = strings.TrimSpace(input.InputPricePerMillionTokens)
	input.OutputPricePerMillionTokens = strings.TrimSpace(input.OutputPricePerMillionTokens)
	input.EffectiveAt = input.EffectiveAt.UTC()
	mutation.RequestID = strings.TrimSpace(mutation.RequestID)
	if input.ModelID == uuid.Nil || !validCurrency(input.Currency) || input.EffectiveAt.IsZero() || mutation.IdempotencyKey == uuid.Nil || mutation.RequestID == "" || utf8.RuneCountInString(mutation.RequestID) > 128 {
		return PriceVersion{}, ErrInvalidInput
	}
	if _, err := ParseRate(input.InputPricePerMillionTokens); err != nil {
		return PriceVersion{}, err
	}
	if _, err := ParseRate(input.OutputPricePerMillionTokens); err != nil {
		return PriceVersion{}, err
	}
	return s.repository.CreatePriceVersion(ctx, input, mutation, actor.UserID)
}

func (s *Service) ListPriceVersions(ctx context.Context, actor identity.Principal, modelID *uuid.UUID, page Page) ([]PriceVersion, error) {
	if !canManageCosts(actor) {
		return nil, ErrForbidden
	}
	if modelID != nil && *modelID == uuid.Nil {
		return nil, ErrInvalidInput
	}
	return s.repository.ListPriceVersions(ctx, modelID, normalizePage(page))
}

func (s *Service) ListSummaries(ctx context.Context, actor identity.Principal, page Page) ([]Summary, error) {
	if !canManageCosts(actor) {
		return nil, ErrForbidden
	}
	return s.repository.ListSummaries(ctx, normalizePage(page))
}

func ParseRate(value string) (int64, error) {
	match := decimalRatePattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return 0, ErrInvalidInput
	}
	whole, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, ErrInvalidInput
	}
	fraction := match[2] + strings.Repeat("0", 9-len(match[2]))
	fractionNanos, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil || whole > MaximumRateNanosPerMillion/1_000_000_000 {
		return 0, ErrInvalidInput
	}
	rate := whole*1_000_000_000 + fractionNanos
	if rate > MaximumRateNanosPerMillion {
		return 0, ErrInvalidInput
	}
	return rate, nil
}

func FormatRate(rateNanosPerMillion int64) string {
	if rateNanosPerMillion < 0 {
		return ""
	}
	whole := rateNanosPerMillion / 1_000_000_000
	fraction := rateNanosPerMillion % 1_000_000_000
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strconv.FormatInt(whole, 10) + "." + strings.TrimRight(fmt.Sprintf("%09d", fraction), "0")
}

func Calculate(tokens, rateNanosPerMillion int64) (int64, error) {
	if tokens < 0 || rateNanosPerMillion < 0 || rateNanosPerMillion > MaximumRateNanosPerMillion {
		return 0, ErrInvalidInput
	}
	product := new(big.Int).Mul(big.NewInt(tokens), big.NewInt(rateNanosPerMillion))
	product.Add(product, big.NewInt(999_999))
	product.Div(product, big.NewInt(1_000_000))
	if !product.IsInt64() || product.Int64() > math.MaxInt64 {
		return 0, ErrOverflow
	}
	return product.Int64(), nil
}

func validCurrency(value string) bool {
	_, err := currency.ParseISO(value)
	return err == nil
}

func normalizePage(page Page) Page {
	if page.Offset < 0 {
		page.Offset = 0
	}
	if page.Size < 1 {
		page.Size = 50
	}
	if page.Size > 200 {
		page.Size = 200
	}
	return page
}
