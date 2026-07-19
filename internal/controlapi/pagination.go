package controlapi

import (
	"net/http"
	"strconv"
)

type listQuery struct {
	Page           int
	PageSize       int
	Search         string
	Status         string
	ProviderID     string
	ResourceDomain string
}

type pageView[T any] struct {
	Items    []T `json:"items"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
	Total    int `json:"total"`
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
		ResourceDomain: r.URL.Query().Get("resourceDomain"),
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
	pageItems := append([]T(nil), items[start:end]...)
	return pageView[T]{Items: pageItems, Page: query.Page, PageSize: query.PageSize, Total: total}
}

func positiveQueryInteger(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func prefix(value string, length int) string {
	if len(value) <= length {
		return value
	}
	return value[:length]
}
