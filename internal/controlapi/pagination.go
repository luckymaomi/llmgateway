package controlapi

import (
	"net/http"
	"strconv"
	"strings"
)

type listQuery struct {
	Page           int
	PageSize       int
	Search         string
	Status         string
	ProviderID     string
	ResourcePoolID string
	SubscriptionID string
	UserID         string
	GatewayKeyID   string
	ModelID        string
	From           string
	To             string
}

type pageView[T any] struct {
	Items    []T   `json:"items"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
	Total    int64 `json:"total"`
}

func parseListQuery(r *http.Request) listQuery {
	page := positiveQueryInteger(r.URL.Query().Get("page"), 1)
	pageSize := positiveQueryInteger(r.URL.Query().Get("pageSize"), 20)
	if pageSize > 100 {
		pageSize = 100
	}
	return listQuery{
		Page:           page,
		PageSize:       pageSize,
		Search:         r.URL.Query().Get("search"),
		Status:         r.URL.Query().Get("status"),
		ProviderID:     r.URL.Query().Get("providerId"),
		ResourcePoolID: r.URL.Query().Get("resourcePoolId"),
		SubscriptionID: r.URL.Query().Get("subscriptionId"),
		UserID:         r.URL.Query().Get("userId"),
		GatewayKeyID:   r.URL.Query().Get("apiKeyId"),
		ModelID:        r.URL.Query().Get("modelId"),
		From:           r.URL.Query().Get("from"),
		To:             r.URL.Query().Get("to"),
	}
}

func paginate[T any](items []T, query listQuery) pageView[T] {
	total := len(items)
	start := (query.Page - 1) * query.PageSize
	if start > total {
		start = total
	}
	end := start + query.PageSize
	if end > total {
		end = total
	}
	pageItems := make([]T, end-start)
	copy(pageItems, items[start:end])
	return pageView[T]{Items: pageItems, Page: query.Page, PageSize: query.PageSize, Total: int64(total)}
}

func (q listQuery) offset() int32 {
	return int32((q.Page - 1) * q.PageSize)
}

func positiveQueryInteger(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func containsFold(value, search string) bool {
	return search == "" || strings.Contains(strings.ToLower(value), strings.ToLower(search))
}
