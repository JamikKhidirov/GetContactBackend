package service

import (
	"context"
	"strings"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/domain"
	"github.com/google/uuid"
)

// SearchResult combines public number profiles and the caller's contacts.
type SearchResult struct {
	Query    string           `json:"query"`
	Numbers  []domain.Number  `json:"numbers"`
	Contacts []domain.Contact `json:"contacts"`
}

// SearchService powers the unified search box.
type SearchService struct {
	numbers  domain.NumberRepository
	contacts domain.ContactRepository
}

// NewSearchService wires the search use case.
func NewSearchService(numbers domain.NumberRepository, contacts domain.ContactRepository) *SearchService {
	return &SearchService{numbers: numbers, contacts: contacts}
}

// Search validates and executes a search across numbers and private contacts.
func (s *SearchService) Search(ctx context.Context, userID uuid.UUID, query string, limit int) (*SearchResult, error) {
	q := strings.TrimSpace(query)
	if len([]rune(q)) < 2 || len([]rune(q)) > 64 {
		return nil, apperr.Invalid(map[string]string{"q": "must be between 2 and 64 characters"})
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	numbers, err := s.numbers.Search(ctx, q, limit)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	page, err := s.contacts.List(ctx, userID, q, nil, domain.Pagination{Page: 1, PerPage: limit})
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &SearchResult{Query: q, Numbers: numbers, Contacts: page.Items}, nil
}
