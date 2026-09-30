package domain_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/media/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

func TestSniff(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		head []byte
		want domain.ContentType
		err  error
	}{
		{"pdf", []byte("%PDF-1.7\n"), domain.PDF, nil},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F'}, domain.JPEG, nil},
		{"png", []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, domain.PNG, nil},
		{"html named .pdf", []byte("<html><script>"), "", domain.ErrUnsupportedType},
		{"windows program", []byte("MZ\x90\x00\x03\x00"), "", domain.ErrUnsupportedType},
		{"svg (can carry script)", []byte("<svg xmlns="), "", domain.ErrUnsupportedType},
		{"truncated png", []byte{0x89, 'P', 'N', 'G'}, "", domain.ErrUnsupportedType},
		{"nothing", nil, "", domain.ErrUnsupportedType},
	}
	for _, tt := range tests {
		got, err := domain.Sniff(tt.head)
		if got != tt.want || !errors.Is(err, tt.err) {
			t.Errorf("%s: Sniff = %q, %v; want %q, %v", tt.name, got, err, tt.want, tt.err)
		}
	}
}

func TestNewObject(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 1, 9, 0, 0, 123456789, time.UTC)
	sum := bytes.Repeat([]byte{1}, 32)
	id, by := shared.NewID[shared.MediaTag](), shared.NewID[shared.UserTag]()

	o, err := domain.NewObject(id, domain.PurposeCRDocument, domain.PDF, 1234, sum, by, now)
	if err != nil {
		t.Fatal(err)
	}
	if o.ID() != id || o.Size() != 1234 || o.ContentType() != domain.PDF || o.CreatedBy() != by ||
		!o.CreatedAt().Equal(now.Truncate(time.Microsecond)) || !bytes.Equal(o.SHA256(), sum) {
		t.Errorf("object = %+v", o)
	}
	sum[0] = 9 // the object keeps its own copy
	if o.SHA256()[0] != 1 {
		t.Error("changing the caller's slice changed the object")
	}

	for name, tt := range map[string]struct {
		size int64
		ct   domain.ContentType
		p    domain.Purpose
		want error
	}{
		"empty":        {0, domain.PDF, domain.PurposeCRDocument, domain.ErrEmpty},
		"over 10 MiB":  {domain.MaxSize + 1, domain.PDF, domain.PurposeCRDocument, domain.ErrTooLarge},
		"exactly max":  {domain.MaxSize, domain.PDF, domain.PurposeCRDocument, nil},
		"unknown type": {10, "text/html", domain.PurposeCRDocument, domain.ErrUnsupportedType},
		"unknown use":  {10, domain.PDF, "avatar", domain.ErrUnknownPurpose},
	} {
		if _, err := domain.NewObject(id, tt.p, tt.ct, tt.size, bytes.Repeat([]byte{1}, 32), by, now); !errors.Is(err, tt.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tt.want)
		}
	}
}
