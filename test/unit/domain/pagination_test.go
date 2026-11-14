package domain_test

import (
	"testing"

	"github.com/getcontact/backend/internal/domain"
)

func TestPaginationOffsetIsBounded(t *testing.T) {
	tests := []struct {
		name string
		p    domain.Pagination
		want int
	}{
		{"first page", domain.Pagination{Page: 1, PerPage: 20}, 0},
		{"second page", domain.Pagination{Page: 2, PerPage: 20}, 20},
		{"zero page", domain.Pagination{Page: 0, PerPage: 20}, 0},
		{"negative page", domain.Pagination{Page: -5, PerPage: 20}, 0},
		{"zero per page", domain.Pagination{Page: 3, PerPage: 0}, 40},
		{"oversized per page", domain.Pagination{Page: 2, PerPage: 10_000}, 100},
		{"huge page", domain.Pagination{Page: 1 << 30, PerPage: 100}, 1_000_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Offset(); got != tt.want {
				t.Fatalf("Offset() = %d, want %d", got, tt.want)
			}
		})
	}
}
