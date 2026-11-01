package work

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrNotFound = errors.New("entry not found")

// Entry is a private work memory. IDs are JSON strings to preserve integer precision.
type Entry struct {
	ID         int64      `json:"id,string"`
	Title      string     `json:"title"`
	Notes      string     `json:"notes"`
	OccurredAt *time.Time `json:"occurred_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// EntryInput is the complete writable representation used for creation and PUT.
// Metrics, evidence links, and an occurrence date are not required.
type EntryInput struct {
	Title      string     `json:"title"`
	Notes      string     `json:"notes"`
	OccurredAt *time.Time `json:"occurred_at"`
}

func (input *EntryInput) validate() error {
	input.Title = strings.TrimSpace(input.Title)
	if strings.ContainsRune(input.Title, '\x00') || strings.ContainsRune(input.Notes, '\x00') {
		return errors.New("title and notes must not contain null characters")
	}
	if input.Title == "" || utf8.RuneCountInString(input.Title) > 200 {
		return errors.New("title must contain between 1 and 200 characters")
	}
	if utf8.RuneCountInString(input.Notes) > 20000 {
		return errors.New("notes must contain at most 20000 characters")
	}
	if input.OccurredAt != nil {
		normalized := input.OccurredAt.UTC()
		if year := normalized.Year(); year < 1 || year > 9999 {
			return errors.New("occurred_at must be an RFC3339 timestamp")
		}
		input.OccurredAt = &normalized
	}
	return nil
}
